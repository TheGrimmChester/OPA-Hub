package auth

import (
	"context"
	"net/http"
	"strings"

	openauth "github.com/TheGrimmChester/open-auth-go"
	openhttp "github.com/TheGrimmChester/open-http-go"
)

type ctxKey int

const userClaimsKey ctxKey = 1

// Middleware returns an HTTP middleware that enforces user JWT auth when
// AuthRequired is true. Public auth/health/ingest/agent enroll paths should
// not use this wrapper.
func (h *Handler) Middleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.AuthRequired {
			next(w, r)
			return
		}
		claims, ok := h.claimsFromRequest(r)
		if !ok {
			openhttp.WriteError(w, http.StatusUnauthorized, "unauthorized", "authentication required")
			return
		}
		ctx := context.WithValue(r.Context(), userClaimsKey, claims)
		next(w, r.WithContext(ctx))
	}
}

func (h *Handler) claimsFromRequest(r *http.Request) (*openauth.UserClaims, bool) {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(strings.ToLower(authHeader), "bearer ") {
		return nil, false
	}
	tok := strings.TrimSpace(authHeader[7:])
	claims, err := h.parseToken(tok)
	if err != nil || claims == nil {
		return nil, false
	}
	return claims, true
}

// UserFromContext returns claims attached by Middleware, if any.
func UserFromContext(ctx context.Context) (*openauth.UserClaims, bool) {
	c, ok := ctx.Value(userClaimsKey).(*openauth.UserClaims)
	return c, ok && c != nil
}
