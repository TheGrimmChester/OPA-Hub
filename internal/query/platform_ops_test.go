package query

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHubTopologyContract(t *testing.T) {
	h := &Handler{
		Version:             "0.7.2",
		StartedAt:           time.Now().UTC().Add(-time.Minute),
		EnrollTokenRequired: true,
	}
	topo := h.currentTopology()
	if topo.Mode != "hub-spoke" {
		t.Fatalf("mode %q", topo.Mode)
	}
	if !topo.IsLeader || topo.ShardCount != 1 || !topo.IngestAuthRequired {
		t.Fatalf("%+v", topo)
	}
}

func TestServeVersionAndTopology(t *testing.T) {
	h := &Handler{Version: "0.7.2", StartedAt: time.Now().UTC()}
	for _, fn := range []func(http.ResponseWriter, *http.Request){
		h.ServeVersion, h.ServeTopology,
	} {
		w := httptest.NewRecorder()
		fn(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 200 {
			t.Fatalf("status %d body %s", w.Code, w.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
	}
}

func TestClampIntAndParseIntDefault(t *testing.T) {
	if clampInt(0, 1, 10) != 1 || clampInt(99, 1, 10) != 10 || clampInt(5, 1, 10) != 5 {
		t.Fatal("clampInt")
	}
	if parseIntDefault("", 50) != 50 || parseIntDefault("abc", 50) != 50 || parseIntDefault("12", 50) != 12 {
		t.Fatal("parseIntDefault")
	}
}

func TestServeDBRoutesRequireClickHouse(t *testing.T) {
	h := &Handler{}
	routes := []func(http.ResponseWriter, *http.Request){
		h.ServeDBInstances, h.ServeDBStatements, h.ServeDBUnusedIndexes, h.ServeDBFingerprintMatch, h.ServeAudit,
	}
	for _, fn := range routes {
		w := httptest.NewRecorder()
		fn(w, httptest.NewRequest("GET", "/", nil))
		if w.Code != 503 {
			t.Fatalf("expected 503 got %d for %T", w.Code, fn)
		}
	}
}
