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
// Credential store and minting go through openauth.LocalIssuer.
type Handler struct {
	JWTSecret     []byte
	ServiceSecret []byte
	AuthRequired  bool
	PublicURL     string
	Issuer        string
	local         *openauth.LocalIssuer
}

// New constructs an auth handler. When JWTSecret is empty, login still works
// for lab installs but issued tokens use a process-local ephemeral secret.
// serviceSecret is OPEN_SERVICE_JWT_SECRET for peer UserOrService routes.
func New(jwtSecret string, authRequired bool, publicURL, serviceSecret string) *Handler {
	local := openauth.NewLocalIssuer([]byte(jwtSecret), "opa-hub", "admin", "admin")
	return &Handler{
		JWTSecret:     local.Secret,
		ServiceSecret: []byte(strings.TrimSpace(serviceSecret)),
		AuthRequired:  authRequired,
		PublicURL:     publicURL,
		Issuer:        "opa-hub",
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
	if h.local != nil {
		return h.local.Parse(token)
	}
	return openauth.ParseUserJWT(token, h.JWTSecret)
}

// ServeLogin handles POST /api/auth/login.
func (h *Handler) ServeLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
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
	writeJSON(w, map[string]any{
		"token":      tok,
		"expires_at": exp.Format(time.RFC3339),
		"mode":       "hub",
		"user": map[string]any{
			"username": claims.Username,
			"role":     claims.Role,
		},
	})
}

// ServeRegister handles POST /api/auth/register.
// Self-registration is capped to viewer. Elevating role requires an admin JWT.
// When AuthRequired is true, registration itself requires an admin JWT.
func (h *Handler) ServeRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
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
		Username string `json:"username"`
		Password string `json:"password"`
		Role     string `json:"role"`
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
	if err := h.local.Register(body.Username, body.Password, role); err != nil {
		if body.Username == "" || len(body.Password) < 8 {
			openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "username and password (>=8) required")
			return
		}
		openhttp.WriteError(w, http.StatusConflict, "conflict", "username already registered")
		return
	}
	writeJSON(w, map[string]any{
		"ok":   true,
		"user": map[string]any{"username": body.Username, "role": role},
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
	}
	if authenticated {
		out["user"] = map[string]any{"username": claims.Username, "role": claims.Role}
	}
	writeJSON(w, out)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
