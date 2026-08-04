package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

func safeSortDir(raw string) string {
	if strings.EqualFold(strings.TrimSpace(raw), "asc") {
		return "ASC"
	}
	return "DESC"
}

func defaultWindowFrom(r *http.Request) string {
	from := safeTimeLiteral(r.URL.Query().Get("from"))
	if from != "" {
		return from
	}
	return "now() - INTERVAL 24 HOUR"
}

// ServeSQLQueries handles GET /api/sql/queries — fingerprint aggregates from spans_min.
func (h *Handler) ServeSQLQueries(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	limit, offset := parseLimitOffset(r, 100, 500)
	service := r.URL.Query().Get("service")
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "last_created_at"
	}
	order := safeSortDir(r.URL.Query().Get("order"))

	baseWhere := "WHERE query_fingerprint IS NOT NULL AND query_fingerprint != '' AND " + tenantWhere(r, "")
	if service != "" {
		baseWhere += fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	}
	from := defaultWindowFrom(r)
	if strings.HasPrefix(strings.ToUpper(from), "NOW()") {
		baseWhere += fmt.Sprintf(" AND start_ts >= %s", from)
	} else {
		baseWhere += fmt.Sprintf(" AND start_ts >= '%s'", escapeSQL(from))
	}
	baseWhere += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))

	orderCol := "last_created_at"
	switch sortBy {
	case "execution_count", "count":
		orderCol = "execution_count"
	case "avg_duration", "duration":
		orderCol = "avg_duration"
	case "max_duration":
		orderCol = "max_duration"
	case "p95_duration":
		orderCol = "p95_duration"
	case "last_created_at", "created_at":
		orderCol = "last_created_at"
	}

	sql := fmt.Sprintf(`SELECT
		query_fingerprint AS fingerprint,
		count() AS execution_count,
		avg(duration_ms) AS avg_duration,
		quantile(0.95)(duration_ms) AS p95_duration,
		quantile(0.99)(duration_ms) AS p99_duration,
		max(duration_ms) AS max_duration,
		max(start_ts) AS last_created_at
		FROM opa.spans_min %s
		GROUP BY query_fingerprint
		ORDER BY %s %s
		LIMIT %d OFFSET %d`, baseWhere, orderCol, order, limit, offset)

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	queries := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		queries = append(queries, map[string]any{
			"fingerprint":     asString(row, "fingerprint"),
			"execution_count": asUint64(row, "execution_count"),
			"avg_duration":    asFloat64(row, "avg_duration"),
			"p95_duration":    asFloat64(row, "p95_duration"),
			"p99_duration":    asFloat64(row, "p99_duration"),
			"max_duration":    asFloat64(row, "max_duration"),
			"last_created_at": asString(row, "last_created_at"),
		})
	}

	countSQL := fmt.Sprintf(`SELECT count(DISTINCT query_fingerprint) AS total FROM opa.spans_min %s`, baseWhere)
	total := uint64(len(queries))
	if countRows, err := h.Writer.Query(countSQL); err == nil && len(countRows) > 0 {
		total = asUint64(countRows[0], "total")
	}

	writeJSON(w, map[string]any{
		"queries": queries,
		"total":   total,
		"source":  "opa-hub",
	})
}

