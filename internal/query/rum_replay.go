package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// ServeRUMReplay handles GET /api/rum/replay/{session_id} — raw masked chunk list.
// POST /api/rum/replay (chunk ingest) remains on the edge agent.
func (h *Handler) ServeRUMReplay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	sessionID := pathTail(r.URL.Path, "/api/rum/replay/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "session id required")
		return
	}
	scope := fmt.Sprintf("WHERE session_id = '%s'", escapeSQL(sessionID)) + tenantAnd(r, "")
	sql := fmt.Sprintf(`SELECT chunk_index, events, masked, occurred_at, bytes
		FROM opa.rum_replay_chunks %s ORDER BY chunk_index ASC LIMIT 500`, scope)
	rows, err := h.Writer.Query(sql)
	if err != nil {
		// Table may not exist yet on older ClickHouse installs — return empty chunks.
		writeJSON(w, map[string]any{"session_id": sessionID, "chunks": []any{}, "source": "opa-hub"})
		return
	}
	chunks := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		chunks = append(chunks, map[string]any{
			"chunk_index": asInt(row, "chunk_index"),
			"events":      asString(row, "events"),
			"masked":      asBoolish(row, "masked"),
			"occurred_at": asString(row, "occurred_at"),
			"bytes":       asInt(row, "bytes"),
		})
	}
	writeJSON(w, map[string]any{"session_id": sessionID, "chunks": chunks, "source": "opa-hub"})
}

// ServeRUMReplayTimeline handles GET /api/rum/replay-timeline/{session_id}.
// Flattens masked DOM event-log chunks into an ordered scrubber timeline.
func (h *Handler) ServeRUMReplayTimeline(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	sessionID := pathTail(r.URL.Path, "/api/rum/replay-timeline/")
	if sessionID == "" || strings.Contains(sessionID, "/") {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "session id required")
		return
	}
	scope := fmt.Sprintf("WHERE session_id = '%s'", escapeSQL(sessionID)) + tenantAnd(r, "")
	sql := fmt.Sprintf(`SELECT chunk_index, events, masked, occurred_at
		FROM opa.rum_replay_chunks %s ORDER BY chunk_index ASC LIMIT 500`, scope)
	rows, err := h.Writer.Query(sql)
	if err != nil {
		writeJSON(w, emptyReplayTimeline(sessionID, 0))
		return
	}

	events := make([]replayEvent, 0, 64)
	for _, row := range rows {
		chunkIdx := asInt(row, "chunk_index")
		fallbackMs := parseOccurredAtMs(asString(row, "occurred_at"))
		parsed := parseReplayEventsJSON(asString(row, "events"))
		for _, m := range parsed {
			events = append(events, mapReplayEvent(m, chunkIdx, fallbackMs))
		}
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].T == events[j].T {
			return events[i].Chunk < events[j].Chunk
		}
		return events[i].T < events[j].T
	})
	byType := map[string]int{}
	for _, e := range events {
		byType[e.Type]++
	}
	writeJSON(w, map[string]any{
		"session_id":   sessionID,
		"masked":       true, // beacon always masks; honesty for the SessionReplayPlayer
		"honesty":      "masked DOM event log — not pixel-perfect rrweb / commercial session replay",
		"event_count":  len(events),
		"events":       events,
		"chunk_count":  len(rows),
		"by_type":      byType,
		"marker_types": []string{"snapshot", "navigation", "mutation", "click", "input", "longtask", "resource", "ajax"},
		"source":       "opa-hub",
	})
}

