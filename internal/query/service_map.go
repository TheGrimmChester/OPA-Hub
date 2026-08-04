package query

import (
	"encoding/json"
	"fmt"
	"net/http"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// ServeServiceMap handles GET /api/service-map.
func (h *Handler) ServeServiceMap(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	timeFrom := serviceMapTimeBound(r.URL.Query().Get("from"), "now() - INTERVAL 24 HOUR")
	timeTo := serviceMapTimeBound(r.URL.Query().Get("to"), "now()")
	tenantChild := tenantWhere(r, "child.")
	tenantParent := tenantWhere(r, "parent.")
	tenantRoot := tenantWhere(r, "")

	degradedErrorRate, downErrorRate, degradedLatency, downLatency := h.loadMapThresholds(r)
	calcHealth := func(errorRate, avgLatency float64) string {
		if errorRate >= downErrorRate || avgLatency >= downLatency {
			return "down"
		}
		if errorRate >= degradedErrorRate || avgLatency >= degradedLatency {
			return "degraded"
		}
		return "healthy"
	}

	edgeSQL := fmt.Sprintf(`SELECT
		parent.service as from_service,
		child.service as to_service,
		avg(child.duration_ms) as avg_latency_ms,
		min(child.duration_ms) as min_latency_ms,
		max(child.duration_ms) as max_latency_ms,
		quantile(0.95)(child.duration_ms) as p95_latency_ms,
		quantile(0.99)(child.duration_ms) as p99_latency_ms,
		sum(CASE WHEN child.status = 'error' OR child.status = '0' THEN 1 ELSE 0 END) * 100.0 / greatest(count(*), 1) as error_rate,
		count() as call_count,
		count() / greatest(toFloat64(dateDiff('second', %s, %s)), 1.0) as throughput,
		sum(coalesce(child.bytes_sent, 0)) as bytes_sent,
		sum(coalesce(child.bytes_received, 0)) as bytes_received
		FROM opa.spans_min as child
		INNER JOIN opa.spans_min as parent ON child.parent_id = parent.span_id AND child.trace_id = parent.trace_id
		WHERE %s AND %s
			AND child.service != parent.service
			AND child.service != ''
			AND parent.service != ''
			AND child.start_ts >= %s AND child.start_ts <= %s
		GROUP BY parent.service, child.service
		ORDER BY call_count DESC`,
		timeFrom, timeTo, tenantChild, tenantParent, timeFrom, timeTo)

	edgeRows, err := h.Writer.Query(edgeSQL)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	services := map[string]bool{}
	edges := make([]map[string]any, 0, len(edgeRows))
	for _, row := range edgeRows {
		fromService := asString(row, "from_service")
		toService := asString(row, "to_service")
		if invalidServiceName(fromService) || invalidServiceName(toService) {
			continue
		}
		services[fromService] = true
		services[toService] = true
		avgLatency := asFloat64(row, "avg_latency_ms")
		errorRate := asFloat64(row, "error_rate")
		successRate := 100.0 - errorRate
		if successRate < 0 {
			successRate = 0
		}
		edges = append(edges, map[string]any{
			"from":            fromService,
			"to":              toService,
			"avg_latency_ms":  avgLatency,
			"min_latency_ms":  asFloat64(row, "min_latency_ms"),
			"max_latency_ms":  asFloat64(row, "max_latency_ms"),
			"p95_latency_ms":  asFloat64(row, "p95_latency_ms"),
			"p99_latency_ms":  asFloat64(row, "p99_latency_ms"),
			"error_rate":      errorRate,
			"success_rate":    successRate,
			"call_count":      asUint64(row, "call_count"),
			"throughput":      asFloat64(row, "throughput"),
			"bytes_sent":      asUint64(row, "bytes_sent"),
			"bytes_received":  asUint64(row, "bytes_received"),
			"health_status":   calcHealth(errorRate, avgLatency),
			"dependency_type": "service",
		})
	}

	externalDeps := map[string]bool{}
	h.appendExternalDependencyEdges(r, timeFrom, timeTo, tenantRoot, calcHealth, services, externalDeps, &edges)

	svcSQL := fmt.Sprintf(`SELECT DISTINCT service
		FROM opa.spans_min
		WHERE %s AND start_ts >= %s AND start_ts <= %s`, tenantRoot, timeFrom, timeTo)
	if svcRows, svcErr := h.Writer.Query(svcSQL); svcErr == nil {
		for _, row := range svcRows {
			s := asString(row, "service")
			if !invalidServiceName(s) {
				services[s] = true
			}
		}
	}

	statsSQL := fmt.Sprintf(`SELECT
		service,
		count() as total_spans,
		avg(duration_ms) as avg_duration,
		min(duration_ms) as min_duration,
		max(duration_ms) as max_duration,
		quantile(0.95)(duration_ms) as p95_duration,
		quantile(0.99)(duration_ms) as p99_duration,
		sum(CASE WHEN status = 'error' OR status = '0' THEN 1 ELSE 0 END) * 100.0 / greatest(count(*), 1) as error_rate,
		sum(coalesce(bytes_sent, 0)) + sum(coalesce(bytes_received, 0)) as total_traffic,
		count() / greatest(toFloat64(dateDiff('second', %s, %s)), 1.0) as throughput
		FROM opa.spans_min
		WHERE %s AND start_ts >= %s AND start_ts <= %s
		GROUP BY service`, timeFrom, timeTo, tenantRoot, timeFrom, timeTo)
	statsBySvc := map[string]map[string]any{}
	if statsRows, statsErr := h.Writer.Query(statsSQL); statsErr == nil {
		for _, row := range statsRows {
			s := asString(row, "service")
			if invalidServiceName(s) {
				continue
			}
			services[s] = true
			statsBySvc[s] = row
		}
	}

	nodes := make([]map[string]any, 0, len(services))
	for service := range services {
		row := statsBySvc[service]
		errorRate := asFloat64(row, "error_rate")
		avgDuration := asFloat64(row, "avg_duration")
		var incoming, outgoing uint64
		for _, e := range edges {
			if e["to"] == service {
				incoming += e["call_count"].(uint64)
			}
			if e["from"] == service {
				outgoing += e["call_count"].(uint64)
			}
		}
		node := map[string]any{
			"id":             service,
			"service":        service,
			"health_status":  calcHealth(errorRate, avgDuration),
			"avg_duration":   avgDuration,
			"error_rate":     errorRate,
			"total_spans":    asUint64(row, "total_spans"),
			"incoming_calls": incoming,
			"outgoing_calls": outgoing,
			"node_type":      "service",
		}
		if v := asFloat64(row, "min_duration"); v > 0 {
			node["min_duration"] = v
		}
		if v := asFloat64(row, "max_duration"); v > 0 {
			node["max_duration"] = v
		}
		if v := asFloat64(row, "p95_duration"); v > 0 {
			node["p95_duration"] = v
		}
		if v := asFloat64(row, "p99_duration"); v > 0 {
			node["p99_duration"] = v
		}
		if v := asFloat64(row, "throughput"); v > 0 {
			node["throughput"] = v
		}
		if v := asFloat64(row, "total_traffic"); v > 0 {
			node["total_traffic"] = v
		}
		nodes = append(nodes, node)
	}

	nodes = appendExternalDepNodes(nodes, edges, externalDeps, calcHealth)

	writeJSON(w, map[string]any{
		"nodes":  nodes,
		"edges":  edges,
		"source": "opa-hub",
	})
}

func (h *Handler) loadMapThresholds(r *http.Request) (degradedError, downError, degradedLat, downLat float64) {
	degradedError, downError, degradedLat, downLat = 10.0, 50.0, 1000.0, 5000.0
	org, proj := writeOrgProject(r)
	sql := fmt.Sprintf(`SELECT degraded_error_rate, down_error_rate, degraded_latency_ms, down_latency_ms
		FROM opa.service_map_thresholds FINAL
		WHERE organization_id = '%s' AND project_id = '%s'
		LIMIT 1`, escapeSQL(org), escapeSQL(proj))
	rows, err := h.Writer.Query(sql)
	if err != nil || len(rows) == 0 {
		return
	}
	row := rows[0]
	if v := asFloat64(row, "degraded_error_rate"); v > 0 {
		degradedError = v
	}
	if v := asFloat64(row, "down_error_rate"); v > 0 {
		downError = v
	}
	if v := asFloat64(row, "degraded_latency_ms"); v > 0 {
		degradedLat = v
	}
	if v := asFloat64(row, "down_latency_ms"); v > 0 {
		downLat = v
	}
	return
}

// ServeServiceMapThresholds handles GET/POST /api/service-map/thresholds.
func (h *Handler) ServeServiceMapThresholds(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	org, proj := writeOrgProject(r)
	switch r.Method {
	case http.MethodGet:
		sql := fmt.Sprintf(`SELECT degraded_error_rate, down_error_rate, degraded_latency_ms, down_latency_ms, updated_at
			FROM opa.service_map_thresholds FINAL
			WHERE organization_id = '%s' AND project_id = '%s'
			LIMIT 1`, escapeSQL(org), escapeSQL(proj))
		rows, err := h.Writer.Query(sql)
		if err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		if len(rows) > 0 {
			writeJSON(w, rows[0])
			return
		}
		writeJSON(w, map[string]any{
			"degraded_error_rate": 10.0,
			"down_error_rate":     50.0,
			"degraded_latency_ms": 1000.0,
			"down_latency_ms":     5000.0,
		})
	case http.MethodPost, http.MethodPut:
		var body map[string]any
		if err := decodeJSONBody(w, r, &body); err != nil {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		degradedError := floatOr(body, "degraded_error_rate", 10.0)
		downError := floatOr(body, "down_error_rate", 50.0)
		degradedLat := floatOr(body, "degraded_latency_ms", 1000.0)
		downLat := floatOr(body, "down_latency_ms", 5000.0)
		sql := fmt.Sprintf(`INSERT INTO opa.service_map_thresholds
			(organization_id, project_id, degraded_error_rate, down_error_rate, degraded_latency_ms, down_latency_ms, updated_at)
			VALUES ('%s', '%s', %.2f, %.2f, %.2f, %.2f, now64(3))`,
			escapeSQL(org), escapeSQL(proj), degradedError, downError, degradedLat, downLat)
		if err := h.Writer.Exec(sql); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		writeJSON(w, map[string]any{
			"success":             true,
			"degraded_error_rate": degradedError,
			"down_error_rate":     downError,
			"degraded_latency_ms": degradedLat,
			"down_latency_ms":     downLat,
			"source":              "opa-hub",
		})
	default:
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required")
	}
}

// ServeServiceMapEdgeTraces handles GET /api/service-map/edge-traces.
func (h *Handler) ServeServiceMapEdgeTraces(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	fromSvc := r.URL.Query().Get("from_service")
	toSvc := r.URL.Query().Get("to_service")
	if fromSvc == "" || toSvc == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "from_service and to_service are required")
		return
	}
	where := fmt.Sprintf("WHERE parent.service = '%s' AND child.service = '%s' AND child.trace_id != ''",
		escapeSQL(fromSvc), escapeSQL(toSvc))
	where += tenantAnd(r, "child.")
	where += timeCompareSQL("child.start_ts", ">=", r.URL.Query().Get("from"))
	if r.URL.Query().Get("from") == "" {
		where += " AND child.start_ts >= now() - INTERVAL 24 HOUR"
	}
	where += timeCompareSQL("child.start_ts", "<=", r.URL.Query().Get("to"))

	sql := fmt.Sprintf(`SELECT DISTINCT child.trace_id AS trace_id, max(child.start_ts) AS created_at, max(child.duration_ms) AS duration_ms
		FROM opa.spans_min AS child
		INNER JOIN opa.spans_min AS parent ON child.parent_id = parent.span_id AND child.trace_id = parent.trace_id
		%s GROUP BY child.trace_id ORDER BY created_at DESC LIMIT 200`, where)
	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	traces := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		traces = append(traces, map[string]any{
			"trace_id":    asString(row, "trace_id"),
			"created_at":  asString(row, "created_at"),
			"duration_ms": asFloat64(row, "duration_ms"),
		})
	}
	writeJSON(w, map[string]any{"from": fromSvc, "to": toSvc, "traces": traces, "source": "opa-hub"})
}

func floatOr(m map[string]any, key string, def float64) float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return def
	}
	switch t := v.(type) {
	case float64:
		return t
	case int:
		return float64(t)
	case string:
		var f float64
		_, _ = fmt.Sscanf(t, "%f", &f)
		if f != 0 {
			return f
		}
	}
	return def
}

func decodeJSONBody(w http.ResponseWriter, r *http.Request, dest any) error {
	return json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(dest)
}
