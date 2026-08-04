package query

import (
	"fmt"
	"net/http"
	"strings"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// exploreSignal identifies the telemetry table behind explore facets.
type exploreSignal string

const (
	exploreSpans   exploreSignal = "spans"
	exploreMetrics exploreSignal = "metrics"
	exploreLogs    exploreSignal = "logs"
	exploreRUM     exploreSignal = "rum"
)

// exploreAttr maps a unified facet field name to a ClickHouse column expression.
// Keep this allowlist tight — values are interpolated into SQL.
var exploreAttr = map[string]map[exploreSignal]string{
	"service": {
		exploreSpans:   "service",
		exploreLogs:    "service",
		exploreMetrics: "arrayElement(label_values, indexOf(label_names, 'service'))",
	},
	"host": {
		exploreSpans:   "hostname",
		exploreLogs:    "host",
		exploreMetrics: "arrayElement(label_values, indexOf(label_names, 'host'))",
	},
	"environment": {
		exploreSpans:   "environment",
		exploreRUM:     "environment",
		exploreMetrics: "arrayElement(label_values, indexOf(label_names, 'environment'))",
	},
	"release": {
		exploreSpans:   "release",
		exploreRUM:     "release",
		exploreMetrics: "arrayElement(label_values, indexOf(label_names, 'release'))",
	},
	"route": {
		exploreSpans: "route",
		exploreRUM:   "route",
	},
	"status": {
		exploreSpans: "status",
	},
	// Runtime / stack dims — populated on NAS (language/framework nearly
	// universal; db_system sparse but non-empty). Prefer these over
	// environment/host/release when those columns are still blank.
	"language": {
		exploreSpans: "language",
	},
	"framework": {
		exploreSpans: "framework",
	},
	"db_system": {
		exploreSpans: "db_system",
	},
	"url_path": {
		exploreSpans: "url_path",
	},
	"level": {
		exploreLogs: "level",
	},
	"metric_name": {
		exploreMetrics: "metric_name",
	},
	"page_url": {
		exploreRUM: "page_url",
	},
	"session_id": {
		exploreRUM: "session_id",
	},
	"geo_country": {
		exploreRUM: "geo_country",
	},
	"trace_id": {
		exploreSpans: "trace_id",
		exploreLogs:  "trace_id",
	},
}

func parseExploreSignal(s string) (exploreSignal, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "spans", "span", "traces", "trace":
		return exploreSpans, true
	case "metrics", "metric":
		return exploreMetrics, true
	case "logs", "log":
		return exploreLogs, true
	case "rum", "frontend", "browser":
		return exploreRUM, true
	default:
		return "", false
	}
}

func exploreTable(signal exploreSignal) string {
	switch signal {
	case exploreSpans:
		return "opa.spans_min"
	case exploreMetrics:
		return "opa.metric_points"
	case exploreLogs:
		return "opa.logs"
	case exploreRUM:
		return "opa.rum_events"
	default:
		return ""
	}
}

func exploreTimeColumn(signal exploreSignal) string {
	switch signal {
	case exploreSpans:
		return "start_ts"
	case exploreMetrics:
		return "ts"
	case exploreLogs:
		return "timestamp"
	case exploreRUM:
		return "occurred_at"
	default:
		return ""
	}
}

func resolveExploreAttr(signal exploreSignal, field string) (string, bool) {
	field = strings.ToLower(strings.TrimSpace(field))
	bySig, ok := exploreAttr[field]
	if !ok {
		return "", false
	}
	col, ok := bySig[signal]
	return col, ok
}

// ServeExploreFacets handles GET /api/explore/facets — value counts for Trace Explorer
// FacetSidebar chips (and other explore sidebars). Ported from agent Wave 14
// handleExploreFacets against the shared ClickHouse signal tables.
func (h *Handler) ServeExploreFacets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	signal, ok := parseExploreSignal(r.URL.Query().Get("signal"))
	if !ok {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "unknown signal")
		return
	}
	field := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("field")))
	if field == "" {
		field = "service"
	}
	col, ok := resolveExploreAttr(signal, field)
	if !ok {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "unknown field for signal")
		return
	}

	hours := clampInt(parseIntDefault(r.URL.Query().Get("hours"), 24), 1, 168)
	timeCol := exploreTimeColumn(signal)
	table := exploreTable(signal)
	scope := fmt.Sprintf("WHERE %s >= now() - INTERVAL %d HOUR AND %s", timeCol, hours, tenantWhere(r, ""))
	sql := fmt.Sprintf(`SELECT %s AS value, count() AS count
		FROM %s %s AND %s != ''
		GROUP BY value ORDER BY count DESC LIMIT 40`, col, table, scope, col)

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	facets := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		facets = append(facets, map[string]any{
			"value": asString(row, "value"),
			"count": asUint64(row, "count"),
		})
	}

	writeJSON(w, map[string]any{
		"signal": string(signal),
		"field":  field,
		"facets": facets,
		"source": "opa-hub",
	})
}
