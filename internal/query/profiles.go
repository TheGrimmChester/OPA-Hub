package query

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// ServeProfilesFlame handles GET /api/profiles/flame — nested call tree for one service.
func (h *Handler) ServeProfilesFlame(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	service := strings.TrimSpace(r.URL.Query().Get("service"))
	if service == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "service parameter is required")
		return
	}
	filter := " WHERE " + tenantWhere(r, "")
	filter += fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	if from := safeTimeLiteral(r.URL.Query().Get("from")); from != "" {
		filter += fmt.Sprintf(" AND hour >= '%s'", escapeSQL(from))
	}
	if to := safeTimeLiteral(r.URL.Query().Get("to")); to != "" {
		filter += fmt.Sprintf(" AND hour <= '%s'", escapeSQL(to))
	}
	sql := fmt.Sprintf(`SELECT
		parent_function,
		function,
		sum(call_count) as call_count,
		sum(total_wall_ms) as total_wall_ms,
		sum(self_wall_ms) as self_wall_ms,
		sum(total_cpu_ms) as total_cpu_ms
		FROM opa.profile_edges%s
		GROUP BY parent_function, function`, filter)
	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	type flameEdge struct {
		function    string
		callCount   uint64
		totalWallMs float64
		selfWallMs  float64
		totalCPUMs  float64
	}
	childEdges := map[string][]flameEdge{}
	for _, row := range rows {
		parent := asString(row, "parent_function")
		childEdges[parent] = append(childEdges[parent], flameEdge{
			function:    asString(row, "function"),
			callCount:   asUint64(row, "call_count"),
			totalWallMs: asFloat64(row, "total_wall_ms"),
			selfWallMs:  asFloat64(row, "self_wall_ms"),
			totalCPUMs:  asFloat64(row, "total_cpu_ms"),
		})
	}
	for parent, edges := range childEdges {
		sort.Slice(edges, func(i, j int) bool { return edges[i].totalWallMs > edges[j].totalWallMs })
		childEdges[parent] = edges
	}

	const flameMaxDepth = 24
	const flameMaxNodes = 4000
	nodeCount := 0
	var build func(edges []flameEdge, path map[string]bool, depth int) []map[string]any
	build = func(edges []flameEdge, path map[string]bool, depth int) []map[string]any {
		out := []map[string]any{}
		if depth >= flameMaxDepth {
			return out
		}
		for _, e := range edges {
			if nodeCount >= flameMaxNodes {
				break
			}
			nodeCount++
			fn, class := e.function, ""
			if i := strings.Index(e.function, "::"); i >= 0 {
				class, fn = e.function[:i], e.function[i+2:]
			}
			node := map[string]any{
				"call_id":     fmt.Sprintf("f%d", nodeCount-1),
				"function":    fn,
				"class":       class,
				"duration_ms": e.totalWallMs,
				"cpu_ms":      e.totalCPUMs,
				"call_count":  e.callCount,
			}
			kids := []map[string]any{}
			if !path[e.function] {
				path[e.function] = true
				kids = build(childEdges[e.function], path, depth+1)
				delete(path, e.function)
			}
			node["children"] = kids
			out = append(out, node)
		}
		return out
	}
	tree := build(childEdges[""], map[string]bool{}, 0)
	var totalMs float64
	for _, e := range childEdges[""] {
		totalMs += e.totalWallMs
	}
	writeJSON(w, map[string]any{
		"service":  service,
		"total_ms": totalMs,
		"tree":     tree,
		"source":   "opa-hub",
	})
}