// ServeSQLQueriesSubpath handles GET /api/sql/queries/{fingerprint}.
func (h *Handler) ServeSQLQueriesSubpath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	raw := strings.TrimPrefix(r.URL.Path, "/api/sql/queries/")
	fingerprint, err := url.PathUnescape(raw)
	if err != nil || fingerprint == "" || strings.Contains(fingerprint, "/") {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "fingerprint required")
		return
	}
	fp := escapeSQL(fingerprint)
	scope := tenantWhere(r, "")

	statsSQL := fmt.Sprintf(`SELECT
		count() AS total_executions,
		avg(duration_ms) AS avg_duration,
		quantile(0.95)(duration_ms) AS p95_duration,
		quantile(0.99)(duration_ms) AS p99_duration,
		max(duration_ms) AS max_duration
		FROM opa.spans_min
		WHERE query_fingerprint = '%s' AND %s`, fp, scope)
	rows, err := h.Writer.Query(statsSQL)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	if len(rows) == 0 || asUint64(rows[0], "total_executions") == 0 {
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "query not found")
		return
	}
	row := rows[0]

	exampleQuery := fingerprint
	exampleSQL := fmt.Sprintf(`SELECT sql FROM opa.spans_full
		WHERE trace_id IN (
			SELECT trace_id FROM opa.spans_min
			WHERE query_fingerprint = '%s' AND %s LIMIT 1
		) AND %s AND sql != '' LIMIT 1`, fp, scope, scope)
	if exRows, err := h.Writer.Query(exampleSQL); err == nil && len(exRows) > 0 {
		sqlStr := asString(exRows[0], "sql")
		var arr []any
		if json.Unmarshal([]byte(sqlStr), &arr) == nil && len(arr) > 0 {
			if m, ok := arr[0].(map[string]any); ok {
				if q, ok := m["query"].(string); ok && q != "" {
					exampleQuery = q
				}
			}
		}
	}

	trendsSQL := fmt.Sprintf(`SELECT
		toStartOfHour(start_ts) AS time,
		avg(duration_ms) AS avg_duration,
		quantile(0.95)(duration_ms) AS p95_duration
		FROM opa.spans_min
		WHERE query_fingerprint = '%s' AND %s
		AND start_ts >= now() - INTERVAL 7 DAY
		GROUP BY time ORDER BY time`, fp, scope)
	trendRows, _ := h.Writer.Query(trendsSQL)
	trends := make([]map[string]any, 0, len(trendRows))
	for _, tRow := range trendRows {
		trends = append(trends, map[string]any{
			"time":         asString(tRow, "time"),
			"avg_duration": asFloat64(tRow, "avg_duration"),
			"p95_duration": asFloat64(tRow, "p95_duration"),
		})
	}

	writeJSON(w, map[string]any{
		"fingerprint":        fingerprint,
		"total_executions":   asUint64(row, "total_executions"),
		"avg_duration":       asFloat64(row, "avg_duration"),
		"p95_duration":       asFloat64(row, "p95_duration"),
		"p99_duration":       asFloat64(row, "p99_duration"),
		"max_duration":       asFloat64(row, "max_duration"),
		"example_query":      exampleQuery,
		"performance_trends": trends,
		"source":             "opa-hub",
	})
}

// ServeRedisOperations handles GET /api/redis/operations.
func (h *Handler) ServeRedisOperations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	limit, offset := parseLimitOffset(r, 100, 500)
	service := r.URL.Query().Get("service")
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "last_created_at"
	}
	order := safeSortDir(r.URL.Query().Get("order"))

	baseWhere := "WHERE redis != '' AND redis != '[]' AND redis != 'null' AND " + tenantWhere(r, "")
	if service != "" {
		baseWhere += fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	}
	from := defaultWindowFrom(r)
	if strings.HasPrefix(strings.ToUpper(from), "NOW()") {
		baseWhere += fmt.Sprintf(" AND start_ts >= %s", from)
	} else {
		baseWhere += fmt.Sprintf(" AND start_ts >= '%s'", escapeSQL(from))
	}
	baseWhere += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))

	inner := fmt.Sprintf(`SELECT
		JSONExtractString(redis_op, 'command') AS command,
		coalesce(JSONExtractString(redis_op, 'key'), '') AS key,
		coalesce(JSONExtractString(redis_op, 'host'), '') AS host,
		coalesce(JSONExtractString(redis_op, 'port'), '') AS port,
		coalesce(JSONExtractFloat(redis_op, 'duration_ms'), 0) AS duration_ms,
		coalesce(JSONExtractBool(redis_op, 'hit'), 0) AS hit,
		start_ts
		FROM opa.spans_full
		ARRAY JOIN JSONExtractArrayRaw(redis) AS redis_op
		%s`, baseWhere)

	orderCol := "last_created_at"
	switch sortBy {
	case "execution_count", "count":
		orderCol = "execution_count"
	case "avg_duration", "duration":
		orderCol = "avg_duration"
	case "max_duration":
		orderCol = "max_duration"
	}

	groupSQL := fmt.Sprintf(`SELECT
		command, key, host, port,
		count() AS execution_count,
		avg(duration_ms) AS avg_duration,
		quantile(0.95)(duration_ms) AS p95_duration,
		quantile(0.99)(duration_ms) AS p99_duration,
		max(duration_ms) AS max_duration,
		sum(CASE WHEN hit = 1 THEN 1 ELSE 0 END) AS hit_count,
		sum(CASE WHEN hit = 0 THEN 1 ELSE 0 END) AS miss_count,
		max(start_ts) AS last_created_at
		FROM (%s)
		WHERE command != '' AND command != 'null'
		GROUP BY command, key, host, port
		ORDER BY %s %s
		LIMIT %d OFFSET %d`, inner, orderCol, order, limit, offset)

	rows, err := h.Writer.Query(groupSQL)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	ops := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		ops = append(ops, map[string]any{
			"command":         asString(row, "command"),
			"key":             asString(row, "key"),
			"host":            asString(row, "host"),
			"port":            asString(row, "port"),
			"execution_count": asUint64(row, "execution_count"),
			"avg_duration":    asFloat64(row, "avg_duration"),
			"p95_duration":    asFloat64(row, "p95_duration"),
			"p99_duration":    asFloat64(row, "p99_duration"),
			"max_duration":    asFloat64(row, "max_duration"),
			"hit_count":       asUint64(row, "hit_count"),
			"miss_count":      asUint64(row, "miss_count"),
			"last_created_at": asString(row, "last_created_at"),
		})
	}

	countSQL := fmt.Sprintf(`SELECT count(DISTINCT (command, key, host, port)) AS total FROM (%s) WHERE command != '' AND command != 'null'`, inner)
	total := uint64(len(ops))
	if countRows, err := h.Writer.Query(countSQL); err == nil && len(countRows) > 0 {
		total = asUint64(countRows[0], "total")
	}

	writeJSON(w, map[string]any{
		"operations": ops,
		"total":      total,
		"source":     "opa-hub",
	})
}

