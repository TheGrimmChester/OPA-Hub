package ingest

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"

	"github.com/TheGrimmChester/opa-hub/internal/registry"
	"github.com/TheGrimmChester/opa-hub/internal/store"
)

// PushRequest is the edge → hub telemetry envelope.
type PushRequest struct {
	AgentID   string          `json:"agent_id"`
	Hostname  string          `json:"hostname,omitempty"`
	SentAt    time.Time       `json:"sent_at,omitempty"`
	BatchID   string          `json:"batch_id,omitempty"`
	Kind      string          `json:"kind"` // spans|metrics|logs|profiles|mixed
	Events    json.RawMessage `json:"events"`
	EventCount int            `json:"event_count,omitempty"`
}

// Handler accepts edge push batches and records them via ClickHouse write hooks.
type Handler struct {
	Reg         *registry.Registry
	Writer      *store.Writer
	EnrollToken string
}

func (h *Handler) enrollOK(r *http.Request) bool {
	if h.EnrollToken == "" {
		return true
	}
	tok := r.Header.Get("X-OPA-Enroll-Token")
	if tok == "" {
		auth := r.Header.Get("Authorization")
		if strings.HasPrefix(strings.ToLower(auth), "bearer ") {
			tok = strings.TrimSpace(auth[7:])
		}
	}
	return tok == h.EnrollToken
}

// ServePush handles POST /api/ingest/push.
func (h *Handler) ServePush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	if !h.enrollOK(r) {
		openhttp.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or missing enroll token")
		return
	}

	limited := http.MaxBytesReader(w, r.Body, 32<<20)
	body, err := io.ReadAll(limited)
	if err != nil {
		openhttp.WriteError(w, http.StatusRequestEntityTooLarge, "payload_too_large", "request body too large")
		return
	}
	var req PushRequest
	if err := json.Unmarshal(body, &req); err != nil {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	if strings.TrimSpace(req.AgentID) == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "agent_id required")
		return
	}
	if req.Kind == "" {
		req.Kind = "mixed"
	}
	n := req.EventCount
	if n <= 0 && len(req.Events) > 0 {
		var arr []json.RawMessage
		if json.Unmarshal(req.Events, &arr) == nil {
			n = len(arr)
		} else {
			n = 1
		}
	}

	table := tableForKind(req.Kind)
	if h.Writer != nil {
		h.Writer.RecordIngest(table, n, len(body))
	}
	if h.Reg != nil {
		h.Reg.MarkPush(req.AgentID)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":          true,
		"agent_id":    req.AgentID,
		"kind":        req.Kind,
		"event_count": n,
		"table":       table,
		"accepted_at": time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func tableForKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "spans", "traces":
		return "spans"
	case "metrics":
		return "metrics"
	case "logs":
		return "logs"
	case "profiles":
		return "profiles"
	default:
		return "telemetry_raw"
	}
}
