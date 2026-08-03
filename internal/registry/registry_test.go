package registry_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TheGrimmChester/opa-hub/internal/registry"
)

func TestRegisterAndList(t *testing.T) {
	reg := registry.New(time.Minute)
	h := &registry.Handler{Reg: reg, EnrollToken: "secret"}

	body := `{"hostname":"host-a","version":"1.0.0","organization_id":"org1","project_id":"proj1"}`
	req := httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(body))
	req.Header.Set("X-OPA-Enroll-Token", "secret")
	rr := httptest.NewRecorder()
	h.ServeRegister(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	var out struct {
		Agent registry.Agent `json:"agent"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Agent.ID == "" || out.Agent.Hostname != "host-a" {
		t.Fatalf("unexpected agent: %+v", out.Agent)
	}

	listRR := httptest.NewRecorder()
	h.ServeList(listRR, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if listRR.Code != http.StatusOK {
		t.Fatal(listRR.Body.String())
	}
	var listed struct {
		Count int `json:"count"`
	}
	_ = json.Unmarshal(listRR.Body.Bytes(), &listed)
	if listed.Count != 1 {
		t.Fatalf("count=%d", listed.Count)
	}
}

func TestEnrollTokenRequired(t *testing.T) {
	reg := registry.New(time.Minute)
	h := &registry.Handler{Reg: reg, EnrollToken: "secret"}
	req := httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(`{"hostname":"x"}`))
	rr := httptest.NewRecorder()
	h.ServeRegister(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("got %d", rr.Code)
	}
}
