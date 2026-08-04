package query

import (
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// ServeMetricNames handles GET /api/metrics/names.
func (h *Handler) ServeMetricNames(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	where := "1=1" + tenantAnd(r, "")
	if prefix := strings.TrimSpace(r.URL.Query().Get("prefix")); prefix != "" {
		if len(prefix) > 128 {
			prefix = prefix[:128]
		}
		where += fmt.Sprintf(" AND positionCaseInsensitive(metric_name, '%s') > 0", escapeSQL(prefix))
	}

	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT metric_name,
		       any(metric_type) AS metric_type,
		       any(unit) AS unit,
		       uniq(series_id) AS series_count,
		       max(last_seen) AS last_seen
		FROM opa.metric_series FINAL
		WHERE %s
		GROUP BY metric_name
		ORDER BY metric_name
		LIMIT 2000`, where))
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"name":         asString(row, "metric_name"),
			"type":         asString(row, "metric_type"),
			"unit":         asString(row, "unit"),
			"series_count": asUint64(row, "series_count"),
			"last_seen":    asString(row, "last_seen"),
		})
	}
	writeJSON(w, map[string]any{"metrics": out, "count": len(out), "source": "opa-hub"})
}

// ServeMetricLabels handles GET /api/metrics/labels.
func (h *Handler) ServeMetricLabels(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	metric := strings.TrimSpace(r.URL.Query().Get("metric"))
	if metric == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "metric is required")
		return
	}

	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT label_name, uniq(label_value) AS value_count
		FROM (
		    SELECT arrayJoin(arrayZip(label_names, label_values)) AS pair,
		           pair.1 AS label_name,
		           pair.2 AS label_value
		    FROM opa.metric_series FINAL
		    WHERE metric_name = '%s'%s
		)
		GROUP BY label_name
		ORDER BY label_name
		LIMIT 200`, escapeSQL(metric), tenantAnd(r, "")))
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		out = append(out, map[string]any{
			"name":        asString(row, "label_name"),
			"value_count": asUint64(row, "value_count"),
		})
	}
	writeJSON(w, map[string]any{"labels": out, "count": len(out), "source": "opa-hub"})
}

