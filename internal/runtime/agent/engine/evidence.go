package engine

import (
	"encoding/json"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
)

// resultHandleTools are the tools that read a parked result back. A call to one
// of them is what marks a handle consumed.
var resultHandleTools = map[string]struct{}{
	"result_get": {}, "handle_read": {},
}

// noteToolCall records a call before it runs: what was asked for, and whether it
// collects a result the model was told about earlier.
func (e *Engine) noteToolCall(call provider.ToolCall) {
	if e.evidence == nil {
		return
	}
	e.evidence.NoteCall(call.Name, call.Arguments)
	if _, found := resultHandleTools[call.Name]; !found {
		return
	}
	var arguments struct {
		Handle string `json:"handle"`
		ID     string `json:"id"`
	}
	// A malformed argument list is the tool's problem to report; here it only
	// means no handle was consumed.
	if err := json.Unmarshal([]byte(call.Arguments), &arguments); err != nil {
		return
	}
	for _, candidate := range []string{arguments.Handle, arguments.ID} {
		e.evidence.ConsumeHandle(candidate)
	}
}

// observeEvidence folds one successful tool result into the evidence set.
//
// Everything it records comes from result metadata the tools already produce, so
// a tool that says nothing about what it found costs the ledger nothing.
func (e *Engine) observeEvidence(call provider.ToolCall, result tool.Result) {
	if e.evidence == nil || result.Metadata == nil {
		return
	}
	for _, hit := range observedEvidenceHits(result.Metadata) {
		path, ok := e.workspaceRelative(hit.Path)
		if !ok {
			continue
		}
		e.evidence.Observe(evidence.Fact{
			Kind: evidence.Kind(hit.Kind), Path: path, Line: hit.Line,
			Symbol: hit.Symbol, Tool: call.Name, Turn: e.turn,
		})
		// A search hit joins the working set as its weakest source: it names a
		// place worth looking at, which is less than having looked.
		e.observePath(workingset.SourceSearch, hit.Path)
	}
	if path, ok := e.workspaceRelative(observedFileRead(result.Metadata)); ok {
		digest, _ := result.Metadata["content_sha256"].(string)
		e.evidence.NoteRead(path, digest)
	}
	if handle, _ := result.Metadata["handle"].(string); handle != "" {
		e.evidence.NoteHandle(handle, call.Name)
	}
}

// observeToolFailure records that a tool call was rejected or errored.
//
// The failure is fed back to the model in this turn's history anyway; the ledger
// exists for the turn after the history is gone. A model that cannot see that it
// already tried an edit with the wrong anchor will try it again with the same
// anchor.
func (e *Engine) observeToolFailure(call provider.ToolCall, result tool.Result) {
	if e.failures == nil {
		return
	}
	reason := result.Content
	if category, _ := result.Metadata["error_category"].(string); category != "" {
		reason = category + ": " + reason
	}
	e.failures.NoteTool(e.turn, call.Name, reason)
}

// observeVerifyFailure records that a verification did not pass. The gate's own
// verdict only reaches this turn's event stream, so without this the next turn
// cannot tell a suite that was never run from one that ran and failed.
func (e *Engine) observeVerifyFailure(scope, status, message string) {
	if e.failures == nil {
		return
	}
	e.failures.NoteVerify(e.turn, scope, status, message)
}

// Failures reports the attempts that did not work, most recently seen first.
func (e *Engine) Failures() []compact.Failure {
	if e == nil {
		return nil
	}
	return e.failures.List()
}

// observeChangeEvidence records a write. A path the thread read first, or one it
// created, rests on evidence; anything else was written blind.
func (e *Engine) observeChangeEvidence(change toolguard.FileChange) {
	if e.evidence == nil {
		return
	}
	path, ok := e.workspaceRelative(change.Path)
	if !ok {
		return
	}
	read := change.Kind == toolguard.FileCreated || e.working.HasSource(workingset.SourceRead, path)
	e.evidence.MarkChanged(path, e.turn, read)
}

// observeDiagnosticsEvidence records whether a checked path is still failing. A
// receipt the runner could not produce says nothing either way, so it is skipped
// rather than read as clean.
func (e *Engine) observeDiagnosticsEvidence(receipts []diagnostics.Receipt) {
	if e.evidence == nil {
		return
	}
	for _, receipt := range receipts {
		if receipt.Status == "unavailable" {
			continue
		}
		if path, ok := e.workspaceRelative(receipt.Path); ok {
			e.evidence.MarkDiagnostics(path, len(receipt.Diagnostics) > 0)
		}
	}
}

// observeVerifiedEvidence records that verification covered paths. Only a passing
// gate may call it: a failed run is the reason the risk exists.
func (e *Engine) observeVerifiedEvidence(paths []string) {
	if e.evidence == nil {
		return
	}
	relative := make([]string, 0, len(paths))
	for _, path := range paths {
		if candidate, ok := e.workspaceRelative(path); ok {
			relative = append(relative, candidate)
		}
	}
	e.evidence.MarkVerified(relative)
}

// EvidenceSnapshot reports the evidence set as of the last sample: what the
// thread found, what it has not proved, and which call patterns wasted a turn.
func (e *Engine) EvidenceSnapshot() evidence.Snapshot {
	if e == nil {
		return evidence.Snapshot{}
	}
	return e.evidence.Snapshot(e.options.EvidenceLimit)
}

func observedEvidenceHits(metadata map[string]any) []tool.EvidenceHit {
	hits, _ := metadata[tool.MetadataEvidence].([]tool.EvidenceHit)
	return hits
}
