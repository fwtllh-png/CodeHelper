package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"

	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (e *Engine) progressSignature(kernel *turnkernel.RuntimeKernel) string {
	e.planMu.Lock()
	done := 0
	for _, step := range e.plan.Steps {
		if step.Done() {
			done++
		}
	}
	e.planMu.Unlock()
	evidenceDigest := ""
	if kernel.Intent() == protocol.TurnIntentAnswer ||
		kernel.Intent() == protocol.TurnIntentPlan {
		snapshot := e.EvidenceSnapshot()
		keys := make([]string, 0, len(snapshot.Facts))
		for _, fact := range snapshot.Facts {
			keys = append(keys, fmt.Sprintf(
				"%s\x00%s\x00%d\x00%s",
				fact.Kind,
				fact.Path,
				fact.Line,
				fact.Symbol,
			))
		}
		for _, path := range e.workingLedger().PathsObservedAt(
			agentcontext.SourceRead,
			e.turn,
		) {
			keys = append(keys, "read\x00"+path)
		}
		sort.Strings(keys)
		sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
		evidenceDigest = hex.EncodeToString(sum[:])
	}
	return kernel.ProgressSignature(done, evidenceDigest)
}
