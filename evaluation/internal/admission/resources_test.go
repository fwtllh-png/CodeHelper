package admission

import (
	"os"
	"path/filepath"
	"testing"
)

func TestVerifyResourceCleanupRejectsNewOwnedResources(t *testing.T) {
	before := ResourceSnapshot{
		TemporaryDirectories: []string{"/tmp/cdt-existing"},
		RuntimeProcesses:     []int{100},
	}
	after := ResourceSnapshot{
		TemporaryDirectories: []string{
			"/tmp/cdt-existing",
			"/tmp/cdt-leaked",
		},
		RuntimeProcesses: []int{100, 200},
	}
	digest, err := VerifyResourceCleanup(before, after)
	if err == nil {
		t.Fatal("resource cleanup accepted leaked resources")
	}
	if digest == "" {
		t.Fatal("resource cleanup returned no evidence digest")
	}
}

func TestVerifyResourceCleanupAllowsPreexistingResources(t *testing.T) {
	snapshot := ResourceSnapshot{
		TemporaryDirectories: []string{"/tmp/cdt-existing"},
		RuntimeProcesses:     []int{100},
	}
	digest, err := VerifyResourceCleanup(snapshot, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" {
		t.Fatal("resource cleanup returned no evidence digest")
	}
}

func TestSnapshotH4ResourcesOwnsOnlyCanaryDirectories(t *testing.T) {
	root := t.TempDir()
	canary := filepath.Join(root, "codehelper-h4-canary-owned")
	foreign := filepath.Join(root, "codehelper-h3-endurance-foreign")
	if err := os.Mkdir(canary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(foreign, 0o700); err != nil {
		t.Fatal(err)
	}
	snapshot, err := SnapshotH4Resources(
		t.Context(),
		root,
		filepath.Join(root, "missing-package-binary"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.TemporaryDirectories) != 1 ||
		snapshot.TemporaryDirectories[0] != canary {
		t.Fatalf("H4 resource snapshot = %+v", snapshot)
	}
}
