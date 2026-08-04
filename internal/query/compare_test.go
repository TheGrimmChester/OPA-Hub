package query

import (
	"testing"
	"time"
)

func TestCompareDimensionsAllowlist(t *testing.T) {
	for _, dim := range []string{"language_version", "name", "service", "db_system"} {
		if !compareDimensions[dim] {
			t.Fatalf("expected %q allowed", dim)
		}
	}
	if compareDimensions["'; DROP"] {
		t.Fatal("injection should not be allowed")
	}
}

func TestHostIsReporting(t *testing.T) {
	recent := time.Now().UTC().Add(-time.Minute).Format("2006-01-02 15:04:05")
	if !hostIsReporting(recent) {
		t.Fatal("recent host should report")
	}
	stale := time.Now().UTC().Add(-10 * time.Minute).Format("2006-01-02 15:04:05")
	if hostIsReporting(stale) {
		t.Fatal("stale host should not report")
	}
	if hostIsReporting("not-a-date") {
		t.Fatal("invalid timestamp should not report")
	}
}
