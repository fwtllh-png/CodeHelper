package task

import (
	"os"
	"strings"
	"testing"
)

func TestExecutableTaskStateHasOneWriteAuthority(t *testing.T) {
	executionSource, err := os.ReadFile("execution.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(executionSource)
	for _, forbidden := range []string{
		"UPDATE tasks SET state",
		"INSERT INTO task_lifecycle",
		"INSERT INTO task_attempts",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("legacy executable Task state writer remains: %s", forbidden)
		}
	}
	projectionSource, err := os.ReadFile("workgraph.go")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(projectionSource), "ExecuteProjected(") {
		t.Fatal("Task projection is not committed with WorkGraph facts")
	}
}

func TestExecutableTaskClaimUsesFairQueueAndAuthorityFence(t *testing.T) {
	source, err := os.ReadFile("workgraph_execution.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, required := range []string{
		"selector.Select(candidates",
		"ExpectedAuthorityDigest:",
		"PermissionDigests:",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("Task WorkGraph claim is missing %s", required)
		}
	}
}
