package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// ServeTraces handles GET /api/traces — paginated trace list from ClickHouse.
func (h *Handler) ServeTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	limit, offset := parseLimitOffset(r, 50, 500)
	service := r.URL.Query().Get("service")
	status := r.URL.Query().Get("status")
	language := r.URL.Query().Get("language")
	framework := r.URL.Query().Get("framework")
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "created_at"
	}
	sortCol := map[string]string{
		"created_at":  "created_at",
		"start_ts":    "start_ts",
		"duration_ms": "duration_ms",
		"service":     "service",
		"status":      "status",
	}[sortBy]
	if sortCol == "" {
		sortCol = "created_at"
	}
	order := strings.ToLower(r.URL.Query().Get("order"))
	if order != "asc" {
		order = "desc"
	}

	where := "WHERE " + tenantWhere(r, "")
	if service != "" {
		where += fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	}
	if status != "" && status != "all" {
		where += fmt.Sprintf(" AND status = '%s'", escapeSQL(status))
	}
	if language != "" {
		where += fmt.Sprintf(" AND language = '%s'", escapeSQL(language))
	}
	if framework != "" {
		where += fmt.Sprintf(" AND framework = '%s'", escapeSQL(framework))
	}
	where += timeCompareSQL("start_ts", ">=", r.URL.Query().Get("from"))
	where += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))

	// One row per trace: prefer entry spans when present, else any span.
	inner := `
SELECT
	trace_id,
	any(service) AS service,
	min(start_ts) AS start_ts,
	max(end_ts) AS end_ts,
	max(duration_ms) AS duration_ms,
	count() AS span_count,
	any(status) AS status,
	any(language) AS language,
	any(language_version) AS language_version,
	any(framework) AS framework,
	any(framework_version) AS framework_version,
	min(created_at) AS created_at
FROM opa.spans_min
` + where + `
GROUP BY trace_id`

	listSQL := fmt.Sprintf(`SELECT * FROM (%s) ORDER BY %s %s LIMIT %d OFFSET %d`,
		inner, sortCol, order, limit, offset)
	rows, err := h.Writer.Query(listSQL)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	traces := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		trace := map[string]any{
			"trace_id":    asString(row, "trace_id"),
			"service":     asString(row, "service"),
			"start_ts":    asString(row, "start_ts"),
			"created_at":  asString(row, "created_at"),
			"end_ts":      asString(row, "end_ts"),
			"duration_ms": asFloat64(row, "duration_ms"),
			"span_count":  asUint64(row, "span_count"),
			"status":      asString(row, "status"),
		}
		if v := asStringPtr(row, "language"); v != nil {
			trace["language"] = v
		}
		if v := asStringPtr(row, "language_version"); v != nil {
			trace["language_version"] = v
		}
		if v := asStringPtr(row, "framework"); v != nil {
			trace["framework"] = v
		}
		if v := asStringPtr(row, "framework_version"); v != nil {
			trace["framework_version"] = v
		}
		traces = append(traces, trace)
	}

	total := int64(len(traces))
	countSQL := "SELECT count() AS total FROM (" + inner + ")"
	if countRows, countErr := h.Writer.Query(countSQL); countErr == nil && len(countRows) > 0 {
		total = int64(asUint64(countRows[0], "total"))
	}

	writeJSON(w, map[string]any{
		"traces": traces,
		"total":  total,
		"limit":  limit,
		"offset": offset,
		"source": "opa-hub",
	})
}

