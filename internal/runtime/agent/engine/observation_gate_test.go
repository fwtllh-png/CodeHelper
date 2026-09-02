package engine

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
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
	blocked := engine.observationGate(call, false)
	if blocked == nil || !blocked.IsError ||
		blocked.Metadata["error_category"] != "located_site_window_required" {
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
	windowed := provider.ToolCall{
		Name:      "file_read",
		Arguments: `{"path":"types.h","start_line":88}`,
	}
	if got := engine.observationGate(windowed, true); got != nil {
		t.Fatalf("finish-only windowed file_read blocked: %+v", got)
	}
}

func TestLocatedSitesComeFromEvidenceLineHits(t *testing.T) {
	engine := evidenceEngine(t)
	engine.observeEvidence(
		provider.ToolCall{Name: "search_text", Arguments: `{"query":"accept"}`},
		tool.Result{Outcome: &tool.Outcome{
			Facts: &tool.OutcomeFacts{Evidence: []tool.EvidenceHit{
				{Kind: tool.EvidenceReference, Path: "paxos_core.cpp", Line: 412},
			}},
		}},
	)
	sites := engine.locatedSites()
	if len(sites) != 1 || sites[0] != "paxos_core.cpp:412" {
		t.Fatalf("located sites = %v", sites)
	}
	blocked := engine.observationGate(provider.ToolCall{
		Name:      "file_read",
		Arguments: `{"path":"paxos_core.cpp"}`,
	}, false)
	if blocked == nil || !blocked.IsError {
		t.Fatalf("evidence-located file_read = %+v", blocked)
	}
}
