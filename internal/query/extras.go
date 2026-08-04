package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// ServeProfiles handles GET /api/profiles — top functions by self-time.
func (h *Handler) ServeProfiles(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	filter := " WHERE " + tenantWhere(r, "")
	if s := r.URL.Query().Get("service"); s != "" {
		filter += fmt.Sprintf(" AND service = '%s'", escapeSQL(s))
	}
	if from := safeTimeLiteral(r.URL.Query().Get("from")); from != "" {
		filter += fmt.Sprintf(" AND hour >= '%s'", escapeSQL(from))
	}
	if to := safeTimeLiteral(r.URL.Query().Get("to")); to != "" {
		filter += fmt.Sprintf(" AND hour <= '%s'", escapeSQL(to))
	}
	limit := 100
	if l := r.URL.Query().Get("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 1000 {
			limit = n
		}
	}
	sql := fmt.Sprintf(`SELECT
		service, function,
		sum(call_count) as call_count,
		sum(total_wall_ms) as total_wall_ms,
		sum(self_wall_ms) as self_wall_ms,
		sum(total_cpu_ms) as total_cpu_ms,
		sum(memory_delta) as memory_delta
		FROM opa.profiles%s
		GROUP BY service, function
		ORDER BY self_wall_ms DESC
		LIMIT %d`, filter, limit)
	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	funcs := make([]map[string]any, 0, len(rows))
	var totalSelf float64
	for _, row := range rows {
		self := asFloat64(row, "self_wall_ms")
		totalSelf += self
		funcs = append(funcs, map[string]any{
			"service":       asString(row, "service"),
			"function":      asString(row, "function"),
			"call_count":    asUint64(row, "call_count"),
			"total_wall_ms": asFloat64(row, "total_wall_ms"),
			"self_wall_ms":  self,
			"total_cpu_ms":  asFloat64(row, "total_cpu_ms"),
			"memory_delta":  asFloat64(row, "memory_delta"),
		})
	}
	for _, f := range funcs {
		if totalSelf > 0 {
			f["self_pct"] = f["self_wall_ms"].(float64) / totalSelf * 100
		} else {
			f["self_pct"] = 0.0
		}
	}
	writeJSON(w, map[string]any{
		"functions":          funcs,
		"total_self_wall_ms": totalSelf,
		"source":             "opa-hub",
	})
}

// ServeErrors handles GET /api/errors — grouped error inbox list.
func (h *Handler) ServeErrors(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	limit, offset := parseLimitOffset(r, 100, 500)
	statusFilter := r.URL.Query().Get("status")
	service := r.URL.Query().Get("service")

	baseWhere := "WHERE 1=1" + tenantAnd(r, "ei.")
	egsScope := ""
	if scope := tenantAnd(r, ""); scope != "" {
		egsScope = " WHERE 1=1" + scope
	}
	if service != "" && service != "unknown" {
		baseWhere += fmt.Sprintf(" AND ei.service = '%s'", escapeSQL(service))
	}
	if statusFilter == "unresolved" {
		baseWhere += " AND coalesce(nullif(egs.status, ''), 'unresolved') = 'unresolved'"
	} else if statusFilter == "resolved" || statusFilter == "ignored" {
		baseWhere += fmt.Sprintf(" AND egs.status = '%s'", escapeSQL(statusFilter))
	}
	baseWhere += timeCompareSQL("ei.occurred_at", ">=", r.URL.Query().Get("from"))
	if r.URL.Query().Get("from") == "" {
		baseWhere += " AND ei.occurred_at >= now() - INTERVAL 7 DAY"
	}
	baseWhere += timeCompareSQL("ei.occurred_at", "<=", r.URL.Query().Get("to"))

	sql := fmt.Sprintf(`SELECT
		ei.group_id as group_id,
		any(ei.error_type) as error_type,
		any(ei.error_message) as error_message,
		any(ei.service) as service,
		count(*) as count,
		min(ei.occurred_at) as first_seen,
		max(ei.occurred_at) as last_seen,
		coalesce(nullif(egs.status, ''), 'unresolved') as status,
		egs.assigned_to as assigned_to
		FROM opa.error_instances ei
		LEFT JOIN (SELECT group_id, status, assigned_to FROM opa.error_group_status FINAL%s) egs ON ei.group_id = egs.group_id
		%s
		GROUP BY ei.group_id, egs.status, egs.assigned_to
		ORDER BY last_seen DESC
		LIMIT %d OFFSET %d`, egsScope, baseWhere, limit, offset)

	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	errors := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		gid := asString(row, "group_id")
		errors = append(errors, map[string]any{
			"id":            gid,
			"error_id":      gid,
			"group_id":      gid,
			"error_type":    asString(row, "error_type"),
			"error_message": asString(row, "error_message"),
			"service":       asString(row, "service"),
			"count":         asUint64(row, "count"),
			"first_seen":    asString(row, "first_seen"),
			"last_seen":     asString(row, "last_seen"),
			"status":        asString(row, "status"),
			"assigned_to":   asStringPtr(row, "assigned_to"),
		})
	}
	writeJSON(w, map[string]any{"errors": errors, "count": len(errors), "source": "opa-hub"})
}

