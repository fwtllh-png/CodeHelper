package engine

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	agentcontext "github.com/fwtllh-png/QCode/internal/runtime/agent/context"
	"github.com/fwtllh-png/QCode/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestObservationGateRequiresWindowAfterSearchHit(t *testing.T) {
	engine := evidenceEngine(t)
	call := provider.ToolCall{
		Name:      "file_read",
		Arguments: `{"path":"paxos_core.cpp"}`,
	}
	if blocked := engine.observationGate(call, false); blocked != nil {
		t.Fatalf("unlocated file_read blocked: %+v", blocked)
	}

	engine.observePath(agentcontext.SourceSearch, "paxos_core.cpp")
	if blocked := engine.observationGate(call, false); blocked != nil {
		t.Fatalf("path-only search hit blocked: %+v", blocked)
	}

	engine.observeEvidence(
		provider.ToolCall{Name: "search_text", Arguments: `{"query":"accept"}`},
		tool.Result{Outcome: &tool.Outcome{
			Facts: &tool.OutcomeFacts{Evidence: []tool.EvidenceHit{
				{Kind: tool.EvidenceReference, Path: "paxos_core.cpp", Line: 412},
			}},
		}},
	)
	blocked := engine.observationGate(call, false)
	if blocked == nil || !blocked.IsError ||
		blocked.Metadata["error_category"] != "located_site_window_required" ||
		blocked.Metadata["required_action"] != "file_read" ||
		blocked.Metadata["start_line"] != 412 ||
		!strings.Contains(
			blocked.Content,
			`{"path":"paxos_core.cpp","start_line":412}`,
		) {
		t.Fatalf("located file_read without start_line = %+v", blocked)
	}

	windowed := provider.ToolCall{
		Name:      "file_read",
		Arguments: `{"path":"paxos_core.cpp","start_line":412}`,
	}
	if got := engine.observationGate(windowed, false); got != nil {
		t.Fatalf("windowed file_read blocked: %+v", got)
	}
	other := provider.ToolCall{
		Name:      "file_read",
		Arguments: `{"path":"types.h"}`,
	}
	if got := engine.observationGate(other, false); got != nil {
		t.Fatalf("unlocated sibling file_read blocked: %+v", got)
	}
	absoluteArgs, err := json.Marshal(map[string]string{
		"path": filepath.Join(engine.options.Workspace, "paxos_core.cpp"),
	})
	if err != nil {
		t.Fatal(err)
	}
	absolute := provider.ToolCall{
		Name:      "file_read",
		Arguments: string(absoluteArgs),
	}
	if got := engine.observationGate(absolute, false); got == nil {
		t.Fatal("absolute located file_read without start_line allowed")
	}
}

func TestObservationGateRequiresWindowInFinishOnly(t *testing.T) {
	engine := evidenceEngine(t)
	call := provider.ToolCall{
		Name:      "file_read",
		Arguments: `{"path":"types.h"}`,
	}
	blocked := engine.observationGate(call, true)
	if blocked == nil || !blocked.IsError ||
		blocked.Metadata["error_category"] != "located_site_window_required" {
		t.Fatalf("finish-only file_read without start_line = %+v", blocked)
	}
	if blocked.Metadata["start_line"] != 1 ||
		!strings.Contains(
			blocked.Content,
			`{"path":"types.h","start_line":1}`,
		) {
		t.Fatalf("finish-only retry window = %+v", blocked)
	}
	windowed := provider.ToolCall{
		Name:      "file_read",
		Arguments: `{"path":"types.h","start_line":88}`,
	}
	if got := engine.observationGate(windowed, true); got != nil {
		t.Fatalf("finish-only windowed file_read blocked: %+v", got)
	}
}

