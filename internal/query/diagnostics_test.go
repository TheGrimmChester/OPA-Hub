package query

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDiagIDStable(t *testing.T) {
	a := diagID("rel", "org", "api", "1.0.0")
	b := diagID("rel", "org", "api", "1.0.0")
	if a != b || !strings.HasPrefix(a, "rel-") {
		t.Fatalf("got %q", a)
	}
	if diagID("rel", "org", "api", "1.0.1") == a {
		t.Fatal("expected different id for different release")
	}
}

func TestDiagnosticsRoutesRequireClickHouse(t *testing.T) {
	h := &Handler{}
	routes := []struct {
		name string
		fn   func(http.ResponseWriter, *http.Request)
		path string
	}{
		{"releases", h.ServeReleases, "/api/releases"},
		{"suspects", h.ServeSuspectCommits, "/api/diagnostics/suspect-commits"},
		{"heap", h.ServeHeapDiagnostics, "/api/diagnostics/heap"},
		{"threads", h.ServeThreadDiagnostics, "/api/diagnostics/threads"},
		{"locks", h.ServeLockDiagnostics, "/api/diagnostics/locks"},
	}
	for _, rt := range routes {
		w := httptest.NewRecorder()
		rt.fn(w, httptest.NewRequest("GET", rt.path, nil))
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s: expected 503 got %d body %s", rt.name, w.Code, w.Body.String())
		}
	}
}

func TestServeSuspectCommitsMethod(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	h.ServeSuspectCommits(w, httptest.NewRequest("POST", "/api/diagnostics/suspect-commits", nil))
	if w.Code != http.StatusMethodNotAllowed {
		t.Fatalf("got %d", w.Code)
	}
}

func TestServeHeapThreadsLocksMethod(t *testing.T) {
	h := &Handler{}
	for _, fn := range []func(http.ResponseWriter, *http.Request){
		h.ServeHeapDiagnostics, h.ServeThreadDiagnostics, h.ServeLockDiagnostics,
	} {
		w := httptest.NewRecorder()
		fn(w, httptest.NewRequest("POST", "/", nil))
		if w.Code != http.StatusMethodNotAllowed {
			t.Fatalf("got %d", w.Code)
		}
	}
}
