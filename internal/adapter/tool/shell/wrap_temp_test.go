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
