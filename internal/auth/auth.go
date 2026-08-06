package auth

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	openauth "github.com/TheGrimmChester/open-auth-go"
	openhttp "github.com/TheGrimmChester/open-http-go"
)

// Handler issues user JWTs for OPA-Dashboard when co-deployed, and for
// standalone OPA installs. Tokens are standard HS256 JWTs (Open-Auth-Go).
// When PEER_OAM_URL is set in co-deployed mode, login proxies to OAM (iss=oam-api).
type Handler struct {
	JWTSecret     []byte
	ServiceSecret []byte
	AuthRequired  bool
	PublicURL     string
	Issuer        string
	AuthMode      string
	local         *openauth.LocalIssuer
}

// New constructs an auth handler. When JWTSecret is empty, login still works
// for lab installs but issued tokens use a process-local ephemeral secret.
// serviceSecret is OPEN_SERVICE_JWT_SECRET for peer UserOrService routes.
// authMode is AUTH_MODE; when PEER_OAM_URL is set and mode is not standalone,
// login/register proxy to OAM instead of LocalIssuer.
func New(jwtSecret string, authRequired bool, publicURL, serviceSecret, authMode string) *Handler {
	local := openauth.NewLocalIssuer([]byte(jwtSecret), "opa-hub", "admin", "admin")
	issuer := "opa-hub"
	if oamAuthConfigured(authMode) {
		issuer = "oam-api"
	}
	return &Handler{
		JWTSecret:     local.Secret,
		ServiceSecret: []byte(strings.TrimSpace(serviceSecret)),
		AuthRequired:  authRequired,
		PublicURL:     publicURL,
		Issuer:        issuer,
		AuthMode:      authMode,
		local:         local,
	}
}

func (h *Handler) mintToken(username, role string) (string, time.Time, error) {
	ttl := 24 * time.Hour
	tok, err := openauth.MintUserJWT(h.JWTSecret, username, role, h.Issuer, ttl)
	if err != nil {
		return "", time.Time{}, err
	}
	return tok, time.Now().UTC().Add(ttl), nil
}

func (h *Handler) parseToken(token string) (*openauth.UserClaims, error) {
	claims, err := openauth.ParseUserJWT(token, h.JWTSecret)
	if err != nil {
		return nil, err
	}
	if oamAuthConfigured(h.AuthMode) {
		if iss := strings.TrimSpace(claims.Issuer); iss != "" && iss != "oam-api" {
			return nil, openauth.ErrInvalidToken
		}
	}
	return claims, nil
}

// ServeLogin handles POST /api/auth/login.
func (h *Handler) ServeLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	if oamAuthConfigured(h.AuthMode) {
		proxyOAMAuth(w, r, "/api/auth/login")
		return
	}
	var creds struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&creds); err != nil {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	tok, exp, claims, err := h.local.Login(creds.Username, creds.Password)
	if err != nil || claims == nil {
		openhttp.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	user := map[string]any{
		"username": claims.Username,
		"role":     claims.Role,
	}
	if claims.OrgID != "" {
		user["org_id"] = claims.OrgID
	}
	if len(claims.ProjectIDs) > 0 {
		user["project_ids"] = claims.ProjectIDs
	}
	writeJSON(w, map[string]any{
		"token":      tok,
		"expires_at": exp.Format(time.RFC3339),
		"mode":       "hub",
		"user":       user,
	})
}

// ServeRegister handles POST /api/auth/register.
// Self-registration is capped to viewer. Elevating role requires an admin JWT.
// When AuthRequired is true, registration itself requires an admin JWT.
// Optional org_id / project_ids bind the new user to a project allowlist.
// Disabled when OAM is the identity home (use OAM admin user APIs).
func (h *Handler) ServeRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	if oamAuthConfigured(h.AuthMode) {
		openhttp.WriteError(w, http.StatusServiceUnavailable, "oam_identity_home",
			"user registration is managed by OAM — use OAM /api/users/set")
		return
	}
	caller, callerOK := h.claimsFromRequest(r)
	if h.AuthRequired {
		if !callerOK {
			openhttp.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		if !openauth.HasPermission(caller.Role, "admin") {
			openhttp.WriteError(w, http.StatusForbidden, "forbidden", "admin required to register users")
			return
		}
	}
	var body struct {
		Username   string   `json:"username"`
		Password   string   `json:"password"`
		Role       string   `json:"role"`
		OrgID      string   `json:"org_id"`
		ProjectIDs []string `json:"project_ids"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "invalid JSON body")
		return
	}
	body.Username = strings.TrimSpace(body.Username)
	role := strings.TrimSpace(body.Role)
	if role == "" {
		role = "viewer"
	}
	// Only an authenticated admin may mint editor/admin accounts.
	if role != "viewer" && !(callerOK && openauth.HasPermission(caller.Role, "admin")) {
		role = "viewer"
	}
	// Non-admins cannot assign membership (self-reg stays unbound viewer).
	orgID := strings.TrimSpace(body.OrgID)
	projectIDs := openauth.NormalizeProjectIDs(body.ProjectIDs)
	if !(callerOK && openauth.HasPermission(caller.Role, "admin")) {
		orgID = ""
		projectIDs = nil
	}
	if err := h.local.RegisterWithMembership(body.Username, body.Password, role, orgID, projectIDs); err != nil {
		if body.Username == "" || len(body.Password) < 8 {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "username and password (>=8) required")
			return
		}
		openhttp.WriteError(w, http.StatusConflict, "conflict", "username already registered")
		return
	}
	user := map[string]any{"username": body.Username, "role": role}
	if orgID != "" {
		user["org_id"] = orgID
	}
	if len(projectIDs) > 0 {
		user["project_ids"] = projectIDs
	}
	writeJSON(w, map[string]any{
		"ok":   true,
		"user": user,
	})
}

// ServeLogout handles POST /api/auth/logout.
func (h *Handler) ServeLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost && r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// ServeStatus handles GET /api/auth/status.
func (h *Handler) ServeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	authHeader := r.Header.Get("Authorization")
	var claims *openauth.UserClaims
	if strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		tok := strings.TrimSpace(authHeader[7:])
		claims, _ = h.parseToken(tok)
	}
	authenticated := claims != nil
	out := map[string]any{
		"authenticated": authenticated,
		"auth_required": h.AuthRequired,
		"issuer":        h.Issuer,
		"public_url":    h.PublicURL,
		"mode":          "hub",
		"oam_login":     oamAuthConfigured(h.AuthMode),
	}
	if authenticated {
		user := map[string]any{"username": claims.Username, "role": claims.Role}
		if claims.OrgID != "" {
			user["org_id"] = claims.OrgID
		}
		if len(claims.ProjectIDs) > 0 {
			user["project_ids"] = claims.ProjectIDs
		}
		out["user"] = user
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
