package query

import (
	"fmt"
	"net/http"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// ServeRUMMetrics handles GET /api/rum/metrics.
func (h *Handler) ServeRUMMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	timeFrom := safeTimeLiteral(r.URL.Query().Get("from"))
	if timeFrom == "" {
		timeFrom = time.Now().Add(-24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	}
	where := fmt.Sprintf("WHERE occurred_at >= '%s'", escapeSQL(timeFrom)) + tenantAnd(r, "")
	sql := fmt.Sprintf(`SELECT
		avg(load_total) as avg_page_load_time,
		avg(load_dom) as avg_dom_ready_time,
		count() as total_page_views,
		sum(has_errors) as total_errors,
		quantile(0.75)(v_lcp) as p75_lcp,
		quantile(0.75)(v_cls) as p75_cls,
		quantile(0.75)(v_inp) as p75_inp,
		quantile(0.75)(v_fcp) as p75_fcp,
		quantile(0.75)(v_ttfb) as p75_ttfb,
		quantile(0.75)(v_fid) as p75_fid
		FROM (%s)`, rumDedupe(where))

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	metrics := map[string]any{
		"avg_page_load_time": 0,
		"avg_dom_ready_time": 0,
		"total_page_views":   0,
		"total_errors":       0,
		"source":             "opa-hub",
	}
	if len(rows) > 0 {
		row := rows[0]
		lcp := asFloat64(row, "p75_lcp")
		cls := asFloat64(row, "p75_cls")
		inp := asFloat64(row, "p75_inp")
		metrics = map[string]any{
			"avg_page_load_time": asFloat64(row, "avg_page_load_time"),
			"avg_dom_ready_time": asFloat64(row, "avg_dom_ready_time"),
			"total_page_views":   asUint64(row, "total_page_views"),
			"total_errors":       asUint64(row, "total_errors"),
			"source":             "opa-hub",
			"core_web_vitals": map[string]any{
				"lcp":  map[string]any{"p75": lcp, "unit": "ms", "rating": cwvRating("lcp", lcp)},
				"cls":  map[string]any{"p75": cls, "unit": "", "rating": cwvRating("cls", cls)},
				"inp":  map[string]any{"p75": inp, "unit": "ms", "rating": cwvRating("inp", inp)},
				"fcp":  map[string]any{"p75": asFloat64(row, "p75_fcp"), "unit": "ms"},
				"ttfb": map[string]any{"p75": asFloat64(row, "p75_ttfb"), "unit": "ms"},
				"fid":  map[string]any{"p75": asFloat64(row, "p75_fid"), "unit": "ms"},
			},
		}
	}

	timelineSQL := fmt.Sprintf(`SELECT
		toStartOfHour(last_ts) as time,
		avg(load_total) as avg_load_time,
		quantile(0.95)(load_total) as p95_load_time
		FROM (%s)
		GROUP BY time ORDER BY time`, rumDedupe(where))
	timelineRows, _ := h.Writer.Query(timelineSQL)
	timeline := make([]map[string]any, 0, len(timelineRows))
	for _, tRow := range timelineRows {
		timeline = append(timeline, map[string]any{
			"time":          asString(tRow, "time"),
			"avg_load_time": asFloat64(tRow, "avg_load_time"),
			"p95_load_time": asFloat64(tRow, "p95_load_time"),
		})
	}
	metrics["timeline"] = timeline
	writeJSON(w, metrics)
}

// ServeRUMSessions handles GET /api/rum/sessions.
func (h *Handler) ServeRUMSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	timeFrom := safeTimeLiteral(r.URL.Query().Get("from"))
	if timeFrom == "" {
		timeFrom = time.Now().Add(-24 * time.Hour).UTC().Format("2006-01-02 15:04:05")
	}
	where := fmt.Sprintf("WHERE session_id != '' AND occurred_at >= '%s'", escapeSQL(timeFrom)) + tenantAnd(r, "")
	sql := fmt.Sprintf(`SELECT session_id,
		toString(min(first_ts)) AS first_seen,
		toString(max(last_ts)) AS last_seen,
		count() AS page_count,
		sum(ajax_n) AS ajax_count,
		sum(err_n) AS error_count,
		avg(load_total) AS avg_load_ms,
		any(user_agent) AS user_agent
		FROM (%s)
		GROUP BY session_id ORDER BY last_seen DESC LIMIT 100`, rumDedupe(where))
	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	sessions := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, map[string]any{
			"session_id":  asString(row, "session_id"),
			"first_seen":  asString(row, "first_seen"),
			"last_seen":   asString(row, "last_seen"),
			"page_count":  asUint64(row, "page_count"),
			"ajax_count":  asUint64(row, "ajax_count"),
			"error_count": asUint64(row, "error_count"),
			"avg_load_ms": asFloat64(row, "avg_load_ms"),
			"user_agent":  asString(row, "user_agent"),
		})
	}
	writeJSON(w, map[string]any{"sessions": sessions, "source": "opa-hub"})
}

// ServeRUMSessionsSubpath handles GET /api/rum/sessions/{id}.
func (h *Handler) ServeRUMSessionsSubpath(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	sessionID := strings.TrimPrefix(r.URL.Path, "/api/rum/sessions/")
	sessionID = strings.Trim(sessionID, "/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "session id required")
		return
	}
	where := fmt.Sprintf("WHERE session_id = '%s'", escapeSQL(sessionID)) + tenantAnd(r, "")
	deduped := rumDedupe(where)

	collect := func(q string, cols ...string) []map[string]any {
		out := []map[string]any{}
		rows, err := h.Writer.Query(q)
		if err != nil {
			return out
		}
		for _, row := range rows {
			m := map[string]any{}
			for _, c := range cols {
				m[c] = row[c]
			}
			out = append(out, m)
		}
		return out
	}

	pageViews := collect(fmt.Sprintf(`SELECT pv AS page_view_id, page_url,
		load_total AS load_ms, toString(first_ts) AS occurred_at
		FROM (%s) ORDER BY first_ts ASC LIMIT 200`, deduped),
		"page_view_id", "page_url", "load_ms", "occurred_at")
	ajax := collect(fmt.Sprintf(`SELECT
		JSONExtractString(aj, 'url') AS url, JSONExtractString(aj, 'method') AS method,
		JSONExtractFloat(aj, 'duration') AS duration,
		JSONExtractInt(aj, 'status') AS status,
		JSONExtractString(aj, 'trace_id') AS trace_id,
		toString(first_ts) AS occurred_at
		FROM (%s) ARRAY JOIN JSONExtractArrayRaw(ajax_json) AS aj
		ORDER BY first_ts ASC LIMIT 500`, deduped),
		"url", "method", "duration", "status", "trace_id", "occurred_at")
	errorRows := collect(fmt.Sprintf(`SELECT
		JSONExtractString(er, 'message') AS message,
		page_url, toString(first_ts) AS occurred_at
		FROM (%s) ARRAY JOIN JSONExtractArrayRaw(errors_json) AS er
		ORDER BY first_ts ASC LIMIT 200`, deduped),
		"message", "page_url", "occurred_at")

	writeJSON(w, map[string]any{
		"session_id": sessionID,
		"page_views": pageViews,
		"ajax":       ajax,
		"errors":     errorRows,
		"source":     "opa-hub",
	})
}