// ServeMetricLabelValues handles GET /api/metrics/label-values.
func (h *Handler) ServeMetricLabelValues(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	metric := strings.TrimSpace(r.URL.Query().Get("metric"))
	label := strings.TrimSpace(r.URL.Query().Get("label"))
	if metric == "" || label == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "metric and label are required")
		return
	}

	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT DISTINCT label_values[indexOf(label_names, '%s')] AS value
		FROM opa.metric_series FINAL
		WHERE metric_name = '%s'
		  AND has(label_names, '%s')%s
		ORDER BY value
		LIMIT 1000`,
		escapeSQL(label), escapeSQL(metric), escapeSQL(label), tenantAnd(r, "")))
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	out := make([]string, 0, len(rows))
	for _, row := range rows {
		if v := asString(row, "value"); v != "" {
			out = append(out, v)
		}
	}
	writeJSON(w, map[string]any{"values": out, "count": len(out), "source": "opa-hub"})
}

type labelMatcher struct {
	name   string
	value  string
	negate bool
	regex  bool
}

func parseLabelMatchers(raw []string) ([]labelMatcher, error) {
	out := make([]labelMatcher, 0, len(raw))
	for _, s := range raw {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		switch {
		case strings.Contains(s, "=~"):
			name, value, _ := strings.Cut(s, "=~")
			if name == "" {
				return nil, fmt.Errorf("matcher %q has an empty label name", s)
			}
			out = append(out, labelMatcher{name: name, value: value, regex: true})
		case strings.Contains(s, "!:"):
			name, value, _ := strings.Cut(s, "!:")
			if name == "" {
				return nil, fmt.Errorf("matcher %q has an empty label name", s)
			}
			out = append(out, labelMatcher{name: name, value: value, negate: true})
		case strings.Contains(s, ":"):
			name, value, _ := strings.Cut(s, ":")
			if name == "" {
				return nil, fmt.Errorf("matcher %q has an empty label name", s)
			}
			out = append(out, labelMatcher{name: name, value: value})
		default:
			return nil, fmt.Errorf("matcher %q must be name:value, name!:value or name=~regex", s)
		}
	}
	return out, nil
}

func matcherSQL(m labelMatcher) string {
	idx := fmt.Sprintf("label_values[indexOf(label_names, '%s')]", escapeSQL(m.name))
	switch {
	case m.regex:
		return fmt.Sprintf("match(%s, '%s')", idx, escapeSQL(m.value))
	case m.negate:
		return fmt.Sprintf("(has(label_names, '%s') AND %s != '%s')",
			escapeSQL(m.name), idx, escapeSQL(m.value))
	default:
		return fmt.Sprintf("(has(label_names, '%s') AND %s = '%s')",
			escapeSQL(m.name), idx, escapeSQL(m.value))
	}
}

func zipLabels(namesRaw, valuesRaw any) map[string]string {
	names, _ := namesRaw.([]any)
	values, _ := valuesRaw.([]any)
	out := make(map[string]string, len(names))
	for i, n := range names {
		name, ok := n.(string)
		if !ok || i >= len(values) {
			continue
		}
		if v, ok := values[i].(string); ok {
			out[name] = v
		}
	}
	return out
}

func (h *Handler) resolveSeriesIDs(r *http.Request, metric string, matchers []labelMatcher, limit int) ([]uint64, map[uint64]map[string]string, error) {
	where := fmt.Sprintf("metric_name = '%s'%s", escapeSQL(metric), tenantAnd(r, ""))
	for _, m := range matchers {
		where += " AND " + matcherSQL(m)
	}
	if limit <= 0 {
		limit = 500
	}
	rows, err := h.Writer.Query(fmt.Sprintf(`
		SELECT series_id, label_names, label_values
		FROM opa.metric_series FINAL
		WHERE %s
		LIMIT %d`, where, limit))
	if err != nil {
		return nil, nil, err
	}
	ids := make([]uint64, 0, len(rows))
	labels := make(map[uint64]map[string]string, len(rows))
	for _, row := range rows {
		id := asUint64(row, "series_id")
		if id == 0 {
			continue
		}
		ids = append(ids, id)
		labels[id] = zipLabels(row["label_names"], row["label_values"])
	}
	return ids, labels, nil
}

// ServeMetricQueryRange handles GET /api/metrics/query-range.
func (h *Handler) ServeMetricQueryRange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	qs := r.URL.Query()
	metric := strings.TrimSpace(qs.Get("metric"))
	if metric == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "metric is required")
		return
	}
	agg := metricAggregation(strings.ToLower(strings.TrimSpace(qs.Get("agg"))))
	if agg == "" {
		agg = aggAvg
	}
	if !validMetricAggregations[agg] {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", fmt.Sprintf("unsupported agg %q", agg))
		return
	}
	matchers, err := parseLabelMatchers(qs["label"])
	if err != nil {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	now := time.Now().UTC()
	from, to, err := parseMetricRange(qs, now)
	if err != nil {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	maxPoints := defaultMaxPoints
	if v, err := strconv.Atoi(qs.Get("max_points")); err == nil && v > 0 {
		maxPoints = v
		if maxPoints > 5000 {
			maxPoints = 5000
		}
	}
	groupBy := strings.TrimSpace(qs.Get("group_by"))
	splitSeries := groupBy != "" || qs.Get("split") == "1"

	var seriesIDs []uint64
	var seriesLabels map[uint64]map[string]string
	if len(matchers) > 0 || splitSeries {
		seriesIDs, seriesLabels, err = h.resolveSeriesIDs(r, metric, matchers, 500)
		if err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		if len(seriesIDs) == 0 {
			writeJSON(w, map[string]any{
				"metric": metric, "series": []any{}, "count": 0,
				"note":   "no series match the given label filters",
				"source": "opa-hub",
			})
			return
		}
	}

	sql, tier, step, err := buildMetricRangeSQL(metricRangeQuery{
		MetricName:    metric,
		SeriesIDs:     seriesIDs,
		From:          from,
		To:            to,
		Agg:           agg,
		MaxPoints:     maxPoints,
		GroupBySeries: splitSeries,
		TenantAnd:     tenantAnd(r, ""),
	}, now)
	if err != nil {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
		return
	}
	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	series := groupRangeRows(rows, splitSeries, groupBy, seriesLabels)
	writeJSON(w, map[string]any{
		"metric": metric,
		"agg":    string(agg),
		"series": series,
		"count":  len(series),
		"source": "opa-hub",
		"resolution": map[string]any{
			"tier":        strings.TrimPrefix(tier.name, "opa."),
			"step_secs":   int64(step / time.Second),
			"from":        from.Format(time.RFC3339),
			"to":          to.Format(time.RFC3339),
			"downsampled": !tier.raw,
		},
	})
}

func groupRangeRows(rows []map[string]any, split bool, groupBy string, seriesLabels map[uint64]map[string]string) []map[string]any {
	if !split {
		points := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			points = append(points, map[string]any{
				"ts":      asString(row, "bucket_ts"),
				"value":   asFloat64(row, "value"),
				"samples": asUint64(row, "sample_count"),
			})
		}
		return []map[string]any{{"name": "", "labels": map[string]string{}, "points": points}}
	}
	type acc struct {
		labels map[string]string
		points []map[string]any
	}
	byKey := map[string]*acc{}
	var order []string
	for _, row := range rows {
		sid := asUint64(row, "series_id")
		labels := seriesLabels[sid]
		key := fmt.Sprintf("%d", sid)
		display := map[string]string{}
		if groupBy != "" {
			key = labels[groupBy]
			display[groupBy] = key
		} else {
			display = labels
		}
		a := byKey[key]
		if a == nil {
			a = &acc{labels: display}
			byKey[key] = a
			order = append(order, key)
		}
		a.points = append(a.points, map[string]any{
			"ts":      asString(row, "bucket_ts"),
			"value":   asFloat64(row, "value"),
			"samples": asUint64(row, "sample_count"),
		})
	}
	sort.Strings(order)
	out := make([]map[string]any, 0, len(order))
	for _, k := range order {
		out = append(out, map[string]any{
			"name":   k,
			"labels": byKey[k].labels,
			"points": byKey[k].points,
		})
	}
	return out
}

// ServeMetricsPerformance handles GET /api/metrics/performance.
func (h *Handler) ServeMetricsPerformance(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	interval := safeInterval(r.URL.Query().Get("interval"))
	where := "WHERE " + tenantWhere(r, "") + entrySpanConjunct("")
	where += timeCompareSQL("start_ts", ">=", r.URL.Query().Get("from"))
	if r.URL.Query().Get("from") == "" {
		where += " AND start_ts >= now() - INTERVAL 24 HOUR"
	}
	where += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))

	sql := fmt.Sprintf(`SELECT
		toStartOfInterval(start_ts, INTERVAL %s) as time,
		count() as throughput,
		quantile(0.50)(duration_ms) as p50_duration,
		quantile(0.95)(duration_ms) as p95_duration,
		quantile(0.99)(duration_ms) as p99_duration,
		sum(CASE WHEN status = 'error' OR status = '0' THEN 1 ELSE 0 END) * 100.0 / greatest(count(*), 1) as error_rate,
		argMax(trace_id, duration_ms) as exemplar_trace_id
		FROM opa.spans_min %s
		GROUP BY time ORDER BY time`, interval, where)

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	metrics := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		metrics = append(metrics, map[string]any{
			"time":              asString(row, "time"),
			"throughput":        asUint64(row, "throughput"),
			"p50_duration":      asFloat64(row, "p50_duration"),
			"p95_duration":      asFloat64(row, "p95_duration"),
			"exemplar_trace_id": asString(row, "exemplar_trace_id"),
			"p99_duration":      asFloat64(row, "p99_duration"),
			"error_rate":        asFloat64(row, "error_rate"),
		})
	}
	writeJSON(w, map[string]any{"metrics": metrics, "source": "opa-hub"})
}

// ServeMetricsNetwork handles GET /api/metrics/network.
func (h *Handler) ServeMetricsNetwork(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	interval := safeInterval(r.URL.Query().Get("interval"))
	where := "WHERE " + tenantWhere(r, "") + entrySpanConjunct("")
	where += timeCompareSQL("start_ts", ">=", r.URL.Query().Get("from"))
	if r.URL.Query().Get("from") == "" {
		where += " AND start_ts >= now() - INTERVAL 24 HOUR"
	}
	where += timeCompareSQL("start_ts", "<=", r.URL.Query().Get("to"))

	sql := fmt.Sprintf(`SELECT
		toStartOfInterval(start_ts, INTERVAL %s) as time,
		sum(bytes_sent) as bytes_sent,
		sum(bytes_received) as bytes_received,
		count() as request_count,
		avg(duration_ms) as avg_latency
		FROM opa.spans_min %s
		GROUP BY time ORDER BY time`, interval, where)

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	metrics := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		metrics = append(metrics, map[string]any{
			"time":           asString(row, "time"),
			"bytes_sent":     asUint64(row, "bytes_sent"),
			"bytes_received": asUint64(row, "bytes_received"),
			"request_count":  asUint64(row, "request_count"),
			"avg_latency":    asFloat64(row, "avg_latency"),
		})
	}
	writeJSON(w, map[string]any{"metrics": metrics, "source": "opa-hub"})
}
