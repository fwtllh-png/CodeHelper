package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestToolExecutionReceiptProjectsIntoDurableToolResult(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Millisecond)
	digest := strings.Repeat("a", 64)
	source := &tool.ExecutionReceipt{
		Tool: tool.ToolRef{
			Name: "exec_command", Source: "builtin:exec_command",
			CatalogID: "catalog-1", Generation: 2, Revision: 3, Authority: 4,
		},
		Source:      tool.InvocationSourceModel,
		Disposition: tool.DispositionWaitForTeardown,
		Attempts: []tool.AttemptReceipt{{
			Sequence: 1, Sandbox: "strong", Status: tool.OutcomeRejected,
			TerminalOwner:           tool.TerminalOwnerGuard,
			PermissionSchemaVersion: 1, PermissionRevision: 7,
			PermissionDigest: digest, PermissionCapability: tool.CapabilityProcess,
			PermissionAccess: tool.AccessRead, Enforcement: "strong",
			Backend: "seatbelt", SandboxStrength: "strong",
			WorkspaceRoot: "/workspace", ReadRoots: []string{"/workspace"},
			WritePaths:  []string{"/workspace/result.txt"},
			NetworkMode: "managed", LoopbackAllowed: true, ProcessAllowed: true,
			Provenance: []tool.PermissionProvenance{{
				Kind: "managed", Value: "grant", Digest: digest, Revision: 7,
			}},
			Denial: &sandbox.Denial{
				Backend: "seatbelt", Operation: sandbox.DenialWrite,
				Resource:   "/workspace/result.txt",
				ReasonCode: sandbox.ReasonPathWriteNotAuthorized,
			},
			Amendment: &tool.PermissionAmendmentReceipt{
				BasePermissionDigest: digest, Kind: "path_write",
				Resource: "/workspace/result.txt", Decision: "denied",
			},
			StartedAt: now, CompletedAt: now.Add(time.Millisecond),
			DurationMS: 1,
		}},
		TerminalStatus: tool.OutcomeRejected,
		TerminalOwner:  tool.TerminalOwnerGuard,
	}
	projected := projectToolExecutionReceipt(source)
	source.Attempts[0].ReadRoots[0] = "/tampered"
	source.Attempts[0].Denial.Resource = "/tampered"
	if projected == nil || projected.Tool.Name != "exec_command" ||
		projected.TerminalOwner != "guard" ||
		len(projected.Attempts) != 1 ||
		projected.Attempts[0].PermissionDigest != digest ||
		projected.Attempts[0].ReadRoots[0] != "/workspace" ||
		!projected.Attempts[0].LoopbackAllowed ||
		projected.Attempts[0].Denial.Resource != "/workspace/result.txt" {
		t.Fatalf("projected receipt = %+v", projected)
	}
	event, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 1, OperationID: "operation", ThreadID: "thread", TurnID: "turn",
		ItemID: "item",
	}, &protocol.ToolResultData{
		Tool: "exec_command", CallID: "call", Output: "denied",
		IsError: true, Execution: projected,
	})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(event)
	if err != nil {
		t.Fatal(err)
	}
	var decoded protocol.Event
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	result, ok := decoded.Data.(*protocol.ToolResultData)
	if !ok || result.Execution == nil ||
		result.Execution.Attempts[0].PermissionDigest != digest ||
		!result.Execution.Attempts[0].LoopbackAllowed {
		t.Fatalf("decoded result = %#v", decoded.Data)
	}
}
