package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

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

// ServeServicesSubpath handles GET /api/services/{name}/stats and …/http-calls.
func (h *Handler) ServeServicesSubpath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/services/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 {
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "service subpath required")
		return
	}
	service, err := url.PathUnescape(parts[0])
	if err != nil || service == "" || service == "metadata" {
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "unknown service path")
		return
	}
	switch parts[1] {
	case "stats":
		h.serveServiceStats(w, r, service)
	case "http-calls":
		h.serveServiceHTTPCalls(w, r, service)
	default:
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "unknown service subpath")
	}
}

func (h *Handler) serveServiceStats(w http.ResponseWriter, r *http.Request, service string) {
	scope := tenantWhere(r, "")
	where := fmt.Sprintf("WHERE %s AND service = '%s'%s",
		scope, escapeSQL(service), entrySpanConjunct(""))
	where += timeCompareSQL("start_ts", ">=", r.URL.Query().Get("from"))
	where += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))

	sql := fmt.Sprintf(`SELECT
		count() AS total_traces,
		count() AS total_spans,
		countIf(status = 'error' OR status = '0') AS error_count,
		avg(duration_ms) AS avg_duration,
		quantile(0.50)(duration_ms) AS p50_duration,
		quantile(0.95)(duration_ms) AS p95_duration,
		quantile(0.99)(duration_ms) AS p99_duration
		FROM opa.spans_min %s`, where)
	rows, err := h.Writer.Query(sql)
	if err != nil || len(rows) == 0 {
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "service not found")
		return
	}
	row := rows[0]
	totalSpans := asUint64(row, "total_spans")
	errorCount := asUint64(row, "error_count")
	errorRate := 0.0
	if totalSpans > 0 {
		errorRate = float64(errorCount) / float64(totalSpans) * 100
	}

	const epLabel = "if(url_host != '', concat(url_scheme, '://', url_host, url_path), name)"
	epSQL := fmt.Sprintf(`SELECT
		%s AS endpoint,
		any(url_path) AS ep_url_path,
		any(url_host) AS ep_url_host,
		count() AS count,
		argMax(trace_id, duration_ms) AS exemplar_trace_id,
		avg(duration_ms) AS avg_duration,
		countIf(status = 'error' OR status = '0') AS error_count
		FROM opa.spans_min %s
		GROUP BY endpoint ORDER BY count DESC LIMIT 10`, epLabel, where)
	epRows, _ := h.Writer.Query(epSQL)
	endpoints := make([]map[string]any, 0, len(epRows))
	for _, ep := range epRows {
		endpoints = append(endpoints, map[string]any{
			"name":              asString(ep, "endpoint"),
			"url_path":          asString(ep, "ep_url_path"),
			"url_host":          asString(ep, "ep_url_host"),
			"count":             asUint64(ep, "count"),
			"exemplar_trace_id": asString(ep, "exemplar_trace_id"),
			"avg_duration":      asFloat64(ep, "avg_duration"),
			"error_count":       asUint64(ep, "error_count"),
		})
	}

	writeJSON(w, map[string]any{
		"service":       service,
		"total_traces":  asUint64(row, "total_traces"),
		"total_spans":   totalSpans,
		"error_count":   errorCount,
		"error_rate":    errorRate,
		"avg_duration":  asFloat64(row, "avg_duration"),
		"p50_duration":  asFloat64(row, "p50_duration"),
		"p95_duration":  asFloat64(row, "p95_duration"),
		"p99_duration":  asFloat64(row, "p99_duration"),
		"top_endpoints": endpoints,
		"source":        "opa-hub",
	})
}

