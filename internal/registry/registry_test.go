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

func TestListScopedByTenant(t *testing.T) {
	reg := registry.New(time.Minute)
	h := &registry.Handler{Reg: reg, EnrollToken: ""}

	for _, body := range []string{
		`{"agent_id":"a1","hostname":"h1","organization_id":"org1","project_id":"proj1"}`,
		`{"agent_id":"a2","hostname":"h2","organization_id":"org2","project_id":"proj2"}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(body))
		rr := httptest.NewRecorder()
		h.ServeRegister(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("register: %s", rr.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.Header.Set("X-Organization-ID", "org1")
	req.Header.Set("X-Project-ID", "proj1")
	rr := httptest.NewRecorder()
	h.ServeList(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatal(rr.Body.String())
	}
	var listed struct {
		Count  int              `json:"count"`
		Agents []registry.Agent `json:"agents"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &listed)
	if listed.Count != 1 || listed.Agents[0].OrganizationID != "org1" {
		t.Fatalf("scoped list: %+v", listed)
	}

	getReq := httptest.NewRequest(http.MethodGet, "/api/agents/a2", nil)
	getReq.Header.Set("X-Organization-ID", "org1")
	getReq.Header.Set("X-Project-ID", "proj1")
	getRR := httptest.NewRecorder()
	h.ServeGet(getRR, getReq)
	if getRR.Code != http.StatusNotFound {
		t.Fatalf("cross-tenant get: got %d", getRR.Code)
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
