package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// Alert is a persisted alert rule (matches OPA-Agent / dashboard shape).
type Alert struct {
	ID              string         `json:"id"`
	Name            string         `json:"name"`
	Description     string         `json:"description"`
	Enabled         bool           `json:"enabled"`
	ConditionType   string         `json:"condition_type"`
	ConditionConfig map[string]any `json:"condition_config"`
	ActionType      string         `json:"action_type"`
	ActionConfig    map[string]any `json:"action_config"`
	Service         *string        `json:"service,omitempty"`
	OrganizationID  string         `json:"organization_id"`
	ProjectID       string         `json:"project_id"`
}

// ServeAlerts handles GET/POST /api/alerts.
func (h *Handler) ServeAlerts(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		where := "WHERE " + tenantWhere(r, "")
		sql := `SELECT id, name, description, enabled, condition_type, condition_config, action_type, action_config, service, organization_id, project_id
			FROM opa.alerts FINAL ` + where + ` ORDER BY updated_at DESC`
		rows, err := h.Writer.Query(sql)
		if err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		alerts := make([]*Alert, 0, len(rows))
		for _, row := range rows {
			a := alertFromRow(row)
			if a.ID == "" {
				continue
			}
			alerts = append(alerts, a)
		}
		writeJSON(w, map[string]any{"alerts": alerts, "source": "opa-hub"})
	case http.MethodPost:
		var alert Alert
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&alert); err != nil {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if alert.ID == "" {
			alert.ID = fmt.Sprintf("alert-%d", time.Now().UnixNano())
		}
		alert.OrganizationID, alert.ProjectID = writeOrgProject(r)
		if err := h.persistAlert(&alert); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		writeJSON(w, alert)
	default:
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required")
	}
}

// ServeAlertsSubpath handles /api/alerts/{id} and /api/alerts/{id}/history.
func (h *Handler) ServeAlertsSubpath(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/alerts/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "alert id required")
		return
	}
	alertID := parts[0]

	if r.Method == http.MethodGet && len(parts) >= 2 && parts[1] == "history" {
		sql := fmt.Sprintf(`SELECT alert_id, alert_name, condition_type, value, threshold, operator, action_type, status, message, fired_at
			FROM opa.alert_history WHERE alert_id = '%s'%s ORDER BY fired_at DESC LIMIT 100`,
			escapeSQL(alertID), tenantAnd(r, ""))
		rows, err := h.Writer.Query(sql)
		if err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		history := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			history = append(history, map[string]any{
				"alert_id":       asString(row, "alert_id"),
				"alert_name":     asString(row, "alert_name"),
				"condition_type": asString(row, "condition_type"),
				"value":          asFloat64(row, "value"),
				"threshold":      asFloat64(row, "threshold"),
				"operator":       asString(row, "operator"),
				"action_type":    asString(row, "action_type"),
				"status":         asString(row, "status"),
				"message":        asString(row, "message"),
				"fired_at":       asString(row, "fired_at"),
			})
		}
		writeJSON(w, map[string]any{"history": history, "source": "opa-hub"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		a, err := h.getAlert(alertID, r)
		if err != nil || a == nil {
			openhttp.WriteError(w, http.StatusNotFound, "not_found", "alert not found")
			return
		}
		writeJSON(w, a)
	case http.MethodPut:
		var alert Alert
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&alert); err != nil {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		alert.ID = alertID
		existing, _ := h.getAlert(alertID, r)
		if existing != nil {
			alert.OrganizationID = existing.OrganizationID
			alert.ProjectID = existing.ProjectID
		} else {
			alert.OrganizationID, alert.ProjectID = writeOrgProject(r)
		}
		if err := h.persistAlert(&alert); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		writeJSON(w, alert)
	case http.MethodDelete:
		if err := h.Writer.Exec(fmt.Sprintf("ALTER TABLE opa.alerts DELETE WHERE id = '%s'", escapeSQL(alertID))); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		// Manual check: hub persists rules; edge agent still evaluates them.
		a, err := h.getAlert(alertID, r)
		if err != nil || a == nil {
			openhttp.WriteError(w, http.StatusNotFound, "not_found", "alert not found")
			return
		}
		writeJSON(w, map[string]any{"status": "accepted", "note": "rule persisted on hub; evaluation runs on edge agent", "source": "opa-hub"})
	default:
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func alertFromRow(row map[string]any) *Alert {
	a := &Alert{
		ID:             asString(row, "id"),
		Name:           asString(row, "name"),
		Description:    asString(row, "description"),
		Enabled:        asUint64(row, "enabled") > 0,
		ConditionType:  asString(row, "condition_type"),
		ActionType:     asString(row, "action_type"),
		OrganizationID: asString(row, "organization_id"),
		ProjectID:      asString(row, "project_id"),
	}
	_ = json.Unmarshal([]byte(asString(row, "condition_config")), &a.ConditionConfig)
	_ = json.Unmarshal([]byte(asString(row, "action_config")), &a.ActionConfig)
	if svc := asString(row, "service"); svc != "" {
		s := svc
		a.Service = &s
	}
	return a
}

func (h *Handler) getAlert(id string, r *http.Request) (*Alert, error) {
	sql := fmt.Sprintf(`SELECT id, name, description, enabled, condition_type, condition_config, action_type, action_config, service, organization_id, project_id
		FROM opa.alerts FINAL WHERE id = '%s'%s LIMIT 1`, escapeSQL(id), tenantAnd(r, ""))
	rows, err := h.Writer.Query(sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return alertFromRow(rows[0]), nil
}

func (h *Handler) persistAlert(a *Alert) error {
	condJSON, _ := json.Marshal(a.ConditionConfig)
	actJSON, _ := json.Marshal(a.ActionConfig)
	enabled := 0
	if a.Enabled {
		enabled = 1
	}
	svc := ""
	if a.Service != nil {
		svc = *a.Service
	}
	sql := fmt.Sprintf(`INSERT INTO opa.alerts (organization_id, project_id, id, name, description, enabled, condition_type, condition_config, action_type, action_config, service, updated_at) VALUES ('%s','%s','%s','%s','%s',%d,'%s','%s','%s','%s','%s', now64(3))`,
		escapeSQL(a.OrganizationID), escapeSQL(a.ProjectID),
		escapeSQL(a.ID), escapeSQL(a.Name), escapeSQL(a.Description), enabled,
		escapeSQL(a.ConditionType), escapeSQL(string(condJSON)),
		escapeSQL(a.ActionType), escapeSQL(string(actJSON)), escapeSQL(svc))
	return h.Writer.Exec(sql)
}