func (h *Handler) serveServiceHTTPCalls(w http.ResponseWriter, r *http.Request, service string) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if limit > 500 {
		limit = 500
	}

	sql := fmt.Sprintf(`SELECT http, start_ts, duration_ms, status, trace_id, span_id
		FROM opa.spans_full
		WHERE %s AND service = '%s' AND http != '' AND http != '[]' AND http != 'null'`,
		tenantWhere(r, ""), escapeSQL(service))
	from := defaultWindowFrom(r)
	if strings.HasPrefix(strings.ToUpper(from), "NOW()") {
		sql += fmt.Sprintf(" AND start_ts >= %s", from)
	} else {
		sql += fmt.Sprintf(" AND start_ts >= '%s'", escapeSQL(from))
	}
	sql += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))
	sql += fmt.Sprintf(" ORDER BY start_ts DESC LIMIT %d", limit)

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	type httpCallStats struct {
		URL            string
		Method         string
		CallCount      int64
		TotalDuration  float64
		ErrorCount     int64
		TotalBytesSent int64
		TotalBytesRecv int64
		MinDuration    float64
		MaxDuration    float64
	}
	callsMap := map[string]*httpCallStats{}
	var totalCalls int64

	for _, row := range rows {
		httpData := asString(row, "http")
		var httpRequests []map[string]any
		if json.Unmarshal([]byte(httpData), &httpRequests) != nil {
			continue
		}
		for _, req := range httpRequests {
			requestURL, _ := req["url"].(string)
			if requestURL == "" {
				requestURL, _ = req["URL"].(string)
			}
			if requestURL == "" {
				continue
			}
			method, _ := req["method"].(string)
			if method == "" {
				method = "GET"
			}
			key := method + " " + requestURL
			call, ok := callsMap[key]
			if !ok {
				call = &httpCallStats{URL: requestURL, Method: method, MinDuration: 999999}
				callsMap[key] = call
			}
			call.CallCount++
			totalCalls++
			duration := asFloat64(req, "duration_ms")
			if duration <= 0 {
				if d := asFloat64(req, "duration"); d > 0 {
					duration = d * 1000
				}
			}
			if duration > 0 {
				call.TotalDuration += duration
				if duration < call.MinDuration {
					call.MinDuration = duration
				}
				if duration > call.MaxDuration {
					call.MaxDuration = duration
				}
			}
			statusCode := int(asFloat64(req, "status_code"))
			if statusCode == 0 {
				statusCode = int(asFloat64(req, "statusCode"))
			}
			if statusCode >= 400 {
				call.ErrorCount++
			} else if errStr, _ := req["error"].(string); errStr != "" {
				call.ErrorCount++
			}
			if sent := asFloat64(req, "bytes_sent"); sent > 0 {
				call.TotalBytesSent += int64(sent)
			} else if sent := asFloat64(req, "request_size"); sent > 0 {
				call.TotalBytesSent += int64(sent)
			}
			if recv := asFloat64(req, "bytes_received"); recv > 0 {
				call.TotalBytesRecv += int64(recv)
			} else if recv := asFloat64(req, "response_size"); recv > 0 {
				call.TotalBytesRecv += int64(recv)
			}
		}
	}

	httpCalls := make([]map[string]any, 0, len(callsMap))
	for _, call := range callsMap {
		avgDuration := 0.0
		errorRate := 0.0
		if call.CallCount > 0 {
			avgDuration = call.TotalDuration / float64(call.CallCount)
			errorRate = float64(call.ErrorCount) / float64(call.CallCount) * 100
			if call.MinDuration == 999999 {
				call.MinDuration = 0
			}
		}
		httpCalls = append(httpCalls, map[string]any{
			"url":                  call.URL,
			"method":               call.Method,
			"call_count":           call.CallCount,
			"avg_duration":         avgDuration,
			"min_duration":         call.MinDuration,
			"max_duration":         call.MaxDuration,
			"error_count":          call.ErrorCount,
			"error_rate":           errorRate,
			"total_bytes_sent":     call.TotalBytesSent,
			"total_bytes_received": call.TotalBytesRecv,
		})
	}
	sort.Slice(httpCalls, func(i, j int) bool {
		ci, _ := httpCalls[i]["call_count"].(int64)
		cj, _ := httpCalls[j]["call_count"].(int64)
		return ci > cj
	})

	writeJSON(w, map[string]any{
		"service":     service,
		"total_calls": totalCalls,
		"http_calls":  httpCalls,
		"source":      "opa-hub",
	})
}
