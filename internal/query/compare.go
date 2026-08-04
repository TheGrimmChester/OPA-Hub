package query

import (
	"fmt"
	"net/http"
	"strings"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

var compareDimensions = map[string]bool{
	"language_version": true,
	"language":         true,
	"framework":        true,
	"framework_version": true,
	"service":          true,
	"name":             true,
	"db_system":        true,
}

// ServeTransactionsCompare handles GET /api/transactions/compare — cohort comparison
// of entry-span aggregates grouped by a whitelisted dimension.
func (h *Handler) ServeTransactionsCompare(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	dimension := strings.TrimSpace(r.URL.Query().Get("dimension"))
	if dimension == "" {
		dimension = "language_version"
	}
	if !compareDimensions[dimension] {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "invalid dimension")
		return
	}

	name := strings.TrimSpace(r.URL.Query().Get("name"))
	service := strings.TrimSpace(r.URL.Query().Get("service"))

	where := "WHERE parent_id = '' AND trace_id != ''"
	where += tenantAnd(r, "")
	where += timeCompareSQL("start_ts", ">=", r.URL.Query().Get("from"))
	where += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))
	if name != "" {
		where += fmt.Sprintf(" AND name = '%s'", escapeSQL(name))
	}
	if service != "" {
		where += fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	}

	sql := fmt.Sprintf(`SELECT
		%s AS grp,
		count() AS cnt,
		avg(duration_ms) AS avg_duration_ms,
		quantile(0.50)(duration_ms) AS p50_duration_ms,
		quantile(0.95)(duration_ms) AS p95_duration_ms,
		quantile(0.99)(duration_ms) AS p99_duration_ms,
		min(duration_ms) AS min_duration_ms,
		max(duration_ms) AS max_duration_ms,
		avg(cpu_ms) AS avg_cpu_ms,
		sum(CASE WHEN status = 'error' OR status = '0' THEN 1 ELSE 0 END) * 100.0 / greatest(count(*), 1) AS error_rate,
		avg(http_requests_count) AS avg_http,
		avg(bytes_sent) AS avg_bytes_sent,
		avg(bytes_received) AS avg_bytes_received
		FROM opa.spans_min
		%s
		GROUP BY grp
		HAVING grp != ''
		ORDER BY cnt DESC
		LIMIT 50`, dimension, where)

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	groups := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		groups = append(groups, map[string]any{
			"value":              asString(row, "grp"),
			"count":              asUint64(row, "cnt"),
			"avg_duration_ms":    asFloat64(row, "avg_duration_ms"),
			"p50_duration_ms":    asFloat64(row, "p50_duration_ms"),
			"p95_duration_ms":    asFloat64(row, "p95_duration_ms"),
			"p99_duration_ms":    asFloat64(row, "p99_duration_ms"),
			"min_duration_ms":    asFloat64(row, "min_duration_ms"),
			"max_duration_ms":    asFloat64(row, "max_duration_ms"),
			"avg_cpu_ms":         asFloat64(row, "avg_cpu_ms"),
			"error_rate":         asFloat64(row, "error_rate"),
			"avg_http":           asFloat64(row, "avg_http"),
			"avg_bytes_sent":     asFloat64(row, "avg_bytes_sent"),
			"avg_bytes_received": asFloat64(row, "avg_bytes_received"),
		})
	}

	writeJSON(w, map[string]any{
		"dimension": dimension,
		"name":      name,
		"service":   service,
		"groups":    groups,
		"source":    "opa-hub",
	})
}
