package server

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	openauth "github.com/TheGrimmChester/open-auth-go"
	openhttp "github.com/TheGrimmChester/open-http-go"
)

// registerTenancyAndPeerRoutes exposes org discovery for OPM and peer health.
// GitHub App/PAT credentials are not stored on the hub — OPM links via PEER_ORA_URL.
func (s *Server) registerTenancyAndPeerRoutes() {
	s.mux.HandleFunc("/api/tenancy/organizations", s.handleTenancyOrganizations)
	s.mux.HandleFunc("/api/github/status", s.handleGitHubStatus)
	s.mux.HandleFunc("/api/peer/health", s.handlePeerHealth)
}

func (s *Server) handleTenancyOrganizations(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	orgs := s.reg.Organizations()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"organizations": orgs,
		"note":          "Hub identity/tenancy directory. GitHub App and PAT credentials live in ORA (PEER_ORA_URL); OPM discovers repos through ora-api connectors scoped by organization_id.",
	})
}

func (s *Server) handleGitHubStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	peerORA := strings.TrimSpace(os.Getenv("PEER_ORA_URL"))
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"credentials_home":    "ora",
		"peer_ora_url":        peerORA,
		"peer_ora_configured": peerORA != "",
		"hub_role":            "identity_and_tenancy",
		"note":                "Connect GitHub App or PAT in ORA. Hub issues user JWTs and lists organizations; it does not store GitHub secrets.",
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
