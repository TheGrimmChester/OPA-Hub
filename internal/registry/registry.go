package registry

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	openhttp "github.com/TheGrimmChester/open-http-go"
	opentenant "github.com/TheGrimmChester/open-tenant-go"
)

// Agent is a registered edge opa-agent instance.
type Agent struct {
	ID             string            `json:"id"`
	Hostname       string            `json:"hostname"`
	Version        string            `json:"version"`
	OrganizationID string            `json:"organization_id"`
	ProjectID      string            `json:"project_id"`
	Labels         map[string]string `json:"labels,omitempty"`
	Status         string            `json:"status"` // online|stale
	RegisteredAt   time.Time         `json:"registered_at"`
	LastSeenAt     time.Time         `json:"last_seen_at"`
	LastPushAt     *time.Time        `json:"last_push_at,omitempty"`
	PushCount      uint64            `json:"push_count"`
}

// Registry is an in-memory agent registry (durable store follows with ClickHouse).
type Registry struct {
	mu         sync.RWMutex
	agents     map[string]*Agent
	staleAfter time.Duration
}

// New returns an empty Registry.
func New(staleAfter time.Duration) *Registry {
	if staleAfter <= 0 {
		staleAfter = 5 * time.Minute
	}
	return &Registry{
		agents:     make(map[string]*Agent),
		staleAfter: staleAfter,
	}
}

type registerRequest struct {
	AgentID        string            `json:"agent_id"`
	Hostname       string            `json:"hostname"`
	Version        string            `json:"version"`
	OrganizationID string            `json:"organization_id"`
	ProjectID      string            `json:"project_id"`
	Labels         map[string]string `json:"labels"`
}

// Register creates or refreshes an agent enrollment.
// Empty org/project on enroll intentionally stamp the lab defaults — agents
// authenticate with an enroll token (not a user JWT). User-facing list/get
// paths must not invent default-org from missing X-Organization-ID.
func (r *Registry) Register(req registerRequest) (*Agent, error) {
	now := time.Now().UTC()
	id := strings.TrimSpace(req.AgentID)
	if id == "" {
		id = newAgentID()
	}
	org := req.OrganizationID
	if org == "" {
		org = "default-org"
	}
	project := req.ProjectID
	if project == "" {
		project = "default-project"
	}
	host := req.Hostname
	if host == "" {
		host = "unknown"
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.agents[id]; ok {
		existing.Hostname = host
		existing.Version = req.Version
		existing.OrganizationID = org
		existing.ProjectID = project
		existing.Labels = req.Labels
		existing.LastSeenAt = now
		existing.Status = "online"
		cp := *existing
		return &cp, nil
	}
	a := &Agent{
		ID:             id,
		Hostname:       host,
		Version:        req.Version,
		OrganizationID: org,
		ProjectID:      project,
		Labels:         req.Labels,
		Status:         "online",
		RegisteredAt:   now,
		LastSeenAt:     now,
	}
	r.agents[id] = a
	cp := *a
	return &cp, nil
}

// Heartbeat refreshes last-seen for a known agent.
func (r *Registry) Heartbeat(agentID string) (*Agent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.agents[agentID]
	if !ok {
		return nil, false
	}
	a.LastSeenAt = time.Now().UTC()
	a.Status = "online"
	cp := *a
	return &cp, true
}

// MarkPush records a successful telemetry push from an agent.
func (r *Registry) MarkPush(agentID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	a, ok := r.agents[agentID]
	if !ok {
		return
	}
	now := time.Now().UTC()
	a.LastSeenAt = now
	a.LastPushAt = &now
	a.PushCount++
	a.Status = "online"
}

// Get returns one agent by id.
func (r *Registry) Get(id string) (*Agent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[id]
	if !ok {
		return nil, false
	}
	cp := *a
	cp.Status = r.statusOf(a)
	return &cp, true
}

// List returns all agents sorted by id.
func (r *Registry) List() []Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Agent, 0, len(r.agents))
	for _, a := range r.agents {
		cp := *a
		cp.Status = r.statusOf(a)
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// ListScoped returns agents matching the request tenant scope.
// When neither dimension is scoped (lab all-tenants), returns List().
// Under auth, empty/"all" org yields an empty list — never invent default-org.
// Non-empty ProjectIDs (X-Project-IDs) filters with project_id IN (...).
func (r *Registry) ListScoped(ctx opentenant.Context) []Agent {
	all := r.List()
	if !ctx.OrgScoped() && !ctx.ProjectScoped() {
		return all
	}
	org, proj := ctx.OrganizationID, ctx.ProjectID
	projectIDs := ctx.ProjectIDs
	if opentenant.AuthEnforced() {
		if org == "" || org == opentenant.All {
			return nil
		}
		// Multi-select list scope uses ProjectIDs; only collapse empty single
		// project when the multi header is absent (contract: same as missing).
		if len(projectIDs) == 0 && (proj == "" || proj == opentenant.All) {
			proj = opentenant.DefaultProjectID
		}
	}
	allow := projectIDAllowSet(projectIDs)
	out := make([]Agent, 0, len(all))
	for _, a := range all {
		aOrg := a.OrganizationID
		if aOrg == "" {
			aOrg = opentenant.DefaultOrganizationID
		}
		aProj := a.ProjectID
		if aProj == "" {
			aProj = opentenant.DefaultProjectID
		}
		if ctx.OrgScoped() && aOrg != org {
			continue
		}
		if len(allow) > 0 {
			if _, ok := allow[aProj]; !ok {
				continue
			}
		} else if ctx.ProjectScoped() && aProj != proj {
			continue
		}
		out = append(out, a)
	}
	return out
}

// agentInScope reports whether agent matches the request tenant.
// Under auth, empty/"all" org matches nothing (fail closed).
func agentInScope(a *Agent, ctx opentenant.Context) bool {
	if a == nil {
		return false
	}
	if !ctx.OrgScoped() && !ctx.ProjectScoped() {
		return true
	}
	org, proj := ctx.OrganizationID, ctx.ProjectID
	projectIDs := ctx.ProjectIDs
	if opentenant.AuthEnforced() {
		if org == "" || org == opentenant.All {
			return false
		}
		if len(projectIDs) == 0 && (proj == "" || proj == opentenant.All) {
			proj = opentenant.DefaultProjectID
		}
	}
	aOrg := a.OrganizationID
	if aOrg == "" {
		aOrg = opentenant.DefaultOrganizationID
	}
	aProj := a.ProjectID
	if aProj == "" {
		aProj = opentenant.DefaultProjectID
	}
	if ctx.OrgScoped() && aOrg != org {
		return false
	}
	if allow := projectIDAllowSet(projectIDs); len(allow) > 0 {
		_, ok := allow[aProj]
		return ok
	}
	if ctx.ProjectScoped() && aProj != proj {
		return false
	}
	return true
}

func projectIDAllowSet(ids []string) map[string]struct{} {
	if len(ids) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || strings.EqualFold(id, opentenant.All) {
			continue
		}
		out[id] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// Count returns registered agent count.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

// OrganizationSummary is a tenancy org known to the hub (from agent labels).
type OrganizationSummary struct {
	ID string `json:"id"`
	// Name is the human-readable label, populated only from the OAM directory —
	// the agent registry knows organization ids and nothing else. omitempty keeps
	// the registry-derived payload byte-identical to before this field existed;
	// peers fall back to the id when it is absent (OSA's picker does exactly that).
	Name       string `json:"name,omitempty"`
	AgentCount int    `json:"agent_count"`
	Source     string `json:"source"`
}

// Organizations returns unique organization_id values from registered agents.
// Includes default-org only when an agent actually enrolled under it (empty
// agent org is remapped for counting continuity — never force-seed the picker).
func (r *Registry) Organizations() []OrganizationSummary {
	r.mu.RLock()
	defer r.mu.RUnlock()
	counts := map[string]int{}
	for _, a := range r.agents {
		org := strings.TrimSpace(a.OrganizationID)
		if org == "" {
			org = "default-org"
		}
		counts[org]++
	}
	ids := make([]string, 0, len(counts))
	for id := range counts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]OrganizationSummary, 0, len(ids))
	for _, id := range ids {
		out = append(out, OrganizationSummary{
			ID:         id,
			AgentCount: counts[id],
			Source:     "agent_registry",
		})
	}
	return out
}