// ServeRUMMobileSessions handles GET /api/rum/mobile/sessions.
func (h *Handler) ServeRUMMobileSessions(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		writeJSON(w, map[string]any{"sessions": []any{}, "source": "opa-hub"})
		return
	}
	scope := "WHERE occurred_at >= now() - INTERVAL 7 DAY" + tenantAnd(r, "")
	if sid := r.URL.Query().Get("session_id"); sid != "" {
		scope += fmt.Sprintf(" AND session_id = '%s'", escapeSQL(sid))
	}
	if p := r.URL.Query().Get("platform"); p != "" {
		scope += fmt.Sprintf(" AND platform = '%s'", escapeSQL(p))
	}
	sql := fmt.Sprintf(`SELECT session_id, platform, count() AS crashes, max(occurred_at) AS last_seen,
		any(app_version) AS app_version, any(device_model) AS device_model
		FROM opa.mobile_crashes %s AND session_id != ''
		GROUP BY session_id, platform
		ORDER BY last_seen DESC LIMIT 100`, scope)
	rows, err := h.Writer.Query(sql)
	if err != nil {
		writeJSON(w, map[string]any{
			"sessions": []any{},
			"note":     "Mobile crash sessions — link via ?session_id= on /api/mobile/crashes",
			"source":   "opa-hub",
		})
		return
	}
	sessions := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		sessions = append(sessions, map[string]any{
			"session_id":   asString(row, "session_id"),
			"platform":     asString(row, "platform"),
			"crashes":      asUint64(row, "crashes"),
			"last_seen":    asString(row, "last_seen"),
			"app_version":  asString(row, "app_version"),
			"device_model": asString(row, "device_model"),
		})
	}
	writeJSON(w, map[string]any{
		"sessions": sessions,
		"note":     "Mobile crash sessions — link via ?session_id= on /api/mobile/crashes",
		"source":   "opa-hub",
	})
}

// ServeMobileCrashes handles GET /api/mobile/crashes — crash list / session detail.
// POST ingest remains on the edge agent.
func (h *Handler) ServeMobileCrashes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}
	scope := "WHERE occurred_at >= now() - INTERVAL 7 DAY" + tenantAnd(r, "")
	if p := r.URL.Query().Get("platform"); p != "" {
		scope += fmt.Sprintf(" AND platform = '%s'", escapeSQL(p))
	}
	sid := r.URL.Query().Get("session_id")
	if sid != "" {
		scope += fmt.Sprintf(" AND session_id = '%s'", escapeSQL(sid))
	}

	var sql string
	var cols []string
	if sid != "" {
		sql = fmt.Sprintf(`SELECT session_id, platform, crash_type, exception_name, exception_message,
			app_version, device_model, os_version, trace_id, occurred_at
			FROM opa.mobile_crashes %s
			ORDER BY occurred_at DESC LIMIT 100`, scope)
		cols = []string{"session_id", "platform", "crash_type", "exception_name", "exception_message",
			"app_version", "device_model", "os_version", "trace_id", "occurred_at"}
	} else {
		sql = fmt.Sprintf(`SELECT platform, crash_type, exception_name, app_version, count() AS count, max(occurred_at) AS last_seen
			FROM opa.mobile_crashes %s
			GROUP BY platform, crash_type, exception_name, app_version
			ORDER BY count DESC LIMIT 100`, scope)
		cols = []string{"platform", "crash_type", "exception_name", "app_version", "count", "last_seen"}
	}
	rows, err := h.Writer.Query(sql)
	if err != nil {
		writeJSON(w, map[string]any{"crashes": []any{}, "source": "opa-hub"})
		return
	}
	crashes := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		m := map[string]any{}
		for _, c := range cols {
			m[c] = row[c]
		}
		crashes = append(crashes, m)
	}
	writeJSON(w, map[string]any{"crashes": crashes, "source": "opa-hub"})
}

type replayEvent struct {
	T            int64          `json:"t"`
	Type         string         `json:"type"`
	Target       string         `json:"target,omitempty"`
	Value        string         `json:"value,omitempty"`
	Mutation     string         `json:"mutation,omitempty"`
	Added        int            `json:"added,omitempty"`
	Removed      int            `json:"removed,omitempty"`
	URL          string         `json:"url,omitempty"`
	Title        string         `json:"title,omitempty"`
	Name         string         `json:"name,omitempty"`
	DurationMs   float64        `json:"duration_ms,omitempty"`
	TransferSize int64          `json:"transfer_size,omitempty"`
	Method       string         `json:"method,omitempty"`
	Status       int            `json:"status,omitempty"`
	TextContent  string         `json:"textContent,omitempty"`
	InnerHTML    string         `json:"innerHTML,omitempty"`
	Chunk        int            `json:"chunk_index"`
	Raw          map[string]any `json:"raw,omitempty"`
}

