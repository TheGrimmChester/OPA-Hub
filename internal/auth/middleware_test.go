package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	openauth "github.com/TheGrimmChester/open-auth-go"
)

func TestMiddlewareRequiresViewerWhenAuthRequired(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "service-secret-distinct-32-bytes!!", "")
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

func TestMiddlewareMutationsRequireEditor(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "", "")
	handler := h.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	viewerTok, err := openauth.MintUserJWT(h.JWTSecret, "v", "viewer", h.Issuer, 0)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/alerts", nil)
	req.Header.Set("Authorization", "Bearer "+viewerTok)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer POST: got %d want 403", rec.Code)
	}

	editorTok, err := openauth.MintUserJWT(h.JWTSecret, "e", "editor", h.Issuer, 0)
	if err != nil {
		t.Fatal(err)
	}
	req2 := httptest.NewRequest(http.MethodPost, "/api/alerts", nil)
	req2.Header.Set("Authorization", "Bearer "+editorTok)
	rec2 := httptest.NewRecorder()
	handler(rec2, req2)
	if rec2.Code != http.StatusNoContent {
		t.Fatalf("editor POST: got %d want 204", rec2.Code)
	}
}

func TestMiddlewareTenantClaimMismatch(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "", "")
	tok, err := openauth.MintUserJWTWithTenant(h.JWTSecret, "bound", "viewer", h.Issuer, "acme", "prod", 0)
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Middleware(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Organization-ID") != "acme" {
			t.Fatalf("org header=%q", r.Header.Get("X-Organization-ID"))
		}
		w.WriteHeader(http.StatusOK)
	})
	bad := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	bad.Header.Set("Authorization", "Bearer "+tok)
	bad.Header.Set("X-Organization-ID", "other")
	rec := httptest.NewRecorder()
	handler(rec, bad)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("mismatch: got %d want 403", rec.Code)
	}

	ok := httptest.NewRequest(http.MethodGet, "/api/services", nil)
	ok.Header.Set("Authorization", "Bearer "+tok)
	rec2 := httptest.NewRecorder()
	handler(rec2, ok)
	if rec2.Code != http.StatusOK {
		t.Fatalf("bound overwrite: got %d want 200", rec2.Code)
	}
}

func TestMiddlewareProjectACLDeny(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "", "")
	tok, err := openauth.MintUserJWTWithACL(h.JWTSecret, "dev", "viewer", h.Issuer, "default-org", []string{"allowed-only"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	deny := httptest.NewRequest(http.MethodGet, "/api/key-transactions", nil)
	deny.Header.Set("Authorization", "Bearer "+tok)
	deny.Header.Set("X-Organization-ID", "default-org")
	deny.Header.Set("X-Project-ID", "other-project")
	rec := httptest.NewRecorder()
	handler(rec, deny)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("ACL deny: got %d want 403", rec.Code)
	}

	allow := httptest.NewRequest(http.MethodGet, "/api/key-transactions", nil)
	allow.Header.Set("Authorization", "Bearer "+tok)
	allow.Header.Set("X-Organization-ID", "default-org")
	allow.Header.Set("X-Project-ID", "allowed-only")
	rec2 := httptest.NewRecorder()
	handler(rec2, allow)
	if rec2.Code != http.StatusOK {
		t.Fatalf("ACL allow: got %d want 200", rec2.Code)
	}

	// Lab admin retains full default-org access.
	adminTok, _, err := h.mintToken("admin", "admin")
	if err != nil {
		t.Fatal(err)
	}
	adminReq := httptest.NewRequest(http.MethodGet, "/api/tenancy/organizations", nil)
	adminReq.Header.Set("Authorization", "Bearer "+adminTok)
	adminReq.Header.Set("X-Organization-ID", "default-org")
	adminReq.Header.Set("X-Project-ID", "default-project")
	rec3 := httptest.NewRecorder()
	handler(rec3, adminReq)
	if rec3.Code != http.StatusOK {
		t.Fatalf("admin default-org: got %d want 200", rec3.Code)
	}
}

