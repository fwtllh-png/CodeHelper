package automation

import (
	"testing"
	"time"
)

func TestParseRRULESubsetAndCanonical(t *testing.T) {
	rule, err := ParseRRULE("rrule:FREQ=HOURLY;INTERVAL=2")
	if err != nil {
		t.Fatal(err)
	}
	if rule.Canonical() != "FREQ=HOURLY;INTERVAL=2" {
		t.Fatalf("canonical = %q", rule.Canonical())
	}
	weekly, err := ParseRRULE("FREQ=WEEKLY;BYDAY=WE,MO")
	if err != nil {
		t.Fatal(err)
	}
	if weekly.Canonical() != "FREQ=WEEKLY;BYDAY=MO,WE" {
		t.Fatalf("weekly canonical = %q", weekly.Canonical())
	}
	if _, err := ParseRRULE("FREQ=DAILY"); err == nil {
		t.Fatal("expected unsupported frequency")
	}
	if _, err := ParseRRULE("FREQ=WEEKLY;BYSECOND=1"); err == nil {
		t.Fatal("expected unsupported field")
	}
}

func TestNextUsesCreationAnchorNotRestart(t *testing.T) {
	rule, err := ParseRRULE("FREQ=HOURLY;INTERVAL=24")
	if err != nil {
		t.Fatal(err)
	}
	anchor := time.Date(2026, 3, 1, 8, 30, 0, 0, time.UTC)
	now := anchor.Add(51 * time.Hour)
	expected := rule.Next(anchor, now)
	reset := rule.Next(now, now)
	if !expected.Equal(time.Date(2026, 3, 4, 8, 30, 0, 0, time.UTC)) {
		t.Fatalf("expected = %s", expected)
	}
	if expected.Equal(reset) {
		t.Fatal("restart-as-anchor must diverge")
	}
}