// ServeTracesSubpath handles GET /api/traces/{id} and /api/traces/{id}/full.
func (h *Handler) ServeTracesSubpath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	path := strings.TrimPrefix(r.URL.Path, "/api/traces/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "trace id required")
		return
	}
	traceID := parts[0]
	wantFull := len(parts) >= 2 && parts[1] == "full"

	if len(parts) >= 2 && parts[1] == "logs" {
		limit := 100
		if v := r.URL.Query().Get("limit"); v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				limit = n
			}
		}
		if limit > 500 {
			limit = 500
		}
		spanID := strings.TrimSpace(r.URL.Query().Get("span_id"))
		where := fmt.Sprintf("trace_id = '%s'", escapeSQL(traceID))
		where += tenantAnd(r, "")
		if spanID != "" {
			where += fmt.Sprintf(" AND span_id = '%s'", escapeSQL(spanID))
		}
		sql := fmt.Sprintf(`SELECT id, trace_id, span_id, service, level, message, timestamp, fields
			FROM opa.logs
			WHERE %s
			ORDER BY timestamp DESC
			LIMIT %d`, where, limit)
		rows, err := h.Writer.Query(sql)
		if err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		logs := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			var fields any
			if s := asString(row, "fields"); s != "" {
				_ = json.Unmarshal([]byte(s), &fields)
			}
			logs = append(logs, map[string]any{
				"id":        asString(row, "id"),
				"trace_id":  asString(row, "trace_id"),
				"span_id":   asString(row, "span_id"),
				"service":   asString(row, "service"),
				"level":     asString(row, "level"),
				"message":   asString(row, "message"),
				"timestamp": asString(row, "timestamp"),
				"fields":    fields,
			})
		}
		writeJSON(w, map[string]any{
			"logs":     logs,
			"count":    len(logs),
			"trace_id": traceID,
			"source":   "opa-hub",
		})
		return
	}

	tenant := tenantWhere(r, "")
	minSQL := fmt.Sprintf(`
SELECT *
FROM opa.spans_min
WHERE %s AND trace_id = '%s'
ORDER BY start_ts`, tenant, escapeSQL(traceID))

	minRows, err := h.Writer.Query(minSQL)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	if len(minRows) == 0 {
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "trace not found")
		return
	}

	spans := make([]map[string]any, 0, len(minRows))
	for _, row := range minRows {
		spans = append(spans, map[string]any{
			"trace_id":          asString(row, "trace_id"),
			"span_id":           asString(row, "span_id"),
			"parent_id":         asString(row, "parent_id"),
			"service":           asString(row, "service"),
			"name":              asString(row, "name"),
			"start_ts":          asString(row, "start_ts"),
			"end_ts":            asString(row, "end_ts"),
			"duration_ms":       asFloat64(row, "duration_ms"),
			"cpu_ms":            asFloat64(row, "cpu_ms"),
			"status":            asString(row, "status"),
			"language":          asString(row, "language"),
			"language_version":  asString(row, "language_version"),
			"framework":         asString(row, "framework"),
			"framework_version": asString(row, "framework_version"),
			"url_scheme":        asString(row, "url_scheme"),
			"url_host":          asString(row, "url_host"),
			"url_path":          asString(row, "url_path"),
			"organization_id":   asString(row, "organization_id"),
			"project_id":        asString(row, "project_id"),
		})
	}

	out := map[string]any{
		"trace_id":   traceID,
		"spans":      spans,
		"span_count": len(spans),
		"source":     "opa-hub",
	}

	if wantFull {
		fullSQL := fmt.Sprintf(`
SELECT span_id, tags, http, sql, net, cache, redis, stack, dumps, w3c_traceparent, w3c_tracestate
FROM opa.spans_full
WHERE %s AND trace_id = '%s'`, tenant, escapeSQL(traceID))
		fullRows, fullErr := h.Writer.Query(fullSQL)
		if fullErr == nil && len(fullRows) > 0 {
			byID := map[string]map[string]any{}
			for _, row := range fullRows {
				byID[asString(row, "span_id")] = row
			}
			enriched := make([]map[string]any, 0, len(spans))
			for _, sp := range spans {
				sid := asString(sp, "span_id")
				if fr, ok := byID[sid]; ok {
					for _, k := range []string{"tags", "http", "sql", "net", "cache", "redis", "stack", "dumps", "w3c_traceparent", "w3c_tracestate"} {
						if v := asString(fr, k); v != "" {
							sp[k] = v
						}
					}
				}
				enriched = append(enriched, sp)
			}
			out["spans"] = enriched
			out["full"] = true
		}
	}

	// Summary fields used by TraceDetail / CompareTraces.
	if len(minRows) > 0 {
		out["service"] = asString(minRows[0], "service")
		out["status"] = asString(minRows[0], "status")
		var maxDur float64
		for _, row := range minRows {
			if d := asFloat64(row, "duration_ms"); d > maxDur {
				maxDur = d
			}
		}
		out["duration_ms"] = maxDur
		out["start_ts"] = asString(minRows[0], "start_ts")
	}

	writeJSON(w, out)
}
