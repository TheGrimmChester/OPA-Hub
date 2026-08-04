package query

import "testing"

func TestParseExploreSignal(t *testing.T) {
	cases := map[string]exploreSignal{
		"":         exploreSpans,
		"spans":    exploreSpans,
		"trace":    exploreSpans,
		"metrics":  exploreMetrics,
		"logs":     exploreLogs,
		"rum":      exploreRUM,
		"frontend": exploreRUM,
	}
	for in, want := range cases {
		got, ok := parseExploreSignal(in)
		if !ok || got != want {
			t.Fatalf("parseExploreSignal(%q)=(%q,%v) want %q", in, got, ok, want)
		}
	}
	if _, ok := parseExploreSignal("bogus"); ok {
		t.Fatal("bogus signal should fail")
	}
}

func TestResolveExploreAttr(t *testing.T) {
	// Trace Explorer FacetSidebar fields (identity + NAS-backed runtime dims).
	for _, field := range []string{"service", "environment", "status", "host", "language", "framework", "db_system", "url_path"} {
		col, ok := resolveExploreAttr(exploreSpans, field)
		if !ok || col == "" {
			t.Fatalf("spans.%s should resolve", field)
		}
	}
	if got, _ := resolveExploreAttr(exploreSpans, "host"); got != "hostname" {
		t.Fatalf("host → hostname, got %q", got)
	}
	if got, _ := resolveExploreAttr(exploreSpans, "language"); got != "language" {
		t.Fatalf("language → language, got %q", got)
	}
	if _, ok := resolveExploreAttr(exploreSpans, "level"); ok {
		t.Fatal("level is logs-only")
	}
	if _, ok := resolveExploreAttr(exploreSpans, "'; DROP TABLE"); ok {
		t.Fatal("injection must not resolve")
	}
}

func TestExploreTableAndTime(t *testing.T) {
	if exploreTable(exploreSpans) != "opa.spans_min" {
		t.Fatal(exploreTable(exploreSpans))
	}
	if exploreTimeColumn(exploreSpans) != "start_ts" {
		t.Fatal(exploreTimeColumn(exploreSpans))
	}
	if exploreTable(exploreRUM) != "opa.rum_events" {
		t.Fatal(exploreTable(exploreRUM))
	}
}
