package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// SyntheticCheck matches the edge agent / dashboard shape and opa.synthetic_checks.
type SyntheticCheck struct {
	ID                 string `json:"id"`
	OrganizationID     string `json:"organization_id"`
	ProjectID          string `json:"project_id"`
	Name               string `json:"name"`
	URL                string `json:"url"`
	Method             string `json:"method"`
	Headers            string `json:"headers"`
	IntervalSeconds    uint32 `json:"interval_seconds"`
	TimeoutMs          uint32 `json:"timeout_ms"`
	AssertStatus       uint16 `json:"assert_status"`
	AssertBodyContains string `json:"assert_body_contains"`
	AssertMaxLatencyMs uint32 `json:"assert_max_latency_ms"`
	Enabled            uint8  `json:"enabled"`
}

// ServeSynthetics handles GET/POST /api/synthetics.
// List/CRUD persist to central ClickHouse; probe workers remain on the edge agent.
func (h *Handler) ServeSynthetics(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listSynthetics(w, r)
	case http.MethodPost:
		var body SyntheticCheck
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		if strings.TrimSpace(body.URL) == "" {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "url is required")
			return
		}
		body.ID = fmt.Sprintf("synth-%d", time.Now().UnixNano())
		body.OrganizationID, body.ProjectID = writeOrgProject(r)
		normalizeSyntheticCheck(&body)
		if err := h.persistSynthetic(&body); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		writeJSON(w, body)
	default:
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET or POST required")
	}
}

// ServeSyntheticsSubpath handles /api/synthetics/{id} and /api/synthetics/{id}/results.
func (h *Handler) ServeSyntheticsSubpath(w http.ResponseWriter, r *http.Request) {
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/synthetics/")
	path = strings.Trim(path, "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "check id required")
		return
	}
	checkID := parts[0]

	current, err := h.getSynthetic(checkID, r)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}
	if current == nil {
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "check not found")
		return
	}

	if len(parts) >= 2 && parts[1] == "results" {
		if r.Method != http.MethodGet {
			openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
			return
		}
		where := fmt.Sprintf("WHERE check_id = '%s'%s", escapeSQL(checkID), tenantAnd(r, ""))
		if from := safeTimeLiteral(r.URL.Query().Get("from")); from != "" {
			where += fmt.Sprintf(" AND ts >= '%s'", escapeSQL(from))
		}
		if to := safeTimeLiteral(r.URL.Query().Get("to")); to != "" {
			where += fmt.Sprintf(" AND ts <= '%s'", escapeSQL(to))
		}
		sql := fmt.Sprintf(`SELECT toString(ts) AS ts, ok, status_code, latency_ms, error
			FROM opa.synthetic_results %s ORDER BY ts DESC LIMIT 500`, where)
		rows, qErr := h.Writer.Query(sql)
		if qErr != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", qErr.Error())
			return
		}
		results := make([]map[string]any, 0, len(rows))
		for _, row := range rows {
			results = append(results, map[string]any{
				"ts":          asString(row, "ts"),
				"ok":          int(asFloat64(row, "ok")),
				"status_code": int(asFloat64(row, "status_code")),
				"latency_ms":  asFloat64(row, "latency_ms"),
				"error":       asString(row, "error"),
			})
		}
		writeJSON(w, map[string]any{"results": results, "source": "opa-hub"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		writeJSON(w, current)
	case http.MethodPut:
		var body SyntheticCheck
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&body); err != nil {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", err.Error())
			return
		}
		body.ID = checkID
		body.OrganizationID = current.OrganizationID
		body.ProjectID = current.ProjectID
		if strings.TrimSpace(body.URL) == "" {
			body.URL = current.URL
		}
		normalizeSyntheticCheck(&body)
		if err := h.persistSynthetic(&body); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		writeJSON(w, body)
	case http.MethodDelete:
		if err := h.Writer.Exec(fmt.Sprintf(
			"ALTER TABLE opa.synthetic_checks DELETE WHERE id = '%s'", escapeSQL(checkID))); err != nil {
			openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

// ServeSyntheticsLocations handles GET /api/synthetics/locations.
func (h *Handler) ServeSyntheticsLocations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	// Probe workers run on edge agents; locations are informational placeholders.
	writeJSON(w, map[string]any{
		"locations": []map[string]any{
			{"id": "edge", "name": "Edge agent", "region": "edge"},
		},
		"source": "opa-hub",
	})
}

func (h *Handler) listSynthetics(w http.ResponseWriter, r *http.Request) {
	scope := tenantAnd(r, "")
	sql := fmt.Sprintf(`SELECT
		id, organization_id, project_id, name, url, method, headers,
		interval_seconds, timeout_ms, assert_status, assert_body_contains,
		assert_max_latency_ms, enabled
		FROM opa.synthetic_checks FINAL
		WHERE 1=1%s
		ORDER BY name`, scope)
	rows, err := h.Writer.Query(sql)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "query_error", err.Error())
		return
	}

	// Health rollup matches the edge agent / dashboard field names
	// (uptime_24h, avg_latency_ms_24h, last_ok, last_run, last_error).
	stats := map[string]map[string]any{}
	healthSQL := fmt.Sprintf(`SELECT check_id,
		100 * avgOrNull(ok) AS uptime,
		avgOrNull(latency_ms) AS avg_latency,
		argMax(ok, ts) AS last_ok,
		toString(max(ts)) AS last_run,
		argMax(error, ts) AS last_error
		FROM opa.synthetic_results
		WHERE ts >= now() - INTERVAL 24 HOUR%s
		GROUP BY check_id`, scope)
	if hRows, hErr := h.Writer.Query(healthSQL); hErr == nil {
		for _, row := range hRows {
			id := asString(row, "check_id")
			if id == "" {
				continue
			}
			stats[id] = map[string]any{
				"uptime_24h":         asFloat64(row, "uptime"),
				"avg_latency_ms_24h": asFloat64(row, "avg_latency"),
				"last_ok":            int64(asFloat64(row, "last_ok")),
				"last_run":           asString(row, "last_run"),
				"last_error":         asString(row, "last_error"),
			}
		}
	}

	checks := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		id := asString(row, "id")
		check := map[string]any{
			"id":                    id,
			"organization_id":       asString(row, "organization_id"),
			"project_id":            asString(row, "project_id"),
			"name":                  asString(row, "name"),
			"url":                   asString(row, "url"),
			"method":                asString(row, "method"),
			"headers":               asString(row, "headers"),
			"interval_seconds":      asUint64(row, "interval_seconds"),
			"timeout_ms":            asUint64(row, "timeout_ms"),
			"assert_status":         asUint64(row, "assert_status"),
			"assert_body_contains":  asString(row, "assert_body_contains"),
			"assert_max_latency_ms": asUint64(row, "assert_max_latency_ms"),
			"enabled":               asUint64(row, "enabled"),
		}
		if st, ok := stats[id]; ok {
			for k, v := range st {
				check[k] = v
			}
		} else {
			check["uptime_24h"] = nil
			check["avg_latency_ms_24h"] = nil
			check["last_ok"] = nil
			check["last_run"] = ""
			check["last_error"] = ""
		}
		checks = append(checks, check)
	}
	writeJSON(w, map[string]any{"checks": checks, "source": "opa-hub"})
}

