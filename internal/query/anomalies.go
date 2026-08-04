package query

import (
	"encoding/json"
	"fmt"
	"net/http"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// ServeAnomalies handles GET /api/anomalies — list detections from opa.anomalies.
// On-demand /api/anomalies/analyze and the periodic detector remain on the edge agent.
func (h *Handler) ServeAnomalies(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	service := r.URL.Query().Get("service")
	severity := r.URL.Query().Get("severity")
	baseWhere := "WHERE 1=1" + tenantAnd(r, "")

	if service != "" {
		baseWhere += fmt.Sprintf(" AND service = '%s'", escapeSQL(service))
	}
	if severity != "" {
		baseWhere += fmt.Sprintf(" AND severity = '%s'", escapeSQL(severity))
	}
	if from := safeTimeLiteral(r.URL.Query().Get("from")); from != "" {
		baseWhere += fmt.Sprintf(" AND detected_at >= '%s'", escapeSQL(from))
	} else {
		baseWhere += " AND detected_at >= now() - INTERVAL 24 HOUR"
	}
	if to := safeTimeLiteral(r.URL.Query().Get("to")); to != "" {
		baseWhere += fmt.Sprintf(" AND detected_at <= '%s'", escapeSQL(to))
	}

	sql := fmt.Sprintf(`SELECT id, type, service, metric, value, expected, score, severity, detected_at, metadata
		FROM opa.anomalies %s ORDER BY detected_at DESC LIMIT 100`, baseWhere)
	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	anomalies := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var metadata map[string]any
		if s := asString(row, "metadata"); s != "" {
			_ = json.Unmarshal([]byte(s), &metadata)
		}
		anomalies = append(anomalies, map[string]any{
			"id":          asString(row, "id"),
			"type":        asString(row, "type"),
			"service":     asString(row, "service"),
			"metric":      asString(row, "metric"),
			"value":       asFloat64(row, "value"),
			"expected":    asFloat64(row, "expected"),
			"score":       asFloat64(row, "score"),
			"severity":    asString(row, "severity"),
			"detected_at": asString(row, "detected_at"),
			"metadata":    metadata,
		})
	}
	writeJSON(w, map[string]any{"anomalies": anomalies, "source": "opa-hub"})
}