// ServeHTTPCalls handles GET /api/http-calls — outbound HTTP aggregates from spans_full.
func (h *Handler) ServeHTTPCalls(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	limit, offset := parseLimitOffset(r, 100, 500)
	service := r.URL.Query().Get("service")
	sortBy := r.URL.Query().Get("sort")
	if sortBy == "" {
		sortBy = "call_count"
	}
	order := safeSortDir(r.URL.Query().Get("order"))

	baseWhere := "WHERE http != '' AND http != '[]' AND http != 'null' AND " + tenantWhere(r, "")
	if service != "" {
		baseWhere += fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	}
	from := defaultWindowFrom(r)
	if strings.HasPrefix(strings.ToUpper(from), "NOW()") {
		baseWhere += fmt.Sprintf(" AND start_ts >= %s", from)
	} else {
		baseWhere += fmt.Sprintf(" AND start_ts >= '%s'", escapeSQL(from))
	}
	baseWhere += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))

	inner := fmt.Sprintf(`SELECT
		coalesce(nullIf(JSONExtractString(http_op, 'url'), ''), JSONExtractString(http_op, 'URL')) AS url,
		coalesce(nullIf(JSONExtractString(http_op, 'method'), ''), 'GET') AS method,
		service,
		coalesce(JSONExtractFloat(http_op, 'duration_ms'),
			if(JSONExtractFloat(http_op, 'duration') > 0, JSONExtractFloat(http_op, 'duration') * 1000, 0)) AS duration_ms,
		coalesce(JSONExtractInt(http_op, 'status_code'), JSONExtractInt(http_op, 'statusCode'), 0) AS status_code,
		coalesce(JSONExtractString(http_op, 'error'), '') AS err,
		coalesce(JSONExtractFloat(http_op, 'bytes_sent'),
			JSONExtractFloat(http_op, 'request_size'),
			JSONExtractFloat(http_op, 'curl_bytes_sent'), 0) AS bytes_sent,
		coalesce(JSONExtractFloat(http_op, 'bytes_received'),
			JSONExtractFloat(http_op, 'response_size'),
			JSONExtractFloat(http_op, 'curl_bytes_received'), 0) AS bytes_received,
		start_ts
		FROM opa.spans_full
		ARRAY JOIN JSONExtractArrayRaw(http) AS http_op
		%s`, baseWhere)

	orderCol := "call_count"
	switch sortBy {
	case "avg_duration", "duration":
		orderCol = "avg_duration"
	case "last_created_at", "created_at":
		orderCol = "last_created_at"
	case "error_count":
		orderCol = "error_count"
	case "call_count", "count":
		orderCol = "call_count"
	}

	groupSQL := fmt.Sprintf(`SELECT
		url, method, service,
		count() AS call_count,
		avg(duration_ms) AS avg_duration,
		min(duration_ms) AS min_duration,
		max(duration_ms) AS max_duration,
		countIf(status_code >= 400 OR err != '') AS error_count,
		sum(bytes_sent) AS total_bytes_sent,
		sum(bytes_received) AS total_bytes_received,
		max(start_ts) AS last_created_at
		FROM (%s)
		WHERE url != ''
		GROUP BY url, method, service
		ORDER BY %s %s
		LIMIT %d OFFSET %d`, inner, orderCol, order, limit, offset)

	rows, err := h.Writer.Query(groupSQL)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	calls := make([]map[string]any, 0, len(rows))
	var totalCalls int64
	for _, row := range rows {
		cc := asUint64(row, "call_count")
		ec := asUint64(row, "error_count")
		errRate := 0.0
		if cc > 0 {
			errRate = float64(ec) / float64(cc) * 100
		}
		totalCalls += int64(cc)
		calls = append(calls, map[string]any{
			"url":                  asString(row, "url"),
			"method":               asString(row, "method"),
			"service":              asString(row, "service"),
			"call_count":           cc,
			"avg_duration":         asFloat64(row, "avg_duration"),
			"min_duration":         asFloat64(row, "min_duration"),
			"max_duration":         asFloat64(row, "max_duration"),
			"error_count":          ec,
			"error_rate":           errRate,
			"total_bytes_sent":     int64(asFloat64(row, "total_bytes_sent")),
			"total_bytes_received": int64(asFloat64(row, "total_bytes_received")),
			"last_created_at":      asString(row, "last_created_at"),
		})
	}

	writeJSON(w, map[string]any{
		"http_calls":  calls,
		"total_calls": totalCalls,
		"total":       len(calls),
		"source":      "opa-hub",
	})
}

