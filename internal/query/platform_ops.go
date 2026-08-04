package query

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strconv"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
)

// hubTopology is the dashboard contract for GET /api/topology (hub-spoke control plane).
type hubTopology struct {
	Mode               string `json:"mode"`
	ReplicaCount       int    `json:"replica_count"`
	ShardCount         int    `json:"shard_count"`
	ShardIndex         int    `json:"shard_index"`
	LeaderElection     bool   `json:"leader_election"`
	IsLeader           bool   `json:"is_leader"`
	Drain              bool   `json:"drain"`
	IngestAuthRequired bool   `json:"ingest_auth_required"`
	AgentCount         int    `json:"agent_count"`
	SupportMatrixURL   string `json:"support_matrix"`
}

func (h *Handler) currentTopology() hubTopology {
	agents := 0
	if h.Reg != nil {
		agents = h.Reg.Count()
	}
	return hubTopology{
		Mode:               "hub-spoke",
		ReplicaCount:       1,
		ShardCount:         1,
		ShardIndex:         0,
		LeaderElection:     false,
		IsLeader:           true,
		Drain:              false,
		IngestAuthRequired: h.EnrollTokenRequired,
		AgentCount:         agents,
		SupportMatrixURL:   "/docs/architecture.md",
	}
}

// ServeVersion handles GET /api/version — hub build identity for PlatformOps.
func (h *Handler) ServeVersion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	writeJSON(w, map[string]any{
		"version":    h.Version,
		"service":    "opa-hub",
		"started_at": h.StartedAt.UTC().Format(time.RFC3339),
		"uptime_s":   int(time.Since(h.StartedAt).Seconds()),
		"go":         runtime.Version(),
		"source":     "opa-hub",
	})
}

// ServeTopology handles GET /api/topology — hub-spoke summary for PlatformOps.
func (h *Handler) ServeTopology(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	writeJSON(w, h.currentTopology())
}

// ServeOpsStatus handles GET /api/ops/status — hub runtime counters for PlatformOps.
func (h *Handler) ServeOpsStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	ingestAccepted := uint64(0)
	if h.Writer != nil {
		if st := h.Writer.Stats(); st != nil {
			ingestAccepted = asUint64(st, "ingest_events")
		}
	}

	writeJSON(w, map[string]any{
		"topology":         h.currentTopology(),
		"version":          h.Version,
		"service":          "opa-hub",
		"goroutines":       runtime.NumGoroutine(),
		"heap_alloc_bytes": ms.HeapAlloc,
		"ingest_accepted":  ingestAccepted,
		"ingest_rejected":  uint64(0),
		"ingest_shed":      uint64(0),
		"ingest_lag_s":     float64(0),
		"load_shed":        false,
		"admission":        map[string]any{"limit_per_sec": 0, "window_hits": int64(0)},
		"source":           "opa-hub",
	})
}

// ServeAudit handles GET /api/audit — privileged ops log from opa.audit_log.
func (h *Handler) ServeAudit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	if h.Writer == nil {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "clickhouse_unavailable", "ClickHouse not configured")
		return
	}

	limit := clampInt(parseIntDefault(r.URL.Query().Get("limit"), 50), 1, 200)
	rows, err := h.Writer.Query(fmt.Sprintf(
		`SELECT audit_id, action, actor, detail, created_at
		 FROM opa.audit_log
		 ORDER BY created_at DESC
		 LIMIT %d`, limit))
	if err != nil {
		writeJSON(w, map[string]any{"events": []any{}, "source": "opa-hub", "error": err.Error()})
		return
	}

	events := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		ev := map[string]any{
			"audit_id":   asString(row, "audit_id"),
			"action":     asString(row, "action"),
			"actor":      asString(row, "actor"),
			"created_at": asString(row, "created_at"),
		}
		if raw := asString(row, "detail"); raw != "" {
			var detail any
			if json.Unmarshal([]byte(raw), &detail) == nil {
				ev["detail"] = detail
			} else {
				ev["detail"] = raw
			}
		}
		events = append(events, ev)
	}
	writeJSON(w, map[string]any{"events": events, "source": "opa-hub"})
}

func clampInt(n, lo, hi int) int {
	if n < lo {
		return lo
	}
	if n > hi {
		return hi
	}
	return n
}

func parseIntDefault(raw string, def int) int {
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return n
}
