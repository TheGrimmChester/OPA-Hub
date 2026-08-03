package query

import (
	"net/http/httptest"
	"testing"
)

func TestEscapeAndTenantWhere(t *testing.T) {
	if got := escapeSQL("a'b"); got != "a''b" {
		t.Fatalf("%q", got)
	}
	r := httptest.NewRequest("GET", "/api/services", nil)
	r.Header.Set("X-Organization-ID", "nas")
	r.Header.Set("X-Project-ID", "infra")
	got := tenantWhere(r, "")
	if got != "1=1 AND organization_id = 'nas' AND project_id = 'infra'" {
		t.Fatalf("%q", got)
	}
	r2 := httptest.NewRequest("GET", "/api/services", nil)
	r2.Header.Set("X-Organization-ID", "all")
	if tenantWhere(r2, "") != "1=1" {
		t.Fatalf("all org should be unscoped")
	}
}

func TestSafeTimeLiteral(t *testing.T) {
	if safeTimeLiteral("now() - INTERVAL 1 DAY") == "" {
		t.Fatal("expected now() expression")
	}
	if safeTimeLiteral("2026-01-01 00:00:00") == "" {
		t.Fatal("expected plain timestamp")
	}
	if safeTimeLiteral("x'; DROP") != "" {
		t.Fatal("rejected injection")
	}
}
