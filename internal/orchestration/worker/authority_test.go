package worker

import (
	"os"
	"strings"
	"testing"
)

func TestWorkerUsesEpochFencedWorkGraphCommands(t *testing.T) {
	source, err := os.ReadFile("worker.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	required := []string{
		".HeartbeatAttempt(",
		".RecordAttemptExecution(",
		".SettleAttempt(",
		".ReleaseAttempt(",
	}
	for _, fragment := range required {
		if !strings.Contains(text, fragment) {
			t.Errorf("worker dispatcher is missing %s", fragment)
		}
	}
	forbidden := []string{
		".Heartbeat(",
		".RecordAttemptTurn(",
		".Settle(",
		".Requeue(",
	}
	for _, fragment := range forbidden {
		if strings.Contains(text, fragment) {
			t.Errorf("worker retained legacy Task authority call %s", fragment)
		}
	}
}
