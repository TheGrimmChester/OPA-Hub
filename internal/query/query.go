package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
	opentenant "github.com/TheGrimmChester/open-tenant-go"

	"github.com/TheGrimmChester/opa-hub/internal/registry"
	"github.com/TheGrimmChester/opa-hub/internal/store"
)

// Handler is the dashboard-facing query/admin surface.
// It reads the central ClickHouse `opa` database — edge agents are not queried
// for routine UI paths.
type Handler struct {
	Reg                 *registry.Registry
	Writer              *store.Writer
	StartedAt           time.Time
	Version             string
	EnrollTokenRequired bool
}

// ServeQueryRoot handles GET /api/query — capability advertisement for the UI.
func (h *Handler) ServeQueryRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	writeJSON(w, map[string]any{
		"service": "opa-hub",
		"role":    "query",
		"capabilities": []string{
			"agents",
			"ingest_accept",
			"auth",
			"health",
			"services",
			"services_metadata",
			"services_stats",
			"services_http_calls",
			"traces",
			"trace_detail",
			"explore_facets",
			"admin",
			"metrics_names",
			"metrics_labels",
			"metrics_label_values",
			"metrics_query_range",
			"metrics_performance",
			"metrics_network",
			"service_map",
			"service_map_thresholds",
			"service_map_edge_traces",
			"alerts",
			"rum_metrics",
			"rum_sessions",
			"rum_detail",
			"rum_slo",
			"rum_facets",
			"rum_vitals_attribution",
			"rum_replay",
			"rum_replay_timeline",
			"rum_mobile_sessions",
			"mobile_crashes",
			"profiles",
			"profiles_flame",
			"errors",
			"errors_detail",
			"errors_group_status",
			"trace_logs",
			"logs",
			"slos",
			"anomalies",
			"synthetics",
			"sql_queries",
			"redis_operations",
			"http_calls",
			"dumps",
			"stats",
			"commands",
			"key_transactions",
			"infra_hosts",
			"transactions_compare",
			"version",
			"topology",
			"ops_status",
			"audit",
			"db_instances",
			"db_statements",
			"db_fingerprint_match",
			"db_unused_indexes",
			"releases",
			"diagnostics_suspect_commits",
			"diagnostics_heap",
			"diagnostics_threads",
			"diagnostics_locks",
		},
		"source": "clickhouse",
		"database": func() string {
			if h.Writer == nil {
				return ""
			}
			return h.Writer.Config().Database
		}(),
	})
}

// ServeAdmin handles GET /api/admin — control-plane summary for operators.
func (h *Handler) ServeAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	agents := 0
	if h.Reg != nil {
		agents = h.Reg.Count()
	}
	ch := map[string]any{"configured": false}
	if h.Writer != nil {
		ch = h.Writer.Stats()
	}
	writeJSON(w, map[string]any{
		"service":           "opa-hub",
		"version":           h.Version,
		"started_at":        h.StartedAt.UTC().Format(time.RFC3339),
		"uptime_sec":        int(time.Since(h.StartedAt).Seconds()),
		"agents":            agents,
		"clickhouse":        ch,
		"topology":          "hub-spoke",
		"dashboard_backend": true,
		"query_owner":       true,
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func escapeSQL(s string) string {
	return opentenant.EscapeSQL(s)
}

func asString(row map[string]any, key string) string {
	v, ok := row[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func asStringPtr(row map[string]any, key string) any {
	s := asString(row, key)
	if s == "" {
		return nil
	}
	return s
}

func asUint64(row map[string]any, key string) uint64 {
	v, ok := row[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return uint64(t)
	case json.Number:
		n, _ := t.Int64()
		return uint64(n)
	case string:
		n, _ := strconv.ParseUint(t, 10, 64)
		return n
	case int:
		return uint64(t)
	case int64:
		return uint64(t)
	case uint64:
		return t
	default:
		n, _ := strconv.ParseUint(fmt.Sprint(t), 10, 64)
		return n
	}
}

func asFloat64(row map[string]any, key string) float64 {
	v, ok := row[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return t
	case json.Number:
		n, _ := t.Float64()
		return n
	case string:
		n, _ := strconv.ParseFloat(t, 64)
		return n
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		n, _ := strconv.ParseFloat(fmt.Sprint(t), 64)
		return n
	}
}

func parseLimitOffset(r *http.Request, defLimit, maxLimit int) (limit, offset int) {
	limit = defLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > maxLimit {
		limit = maxLimit
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			offset = n
		}
	}
	return limit, offset
}

func tenantWhere(r *http.Request, alias string) string {
	return opentenant.FromRequest(r).ScopeBool(alias)
}

func safeTimeLiteral(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	// Allow ClickHouse function expressions used by the dashboard, otherwise quote.
	upper := strings.ToUpper(s)
	if strings.HasPrefix(upper, "NOW()") || strings.HasPrefix(upper, "TODAY()") ||
		strings.HasPrefix(upper, "PARSEDATETIME") {
		return s
	}
	// Reject characters that break out of a string literal.
	if strings.ContainsAny(s, "';\\") {
		return ""
	}
	// Normalize RFC3339 / ISO / epoch into ClickHouse DateTime literals.
	// opa.profiles.hour (and similar DateTime cols) reject trailing Z / fractional
	// seconds when compared as quoted strings — dashboard chTime is already safe,
	// but clients and fromISO-style params are not.
	if t, err := parseFlexibleTime(s); err == nil {
		return t.UTC().Format("2006-01-02 15:04:05")
	}
	return s
}

// timeCompareSQL returns " AND <col> >= <expr>" (or <=) with correct quoting.
// String timestamps go through parseDateTimeBestEffort so RFC3339 (…T…Z) works
// the same as dashboard chTime (YYYY-MM-DD HH:MM:SS) — bare quoted strings fail
// against DateTime64 on ClickHouse 24.x.
func timeCompareSQL(col, op, raw string) string {
	lit := safeTimeLiteral(raw)
	if lit == "" {
		return ""
	}
	upper := strings.ToUpper(lit)
	if strings.HasPrefix(upper, "NOW()") || strings.HasPrefix(upper, "TODAY()") ||
		strings.HasPrefix(upper, "PARSEDATETIME") {
		return fmt.Sprintf(" AND %s %s %s", col, op, lit)
	}
	return fmt.Sprintf(" AND %s %s parseDateTimeBestEffort('%s')", col, op, escapeSQL(lit))
}

func entrySpanConjunct(alias string) string {
	return fmt.Sprintf(" AND %sis_entry = 1", alias)
}
