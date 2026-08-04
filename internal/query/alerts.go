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

const alertTestRequestsDDL = `CREATE TABLE IF NOT EXISTS opa.alert_test_requests
(
    organization_id String DEFAULT '',
    project_id      String DEFAULT '',
    request_id      String,
    alert_id        String,
    requested_at    DateTime64(3) DEFAULT now64(3),
    updated_at      DateTime64(3) DEFAULT now64(3),
    status          String DEFAULT 'pending'
)
ENGINE = ReplacingMergeTree(updated_at)
ORDER BY (organization_id, project_id, request_id)
TTL toDateTime(requested_at) + INTERVAL 7 DAY`

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
		owned := opentenant.FromRequest(r).OwnedRowPredicate("")
		if err := h.Writer.Exec(fmt.Sprintf(
			"ALTER TABLE opa.alerts DELETE WHERE id = '%s' AND %s",
			escapeSQL(alertID), owned)); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodPost:
		// Manual Test: queue for edge delivery (force-fire), then wait briefly
		// for opa.alert_history so the dashboard Test button is synchronous when
		// an edge leader is healthy.
		a, err := h.getAlert(alertID, r)
		if err != nil || a == nil {
			openhttp.WriteError(w, http.StatusNotFound, "not_found", "alert not found")
			return
		}
		reqID, qerr := h.queueAlertTest(a)
		if qerr != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", qerr.Error())
			return
		}
		delivery, hist := h.waitAlertTestDelivery(alertID, reqID, 12*time.Second)
		resp := map[string]any{
			"status":     delivery,
			"request_id": reqID,
			"alert_id":   alertID,
			"source":     "opa-hub",
		}
		if delivery == "queued" {
			resp["note"] = "test queued for edge agent; history will appear when the leader delivers"
		}
		if hist != nil {
			resp["delivery"] = hist
		}
		writeJSON(w, resp)
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

func (h *Handler) ensureAlertTestRequestsTable() error {
	return h.Writer.Exec(alertTestRequestsDDL)
}

// queueAlertTest inserts a pending row for the edge alert worker to force-fire.
func (h *Handler) queueAlertTest(a *Alert) (string, error) {
	if err := h.ensureAlertTestRequestsTable(); err != nil {
		return "", err
	}
	reqID := fmt.Sprintf("test-%d", time.Now().UnixNano())
	sql := fmt.Sprintf(`INSERT INTO opa.alert_test_requests
		(organization_id, project_id, request_id, alert_id, requested_at, updated_at, status)
		VALUES ('%s','%s','%s','%s', now64(3), now64(3), 'pending')`,
		escapeSQL(a.OrganizationID), escapeSQL(a.ProjectID),
		escapeSQL(reqID), escapeSQL(a.ID))
	if err := h.Writer.Exec(sql); err != nil {
		return "", err
	}
	return reqID, nil
}

// waitAlertTestDelivery polls opa.alert_history for a Manual test row that
// references requestID. Returns "delivered" with the history map, or "queued".
func (h *Handler) waitAlertTestDelivery(alertID, requestID string, timeout time.Duration) (string, map[string]any) {
	deadline := time.Now().Add(timeout)
	needle := "request_id=" + requestID
	for time.Now().Before(deadline) {
		sql := fmt.Sprintf(`SELECT alert_id, alert_name, condition_type, value, threshold, operator, action_type, status, message, fired_at
			FROM opa.alert_history
			WHERE alert_id = '%s' AND position(message, '%s') > 0
			ORDER BY fired_at DESC LIMIT 1`,
			escapeSQL(alertID), escapeSQL(needle))
		rows, err := h.Writer.Query(sql)
		if err == nil && len(rows) > 0 {
			row := rows[0]
			return "delivered", map[string]any{
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
			}
		}
		time.Sleep(400 * time.Millisecond)
	}
	return "queued", nil
}
