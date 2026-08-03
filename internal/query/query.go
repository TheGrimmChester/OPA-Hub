package query

import (
	"encoding/json"
	"net/http"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"

	"github.com/TheGrimmChester/opa-hub/internal/registry"
	"github.com/TheGrimmChester/opa-hub/internal/store"
)

// Handler is the dashboard-facing query/admin skeleton.
// Full span/metric query ownership moves here from today's all-in-one Agent.
type Handler struct {
	Reg    *registry.Registry
	Writer *store.Writer
	StartedAt time.Time
	Version   string
}

// ServeQueryRoot handles GET /api/query — capability advertisement for the UI.
func (h *Handler) ServeQueryRoot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	writeJSON(w, map[string]any{
		"service": "opa-hub",
		"role":    "query",
		"capabilities": []string{
			"agents",
			"ingest_accept",
			"auth",
			"health",
		},
		"note": "Full trace/metric/log query surfaces seed here as ownership moves from edge OPA-Agent.",
	})
}

// ServeAdmin handles GET /api/admin — control-plane summary for operators.
func (h *Handler) ServeAdmin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	agents := 0
	if h.Reg != nil {
		agents = h.Reg.Count()
	}
	ch := map[string]any{"configured": false}
	if h.Writer != nil {
		ch = h.Writer.Stats()
	}
	writeJSON(w, map[string]any{
		"service":      "opa-hub",
		"version":      h.Version,
		"started_at":   h.StartedAt.UTC().Format(time.RFC3339),
		"uptime_sec":   int(time.Since(h.StartedAt).Seconds()),
		"agents":       agents,
		"clickhouse":   ch,
		"topology":     "hub-spoke",
		"dashboard_backend": true,
	})
}

// ServeServicesSkeleton handles GET /api/services — placeholder list for dashboard wiring.
func (h *Handler) ServeServicesSkeleton(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	writeJSON(w, map[string]any{
		"services": []any{},
		"count":    0,
		"source":   "opa-hub",
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
