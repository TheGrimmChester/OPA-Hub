package query

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEscapeAndTenantWhere(t *testing.T) {
	if got := escapeSQL("a'b"); got != "a''b" {
		t.Fatalf("%q", got)
	}
	r := httptest.NewRequest("GET", "/api/services", nil)
	r.Header.Set("X-Organization-ID", "nas")
	r.Header.Set("X-Project-ID", "infra")
	got := tenantWhere(r, "")
	want := "(coalesce(nullif(organization_id, ''), 'default-org') = 'nas' AND coalesce(nullif(project_id, ''), 'default-project') = 'infra')"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	r2 := httptest.NewRequest("GET", "/api/services", nil)
	r2.Header.Set("X-Organization-ID", "all")
	if tenantWhere(r2, "") != "1=1" {
		t.Fatalf("all org should be unscoped, got %q", tenantWhere(r2, ""))
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
	// RFC3339 / ISO must become ClickHouse DateTime (no Z / T / fractional).
	cases := map[string]string{
		"2026-07-28T12:14:18Z":        "2026-07-28 12:14:18",
		"2026-07-28T12:14:18.000Z":    "2026-07-28 12:14:18",
		"2026-07-28T12:14:18":         "2026-07-28 12:14:18",
		"2026-07-28 12:14:18.000":     "2026-07-28 12:14:18",
		"2026-07-28 12:14:18":         "2026-07-28 12:14:18",
		"2026-07-28":                  "2026-07-28 00:00:00",
	}
	for in, want := range cases {
		if got := safeTimeLiteral(in); got != want {
			t.Fatalf("safeTimeLiteral(%q)=%q want %q", in, got, want)
		}
	}
}

func TestTimeCompareSQL(t *testing.T) {
	got := timeCompareSQL("start_ts", ">=", "2026-08-04T11:14:47Z")
	want := " AND start_ts >= parseDateTimeBestEffort('2026-08-04 11:14:47')"
	if got != want {
		t.Fatalf("rfc3339: got %q want %q", got, want)
	}
	got = timeCompareSQL("start_ts", "<=", "2026-08-04 12:00:00")
	want = " AND start_ts <= parseDateTimeBestEffort('2026-08-04 12:00:00')"
	if got != want {
		t.Fatalf("chTime: got %q want %q", got, want)
	}
	got = timeCompareSQL("start_ts", ">=", "now() - INTERVAL 1 HOUR")
	want = " AND start_ts >= now() - INTERVAL 1 HOUR"
	if got != want {
		t.Fatalf("now(): got %q want %q", got, want)
	}
	if timeCompareSQL("start_ts", ">=", "") != "" {
		t.Fatal("empty raw should yield empty clause")
	}
	if timeCompareSQL("start_ts", ">=", "x'; DROP") != "" {
		t.Fatal("injection must be rejected")
	}
}

func TestSafeIntervalAndMatchers(t *testing.T) {
	if safeInterval("bogus") != "1 HOUR" {
		t.Fatal("default interval")
	}
	if safeInterval("5m") != "5 MINUTE" {
		t.Fatal("5m")
	}
	ms, err := parseLabelMatchers([]string{"host:web-1", "env!:prod", "name=~api.*"})
	if err != nil || len(ms) != 3 {
		t.Fatalf("%v %v", ms, err)
	}
	if !ms[1].negate || !ms[2].regex {
		t.Fatalf("%+v", ms)
	}
	d, err := parseRangeDuration("14d")
	if err != nil || d != 14*24*time.Hour {
		t.Fatalf("%v %v", d, err)
	}
}

func TestSafeSortDirAndFormatBytes(t *testing.T) {
	if safeSortDir("asc") != "ASC" || safeSortDir("DESC") != "DESC" || safeSortDir("") != "DESC" {
		t.Fatalf("sort dir")
	}
	if formatBytes(500) != "500 B" || !strings.Contains(formatBytes(2048), "KB") {
		t.Fatalf("formatBytes")
	}
	if newID() == "" {
		t.Fatal("newID")
	}
}
