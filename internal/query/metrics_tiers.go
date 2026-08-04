package query

import (
	"fmt"
	"strings"
	"time"
)

type tierSpec struct {
	name       string
	resolution time.Duration
	retention  time.Duration
	timeCol    string
	raw        bool
}

var metricTierSpecs = []tierSpec{
	{name: "opa.metric_points", resolution: 0, retention: 7 * 24 * time.Hour, timeCol: "ts", raw: true},
	{name: "opa.metric_1m", resolution: time.Minute, retention: 30 * 24 * time.Hour, timeCol: "bucket"},
	{name: "opa.metric_5m", resolution: 5 * time.Minute, retention: 90 * 24 * time.Hour, timeCol: "bucket"},
	{name: "opa.metric_1h", resolution: time.Hour, retention: 400 * 24 * time.Hour, timeCol: "bucket"},
	{name: "opa.metric_1d", resolution: 24 * time.Hour, retention: 1095 * 24 * time.Hour, timeCol: "bucket"},
}

const defaultMaxPoints = 500
const metricTierQuantiles = "0.5, 0.9, 0.95, 0.99"

type metricAggregation string

const (
	aggAvg  metricAggregation = "avg"
	aggSum  metricAggregation = "sum"
	aggMin  metricAggregation = "min"
	aggMax  metricAggregation = "max"
	aggLast metricAggregation = "last"
	aggP50  metricAggregation = "p50"
	aggP90  metricAggregation = "p90"
	aggP95  metricAggregation = "p95"
	aggP99  metricAggregation = "p99"
)

var quantileIndex = map[metricAggregation]int{aggP50: 1, aggP90: 2, aggP95: 3, aggP99: 4}

var validMetricAggregations = map[metricAggregation]bool{
	aggAvg: true, aggSum: true, aggMin: true, aggMax: true, aggLast: true,
	aggP50: true, aggP90: true, aggP95: true, aggP99: true,
}

func selectMetricTier(from, to, now time.Time, maxPoints int) (tierSpec, time.Duration) {
	if maxPoints <= 0 {
		maxPoints = defaultMaxPoints
	}
	span := to.Sub(from)
	if span <= 0 {
		span = time.Minute
	}
	target := span / time.Duration(maxPoints)
	age := now.Sub(from)
	var eligible []tierSpec
	for _, t := range metricTierSpecs {
		if age <= t.retention {
			eligible = append(eligible, t)
		}
	}
	if len(eligible) == 0 {
		coarsest := metricTierSpecs[len(metricTierSpecs)-1]
		return coarsest, coarsest.resolution
	}
	chosen := eligible[0]
	for _, t := range eligible {
		if t.resolution <= target && t.resolution >= chosen.resolution {
			chosen = t
		}
	}
	step := target
	if chosen.resolution > 0 {
		if step < chosen.resolution {
			step = chosen.resolution
		} else {
			step = step.Truncate(chosen.resolution)
		}
	} else if step < time.Second {
		step = time.Second
	}
	return chosen, step
}

func quantileLevel(agg metricAggregation) string {
	switch agg {
	case aggP50:
		return "0.5"
	case aggP90:
		return "0.9"
	case aggP95:
		return "0.95"
	case aggP99:
		return "0.99"
	}
	return "0.5"
}

func valueExpr(t tierSpec, agg metricAggregation) (string, error) {
	if !validMetricAggregations[agg] {
		return "", fmt.Errorf("unsupported aggregation %q", agg)
	}
	if t.raw {
		switch agg {
		case aggAvg:
			return "avg(value)", nil
		case aggSum:
			return "sum(value)", nil
		case aggMin:
			return "min(value)", nil
		case aggMax:
			return "max(value)", nil
		case aggLast:
			return "argMax(value, ts)", nil
		default:
			return fmt.Sprintf("quantileTDigest(%s)(value)", quantileLevel(agg)), nil
		}
	}
	switch agg {
	case aggAvg:
		return "sum(sum_v) / greatest(sum(samples), 1)", nil
	case aggSum:
		return "sum(sum_v)", nil
	case aggMin:
		return "min(min_v)", nil
	case aggMax:
		return "max(max_v)", nil
	case aggLast:
		return "argMaxMerge(last_state)", nil
	default:
		return fmt.Sprintf("arrayElement(quantilesTDigestMerge(%s)(q_state), %d)",
			metricTierQuantiles, quantileIndex[agg]), nil
	}
}

type metricRangeQuery struct {
	MetricName    string
	SeriesIDs     []uint64
	From, To      time.Time
	Agg           metricAggregation
	MaxPoints     int
	GroupBySeries bool
	TenantAnd     string // already includes leading " AND …" or empty
}

func buildMetricRangeSQL(q metricRangeQuery, now time.Time) (string, tierSpec, time.Duration, error) {
	tier, step := selectMetricTier(q.From, q.To, now, q.MaxPoints)
	valExpr, err := valueExpr(tier, q.Agg)
	if err != nil {
		return "", tier, 0, err
	}
	stepSecs := int64(step / time.Second)
	if stepSecs < 1 {
		stepSecs = 1
	}

	var b strings.Builder
	b.WriteString("SELECT toStartOfInterval(")
	b.WriteString(tier.timeCol)
	b.WriteString(fmt.Sprintf(", INTERVAL %d SECOND) AS bucket_ts", stepSecs))
	if q.GroupBySeries {
		b.WriteString(", series_id")
	}
	b.WriteString(", ")
	b.WriteString(valExpr)
	b.WriteString(" AS value, sum(")
	if tier.raw {
		b.WriteString("1")
	} else {
		b.WriteString("samples")
	}
	b.WriteString(") AS sample_count FROM ")
	b.WriteString(tier.name)
	b.WriteString(" WHERE metric_name = '")
	b.WriteString(escapeSQL(q.MetricName))
	b.WriteString("'")
	b.WriteString(fmt.Sprintf(" AND %s >= '%s' AND %s <= '%s'",
		tier.timeCol, safeTimeLiteral(q.From.UTC().Format("2006-01-02 15:04:05")),
		tier.timeCol, safeTimeLiteral(q.To.UTC().Format("2006-01-02 15:04:05"))))
	if len(q.SeriesIDs) > 0 {
		ids := make([]string, 0, len(q.SeriesIDs))
		for _, id := range q.SeriesIDs {
			ids = append(ids, fmt.Sprintf("%d", id))
		}
		b.WriteString(" AND series_id IN (")
		b.WriteString(strings.Join(ids, ","))
		b.WriteString(")")
	}
	b.WriteString(q.TenantAnd)
	b.WriteString(" GROUP BY bucket_ts")
	if q.GroupBySeries {
		b.WriteString(", series_id")
	}
	b.WriteString(" ORDER BY bucket_ts")
	maxRows := defaultMaxPoints * 20
	if q.MaxPoints > 0 {
		maxRows = q.MaxPoints * 20
	}
	b.WriteString(fmt.Sprintf(" LIMIT %d", maxRows))
	return b.String(), tier, step, nil
}
