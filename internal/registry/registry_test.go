package registry_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	opentenant "github.com/TheGrimmChester/open-tenant-go"

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

func TestEnrollFailsClosedWhenAuthRequiredAndTokenEmpty(t *testing.T) {
	reg := registry.New(time.Minute)
	h := &registry.Handler{Reg: reg, EnrollToken: "", AuthRequired: true}
	req := httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(`{"hostname":"x","organization_id":"org1"}`))
	rr := httptest.NewRecorder()
	h.ServeRegister(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("auth-required + empty enroll token: got %d want 401", rr.Code)
	}

	// Lab posture: auth off + empty token remains open.
	hOpen := &registry.Handler{Reg: reg, EnrollToken: "", AuthRequired: false}
	rr2 := httptest.NewRecorder()
	hOpen.ServeRegister(rr2, httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(`{"hostname":"y","organization_id":"org1"}`)))
	if rr2.Code != http.StatusOK {
		t.Fatalf("lab open enroll: got %d body %s", rr2.Code, rr2.Body.String())
	}
}

func TestListScopedEmptyOrgUnderAuthFailClosed(t *testing.T) {
	prev := opentenant.AuthEnforced()
	opentenant.SetAuthEnforced(true)
	t.Cleanup(func() { opentenant.SetAuthEnforced(prev) })

	reg := registry.New(time.Minute)
	h := &registry.Handler{Reg: reg, EnrollToken: ""}
	for _, body := range []string{
		`{"agent_id":"a1","hostname":"h1","organization_id":"org1","project_id":"proj1"}`,
		`{"agent_id":"a-def","hostname":"hd","organization_id":"org1","project_id":"default-project"}`,
	} {
		rr := httptest.NewRecorder()
		h.ServeRegister(rr, httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(body)))
		if rr.Code != http.StatusOK {
			t.Fatalf("register: %s", rr.Body.String())
		}
	}

	// Missing org header under auth must not invent default-org / dump all agents.
	listRR := httptest.NewRecorder()
	h.ServeList(listRR, httptest.NewRequest(http.MethodGet, "/api/agents", nil))
	if listRR.Code != http.StatusOK {
		t.Fatal(listRR.Body.String())
	}
	var listed struct {
		Count  int              `json:"count"`
		Agents []registry.Agent `json:"agents"`
	}
	_ = json.Unmarshal(listRR.Body.Bytes(), &listed)
	if listed.Count != 0 || len(listed.Agents) != 0 {
		t.Fatalf("empty org under auth must return nil/empty agents, got count=%d agents=%+v", listed.Count, listed.Agents)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.Header.Set("X-Organization-ID", "all")
	req.Header.Set("X-Project-ID", "all")
	listRR = httptest.NewRecorder()
	h.ServeList(listRR, req)
	_ = json.Unmarshal(listRR.Body.Bytes(), &listed)
	if listed.Count != 0 {
		t.Fatalf("all/all under auth must return 0 agents, got %d", listed.Count)
	}

	// Concrete org + empty/all project collapses to default-project (not org-wide dump).
	req = httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.Header.Set("X-Organization-ID", "org1")
	listRR = httptest.NewRecorder()
	h.ServeList(listRR, req)
	_ = json.Unmarshal(listRR.Body.Bytes(), &listed)
	if listed.Count != 1 || listed.Agents[0].ID != "a-def" {
		t.Fatalf("empty project under auth should default-project only: %+v", listed)
	}
}

func TestListScopedHonorsProjectIDs(t *testing.T) {
	prev := opentenant.AuthEnforced()
	opentenant.SetAuthEnforced(true)
	t.Cleanup(func() { opentenant.SetAuthEnforced(prev) })

	reg := registry.New(time.Minute)
	h := &registry.Handler{Reg: reg, EnrollToken: ""}
	for _, body := range []string{
		`{"agent_id":"a-alpha","hostname":"ha","organization_id":"acme","project_id":"alpha"}`,
		`{"agent_id":"a-beta","hostname":"hb","organization_id":"acme","project_id":"beta"}`,
		`{"agent_id":"a-gamma","hostname":"hg","organization_id":"acme","project_id":"gamma"}`,
		`{"agent_id":"a-other","hostname":"ho","organization_id":"other","project_id":"alpha"}`,
	} {
		rr := httptest.NewRecorder()
		h.ServeRegister(rr, httptest.NewRequest(http.MethodPost, "/api/agents/register", bytes.NewBufferString(body)))
		if rr.Code != http.StatusOK {
			t.Fatalf("register: %s", rr.Body.String())
		}
	}

	req := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req.Header.Set("X-Organization-ID", "acme")
	req.Header.Set("X-Project-IDs", "alpha,beta")
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
	if listed.Count != 2 {
		t.Fatalf("ProjectIDs allowlist: got count=%d agents=%+v", listed.Count, listed.Agents)
	}
	seen := map[string]bool{}
	for _, a := range listed.Agents {
		seen[a.ID] = true
		if a.ProjectID != "alpha" && a.ProjectID != "beta" {
			t.Fatalf("unexpected project in list: %+v", a)
		}
		if a.OrganizationID != "acme" {
			t.Fatalf("foreign org leaked: %+v", a)
		}
	}
	if !seen["a-alpha"] || !seen["a-beta"] || seen["a-gamma"] || seen["a-other"] {
		t.Fatalf("allowlist membership wrong: %+v", seen)
	}

	// Empty ProjectIDs + empty/all project under auth → default-project only.
	req2 := httptest.NewRequest(http.MethodGet, "/api/agents", nil)
	req2.Header.Set("X-Organization-ID", "acme")
	req2.Header.Set("X-Project-ID", "all")
	rr2 := httptest.NewRecorder()
	h.ServeList(rr2, req2)
	_ = json.Unmarshal(rr2.Body.Bytes(), &listed)
	if listed.Count != 0 {
		t.Fatalf("all project under auth must not widen; got %+v", listed)
	}
}
