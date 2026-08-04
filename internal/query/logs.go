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
)

// ServeLogs handles GET /api/logs — logs explorer used by the dashboard main nav.
func (h *Handler) ServeLogs(w http.ResponseWriter, r *http.Request) {
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
	level := r.URL.Query().Get("level")
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	cursor := r.URL.Query().Get("cursor")
	all := r.URL.Query().Get("all")
	since := safeTimeLiteral(r.URL.Query().Get("since"))

	org := strings.TrimSpace(r.Header.Get("X-Organization-ID"))
	proj := strings.TrimSpace(r.Header.Get("X-Project-ID"))
	// Always join when auth-enforced (or when a concrete tenant header is set)
	// so missing/"all" cannot dump every tenant's logs. Lab mode without
	// headers still uses the unscoped path for local exploration.
	useJoin := opentenant.AuthEnforced() ||
		(org != "" && !strings.EqualFold(org, "all")) ||
		(proj != "" && !strings.EqualFold(proj, "all"))

	var (
		baseWhere       string
		fromClause      string
		serviceColumn   = "service"
		levelColumn     = "level"
		messageColumn   = "message"
		timestampColumn = "timestamp"
		countExpr       = "count()"
		errorCountExpr  = "countIf(upper(level) IN ('ERROR', 'CRITICAL', 'FATAL'))"
	)

	if useJoin {
		spanScope := tenantWhere(r, "spans_min.")
		ownScope := tenantWhere(r, "logs.")
		// Accept either the log's own tenant columns or the joined span tenant.
		tenantFilter := fmt.Sprintf("((%s) OR (%s))", ownScope, spanScope)
		baseWhere = "WHERE 1=1 AND " + tenantFilter
		fromClause = "opa.logs AS logs LEFT JOIN opa.spans_min AS spans_min ON logs.trace_id = spans_min.trace_id"
		serviceColumn = "logs.service"
		levelColumn = "logs.level"
		messageColumn = "logs.message"
		timestampColumn = "logs.timestamp"
		countExpr = "uniqExact(logs.id)"
		errorCountExpr = fmt.Sprintf("uniqExactIf(logs.id, upper(%s) IN ('ERROR', 'CRITICAL', 'FATAL'))", levelColumn)
	} else {
		baseWhere = "WHERE 1=1"
		fromClause = "opa.logs"
	}

	if service != "" {
		baseWhere += fmt.Sprintf(" AND %s = '%s'", serviceColumn, escapeSQL(service))
	}
	if level != "" {
		baseWhere += fmt.Sprintf(" AND %s = '%s'", levelColumn, escapeSQL(level))
	}
	if q != "" {
		baseWhere += fmt.Sprintf(" AND positionCaseInsensitive(%s, '%s') > 0", messageColumn, escapeSQL(q))
	}

	// Snapshot predicates before pagination for histogram/facets.
	facetWhere := baseWhere
	if cursor != "" {
		if cursorInt, err := strconv.ParseInt(cursor, 10, 64); err == nil {
			cursorTime := time.UnixMilli(cursorInt).UTC().Format("2006-01-02 15:04:05.000")
			baseWhere += fmt.Sprintf(" AND %s < '%s'", timestampColumn, cursorTime)
		}
	} else if all == "" {
		if since != "" {
			baseWhere += fmt.Sprintf(" AND %s >= '%s'", timestampColumn, escapeSQL(since))
			facetWhere += fmt.Sprintf(" AND %s >= '%s'", timestampColumn, escapeSQL(since))
		} else if from := safeTimeLiteral(r.URL.Query().Get("from")); from != "" {
			baseWhere += fmt.Sprintf(" AND %s >= '%s'", timestampColumn, escapeSQL(from))
			facetWhere += fmt.Sprintf(" AND %s >= '%s'", timestampColumn, escapeSQL(from))
		} else {
			baseWhere += fmt.Sprintf(" AND %s >= now() - INTERVAL 7 DAY", timestampColumn)
			facetWhere += fmt.Sprintf(" AND %s >= now() - INTERVAL 7 DAY", timestampColumn)
		}
		if to := safeTimeLiteral(r.URL.Query().Get("to")); to != "" {
			baseWhere += fmt.Sprintf(" AND %s <= '%s'", timestampColumn, escapeSQL(to))
			facetWhere += fmt.Sprintf(" AND %s <= '%s'", timestampColumn, escapeSQL(to))
		}
	} else if since != "" {
		baseWhere += fmt.Sprintf(" AND %s >= '%s'", timestampColumn, escapeSQL(since))
		facetWhere += fmt.Sprintf(" AND %s >= '%s'", timestampColumn, escapeSQL(since))
	}

	selectPrefix := `id, trace_id, span_id, service, level, message,
		toUnixTimestamp64Milli(timestamp) AS timestamp_ms, timestamp, fields`
	if useJoin {
		selectPrefix = `DISTINCT logs.id AS id, logs.trace_id AS trace_id, logs.span_id AS span_id,
			logs.service AS service, logs.level AS level, logs.message AS message,
			toUnixTimestamp64Milli(logs.timestamp) AS timestamp_ms, logs.timestamp AS timestamp, logs.fields AS fields`
	}

	sql := fmt.Sprintf(`SELECT %s FROM %s %s ORDER BY %s DESC LIMIT %d OFFSET %d`,
		selectPrefix, fromClause, baseWhere, timestampColumn, limit, offset)
	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	logs := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var fields map[string]any
		if s := asString(row, "fields"); s != "" && s != "null" {
			_ = json.Unmarshal([]byte(s), &fields)
		}
		tsMs := int64(asFloat64(row, "timestamp_ms"))
		if tsMs == 0 {
			if ts := asString(row, "timestamp"); ts != "" {
				for _, layout := range []string{"2006-01-02 15:04:05.000", "2006-01-02 15:04:05", time.RFC3339} {
					if t, e := time.Parse(layout, ts); e == nil {
						tsMs = t.UnixMilli()
						break
					}
				}
			}
		}
		logs = append(logs, map[string]any{
			"id":        asString(row, "id"),
			"trace_id":  asString(row, "trace_id"),
			"span_id":   asString(row, "span_id"),
			"service":   asString(row, "service"),
			"level":     asString(row, "level"),
			"message":   asString(row, "message"),
			"timestamp": tsMs,
			"fields":    fields,
		})
	}

	nextCursor := int64(0)
	hasMore := false
	if len(logs) > 0 {
		if ts, ok := logs[len(logs)-1]["timestamp"].(int64); ok && ts > 0 {
			nextCursor = ts
			hasMore = len(logs) == limit
		}
	}

	bucketSeconds := 300
	if fromTS, toTS := r.URL.Query().Get("from"), r.URL.Query().Get("to"); fromTS != "" {
		layout := "2006-01-02 15:04:05"
		start, err1 := time.Parse(layout, safeTimeLiteral(fromTS))
		end := time.Now().UTC()
		if toTS != "" {
			if parsed, err2 := time.Parse(layout, safeTimeLiteral(toTS)); err2 == nil {
				end = parsed
			}
		}
		if err1 == nil && end.After(start) {
			if s := int(end.Sub(start).Seconds()) / 24; s > 60 {
				bucketSeconds = s
			} else {
				bucketSeconds = 60
			}
		}
	}

	histogram := make([]map[string]any, 0)
	histSQL := fmt.Sprintf(`SELECT toString(toStartOfInterval(%s, INTERVAL %d SECOND)) AS bucket,
		%s AS count, %s AS error_count
		FROM %s %s GROUP BY bucket ORDER BY bucket`,
		timestampColumn, bucketSeconds, countExpr, errorCountExpr, fromClause, facetWhere)
	if histRows, histErr := h.Writer.Query(histSQL); histErr == nil {
		for _, row := range histRows {
			histogram = append(histogram, map[string]any{
				"time":        asString(row, "bucket"),
				"count":       asUint64(row, "count"),
				"error_count": asUint64(row, "error_count"),
			})
		}
	}

	facet := func(column string) []map[string]any {
		out := make([]map[string]any, 0)
		facetSQL := fmt.Sprintf(`SELECT %s AS value, %s AS count FROM %s %s
			GROUP BY value ORDER BY count DESC LIMIT 20`, column, countExpr, fromClause, facetWhere)
		facetRows, facetErr := h.Writer.Query(facetSQL)
		if facetErr != nil {
			return out
		}
		for _, row := range facetRows {
			out = append(out, map[string]any{
				"value": asString(row, "value"),
				"count": asUint64(row, "count"),
			})
		}
		return out
	}

	writeJSON(w, map[string]any{
		"logs":        logs,
		"total":       len(logs),
		"has_more":    hasMore,
		"next_cursor": nextCursor,
		"histogram":   histogram,
		"facets": map[string]any{
			"levels":   facet(levelColumn),
			"services": facet(serviceColumn),
		},
		"source": "opa-hub",
	})
}
