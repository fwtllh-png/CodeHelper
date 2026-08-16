package workflow

import (
	"os"
	"strings"
	"testing"
)

func TestWorkflowRuntimeDoesNotOwnDAGReadiness(t *testing.T) {
	source, err := os.ReadFile("runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(source)
	for _, forbidden := range []string{
		"runWave",
		"func (e *execution) walk",
		".dependencies()",
		"dependencyFailed",
	} {
		if strings.Contains(text, forbidden) {
			t.Errorf("Workflow retained DAG state authority: %s", forbidden)
		}
	}
	for _, required := range []string{
		"protocol.NodeStateReady",
		"kernel.CommandClaimNode",
		"kernel.CommandSettleExecution",
		"e.reserveAttempt(reservationID)",
		"e.ledger.Settle(",
		"ExpectedAuthorityDigest:",
	} {
		if !strings.Contains(text, required) {
			t.Errorf("Workflow dispatcher is missing %s", required)
		}
	}
}