func TestRequireUserOrServiceProjectACL(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "service-secret-distinct-32-bytes!!", "")
	tok, err := openauth.MintUserJWTWithACL(h.JWTSecret, "dev", "viewer", h.Issuer, "default-org", []string{"alpha"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	handler := h.RequireUserOrService("viewer", "health:read", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	bad := httptest.NewRequest(http.MethodGet, "/api/tenancy/organizations", nil)
	bad.Header.Set("Authorization", "Bearer "+tok)
	bad.Header.Set("X-Organization-ID", "default-org")
	bad.Header.Set("X-Project-ID", "beta")
	rec := httptest.NewRecorder()
	handler(rec, bad)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("tenancy ACL deny: got %d want 403", rec.Code)
	}
}

func TestMiddlewareAllowsWhenAuthNotRequired(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", false, "", "", "")
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

<<<<<<< Updated upstream
func TestRequireAdminRoleForAdminSurface(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "service-secret-distinct-32-bytes!!", "")
	handler := h.Require("admin", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	noTok := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
	rec := httptest.NewRecorder()
	handler(rec, noTok)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no JWT: got %d want 401", rec.Code)
	}

	for _, role := range []string{"viewer", "editor"} {
		tok, err := openauth.MintUserJWT(h.JWTSecret, role+"-user", role, h.Issuer, 0)
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		rr := httptest.NewRecorder()
		handler(rr, req)
		if rr.Code != http.StatusForbidden {
			t.Fatalf("%s: got %d want 403", role, rr.Code)
		}
	}

	adminTok, err := openauth.MintUserJWT(h.JWTSecret, "admin", "admin", h.Issuer, 0)
	if err != nil {
		t.Fatal(err)
	}
	ok := httptest.NewRequest(http.MethodGet, "/api/admin", nil)
	ok.Header.Set("Authorization", "Bearer "+adminTok)
	okRec := httptest.NewRecorder()
	handler(okRec, ok)
	if okRec.Code != http.StatusOK {
		t.Fatalf("admin: got %d want 200", okRec.Code)
=======
func TestMiddlewareNoJWT(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "", "")
	handler := h.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no JWT: got %d want 401", rec.Code)
	}
}

func TestMiddlewarePersonalRejectsOrgHeader(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "", "")
	tok, err := openauth.MintUserJWTWithAccount(
		h.JWTSecret, "alice", "viewer", h.Issuer,
		openauth.AccountTypePersonal, "", nil, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Organization-ID", "acme")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("personal + org header: got %d want 403", rec.Code)
	}
}

func TestMiddlewareOrgMemberForeignOrg(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "", "")
	tok, err := openauth.MintUserJWTWithAccount(
		h.JWTSecret, "bob", "viewer", h.Issuer,
		openauth.AccountTypeOrganization, "acme", nil, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	handler := h.Middleware(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Organization-ID", "other")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("org member foreign org: got %d want 403", rec.Code)
	}
}

func TestMiddlewareProjectIDAllStripped(t *testing.T) {
	h := New("test-jwt-secret-at-least-32-bytes-ok", true, "", "", "")
	tok, err := openauth.MintUserJWTWithACL(
		h.JWTSecret, "dev", "viewer", h.Issuer,
		"acme", []string{"allowed-only"}, 0,
	)
	if err != nil {
		t.Fatal(err)
	}
	var sawProj string
	handler := h.Middleware(func(w http.ResponseWriter, r *http.Request) {
		sawProj = r.Header.Get("X-Project-ID")
		w.WriteHeader(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodGet, "/api/alerts", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("X-Organization-ID", "acme")
	req.Header.Set("X-Project-ID", "all")
	rec := httptest.NewRecorder()
	handler(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("X-Project-ID all: got %d want 200 body=%s", rec.Code, rec.Body.String())
	}
	if sawProj == "" || strings.EqualFold(sawProj, "all") {
		t.Fatalf("X-Project-ID all must be stripped/pinned under auth, got %q", sawProj)
	}
	if sawProj != "allowed-only" {
		t.Fatalf("single-allowlist pin: got %q want allowed-only", sawProj)
>>>>>>> Stashed changes
	}
}

