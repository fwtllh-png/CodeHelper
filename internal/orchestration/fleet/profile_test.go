package fleet_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fleet"
)

func TestLoadProfileAndRoster(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "default.toml")
	if err := fleet.WriteTemplate(path); err != nil {
		t.Fatal(err)
	}
	profile, err := fleet.LoadProfile(path)
	if err != nil {
		t.Fatal(err)
	}
	if profile.Name != "default" || profile.MaxWorkers != 2 {
		t.Fatalf("profile = %#v", profile)
	}
	if profile.LeaseTimeoutDuration() != 2*time.Minute {
		t.Fatalf("lease = %s", profile.LeaseTimeoutDuration())
	}
	roster, err := fleet.LoadRoster(dir, "default")
	if err != nil {
		t.Fatal(err)
	}
	if roster.Name != profile.Name {
		t.Fatalf("roster = %#v", roster)
	}
}