func TestObservationGateRejectsKnownPathFullReadWithoutRenewing(t *testing.T) {
	engine := evidenceEngine(t)
	kernel := newEngineTurnKernel(
		protocol.TurnIntentAnswer,
		"act",
		nil,
		0,
		nil,
		nil,
	)
	if err := kernel.BindWorkItem(turnkernel.BindWorkItem{
		Goal: "你上一轮socket_transport_test测试时死锁了",
		KnownReads: map[string]turnkernel.WorkItemRead{
			"socket_transport.cpp": {Window: "full"},
		},
	}); err != nil {
		t.Fatal(err)
	}
	engine.admissionKernel = kernel
	before := engine.progressSignature(kernel)
	blocked := engine.observationGate(provider.ToolCall{
		Name:      "file_read",
		Arguments: `{"path":"socket_transport.cpp"}`,
	}, false)
	if blocked == nil || !blocked.IsError ||
		blocked.Metadata["error_category"] != "work_item_known_read_refused" ||
		blocked.Metadata["retry_original"] != false ||
		blocked.Metadata["required_action"] == "" {
		t.Fatalf("known full file_read = %+v", blocked)
	}
	if after := engine.progressSignature(kernel); after != before {
		t.Fatalf("known-read refusal renewed signature: before=%q after=%q", before, after)
	}
	windowed := provider.ToolCall{
		Name:      "file_read",
		Arguments: `{"path":"socket_transport.cpp","start_line":88}`,
	}
	if got := engine.observationGate(windowed, false); got != nil {
		t.Fatalf("new window file_read blocked: %+v", got)
	}
}

func TestObservationGateRejectsGitPatrolOnContinue(t *testing.T) {
	engine := evidenceEngine(t)
	kernel, err := turnkernel.NewRuntimeKernel(
		turnkernel.KernelIdentity{
			TurnID:          "continue-git",
			ProfileRevision: 1,
			Goal:            "你上一轮socket_transport_test测试时死锁了",
			WorkItem: turnkernel.WorkItem{
				KnownReads: map[string]turnkernel.WorkItemRead{
					"socket_transport.cpp": {Window: "full"},
				},
			},
		},
		protocol.TurnIntentAnswer,
		"act",
		&protocol.TurnRecoveryContext{
			Action:       protocol.TurnRecoveryContinue,
			SourceTurnID: "turn-source",
		},
		false,
		nil,
		nil,
		nil,
		nil,
		nil,
		turnkernel.DefaultPolicy(),
		turnkernel.NewEphemeralCoordinatorRuntime(),
	)
	if err != nil {
		t.Fatal(err)
	}
	engine.admissionKernel = kernel
	before := engine.progressSignature(kernel)
	for _, name := range []string{"git_status", "git_diff"} {
		blocked := engine.observationGate(provider.ToolCall{Name: name}, false)
		if blocked == nil || !blocked.IsError ||
			blocked.Metadata["error_category"] != "work_item_git_patrol_refused" ||
			blocked.Metadata["retry_original"] != false {
			t.Fatalf("%s patrol = %+v", name, blocked)
		}
	}
	if after := engine.progressSignature(kernel); after != before {
		t.Fatalf("git patrol refusal renewed signature: before=%q after=%q", before, after)
	}
}

func TestLocatedReadLineComesFromEvidenceLineHits(t *testing.T) {
	engine := evidenceEngine(t)
	engine.observeEvidence(
		provider.ToolCall{Name: "search_text", Arguments: `{"query":"accept"}`},
		tool.Result{Outcome: &tool.Outcome{
			Facts: &tool.OutcomeFacts{Evidence: []tool.EvidenceHit{
				{Kind: tool.EvidenceReference, Path: "paxos_core.cpp", Line: 412},
				{Kind: tool.EvidenceTextMatch, Path: "paxos_core.cpp", Line: 127},
			}},
		}},
	)
	sites := engine.locatedSites()
	if len(sites) != 2 || sites[0] != "paxos_core.cpp:412" ||
		sites[1] != "paxos_core.cpp:127" {
		t.Fatalf("located sites = %v", sites)
	}
	line, found := engine.locatedReadLine("paxos_core.cpp")
	if !found || line != 127 {
		t.Fatalf("located read line = %d, %t", line, found)
	}
	blocked := engine.observationGate(provider.ToolCall{
		Name:      "file_read",
		Arguments: `{"path":"paxos_core.cpp"}`,
	}, false)
	if blocked == nil || !blocked.IsError {
		t.Fatalf("evidence-located file_read = %+v", blocked)
	}
}
