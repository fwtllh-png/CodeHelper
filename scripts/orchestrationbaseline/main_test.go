package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsNewLifecycleSchedulerSite(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/orchestration/existing.go", `package orchestration
import "time"
func existing() { go func() {}(); _ = time.NewTicker(time.Second) }
`)
	writeBaseline(t, root, map[string]siteLimit{
		"internal/orchestration/existing.go": {GoStatements: 1, NewTickers: 1},
	})
	if _, err := run(root, "baseline.json", ""); err != nil {
		t.Fatalf("baseline rejected: %v", err)
	}

	writeFixture(t, root, "internal/orchestration/new.go", `package orchestration
func added() { go func() {}() }
`)
	if _, err := run(root, "baseline.json", ""); err == nil ||
		!strings.Contains(err.Error(), "new lifecycle site") {
		t.Fatalf("new scheduler error = %v", err)
	}
}

func TestRunAllowsSchedulerRetirement(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/orchestration/existing.go", `package orchestration
func existing() {}
`)
	writeBaseline(t, root, map[string]siteLimit{
		"internal/orchestration/existing.go": {GoStatements: 1},
	})
	if _, err := run(root, "baseline.json", ""); err != nil {
		t.Fatalf("scheduler retirement rejected: %v", err)
	}
}

func writeBaseline(t *testing.T, root string, sites map[string]siteLimit) {
	t.Helper()
	data, err := json.Marshal(contract{
		SchemaVersion: 1,
		RequirementID: "TASK-ORCHESTRATION-OR0",
		Roots:         []string{"internal/orchestration"},
		Sites:         sites,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "baseline.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeFixture(t *testing.T, root, relative, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
