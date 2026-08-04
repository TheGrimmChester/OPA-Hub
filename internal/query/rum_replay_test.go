package query

import (
	"encoding/json"
	"testing"
)

func TestPathTail(t *testing.T) {
	if got := pathTail("/api/rum/replay-timeline/abc/", "/api/rum/replay-timeline/"); got != "abc" {
		t.Fatalf("%q", got)
	}
	if got := pathTail("/api/rum/replay/sid-1", "/api/rum/replay/"); got != "sid-1" {
		t.Fatalf("%q", got)
	}
}

func TestParseReplayEventsJSON(t *testing.T) {
	raw := `[{"t":1000,"type":"click","target":"#btn"},{"t":1100,"type":"navigate","url":"/x"}]`
	parsed := parseReplayEventsJSON(raw)
	if len(parsed) != 2 {
		t.Fatalf("len=%d", len(parsed))
	}
	e0 := mapReplayEvent(parsed[0], 0, 0)
	if e0.Type != "click" || e0.Target != "#btn" || e0.T != 1000 {
		t.Fatalf("%+v", e0)
	}
	e1 := mapReplayEvent(parsed[1], 1, 0)
	if e1.Type != "navigation" {
		t.Fatalf("navigate alias: %+v", e1)
	}
}

func TestParseReplayEventsWrappedString(t *testing.T) {
	inner := `[{"t":5,"type":"snapshot","title":"Home"}]`
	wrapped, err := json.Marshal(inner)
	if err != nil {
		t.Fatal(err)
	}
	parsed := parseReplayEventsJSON(string(wrapped))
	if len(parsed) != 1 || strFromMap(parsed[0], "type") != "snapshot" {
		t.Fatalf("%v", parsed)
	}
}

func TestAsBoolish(t *testing.T) {
	if !asBoolish(map[string]any{"m": uint8(1)}, "m") {
		t.Fatal("uint8 1")
	}
	if asBoolish(map[string]any{"m": "0"}, "m") {
		t.Fatal("string 0")
	}
}
