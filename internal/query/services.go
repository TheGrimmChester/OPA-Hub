package query

import (
	"fmt"
	"net/http"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// ServeServices handles GET /api/services — per-service aggregates from ClickHouse.
func (h *Handler) ServeServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	where := "WHERE " + tenantWhere(r, "") + entrySpanConjunct("")
	where += timeCompareSQL("start_ts", ">=", r.URL.Query().Get("from"))
	where += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))

	globalSQL := `
SELECT
	count() AS total_traces,
	count() AS total_spans,
	countIf(status = 'error' OR status = '0') AS error_count,
	sum(cpu_ms) AS total_cpu_ms,
	sum(bytes_sent) AS total_bytes_sent,
	sum(bytes_received) AS total_bytes_received,
	sum(http_requests_count) AS total_http_requests,
	countIf(query_fingerprint IS NOT NULL AND query_fingerprint != '') AS total_sql_queries,
	avg(duration_ms) AS avg_duration
FROM opa.spans_min ` + where

	globalRows, err := h.Writer.Query(globalSQL)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	globalTotals := map[string]any{
		"total_traces":         0,
		"total_spans":          0,
		"error_count":          0,
		"total_cpu_ms":         0.0,
		"total_bytes_sent":     0,
		"total_bytes_received": 0,
		"total_http_requests":  0,
		"total_sql_queries":    0,
		"avg_duration":         0.0,
	}
	if len(globalRows) > 0 {
		row := globalRows[0]
		globalTotals = map[string]any{
			"total_traces":         asUint64(row, "total_traces"),
			"total_spans":          asUint64(row, "total_spans"),
			"error_count":          asUint64(row, "error_count"),
			"total_cpu_ms":         asFloat64(row, "total_cpu_ms"),
			"total_bytes_sent":     asUint64(row, "total_bytes_sent"),
			"total_bytes_received": asUint64(row, "total_bytes_received"),
			"total_http_requests":  asUint64(row, "total_http_requests"),
			"total_sql_queries":    asUint64(row, "total_sql_queries"),
			"avg_duration":         asFloat64(row, "avg_duration"),
		}
	}

	svcSQL := `
SELECT service,
	any(language) AS language,
	any(language_version) AS language_version,
	any(framework) AS framework,
	any(framework_version) AS framework_version,
	count() AS total_traces,
	count() AS total_spans,
	countIf(status = 'error' OR status = '0') AS error_count,
	sum(cpu_ms) AS total_cpu_ms,
	sum(bytes_sent) AS total_bytes_sent,
	sum(bytes_received) AS total_bytes_received,
	sum(http_requests_count) AS total_http_requests,
	count(DISTINCT query_fingerprint) AS sql_query_count,
	avg(duration_ms) AS avg_duration,
	quantile(0.95)(duration_ms) AS p95_duration,
	quantile(0.99)(duration_ms) AS p99_duration
FROM opa.spans_min ` + where + `
GROUP BY service
ORDER BY total_traces DESC`

	rows, err := h.Writer.Query(svcSQL)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	services := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		totalTraces := asUint64(row, "total_traces")
		totalSpans := asUint64(row, "total_spans")
		errorCount := asUint64(row, "error_count")
		errorRate := 0.0
		if totalSpans > 0 {
			errorRate = float64(errorCount) / float64(totalSpans) * 100
		}
		services = append(services, map[string]any{
			"service":              asString(row, "service"),
			"language":             asString(row, "language"),
			"language_version":     asStringPtr(row, "language_version"),
			"framework":            asStringPtr(row, "framework"),
			"framework_version":    asStringPtr(row, "framework_version"),
			"total_traces":         totalTraces,
			"total_spans":          totalSpans,
			"error_count":          errorCount,
			"error_rate":           errorRate,
			"total_cpu_ms":         asFloat64(row, "total_cpu_ms"),
			"total_bytes_sent":     asUint64(row, "total_bytes_sent"),
			"total_bytes_received": asUint64(row, "total_bytes_received"),
			"total_http_requests":  asUint64(row, "total_http_requests"),
			"sql_query_count":      asUint64(row, "sql_query_count"),
			"avg_duration":         asFloat64(row, "avg_duration"),
			"p95_duration":         asFloat64(row, "p95_duration"),
			"p99_duration":         asFloat64(row, "p99_duration"),
			"top_sql_queries":      []any{},
			"top_http_requests":    []any{},
		})
	}

	writeJSON(w, map[string]any{
		"global_totals": globalTotals,
		"services":      services,
		"source":        "opa-hub",
		"count":         len(services),
	})
}

// ServeServicesMetadata handles GET /api/services/metadata.
func (h *Handler) ServeServicesMetadata(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	where := "WHERE " + tenantWhere(r, "")
	service := r.URL.Query().Get("service")
	if service != "" {
		where += fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	}

	sql := `
SELECT service,
	any(language) AS language,
	any(language_version) AS language_version,
	any(framework) AS framework,
	any(framework_version) AS framework_version,
	count() AS span_count
FROM opa.spans_min ` + where + `
GROUP BY service
ORDER BY service`

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	services := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		services = append(services, map[string]any{
			"service":           asString(row, "service"),
			"language":          asString(row, "language"),
			"language_version":  asStringPtr(row, "language_version"),
			"framework":         asStringPtr(row, "framework"),
			"framework_version": asStringPtr(row, "framework_version"),
			"span_count":        asUint64(row, "span_count"),
		})
	}
	writeJSON(w, map[string]any{"services": services, "source": "opa-hub"})
}
