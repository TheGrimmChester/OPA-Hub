package oamdir

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestConfiguredFollowsPeerURL(t *testing.T) {
	t.Setenv("PEER_OAM_URL", "")
	if Configured() {
		t.Fatal("unset PEER_OAM_URL must leave the hub on its agent registry")
	}
	t.Setenv("PEER_OAM_URL", "http://oam:8090/")
	if !Configured() {
		t.Fatal("a set PEER_OAM_URL must enable the directory")
	}
	if PeerURL() != "http://oam:8090" {
		t.Fatalf("PeerURL=%q — the trailing slash must be trimmed so paths do not double up", PeerURL())
	}
}

func TestOrganizationsReadsTheDirectory(t *testing.T) {
	t.Setenv("OPA_SEC_KEY_PREFIX", "test-read:")
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		_ = json.NewEncoder(w).Encode(map[string]any{
			"organizations": []Org{{ID: "acme", Name: "Acme", Source: "oam"}},
		})
	}))
	defer srv.Close()

	t.Setenv("PEER_OAM_URL", srv.URL)
	t.Setenv("OPEN_SERVICE_JWT_SECRET", "service-secret-at-least-32-bytes-long!")

	orgs, err := New().Organizations()
	if err != nil {
		t.Fatalf("Organizations: %v", err)
	}
	if len(orgs) != 1 || orgs[0].ID != "acme" {
		t.Fatalf("unexpected orgs: %+v", orgs)
	}
	if gotPath != "/api/tenancy/organizations" {
		t.Fatalf("path=%q", gotPath)
	}
	// A service JWT must be presented: OAM's directory route is authenticated, so
	// a missing token would silently degrade the hub to its registry fallback.
	if len(gotAuth) < 10 || gotAuth[:7] != "Bearer " {
		t.Fatalf("no bearer token sent: %q", gotAuth)
	}
}

// Without a service secret the client cannot authenticate, and it must say so
// rather than sending an unauthenticated request that returns 401.
func TestOrganizationsRequiresAServiceSecret(t *testing.T) {
	t.Setenv("OPA_SEC_KEY_PREFIX", "test-nosecret:")
	t.Setenv("PEER_OAM_URL", "http://oam.invalid:8090")
	t.Setenv("OPEN_SERVICE_JWT_SECRET", "")
	if _, err := New().Organizations(); err == nil {
		t.Fatal("expected an error when OPEN_SERVICE_JWT_SECRET is unset")
	}
}

// A brief OAM outage must not blank out every product's org picker: the client
// serves its last known directory instead of an error.
func TestStaleCacheSurvivesAnOutage(t *testing.T) {
	t.Setenv("OPA_SEC_KEY_PREFIX", "test-stale:")
	fail := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if fail {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"organizations": []Org{{ID: "acme"}},
		})
	}))
	defer srv.Close()
	t.Setenv("PEER_OAM_URL", srv.URL)
	t.Setenv("OPEN_SERVICE_JWT_SECRET", "service-secret-at-least-32-bytes-long!")

	c := New()
	if _, err := c.Organizations(); err != nil {
		t.Fatalf("first fetch: %v", err)
	}
	// Expire the cache, then break the server.
	c.mu.Lock()
	c.cachedAt = c.cachedAt.Add(-2 * cacheTTL)
	c.mu.Unlock()
	fail = true

	orgs, err := c.Organizations()
	if err != nil {
		t.Fatalf("an outage must serve the stale directory, not an error: %v", err)
	}
	if len(orgs) != 1 || orgs[0].ID != "acme" {
		t.Fatalf("stale directory lost: %+v", orgs)
	}
}

// With no cache and a failing peer there is nothing honest to serve, so the error
// propagates and the caller falls back to the registry.
func TestNoCacheAndFailureReturnsError(t *testing.T) {
	t.Setenv("OPA_SEC_KEY_PREFIX", "test-nocache:")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	t.Setenv("PEER_OAM_URL", srv.URL)
	t.Setenv("OPEN_SERVICE_JWT_SECRET", "service-secret-at-least-32-bytes-long!")
	if _, err := New().Organizations(); err == nil {
		t.Fatal("expected an error so the caller falls back to the agent registry")
	}
}