func emptyReplayTimeline(sessionID string, chunkCount int) map[string]any {
	return map[string]any{
		"session_id":   sessionID,
		"masked":       true,
		"honesty":      "masked DOM event log — not pixel-perfect rrweb / commercial session replay",
		"event_count":  0,
		"events":       []any{},
		"chunk_count":  chunkCount,
		"by_type":      map[string]int{},
		"marker_types": []string{"snapshot", "navigation", "mutation", "click", "input", "longtask", "resource", "ajax"},
		"source":       "opa-hub",
	}
}

func pathTail(path, prefix string) string {
	return strings.Trim(strings.TrimPrefix(path, prefix), "/")
}

func parseReplayEventsJSON(raw string) []map[string]any {
	if raw == "" {
		return nil
	}
	var parsed []map[string]any
	if json.Unmarshal([]byte(raw), &parsed) == nil {
		return parsed
	}
	var wrapped string
	if json.Unmarshal([]byte(raw), &wrapped) == nil && wrapped != "" {
		_ = json.Unmarshal([]byte(wrapped), &parsed)
	}
	return parsed
}

func mapReplayEvent(m map[string]any, chunkIdx int, fallbackMs int64) replayEvent {
	e := replayEvent{Chunk: chunkIdx, Raw: m}
	e.Type = strFromMap(m, "type")
	if e.Type == "navigate" {
		e.Type = "navigation"
	}
	e.Target = strFromMap(m, "target")
	e.Value = strFromMap(m, "value")
	e.Mutation = strFromMap(m, "mutation")
	e.URL = strFromMap(m, "url")
	e.Title = strFromMap(m, "title")
	e.Name = strFromMap(m, "name")
	e.Method = strFromMap(m, "method")
	e.TextContent = strFromMap(m, "textContent", "text_content")
	e.InnerHTML = strFromMap(m, "innerHTML", "inner_html")
	if n, ok := asMapFloat(m, "added"); ok {
		e.Added = int(n)
	}
	if n, ok := asMapFloat(m, "removed"); ok {
		e.Removed = int(n)
	}
	if n, ok := asMapFloat(m, "duration_ms"); ok {
		e.DurationMs = n
	} else if n, ok := asMapFloat(m, "duration"); ok {
		e.DurationMs = n
	}
	if n, ok := asMapFloat(m, "transfer_size"); ok {
		e.TransferSize = int64(n)
	}
	if n, ok := asMapFloat(m, "status"); ok {
		e.Status = int(n)
	}
	e.T = mapTimeMs(m["t"])
	if e.T == 0 {
		e.T = fallbackMs
	}
	return e
}

func strFromMap(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			switch t := v.(type) {
			case string:
				return t
			default:
				return fmt.Sprint(t)
			}
		}
	}
	return ""
}

func asMapFloat(m map[string]any, key string) (float64, bool) {
	v, ok := m[key]
	if !ok || v == nil {
		return 0, false
	}
	switch t := v.(type) {
	case float64:
		return t, true
	case json.Number:
		n, err := t.Float64()
		return n, err == nil
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case string:
		n, err := strconv.ParseFloat(t, 64)
		return n, err == nil
	default:
		n, err := strconv.ParseFloat(fmt.Sprint(t), 64)
		return n, err == nil
	}
}

func mapTimeMs(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case int64:
		return t
	case int:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		n, _ := strconv.ParseInt(t, 10, 64)
		return n
	default:
		return 0
	}
}

func parseOccurredAtMs(at string) int64 {
	if at == "" {
		return 0
	}
	layouts := []string{
		"2006-01-02 15:04:05.000",
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	}
	for _, layout := range layouts {
		if ts, err := time.Parse(layout, at); err == nil {
			return ts.UnixMilli()
		}
	}
	return 0
}

func asInt(row map[string]any, key string) int {
	return int(asUint64(row, key))
}

func asBoolish(row map[string]any, key string) bool {
	v, ok := row[key]
	if !ok || v == nil {
		return false
	}
	switch t := v.(type) {
	case bool:
		return t
	case uint8:
		return t != 0
	case int:
		return t != 0
	case int64:
		return t != 0
	case float64:
		return t != 0
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	default:
		s := fmt.Sprint(t)
		return s == "1" || strings.EqualFold(s, "true")
	}
}
