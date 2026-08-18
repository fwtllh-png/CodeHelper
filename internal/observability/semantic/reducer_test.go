package semantic

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestReducerBuildsLifecycleVisibilityAndExplainGraph(t *testing.T) {
	envelopes := lifecycleFixture()
	graph, err := (Reducer{Payloads: acceptingVerifier{}}).Reduce(
		t.Context(),
		envelopes,
	)
	if err != nil {
		t.Fatal(err)
	}
	turn := graph.Turns[turnKey("runtime-1", "turn-1")]
	if turn.Window.Status != StatusCommitted ||
		turn.Window.StartedSequence != 1 ||
		turn.Window.EndedSequence != 11 {
		t.Fatalf("turn = %+v", turn)
	}
	inference := graph.InferenceCalls[inferenceKey("runtime-1", "turn-1", "sample-1")]
	if inference.Window.Status != StatusCompleted ||
		inference.Attempt != 1 ||
		!inference.AttemptExplicit {
		t.Fatalf("inference = %+v", inference)
	}
	tool := graph.ToolAttempts[toolKey("runtime-1", "turn-1", "call-1", 2)]
	if tool.Window.Status != StatusCompleted ||
		tool.Attempt != 2 ||
		!tool.AttemptExplicit ||
		tool.RuntimeResult == nil ||
		len(tool.ModelVisible) != 1 {
		t.Fatalf("tool = %+v", tool)
	}
	if len(graph.Visibility) != 1 ||
		graph.Visibility[0].SourceID != tool.ID ||
		graph.Visibility[0].Target != "model_request" {
		t.Fatalf("visibility = %+v", graph.Visibility)
	}
	approval := graph.Approvals[semanticID("approval", "runtime-1", "turn-1", "call-1")]
	if approval.Window.Status != StatusResolved {
		t.Fatalf("approval = %+v", approval)
	}
	terminal := graph.TerminalOps[semanticID("terminal", "runtime-1", "turn-1")]
	if terminal.Window.Status != StatusCommitted {
		t.Fatalf("terminal = %+v", terminal)
	}
	explanation, err := graph.ExplainTool("call-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(explanation.Visibility) != 1 ||
		len(explanation.Payloads) != 1 ||
		explanation.NextEvidence != "" {
		t.Fatalf("explanation = %+v", explanation)
	}
	visibility, err := graph.ExplainVisibility("sample-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(visibility.Visibility) != 1 {
		t.Fatalf("visibility explanation = %+v", visibility)
	}
}

func TestExplainFailureReconstructsStopAttemptsRecoveryAndCorrelation(
	t *testing.T,
) {
	turnIdentity := observation.Identity{
		RuntimeID: "runtime-1", SessionID: "session-1",
		ThreadID: "thread-1", TurnID: "turn-failed",
		ResumeID: "operation-resume-1",
	}
	providerIdentity := withAttempt(
		withSample(turnIdentity, "sample-2"),
		2,
	)
	effectIdentity := observation.Identity{
		RuntimeID: "runtime-1", EffectID: "terminal:turn-failed",
		RunID: "run-1", NodeID: "node-1", AttemptID: "attempt-2",
		LeaseOwner: "worker-1", LeaseEpoch: 7,
	}
	terminalIdentity := withTerminal(turnIdentity)
	terminalIdentity.EffectID = "terminal:turn-failed"
	outcome := observation.TerminalOutcome{
		Status: observation.TerminalFailed,
		Code:   string(protocol.CodeUnavailable),
		Fault: &protocol.FaultMetadata{
			Origin:      protocol.FaultOriginProvider,
			Stage:       protocol.FaultStageModelSample,
			Disposition: protocol.FaultRetryTurn,
			RetryOwner:  protocol.FaultRetryOwnerEngine,
			ResumeHint:  protocol.FaultResumeRetryTurn,
			SideEffects: protocol.SideEffectUnchanged,
		},
	}
	summary, err := observation.EncodeTerminalSummary("measurement-1", outcome)
	if err != nil {
		t.Fatal(err)
	}
	envelopes := []observation.Envelope{
		testEnvelope(observation.KindTurnStarted, 1, turnIdentity),
		testEnvelope(observation.KindModelRequestSent, 2, providerIdentity),
		testEnvelope(observation.KindModelRequestFailed, 3, providerIdentity),
		testEnvelope(observation.KindEffectRequested, 4, effectIdentity),
		testEnvelope(observation.KindEffectStarted, 5, effectIdentity),
		testEnvelope(observation.KindEffectFinished, 6, effectIdentity),
		testEnvelope(
			observation.KindTurnTerminalPrepared,
			7,
			terminalIdentity,
		),
		testEnvelope(
			observation.KindTurnTerminalCommitted,
			8,
			terminalIdentity,
		),
	}
	envelopes[6].Summary, envelopes[7].Summary = summary, summary
	graph, err := (Reducer{}).Reduce(t.Context(), envelopes)
	if err != nil {
		t.Fatal(err)
	}
	explanation, err := graph.ExplainFailure("turn-failed")
	if err != nil {
		t.Fatal(err)
	}
	failure := explanation.Failure
	if explanation.Conclusion != "turn failed" || failure == nil ||
		failure.StoppedAt != string(protocol.FaultStageModelSample) ||
		failure.ReasonCode != string(protocol.CodeUnavailable) ||
		!failure.CanContinue ||
		failure.RecoveryAction != string(protocol.FaultResumeRetryTurn) ||
		len(failure.Attempts) != 2 ||
		failure.Correlation.OperationID != "operation-1" ||
		failure.Correlation.EffectID != "terminal:turn-failed" ||
		failure.Correlation.LeaseOwner != "worker-1" ||
		failure.Correlation.LeaseEpoch != 7 ||
		!slices.Equal(
			failure.Correlation.ResumeIDs,
			[]string{"operation-resume-1"},
		) {
		t.Fatalf("failure explanation = %+v", explanation)
	}
}

func TestExplainFailureDoesNotTreatPreparedOutcomeAsCommitted(t *testing.T) {
	identity := observation.Identity{
		RuntimeID: "runtime-1", TurnID: "turn-prepared",
		OperationID: "operation-1", EffectID: "effect-1",
	}
	summary, err := observation.EncodeTerminalSummary(
		"measurement-1",
		observation.TerminalOutcome{
			Status: observation.TerminalFailed,
			Code:   string(protocol.CodeInternal),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	started := testEnvelope(
		observation.KindTurnStarted,
		1,
		identity,
	)
	prepared := testEnvelope(
		observation.KindTurnTerminalPrepared,
		2,
		identity,
	)
	prepared.Summary = summary
	graph, err := (Reducer{}).Reduce(
		t.Context(),
		[]observation.Envelope{started, prepared},
	)
	if err != nil {
		t.Fatal(err)
	}
	explanation, err := graph.ExplainFailure("turn-prepared")
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Failure != nil ||
		explanation.Conclusion != "no failed terminal observation is present" ||
		explanation.NextEvidence !=
			"committed structured terminal outcome observation" {
		t.Fatalf("prepared failure explanation = %+v", explanation)
	}
}

func TestReducerLeavesMissingEndOpen(t *testing.T) {
	envelopes := []observation.Envelope{
		testEnvelope(
			observation.KindTurnStarted,
			1,
			observation.Identity{
				RuntimeID: "runtime-1",
				TurnID:    "turn-1",
			},
		),
		testEnvelope(
			observation.KindToolStarted,
			2,
			observation.Identity{
				RuntimeID: "runtime-1",
				TurnID:    "turn-1",
				CallID:    "call-open",
				Attempt:   1,
			},
		),
	}
	graph, err := (Reducer{}).Reduce(t.Context(), envelopes)
	if err != nil {
		t.Fatal(err)
	}
	tool := graph.ToolAttempts[toolKey("runtime-1", "turn-1", "call-open", 1)]
	if tool.Window.Status != StatusOpen ||
		tool.Window.EndedSequence != 0 ||
		tool.Window.EndedAt != nil {
		t.Fatalf("tool = %+v", tool)
	}
	if graph.Turns[turnKey("runtime-1", "turn-1")].Window.Status !=
		StatusOpen {
		t.Fatalf("turn = %+v", graph.Turns)
	}
}

func TestReducerMakesMissingStartAndProvisionalAttemptExplicit(t *testing.T) {
	envelope := testEnvelope(
		observation.KindToolFinished,
		1,
		observation.Identity{
			RuntimeID: "runtime-1",
			TurnID:    "turn-1",
			CallID:    "call-1",
		},
	)
	graph, err := (Reducer{}).Reduce(
		t.Context(),
		[]observation.Envelope{envelope},
	)
	if err != nil {
		t.Fatal(err)
	}
	tool := graph.ToolAttempts[toolKey("runtime-1", "turn-1", "call-1", 1)]
	if tool.AttemptExplicit {
		t.Fatalf("tool = %+v", tool)
	}
	if !hasUnknown(graph.Unknowns, "missing_start") ||
		!hasUnknown(graph.Unknowns, "tool_attempt_number") {
		t.Fatalf("unknowns = %+v", graph.Unknowns)
	}
}

func TestReducerReportsGapsConflictsAndUnavailablePayload(t *testing.T) {
	first := testEnvelope(
		observation.KindToolStarted,
		1,
		observation.Identity{
			RuntimeID: "runtime-1", TurnID: "turn-1",
			CallID: "call-1", Attempt: 1,
		},
	)
	second := testEnvelope(
		observation.KindToolStarted,
		2,
		first.Identity,
	)
	second.ObservedSequence = 3
	third := testEnvelope(
		observation.KindToolResultProduced,
		3,
		first.Identity,
	)
	third.ObservedSequence = 4
	third.Policy = conversationPolicy()
	third.Payload = payloadRef("missing")
	graph, err := (Reducer{
		Payloads: rejectingVerifier{err: errors.New("not found")},
	}).Reduce(t.Context(), []observation.Envelope{first, second, third})
	if err != nil {
		t.Fatal(err)
	}
	if len(graph.Inconsistencies) != 1 ||
		graph.Inconsistencies[0].Code != "duplicate_start" {
		t.Fatalf("inconsistencies = %+v", graph.Inconsistencies)
	}
	for _, code := range []string{
		"observation_gap",
		"payload_unavailable",
	} {
		if !hasUnknown(graph.Unknowns, code) {
			t.Fatalf("missing %s in %+v", code, graph.Unknowns)
		}
	}
}

func TestReducerComputesObservedGapsAfterPriorityReordering(t *testing.T) {
	first := testEnvelope(
		observation.KindRuntimeReady,
		1,
		observation.Identity{RuntimeID: "runtime-1"},
	)
	first.ObservedSequence = 2
	second := testEnvelope(
		observation.KindRuntimeStopping,
		2,
		observation.Identity{RuntimeID: "runtime-1"},
	)
	second.ObservedSequence = 1
	graph, err := (Reducer{}).Reduce(
		t.Context(),
		[]observation.Envelope{first, second},
	)
	if err != nil {
		t.Fatal(err)
	}
	if hasUnknown(graph.Unknowns, "observation_gap") {
		t.Fatalf("priority reordering created a false gap: %+v", graph.Unknowns)
	}
}

func TestReducerBuildsEffectAndAgentInteractionSemantics(t *testing.T) {
	effectIdentity := observation.Identity{
		RuntimeID: "runtime-1",
		EffectID:  "effect-1",
		RunID:     "run-1",
		NodeID:    "node-1",
		AttemptID: "attempt-1",
	}
	agentIdentity := observation.Identity{
		RuntimeID: "runtime-1",
		AgentID:   "agent-1",
	}
	envelopes := []observation.Envelope{
		testEnvelope(observation.KindEffectRequested, 1, effectIdentity),
		testEnvelope(observation.KindEffectStarted, 2, effectIdentity),
		testEnvelope(observation.KindEffectResultRetained, 3, effectIdentity),
		testEnvelope(observation.KindEffectFinished, 4, effectIdentity),
		testEnvelope(observation.KindAgentSpawned, 5, agentIdentity),
		testEnvelope(observation.KindAgentTaskDelivered, 6, agentIdentity),
		testEnvelope(observation.KindAgentMessageSent, 7, agentIdentity),
		testEnvelope(observation.KindAgentResultDelivered, 8, agentIdentity),
		testEnvelope(observation.KindAgentClosed, 9, agentIdentity),
	}
	envelopes[5].Summary = []byte(
		`{"source_agent_id":"parent","target_agent_id":"agent-1"}`,
	)
	graph, err := (Reducer{}).Reduce(t.Context(), envelopes)
	if err != nil {
		t.Fatal(err)
	}
	effect := graph.Effects[semanticID("effect", "runtime-1", "effect-1")]
	if effect.Window.Status != StatusCompleted ||
		effect.RunID != "run-1" ||
		effect.NodeID != "node-1" ||
		effect.AttemptID != "attempt-1" {
		t.Fatalf("effect = %+v", effect)
	}
	agent := graph.Agents[semanticID("agent", "runtime-1", "agent-1")]
	if agent.Window.Status != StatusClosed ||
		len(graph.Interactions) != 3 ||
		graph.Interactions[0].SourceAgent != "parent" ||
		graph.Interactions[0].TargetAgent != "agent-1" {
		t.Fatalf(
			"agent=%+v interactions=%+v",
			agent,
			graph.Interactions,
		)
	}
}

func TestReducerRejectsSequenceAndObservationIdentityCorruption(t *testing.T) {
	first := testEnvelope(
		observation.KindTurnStarted,
		2,
		observation.Identity{RuntimeID: "runtime-1", TurnID: "turn-1"},
	)
	second := testEnvelope(
		observation.KindTurnStarted,
		1,
		observation.Identity{RuntimeID: "runtime-1", TurnID: "turn-2"},
	)
	if _, err := (Reducer{}).Reduce(
		t.Context(),
		[]observation.Envelope{first, second},
	); !errors.Is(err, ErrSequence) {
		t.Fatalf("sequence error = %v", err)
	}
	second = testEnvelope(
		observation.KindTurnStarted,
		3,
		observation.Identity{RuntimeID: "runtime-1", TurnID: "turn-2"},
	)
	second.ID = first.ID
	if _, err := (Reducer{}).Reduce(
		t.Context(),
		[]observation.Envelope{first, second},
	); !errors.Is(err, ErrDuplicateObservation) {
		t.Fatalf("identity error = %v", err)
	}
}

func TestSO3GoldenReplayIsByteIdentical(t *testing.T) {
	reducer := Reducer{Payloads: acceptingVerifier{}}
	first, err := reducer.Reduce(t.Context(), lifecycleFixture())
	if err != nil {
		t.Fatal(err)
	}
	second, err := reducer.Reduce(t.Context(), lifecycleFixture())
	if err != nil {
		t.Fatal(err)
	}
	firstJSON, err := CanonicalJSON(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := CanonicalJSON(second)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstJSON, secondJSON) {
		t.Fatal("same journal did not produce byte-identical graph")
	}
	sum := sha256.Sum256(firstJSON)
	const goldenDigest = "22de2612635d02a5ccc3aa20898fad66cacf3590504804cb9b99ce184fdd430d"
	if digest := hex.EncodeToString(sum[:]); digest != goldenDigest {
		t.Fatalf("golden digest = %s", digest)
	}
}

func lifecycleFixture() []observation.Envelope {
	identity := observation.Identity{
		RuntimeID: "runtime-1",
		SessionID: "session-1",
		TurnID:    "turn-1",
	}
	values := []observation.Envelope{
		testEnvelope(observation.KindTurnStarted, 1, identity),
		testEnvelope(
			observation.KindModelRequestSent,
			2,
			withAttempt(withSample(identity, "sample-1"), 1),
		),
		testEnvelope(
			observation.KindModelResponseCompleted,
			3,
			withAttempt(withSample(identity, "sample-1"), 1),
		),
		testEnvelope(
			observation.KindToolStarted,
			4,
			withCall(identity, "call-1", 2),
		),
		testEnvelope(
			observation.KindToolResultProduced,
			5,
			withCall(identity, "call-1", 2),
		),
		testEnvelope(
			observation.KindToolResultModelVisible,
			6,
			withSample(withCall(identity, "call-1", 2), "sample-1"),
		),
		testEnvelope(
			observation.KindToolFinished,
			7,
			withCall(identity, "call-1", 2),
		),
		testEnvelope(
			observation.KindApprovalRequested,
			8,
			withCall(identity, "call-1", 2),
		),
		testEnvelope(
			observation.KindApprovalResolved,
			9,
			withCall(identity, "call-1", 2),
		),
		testEnvelope(
			observation.KindTurnTerminalPrepared,
			10,
			withTerminal(identity),
		),
		testEnvelope(
			observation.KindTurnTerminalCommitted,
			11,
			withTerminal(identity),
		),
	}
	for index := range values {
		if values[index].Kind == observation.KindToolResultProduced ||
			values[index].Kind == observation.KindToolResultModelVisible {
			values[index].Policy = conversationPolicy()
			values[index].Payload = payloadRef("result")
		}
	}
	return values
}

func testEnvelope(
	kind observation.Kind,
	sequence uint64,
	identity observation.Identity,
) observation.Envelope {
	return observation.Envelope{
		SchemaVersion: observation.SchemaVersion,
		ID: observation.ObservationID(
			"obs_" + strings.Repeat(
				string("0123456789abcdef"[sequence%16]),
				32,
			),
		),
		Kind:             kind,
		ObservedSequence: sequence,
		Sequence:         sequence,
		RecordedAt:       time.Unix(int64(sequence), 0).UTC(),
		Identity:         identity,
		Policy: observation.DataPolicy{
			Class:     observation.DataOperational,
			Redaction: observation.RedactionNotRequired,
		},
	}
}

func withSample(
	identity observation.Identity,
	sample string,
) observation.Identity {
	identity.SampleID = sample
	return identity
}

func withCall(
	identity observation.Identity,
	call string,
	attempt uint32,
) observation.Identity {
	identity.CallID = call
	identity.Attempt = attempt
	return identity
}

func withAttempt(
	identity observation.Identity,
	attempt uint32,
) observation.Identity {
	identity.Attempt = attempt
	return identity
}

func withTerminal(identity observation.Identity) observation.Identity {
	identity.OperationID = protocol.OperationID("operation-1")
	identity.EffectID = protocol.EffectID("effect-1")
	return identity
}

func conversationPolicy() observation.DataPolicy {
	return observation.DataPolicy{
		Class:     observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
}

func payloadRef(value string) *observation.PayloadRef {
	digest := sha256.Sum256([]byte(value))
	return &observation.PayloadRef{
		Digest:        "sha256:" + hex.EncodeToString(digest[:]),
		MediaType:     "application/json",
		OriginalBytes: uint64(len(value)),
		StoredBytes:   uint64(len(value)),
		DataClass:     observation.DataConversation,
		Redaction:     observation.RedactionApplied,
	}
}

func hasUnknown(values []UnknownFact, code string) bool {
	for _, value := range values {
		if value.Code == code {
			return true
		}
	}
	return false
}

type acceptingVerifier struct{}

func (acceptingVerifier) Verify(
	context.Context,
	observation.PayloadRef,
) error {
	return nil
}

type rejectingVerifier struct {
	err error
}

func (v rejectingVerifier) Verify(
	context.Context,
	observation.PayloadRef,
) error {
	return v.err
}

func BenchmarkSO3SemanticReplay(b *testing.B) {
	envelopes := lifecycleFixture()
	reducer := Reducer{Payloads: acceptingVerifier{}}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := reducer.Reduce(context.Background(), envelopes); err != nil {
			b.Fatal(err)
		}
	}
}
