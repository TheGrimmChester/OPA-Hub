package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
	opentenant "github.com/TheGrimmChester/open-tenant-go"
)

// SLO is a persisted service-level objective (matches OPA-Agent / dashboard shape).
type SLO struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Service     string  `json:"service"`
	SLOType     string  `json:"slo_type"`
	TargetValue float64 `json:"target_value"`
	WindowHours uint64  `json:"window_hours"`
	CreatedAt   string  `json:"created_at,omitempty"`
	UpdatedAt   string  `json:"updated_at,omitempty"`
}

// ServeSLOs handles GET/POST /api/slos.
// List/CRUD persist to central ClickHouse; the edge agent evaluates compliance
// into opa.slo_metrics (same pattern as alert rules).
func (h *Handler) ServeSLOs(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		where := " WHERE " + tenantWhere(r, "")
		sql := `SELECT id, name, description, service, slo_type, target_value, window_hours, created_at, updated_at
			FROM opa.slos` + where + ` ORDER BY created_at DESC`
		rows, err := h.Writer.Query(sql)
		if err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		slos := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			slos = append(slos, map[string]any{
				"id":           asString(row, "id"),
				"name":         asString(row, "name"),
				"description":  asString(row, "description"),
				"service":      asString(row, "service"),
				"slo_type":     asString(row, "slo_type"),
				"target_value": asFloat64(row, "target_value"),
				"window_hours": asUint64(row, "window_hours"),
				"created_at":   asString(row, "created_at"),
				"updated_at":   asString(row, "updated_at"),
			})
		}
		writeJSON(w, map[string]any{"slos": slos, "source": "opa-hub"})
	case http.MethodPost:
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		sloID := fmt.Sprintf("slo-%d", time.Now().UnixNano())
		if id, ok := body["id"].(string); ok && id != "" {
			sloID = id
		}
		org, proj := writeOrgProject(r)
		name := mapString(body, "name")
		desc := mapString(body, "description")
		service := mapString(body, "service")
		sloType := mapString(body, "slo_type")
		target := mapFloat(body, "target_value")
		window := mapUint(body, "window_hours")
		sql := fmt.Sprintf(`INSERT INTO opa.slos (organization_id, project_id, id, name, description, service, slo_type, target_value, window_hours) VALUES ('%s', '%s', '%s', '%s', '%s', '%s', '%s', %f, %d)`,
			escapeSQL(org), escapeSQL(proj), escapeSQL(sloID),
			escapeSQL(name), escapeSQL(desc), escapeSQL(service),
			escapeSQL(sloType), target, window)
		if err := h.Writer.Exec(sql); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		writeJSON(w, map[string]any{
			"id":           sloID,
			"name":         name,
			"description":  desc,
			"service":      service,
			"slo_type":     sloType,
			"target_value": target,
			"window_hours": window,
			"source":       "opa-hub",
		})
	default:
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required")
	}
}

// ServeSLOsSubpath handles /api/slos/{id} and /api/slos/{id}/compliance.
func (h *Handler) ServeSLOsSubpath(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/slos/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "slo id required")
		return
	}
	sloID := parts[0]

	if len(parts) >= 2 && parts[1] == "compliance" {
		if r.Method != http.MethodGet {
			openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
			return
		}
		sql := fmt.Sprintf(`SELECT
			actual_value, compliance_percentage, is_breach, error_budget_remaining, burn_rate, window_start, window_end
			FROM opa.slo_metrics
			WHERE slo_id = '%s'%s
			ORDER BY window_start DESC LIMIT 30`,
			escapeSQL(sloID), tenantAnd(r, ""))
		rows, err := h.Writer.Query(sql)
		if err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		metrics := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			metrics = append(metrics, map[string]any{
				"actual_value":           asFloat64(row, "actual_value"),
				"compliance_percentage":  asFloat64(row, "compliance_percentage"),
				"is_breach":              asUint64(row, "is_breach") > 0,
				"error_budget_remaining": asFloat64(row, "error_budget_remaining"),
				"burn_rate":              asFloat64(row, "burn_rate"),
				"window_start":           asString(row, "window_start"),
				"window_end":             asString(row, "window_end"),
			})
		}
		writeJSON(w, map[string]any{"metrics": metrics, "source": "opa-hub"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		sql := fmt.Sprintf(`SELECT id, name, description, service, slo_type, target_value, window_hours
			FROM opa.slos WHERE id = '%s'%s LIMIT 1`, escapeSQL(sloID), tenantAnd(r, ""))
		rows, err := h.Writer.Query(sql)
		if err != nil || len(rows) == 0 {
			openhttp.WriteError(w, http.StatusNotFound, "not_found", "slo not found")
			return
		}
		row := rows[0]
		writeJSON(w, map[string]any{
			"id":           asString(row, "id"),
			"name":         asString(row, "name"),
			"description":  asString(row, "description"),
			"service":      asString(row, "service"),
			"slo_type":     asString(row, "slo_type"),
			"target_value": asFloat64(row, "target_value"),
			"window_hours": asUint64(row, "window_hours"),
			"source":       "opa-hub",
		})
	case http.MethodPut:
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		sql := fmt.Sprintf(`ALTER TABLE opa.slos UPDATE
			name = '%s', description = '%s', service = '%s', slo_type = '%s',
			target_value = %f, window_hours = %d, updated_at = now()
			WHERE id = '%s'`,
			escapeSQL(mapString(body, "name")),
			escapeSQL(mapString(body, "description")),
			escapeSQL(mapString(body, "service")),
			escapeSQL(mapString(body, "slo_type")),
			mapFloat(body, "target_value"),
			mapUint(body, "window_hours"),
			escapeSQL(sloID),
		)
		if err := h.Writer.Exec(sql); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		writeJSON(w, map[string]any{"status": "updated", "source": "opa-hub"})
	case http.MethodDelete:
		owned := opentenant.FromRequest(r).OwnedRowPredicate("")
		if err := h.Writer.Exec(fmt.Sprintf(
			"ALTER TABLE opa.slos DELETE WHERE id = '%s' AND %s",
			escapeSQL(sloID), owned)); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func mapString(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch t := v.(type) {
	case string:
		return t
	default:
		return fmt.Sprint(t)
	}
}

func mapFloat(m map[string]any, key string) float64 {
	v, ok := m[key]
	if !ok || v == nil {
		return 0
	}
	switch t := v.(type) {
	case float64:
		return t
	case json.Number:
		n, _ := t.Float64()
		return n
	case int:
		return float64(t)
	case int64:
		return float64(t)
	default:
		n, _ := json.Number(fmt.Sprint(t)).Float64()
		return n
	}
}

func mapUint(m map[string]any, key string) uint64 {
	return uint64(mapFloat(m, key))
}