// ServeErrorsSubpath handles:
//   GET  /api/errors/{group_id} — error detail
//   POST /api/errors/groups/{group_id}/status|assign — persist to opa.error_group_status
func (h *Handler) ServeErrorsSubpath(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/errors/"), "/")
	if rest == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "error id required")
		return
	}

	// Mutations: /api/errors/groups/{group_id}/{status|assign}
	if strings.HasPrefix(rest, "groups/") {
		if r.Method != http.MethodPost {
			openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
			return
		}
		parts := strings.Split(rest, "/")
		// groups / {id} / {action}
		if len(parts) < 3 || parts[1] == "" {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "group id and action required")
			return
		}
		groupID := parts[1]
		action := parts[2]
		var body struct {
			Status     string  `json:"status"`
			AssignedTo *string `json:"assigned_to"`
		}
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body)
		switch action {
		case "status":
			if body.Status != "unresolved" && body.Status != "resolved" && body.Status != "ignored" {
				openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "invalid status")
				return
			}
			h.writeErrorGroupStatus(w, r, groupID, body.Status, nil)
		case "assign":
			h.writeErrorGroupStatus(w, r, groupID, "", body.AssignedTo)
		default:
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "unknown action")
		}
		return
	}

	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	groupID := rest
	if strings.Contains(groupID, "/") {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "error id required")
		return
	}
	errWhere := fmt.Sprintf("group_id = '%s'", escapeSQL(groupID)) + tenantAnd(r, "")

	sql := fmt.Sprintf(`SELECT
		any(error_type) as error_type,
		any(error_message) as error_message,
		any(service) as service,
		count() as count,
		min(occurred_at) as first_seen,
		max(occurred_at) as last_seen
		FROM opa.error_instances WHERE %s`, errWhere)
	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	if len(rows) == 0 || asUint64(rows[0], "count") == 0 {
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "error not found")
		return
	}
	row := rows[0]

	stackTrace := []any{}
	stackRows, _ := h.Writer.Query(fmt.Sprintf(`SELECT stack_trace FROM opa.error_instances
		WHERE %s AND stack_trace != '' LIMIT 1`, errWhere))
	if len(stackRows) > 0 {
		if s := asString(stackRows[0], "stack_trace"); s != "" {
			if err := json.Unmarshal([]byte(s), &stackTrace); err != nil {
				stackTrace = []any{s}
			}
		}
	}

	traceRows, _ := h.Writer.Query(fmt.Sprintf(`SELECT trace_id, max(occurred_at) as occurred_at
		FROM opa.error_instances WHERE %s AND trace_id != ''
		GROUP BY trace_id ORDER BY occurred_at DESC LIMIT 10`, errWhere))
	relatedTraces := make([]map[string]any, 0, len(traceRows))
	for _, tRow := range traceRows {
		relatedTraces = append(relatedTraces, map[string]any{
			"trace_id":    asString(tRow, "trace_id"),
			"occurred_at": asString(tRow, "occurred_at"),
		})
	}

	trendRows, _ := h.Writer.Query(fmt.Sprintf(`SELECT
		toStartOfHour(occurred_at) as time, count() as count
		FROM opa.error_instances WHERE %s AND occurred_at >= now() - INTERVAL 7 DAY
		GROUP BY time ORDER BY time`, errWhere))
	trends := make([]map[string]any, 0, len(trendRows))
	for _, tRow := range trendRows {
		trends = append(trends, map[string]any{
			"time":  asString(tRow, "time"),
			"count": asUint64(tRow, "count"),
		})
	}

	status := "unresolved"
	var assignedTo any
	statusSQL := fmt.Sprintf(`SELECT status, assigned_to FROM opa.error_group_status FINAL
		WHERE group_id = '%s'%s LIMIT 1`, escapeSQL(groupID), tenantAnd(r, ""))
	if sr, _ := h.Writer.Query(statusSQL); len(sr) > 0 {
		if s := asString(sr[0], "status"); s != "" {
			status = s
		}
		assignedTo = asStringPtr(sr[0], "assigned_to")
	}

	writeJSON(w, map[string]any{
		"error_id":       groupID,
		"group_id":       groupID,
		"error_type":     asString(row, "error_type"),
		"error_message":  asString(row, "error_message"),
		"service":        asString(row, "service"),
		"count":          asUint64(row, "count"),
		"first_seen":     asString(row, "first_seen"),
		"last_seen":      asString(row, "last_seen"),
		"status":         status,
		"assigned_to":    assignedTo,
		"stack_trace":    stackTrace,
		"related_traces": relatedTraces,
		"trends":         trends,
		"source":         "opa-hub",
	})
}

// writeErrorGroupStatus inserts into opa.error_group_status (ReplacingMergeTree).
func (h *Handler) writeErrorGroupStatus(w http.ResponseWriter, r *http.Request, groupID, status string, assignedTo *string) {
	org, proj := writeOrgProject(r)
	assignVal := "NULL"
	if assignedTo != nil {
		assignVal = fmt.Sprintf("'%s'", escapeSQL(*assignedTo))
	}
	curStatus, curAssign := "unresolved", "NULL"
	seedSQL := fmt.Sprintf(`SELECT status, assigned_to FROM opa.error_group_status FINAL
		WHERE group_id = '%s'%s LIMIT 1`, escapeSQL(groupID), tenantAnd(r, ""))
	if sr, _ := h.Writer.Query(seedSQL); len(sr) > 0 {
		if s := asString(sr[0], "status"); s != "" {
			curStatus = s
		}
		if a := asString(sr[0], "assigned_to"); a != "" {
			curAssign = fmt.Sprintf("'%s'", escapeSQL(a))
		}
	}
	if status == "" {
		status = curStatus
	}
	if assignedTo == nil {
		assignVal = curAssign
	}
	sql := fmt.Sprintf(`INSERT INTO opa.error_group_status (organization_id, project_id, group_id, status, assigned_to, updated_at)
		VALUES ('%s','%s','%s','%s',%s, now64(3))`,
		escapeSQL(org), escapeSQL(proj), escapeSQL(groupID), escapeSQL(status), assignVal)
	if err := h.Writer.Exec(sql); err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	writeJSON(w, map[string]any{"group_id": groupID, "status": status, "source": "opa-hub"})
}
