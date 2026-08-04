package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMiddlewareRequiresViewerWhenAuthRequired(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "service-secret-distinct-32-bytes!!")
	called := false
	handler := h.Middleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/tenancy/organizations", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("without token: got %d want 401", rec.Code)
	}
	if called {
		t.Fatal("handler should not run without token")
	}

	tok, _, err := h.mintToken("admin", "admin")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/tenancy/organizations", nil)
	req2.Header.Set("Authorization", "Bearer "+tok)
	rec2 := httptest.NewRecorder()
	handler(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("with admin token: got %d want 200", rec2.Code)
	}
	if !called {
		t.Fatal("handler should run with valid token")
	}
}

func TestMiddlewareAllowsWhenAuthNotRequired(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", false, "", "")
	called := false
	handler := h.Middleware(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/tenancy/organizations", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK || !called {
		t.Fatalf("auth optional: got %d called=%v", rec.Code, called)
	}
}
