package shell

import (
	"strings"
	"testing"
)

func TestWrapSandboxTempCommandRemapsCdTmp(t *testing.T) {
	got := wrapSandboxTempCommand(`cd /tmp && pwd`)
	if !strings.Contains(got, `cd(){`) {
		t.Fatalf("missing cd wrapper: %q", got)
	}
	if !strings.HasSuffix(got, `cd /tmp && pwd`) && !strings.Contains(got, `cd /tmp && pwd`) {
		t.Fatalf("original command lost: %q", got)
	}
	if wrapSandboxTempCommand("  ") != "" && strings.TrimSpace(wrapSandboxTempCommand("")) != "" {
		t.Fatal("empty command should stay empty")
	}
}

func TestSandboxPathHintDirectsWorkspaceEditsToFileTools(t *testing.T) {
	for _, message := range []string{
		"mkdir generated: Operation not permitted",
		"touch generated/file: Permission denied",
	} {
		hint := sandboxPathHint(message)
		if !strings.Contains(hint, "file_write or file_apply") ||
			!strings.Contains(hint, "parent directories") {
			t.Fatalf("sandboxPathHint(%q) = %q", message, hint)
		}
	}
}
