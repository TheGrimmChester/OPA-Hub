package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOAMAuthConfigured(t *testing.T) {
	t.Setenv("PEER_OAM_URL", "http://oam:8090")
	if !oamAuthConfigured("codeployed") {
		t.Fatal("expected OAM auth when PEER_OAM_URL set and codeployed")
	}
	if oamAuthConfigured("standalone") {
		t.Fatal("standalone must keep LocalIssuer even with PEER_OAM_URL")
	}
	t.Setenv("PEER_OAM_URL", "")
	if oamAuthConfigured("codeployed") {
		t.Fatal("expected false without PEER_OAM_URL")
	}
}

func TestServeLoginProxiesToOAM(t *testing.T) {
	oam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/auth/login" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token":"from-oam","issuer":"oam-api"}`))
	}))
	defer oam.Close()

	t.Setenv("PEER_OAM_URL", oam.URL)
	h := New(strings.Repeat("s", 32), true, "", "", "codeployed")

	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"a","password":"b"}`))
	rr := httptest.NewRecorder()
	h.ServeLogin(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "from-oam") {
		t.Fatalf("expected proxied body, got %s", rr.Body.String())
	}
}

func TestServeRegisterBlockedWhenOAM(t *testing.T) {
	t.Setenv("PEER_OAM_URL", "http://oam:8090")
	h := New(strings.Repeat("s", 32), false, "", "", "codeployed")
	req := httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"username":"x","password":"password1"}`))
	rr := httptest.NewRecorder()
	h.ServeRegister(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("status=%d want 503", rr.Code)
	}
}
