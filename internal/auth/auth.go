package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	openauth "github.com/TheGrimmChester/open-auth-go"
	openhttp "github.com/TheGrimmChester/open-http-go"
)

// Handler issues user JWTs for OPA-Dashboard when co-deployed, and for
// standalone OPA installs. Tokens are standard HS256 JWTs (Open-Auth-Go).
type Handler struct {
	JWTSecret    []byte
	AuthRequired bool
	PublicURL    string
	Issuer       string

	mu    sync.RWMutex
	users map[string]userRecord // username → record
}

type userRecord struct {
	Username     string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

// New constructs an auth handler. When JWTSecret is empty, login still works
// for lab installs but issued tokens use a process-local ephemeral secret.
func New(jwtSecret string, authRequired bool, publicURL string) *Handler {
	secret := []byte(jwtSecret)
	if len(secret) == 0 {
		secret = make([]byte, 32)
		_, _ = rand.Read(secret)
	}
	h := &Handler{
		JWTSecret:    secret,
		AuthRequired: authRequired,
		PublicURL:    publicURL,
		Issuer:       "opa-hub",
		users:        make(map[string]userRecord),
	}
	// Seed a lab admin when no durable store is configured.
	h.users["admin"] = userRecord{
		Username:     "admin",
		PasswordHash: hashPassword("admin", secret),
		Role:         "admin",
		CreatedAt:    time.Now().UTC(),
	}
	return h
}

func hashPassword(password string, secret []byte) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(password))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
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
	h.mu.RLock()
	u, ok := h.users[creds.Username]
	h.mu.RUnlock()
	if !ok || u.PasswordHash != hashPassword(creds.Password, h.JWTSecret) {
		openhttp.WriteError(w, http.StatusUnauthorized, "unauthorized", "invalid credentials")
		return
	}
	tok, exp, err := h.mintToken(u.Username, u.Role)
	if err != nil {
		openhttp.WriteError(w, http.StatusInternalServerError, "token_error", "failed to mint token")
		return
	}
	writeJSON(w, map[string]any{
		"token":      tok,
		"expires_at": exp.Format(time.RFC3339),
		"mode":       "hub",
		"user": map[string]any{
			"username": u.Username,
			"role":     u.Role,
		},
	})
}

// ServeRegister handles POST /api/auth/register.
func (h *Handler) ServeRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		openhttp.WriteError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
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
	if body.Username == "" || len(body.Password) < 8 {
		openhttp.WriteError(w, http.StatusBadRequest, "bad_request", "username and password (>=8) required")
		return
	}
	role := body.Role
	if role == "" {
		role = "viewer"
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, exists := h.users[body.Username]; exists {
		openhttp.WriteError(w, http.StatusConflict, "conflict", "username already registered")
		return
	}
	h.users[body.Username] = userRecord{
		Username:     body.Username,
		PasswordHash: hashPassword(body.Password, h.JWTSecret),
		Role:         role,
		CreatedAt:    time.Now().UTC(),
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