func (h *Handler) getSynthetic(id string, r *http.Request) (*SyntheticCheck, error) {
	sql := fmt.Sprintf(`SELECT id, organization_id, project_id, name, url, method, headers,
		interval_seconds, timeout_ms, assert_status, assert_body_contains,
		assert_max_latency_ms, enabled
		FROM opa.synthetic_checks FINAL WHERE id = '%s'%s LIMIT 1`,
		escapeSQL(id), tenantAnd(r, ""))
	rows, err := h.Writer.Query(sql)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	row := rows[0]
	return &SyntheticCheck{
		ID:                 asString(row, "id"),
		OrganizationID:     asString(row, "organization_id"),
		ProjectID:          asString(row, "project_id"),
		Name:               asString(row, "name"),
		URL:                asString(row, "url"),
		Method:             asString(row, "method"),
		Headers:            asString(row, "headers"),
		IntervalSeconds:    uint32(asUint64(row, "interval_seconds")),
		TimeoutMs:          uint32(asUint64(row, "timeout_ms")),
		AssertStatus:       uint16(asUint64(row, "assert_status")),
		AssertBodyContains: asString(row, "assert_body_contains"),
		AssertMaxLatencyMs: uint32(asUint64(row, "assert_max_latency_ms")),
		Enabled:            uint8(asUint64(row, "enabled")),
	}, nil
}

func (h *Handler) persistSynthetic(c *SyntheticCheck) error {
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000")
	sql := fmt.Sprintf(`INSERT INTO opa.synthetic_checks (
		id, organization_id, project_id, name, url, method, headers,
		interval_seconds, timeout_ms, assert_status, assert_body_contains,
		assert_max_latency_ms, enabled, created_at, updated_at
	) VALUES (
		'%s','%s','%s','%s','%s','%s','%s',
		%d,%d,%d,'%s',%d,%d, parseDateTime64BestEffort('%s'), parseDateTime64BestEffort('%s')
	)`,
		escapeSQL(c.ID), escapeSQL(c.OrganizationID), escapeSQL(c.ProjectID),
		escapeSQL(c.Name), escapeSQL(c.URL), escapeSQL(c.Method), escapeSQL(c.Headers),
		c.IntervalSeconds, c.TimeoutMs, c.AssertStatus, escapeSQL(c.AssertBodyContains),
		c.AssertMaxLatencyMs, c.Enabled, escapeSQL(now), escapeSQL(now))
	return h.Writer.Exec(sql)
}

func normalizeSyntheticCheck(c *SyntheticCheck) {
	c.Name = strings.TrimSpace(c.Name)
	if c.Name == "" {
		c.Name = c.URL
	}
	c.Method = strings.ToUpper(strings.TrimSpace(c.Method))
	if c.Method == "" {
		c.Method = "GET"
	}
	if strings.TrimSpace(c.Headers) == "" {
		c.Headers = "{}"
	}
	if c.IntervalSeconds < 15 {
		c.IntervalSeconds = 60
	}
	if c.TimeoutMs == 0 || c.TimeoutMs > 60000 {
		c.TimeoutMs = 10000
	}
	if c.Enabled != 0 {
		c.Enabled = 1
	}
}
