package admission

import "testing"

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