// ServeDumps handles GET /api/dumps — flattened variable dumps from spans_full.
func (h *Handler) ServeDumps(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	limit, _ := parseLimitOffset(r, 100, 500)
	service := r.URL.Query().Get("service")
	since := safeTimeLiteral(r.URL.Query().Get("since"))
	all := r.URL.Query().Get("all")
	cursor := r.URL.Query().Get("cursor")

	sql := `SELECT trace_id, span_id, service, name, start_ts, dumps
		FROM opa.spans_full
		WHERE dumps != '' AND dumps != '[]' AND dumps != 'null' AND ` + tenantWhere(r, "")
	if service != "" {
		sql += fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	}
	if cursor != "" {
		if cursorInt, err := strconv.ParseInt(cursor, 10, 64); err == nil {
			cursorTime := time.UnixMilli(cursorInt).UTC().Format("2006-01-02 15:04:05.000")
			sql += fmt.Sprintf(" AND start_ts < '%s'", cursorTime)
		}
	} else if all == "" {
		if since != "" {
			if strings.HasPrefix(strings.ToUpper(since), "NOW()") {
				sql += fmt.Sprintf(" AND start_ts >= %s", since)
			} else {
				sql += fmt.Sprintf(" AND start_ts >= '%s'", escapeSQL(since))
			}
		} else {
			sql += " AND start_ts >= now() - INTERVAL 7 DAY"
		}
	} else if since != "" {
		sql += fmt.Sprintf(" AND start_ts >= '%s'", escapeSQL(since))
	}
	sql += fmt.Sprintf(" ORDER BY start_ts DESC LIMIT %d", limit)

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	allDumps := make([]map[string]any, 0)
	counter := 0
	for _, row := range rows {
		traceID := asString(row, "trace_id")
		spanID := asString(row, "span_id")
		serviceName := asString(row, "service")
		spanName := asString(row, "name")
		startTS := asString(row, "start_ts")
		dumpsStr := asString(row, "dumps")
		var dumpsArray []any
		if json.Unmarshal([]byte(dumpsStr), &dumpsArray) != nil {
			continue
		}
		for _, dumpRaw := range dumpsArray {
			dumpMap, ok := dumpRaw.(map[string]any)
			if !ok {
				continue
			}
			counter++
			timestamp := int64(asFloat64(dumpMap, "timestamp"))
			if timestamp == 0 {
				if v, ok := dumpMap["timestamp"].(json.Number); ok {
					timestamp, _ = v.Int64()
				}
			}
			line := int64(asFloat64(dumpMap, "line"))
			file, _ := dumpMap["file"].(string)
			text, _ := dumpMap["text"].(string)
			allDumps = append(allDumps, map[string]any{
				"id":            fmt.Sprintf("%s-%s-%d-%d", traceID, spanID, timestamp, counter),
				"trace_id":      traceID,
				"span_id":       spanID,
				"service":       serviceName,
				"span_name":     spanName,
				"timestamp":     timestamp,
				"file":          file,
				"line":          line,
				"data":          dumpMap["data"],
				"text":          text,
				"span_start_ts": startTS,
			})
		}
	}

	sort.Slice(allDumps, func(i, j int) bool {
		ti, _ := allDumps[i]["timestamp"].(int64)
		tj, _ := allDumps[j]["timestamp"].(int64)
		return ti > tj
	})

	nextCursor := int64(0)
	hasMore := false
	if len(allDumps) > 0 {
		last := allDumps[len(allDumps)-1]
		if spanStartTS, ok := last["span_start_ts"].(string); ok {
			if t, err := time.Parse("2006-01-02 15:04:05.000", spanStartTS); err == nil {
				nextCursor = t.UnixMilli()
				hasMore = len(allDumps) == limit
			}
		}
		if nextCursor == 0 {
			if ts, ok := last["timestamp"].(int64); ok && ts > 0 {
				nextCursor = ts
				hasMore = len(allDumps) == limit
			}
		}
	}

	writeJSON(w, map[string]any{
		"dumps":       allDumps,
		"total":       len(allDumps),
		"has_more":    hasMore,
		"next_cursor": nextCursor,
		"source":      "opa-hub",
	})
}
