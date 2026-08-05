package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	openauth "github.com/TheGrimmChester/open-auth-go"
	openhttp "github.com/TheGrimmChester/open-http-go"
	opentenant "github.com/TheGrimmChester/open-tenant-go"

	"github.com/TheGrimmChester/opa-hub/internal/oamdir"
	"github.com/TheGrimmChester/opa-hub/internal/registry"
)

// registerTenancyAndPeerRoutes exposes org discovery for OPM/OSA and peer health.
// GitHub App/PAT credentials are not stored on the hub — peers link via PEER_ORA_URL.
// Tenancy discovery requires a user JWT (viewer+) or a service JWT (health:read).
func (s *Server) registerTenancyAndPeerRoutes() {
	s.mux.HandleFunc("/api/tenancy/organizations", s.authH.RequireUserOrService("viewer", "health:read", s.handleTenancyOrganizations))
	s.mux.HandleFunc("/api/github/status", s.authH.RequireUserOrService("viewer", "health:read", s.handleGitHubStatus))
	s.mux.HandleFunc("/api/peer/health", s.handlePeerHealth)
}

func (s *Server) handleTenancyOrganizations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	orgs, directorySource := s.organizations()
	ctx := opentenant.FromRequest(r)
	if ctx.OrgScoped() {
		want := ctx.OrganizationID
		if opentenant.AuthEnforced() && (want == "" || want == opentenant.All) {
			want = opentenant.DefaultOrganizationID
		}
		filtered := make([]registry.OrganizationSummary, 0, 1)
		for _, o := range orgs {
			if o.ID == want {
				filtered = append(filtered, o)
			}
		}
		// Always surface the scoped org even if no agents enrolled yet.
		if len(filtered) == 0 {
			filtered = append(filtered, registry.OrganizationSummary{
				ID:         want,
				AgentCount: 0,
				Source:     "request_scope",
			})
		}
		orgs = filtered
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"organizations":    orgs,
		"directory_source": directorySource,
		"note":             tenancyNote(directorySource),
	})
}

// organizations returns the org list and where it came from.
//
// OAM is authoritative when configured; the agent registry is the fallback. The
// registry only knows organizations that telemetry has arrived under and is
// in-memory, so an org created a minute ago — or any org after a hub restart — is
// absent from it. OPM and OSA seed their org pickers from this endpoint, which made
// that absence user-visible.
func (s *Server) organizations() ([]registry.OrganizationSummary, string) {
	if s.oamDir != nil && oamdir.Configured() {
		if orgs, err := s.oamDir.Organizations(); err == nil {
			out := make([]registry.OrganizationSummary, 0, len(orgs))
			// Agent counts stay a registry fact: OAM knows which organizations
			// exist, not how much telemetry each is sending.
			counts := map[string]int{}
			for _, r := range s.reg.Organizations() {
				counts[r.ID] = r.AgentCount
			}
			for _, o := range orgs {
				out = append(out, registry.OrganizationSummary{
					ID:         o.ID,
					AgentCount: counts[o.ID],
					Source:     "oam",
				})
			}
			return out, "oam"
		} else if s.log != nil {
			// Loud, not silent: a hub quietly serving a stale registry list while
			// OAM is unreachable is how "my new org is missing" becomes a
			// multi-hour hunt.
			s.log.Warn("oam directory unavailable; falling back to the agent registry",
				map[string]any{"error": err.Error(), "peer_oam_url": oamdir.PeerURL()})
		}
	}
	return s.reg.Organizations(), "agent_registry"
}

func tenancyNote(source string) string {
	if source == "oam" {
		return "Authoritative directory from OAM (PEER_OAM_URL). Credentials and per-agent model bindings live there too; jobs resolve them via POST /api/agents/resolve."
	}
	return "Derived from the in-memory agent registry: an organization appears only once an agent has enrolled under it, and the list resets on hub restart. Set PEER_OAM_URL for the authoritative directory."
}

func (s *Server) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	peerORA := strings.TrimSpace(os.Getenv("PEER_ORA_URL"))
	// Where credentials actually live. Once OAM is configured it is the store, and
	// ORA keeps only the GitHub *protocol* work (install-url, callback, clone
	// credentials, PR/issue writes) on top of OAM-held secrets. Reporting "ora"
	// unconditionally would send an operator to the wrong service.
	credentialsHome := "ora"
	if oamdir.Configured() {
		credentialsHome = "oam"
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"credentials_home":    credentialsHome,
		"peer_ora_url":        peerORA,
		"peer_ora_configured": peerORA != "",
		"peer_oam_url":        oamdir.PeerURL(),
		"peer_oam_configured": oamdir.Configured(),
		"hub_role":            "identity_and_tenancy",
		"note":                githubStatusNote(credentialsHome),
	})
}

func (s *Server) handlePeerHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	secret := []byte(strings.TrimSpace(s.cfg.ServiceJWTSecret))
	if len(secret) == 0 {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "service_auth": "disabled"})
		return
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		http.Error(w, "unauthorized", 401)
		return
	}
	token := strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	claims, err := openauth.ValidateServiceJWT(token, secret, "opa-hub")
	if err != nil {
		http.Error(w, "invalid service token", 401)
		return
	}
	if err := openauth.RequireScope(claims, "health:read"); err != nil {
		http.Error(w, "missing scope", 403)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"service": "opa-hub",
		"iss":     claims.Issuer,
		"scope":   claims.Scope,
	})
}

func githubStatusNote(credentialsHome string) string {
	if credentialsHome == "oam" {
		return "Credentials live in OAM (PEER_OAM_URL): connectors, PATs and AI provider keys, scoped admin|org|user. " +
			"ORA still owns the GitHub protocol work — install-url, callback, clone credentials, PR and issue writes — using OAM-held secrets. " +
			"The hub stores no secrets."
	}
	return "Connect GitHub App or PAT in ORA. Hub issues user JWTs and lists organizations; OPM/OSA list repos via ORA. Hub does not store GitHub secrets."
}