func (r *Registry) statusOf(a *Agent) string {
	if time.Since(a.LastSeenAt) > r.staleAfter {
		return "stale"
	}
	return "online"
}

func newAgentID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return "agent-" + hex.EncodeToString(b)
}

// Handler exposes registry HTTP routes.
type Handler struct {
	Reg          *Registry
	EnrollToken  string
	AuthRequired bool // when true, empty EnrollToken fails closed
}

func (h *Handler) enrollOK(r *http.Request) bool {
	if h.EnrollToken == "" {
		// Lab/dev may leave OPA_HUB_ENROLL_TOKEN unset. Production posture
		// (OPA_AUTH_REQUIRED) must not leave enroll/heartbeat open.
		return !h.AuthRequired
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

// ServeRegister handles POST /api/agents/register.
func (h *Handler) ServeRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	if !h.enrollOK(r) {
		openhttp.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or missing enroll token")
		return
	}
	var req registerRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	agent, err := h.Reg.Register(req)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "register_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": agent})
}

// ServeHeartbeat handles POST /api/agents/heartbeat.
func (h *Handler) ServeHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	if !h.enrollOK(r) {
		openhttp.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid or missing enroll token")
		return
	}
	var body struct {
		AgentID string `json:"agent_id"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil || body.AgentID == "" {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "agent_id required")
		return
	}
	agent, ok := h.Reg.Heartbeat(body.AgentID)
	if !ok {
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "agent not registered")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": agent})
}

// ServeList handles GET /api/agents.
func (h *Handler) ServeList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	ctx := opentenant.FromRequest(r)
	agents := h.Reg.ListScoped(ctx)
	writeJSON(w, http.StatusOK, map[string]any{
		"agents": agents,
		"count":  len(agents),
	})
}

// ServeGet handles GET /api/agents/{id}.
func (h *Handler) ServeGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/agents/")
	id = strings.Trim(id, "/")
	if id == "" || id == "register" || id == "heartbeat" {
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	agent, ok := h.Reg.Get(id)
	if !ok || !agentInScope(agent, opentenant.FromRequest(r)) {
		openhttp.WriteError(w, http.StatusNotFound, "not_found", "agent not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"agent": agent})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
