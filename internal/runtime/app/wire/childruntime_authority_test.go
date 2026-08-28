package wire

import (
	"os"
	"strings"
	"testing"
)

func TestChildRuntimeUsesAgentGraphAndDirectEventObservation(t *testing.T) {
	source, err := os.ReadFile("childruntime.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{
		".Events(",
		"time.Sleep(",
		"waitForStart",
		"waitForTerminal",
		"ensurePump",
		"func (c *childRuntime) pump",
		"orchestration/kernel",
		"orchestration/model",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("ChildRuntime retained polling authority: %s", forbidden)
		}
	}
	for _, required := range []string{
		"runtime.ObserveEvents(c.observe)",
		"manager.ActivateResident(agentID)",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("ChildRuntime is missing %s", required)
		}
	}
}
