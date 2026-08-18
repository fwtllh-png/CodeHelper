package acp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestConvergenceFailureReturnsRecoverablePromptResult(t *testing.T) {
	var output bytes.Buffer
	active := &activeTurn{
		sessionID: "session-1",
		turnID:    "turn-1",
		requestID: json.RawMessage(`"prompt-1"`),
		done:      make(chan struct{}),
	}
	server := &Server{
		output: &frameWriter{buffer: bufio.NewWriter(&output)},
		active: map[string]*activeTurn{active.sessionID: active},
	}
	server.advance(active, protocol.Event{
		Kind: protocol.EventTurnFailed,
		Data: &protocol.TurnFailedData{
			Code:    protocol.CodeConflict,
			Message: "convergence budget exhausted",
			Convergence: &protocol.TurnConvergence{
				Cause:          "output_limit",
				Used:           3,
				Limit:          3,
				Summary:        "The answer is partially complete.",
				PendingActions: []string{"Continue the answer."},
			},
		},
	})
	var frame struct {
		Result struct {
			StopReason string `json:"stopReason"`
			Output     string `json:"output"`
		} `json:"result"`
		Error json.RawMessage `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &frame); err != nil {
		t.Fatal(err)
	}
	if len(frame.Error) != 0 ||
		frame.Result.StopReason != "max_tokens" ||
		frame.Result.Output != "The answer is partially complete." {
		t.Fatalf("ACP convergence frame = %+v", frame)
	}
	select {
	case <-active.done:
	default:
		t.Fatal("convergence result did not settle the active prompt")
	}
}

func TestUnavailableTurnFailurePreservesRecoverableACPCode(t *testing.T) {
	var output bytes.Buffer
	active := &activeTurn{
		sessionID: "session-1",
		turnID:    "turn-1",
		requestID: json.RawMessage(`"prompt-1"`),
		done:      make(chan struct{}),
	}
	server := &Server{
		output: &frameWriter{buffer: bufio.NewWriter(&output)},
		active: map[string]*activeTurn{active.sessionID: active},
	}
	fault := &protocol.FaultMetadata{
		Origin:      protocol.FaultOriginProvider,
		Disposition: protocol.FaultRetryTurn,
		SideEffects: protocol.SideEffectUnchanged,
	}
	server.advance(active, protocol.Event{
		Kind: protocol.EventTurnFailed,
		Data: &protocol.TurnFailedData{
			Code:    protocol.CodeUnavailable,
			Message: "provider unavailable",
			Fault:   fault,
		},
	})
	var frame struct {
		Error struct {
			Code int `json:"code"`
			Data struct {
				Code  protocol.ErrorCode      `json:"code"`
				Fault *protocol.FaultMetadata `json:"fault"`
			} `json:"data"`
		} `json:"error"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &frame); err != nil {
		t.Fatal(err)
	}
	if frame.Error.Code != codeUnavailable ||
		frame.Error.Data.Code != protocol.CodeUnavailable ||
		frame.Error.Data.Fault == nil ||
		frame.Error.Data.Fault.Disposition != protocol.FaultRetryTurn {
		t.Fatalf("ACP unavailable frame = %+v", frame)
	}
}

func TestPreparedStartTurnPreservesIntent(t *testing.T) {
	request := preparedStartTurn(
		"internal recovery context",
		"Continue: fix the parser",
		protocol.TurnIntentWorkspaceChange,
		"recover-source",
		&protocol.TurnRecoveryContext{
			Action:       protocol.TurnRecoveryContinue,
			SourceTurnID: "turn-source",
		},
	)
	if request.kind != protocol.OperationStartTurn {
		t.Fatalf("operation kind = %q", request.kind)
	}
	payload, ok := request.payload.(*protocol.StartTurnPayload)
	if !ok {
		t.Fatalf("payload type = %T", request.payload)
	}
	if payload.Prompt != "internal recovery context" ||
		payload.DisplayPrompt != "Continue: fix the parser" ||
		payload.Intent != protocol.TurnIntentWorkspaceChange ||
		payload.Recovery == nil ||
		payload.Recovery.SourceTurnID != "turn-source" ||
		request.idempotencyKey != "recover-source" {
		t.Fatalf("prepared request = %+v, payload = %+v", request, payload)
	}
}
