package semantic

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
)

var (
	ErrSequence             = errors.New("semantic reducer input sequence is invalid")
	ErrDuplicateObservation = errors.New("semantic reducer observation identity is duplicated")
)

type PayloadVerifier interface {
	Verify(context.Context, observation.PayloadRef) error
}

type Reducer struct {
	Payloads PayloadVerifier
}

func (r Reducer) Reduce(
	ctx context.Context,
	envelopes []observation.Envelope,
) (Graph, error) {
	graph := emptyGraph()
	var previousSequence uint64
	observedByRuntime := make(map[string][]uint64)
	for _, envelope := range envelopes {
		if err := ctx.Err(); err != nil {
			return Graph{}, err
		}
		if err := envelope.Validate(); err != nil {
			return Graph{}, fmt.Errorf("reduce observation %q: %w", envelope.ID, err)
		}
		if envelope.Sequence <= previousSequence {
			return Graph{}, fmt.Errorf(
				"%w: got %d after %d",
				ErrSequence,
				envelope.Sequence,
				previousSequence,
			)
		}
		if _, exists := graph.Observations[string(envelope.ID)]; exists {
			return Graph{}, fmt.Errorf(
				"%w: %s",
				ErrDuplicateObservation,
				envelope.ID,
			)
		}
		observedByRuntime[envelope.Identity.RuntimeID] = append(
			observedByRuntime[envelope.Identity.RuntimeID],
			envelope.ObservedSequence,
		)
		evidence := r.evidence(ctx, &graph, envelope)
		graph.Observations[string(envelope.ID)] = ObservationSummary{
			ID: envelope.ID, Kind: envelope.Kind,
			Sequence:         envelope.Sequence,
			ObservedSequence: envelope.ObservedSequence,
			RuntimeID:        envelope.Identity.RuntimeID,
		}
		r.reduceRuntime(&graph, envelope, evidence)
		r.reduceIdentity(&graph, envelope, evidence)
		r.reduceKind(&graph, envelope, evidence)
		previousSequence = envelope.Sequence
	}
	graph.LastSequence = previousSequence
	reduceObservedGaps(&graph, observedByRuntime)
	r.validateCausality(&graph, envelopes)
	normalize(&graph)
	return graph, nil
}

func (r Reducer) evidence(
	ctx context.Context,
	graph *Graph,
	envelope observation.Envelope,
) Evidence {
	evidence := Evidence{
		ObservationID: envelope.ID,
		Kind:          envelope.Kind,
		Sequence:      envelope.Sequence,
	}
	if envelope.Payload == nil {
		return evidence
	}
	payload := PayloadEvidence{
		Reference:    *envelope.Payload,
		Availability: PayloadUnverified,
	}
	if r.Payloads == nil {
		graph.Unknowns = append(graph.Unknowns, UnknownFact{
			Code: "payload_unverified", ObjectKind: "payload",
			ObjectID: envelope.Payload.Digest,
			Sequence: envelope.Sequence, Observation: envelope.ID,
			Detail: "no payload verifier was configured",
		})
	} else if err := r.Payloads.Verify(ctx, *envelope.Payload); err != nil {
		payload.Availability = PayloadUnavailable
		graph.Unknowns = append(graph.Unknowns, UnknownFact{
			Code: "payload_unavailable", ObjectKind: "payload",
			ObjectID: envelope.Payload.Digest,
			Sequence: envelope.Sequence, Observation: envelope.ID,
			Detail: err.Error(),
		})
	} else {
		payload.Availability = PayloadAvailable
	}
	evidence.Payload = &payload
	return evidence
}

func (r Reducer) reduceRuntime(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	id := envelope.Identity.RuntimeID
	node := graph.Runtimes[id]
	if node.ID == "" {
		node = RuntimeNode{
			ID:     id,
			Window: ExecutionWindow{Status: StatusUnknown},
		}
	}
	node.Evidence = append(node.Evidence, evidence)
	switch envelope.Kind {
	case observation.KindRuntimeStarted:
		applyStart(graph, "runtime", id, &node.Window, envelope)
	case observation.KindRuntimeStopped:
		applyEnd(graph, "runtime", id, &node.Window, envelope, StatusClosed)
	case observation.KindRuntimeCrashed:
		applyEnd(graph, "runtime", id, &node.Window, envelope, StatusFailed)
	}
	graph.Runtimes[id] = node
}

func (r Reducer) reduceIdentity(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	identity := envelope.Identity
	if identity.ThreadID != "" {
		key := semanticID(
			"thread",
			identity.RuntimeID,
			string(identity.ThreadID),
		)
		thread := graph.Threads[key]
		if thread.ID == "" {
			thread = ThreadNode{
				ID: key, RuntimeID: identity.RuntimeID,
				SessionID: identity.SessionID,
			}
		}
		if thread.SessionID == "" {
			thread.SessionID = identity.SessionID
		} else if identity.SessionID != "" &&
			thread.SessionID != identity.SessionID {
			addIdentityConflict(
				graph, "thread", key, envelope,
				"session_id", thread.SessionID, identity.SessionID,
			)
		}
		thread.Evidence = append(thread.Evidence, evidence)
		graph.Threads[key] = thread
	}
	if identity.TurnID == "" {
		return
	}
	key := turnKey(identity.RuntimeID, string(identity.TurnID))
	turn := graph.Turns[key]
	if turn.ID == "" {
		turn = TurnNode{
			ID: key, RuntimeID: identity.RuntimeID,
			SessionID: identity.SessionID,
			ThreadID:  string(identity.ThreadID),
			TurnID:    string(identity.TurnID),
			Window:    ExecutionWindow{Status: StatusUnknown},
		}
	}
	mergeTurnIdentity(graph, &turn, envelope)
	turn.Evidence = append(turn.Evidence, evidence)
	if envelope.Kind == observation.KindTurnStarted {
		applyStart(graph, "turn", key, &turn.Window, envelope)
	}
	if envelope.Kind == observation.KindTurnRecovered {
		turn.Recovered = true
	}
	if envelope.Kind == observation.KindTurnTerminalCommitted {
		applyEnd(graph, "turn", key, &turn.Window, envelope, StatusCommitted)
	}
	graph.Turns[key] = turn
	if identity.ThreadID != "" {
		threadKey := semanticID(
			"thread", identity.RuntimeID, string(identity.ThreadID),
		)
		thread := graph.Threads[threadKey]
		thread.TurnIDs = appendUnique(thread.TurnIDs, key)
		graph.Threads[threadKey] = thread
	}
}

func (r Reducer) reduceKind(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	switch envelope.Kind {
	case observation.KindModelRequestPrepared,
		observation.KindModelRequestSent,
		observation.KindModelFirstOutput,
		observation.KindModelResponseCompleted,
		observation.KindModelRequestFailed,
		observation.KindModelRetryScheduled:
		reduceInference(graph, envelope, evidence)
	case observation.KindToolProposed,
		observation.KindToolAdmitted,
		observation.KindToolStarted,
		observation.KindToolRuntimeOutput,
		observation.KindToolResultProduced,
		observation.KindToolResultModelVisible,
		observation.KindToolFinished:
		reduceTool(graph, envelope, evidence)
	case observation.KindEffectRequested,
		observation.KindEffectStarted,
		observation.KindEffectResultRetained,
		observation.KindEffectRequeued,
		observation.KindEffectFinished:
		reduceEffect(graph, envelope, evidence)
	case observation.KindApprovalRequested, observation.KindApprovalResolved:
		reduceApproval(graph, envelope, evidence)
	case observation.KindTurnTerminalPrepared,
		observation.KindTurnTerminalCommitted:
		reduceTerminal(graph, envelope, evidence)
	case observation.KindVerificationStarted,
		observation.KindVerificationFinished:
		reduceVerification(graph, envelope, evidence)
	case observation.KindAgentSpawned,
		observation.KindAgentClosed,
		observation.KindAgentMessageSent,
		observation.KindAgentTaskDelivered,
		observation.KindAgentResultDelivered:
		reduceAgent(graph, envelope, evidence)
	case observation.KindContextVisibilityCommitted:
		reduceContextVisibility(graph, envelope, evidence)
	}
}

func reduceInference(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	identity := envelope.Identity
	key := inferenceKey(
		identity.RuntimeID,
		string(identity.TurnID),
		identity.SampleID,
	)
	node := graph.InferenceCalls[key]
	if node.ID == "" {
		attempt := identity.Attempt
		node = InferenceNode{
			ID: key, RuntimeID: identity.RuntimeID,
			TurnID: string(identity.TurnID), SampleID: identity.SampleID,
			Attempt: attempt, AttemptExplicit: attempt != 0,
			Window: ExecutionWindow{Status: StatusUnknown},
		}
		if attempt == 0 {
			graph.Unknowns = append(graph.Unknowns, UnknownFact{
				Code: "inference_attempt_number", ObjectKind: "inference",
				ObjectID: key, Sequence: envelope.Sequence,
				Observation: envelope.ID,
				Detail:      "provider attempt ordinal is unavailable",
			})
		}
	}
	node.Evidence = append(node.Evidence, evidence)
	switch envelope.Kind {
	case observation.KindModelRequestSent:
		applyStart(graph, "inference", key, &node.Window, envelope)
	case observation.KindModelResponseCompleted:
		applyEnd(
			graph, "inference", key, &node.Window, envelope, StatusCompleted,
		)
	case observation.KindModelRequestFailed:
		applyEnd(graph, "inference", key, &node.Window, envelope, StatusFailed)
	}
	graph.InferenceCalls[key] = node
}

func reduceTool(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	identity := envelope.Identity
	attempt := identity.Attempt
	explicit := attempt != 0
	if attempt == 0 {
		attempt = 1
	}
	key := toolKey(
		identity.RuntimeID,
		string(identity.TurnID),
		identity.CallID,
		attempt,
	)
	node := graph.ToolAttempts[key]
	if node.ID == "" {
		node = ToolNode{
			ID: key, RuntimeID: identity.RuntimeID,
			TurnID: string(identity.TurnID), CallID: identity.CallID,
			Attempt: attempt, AttemptExplicit: explicit,
			Window: ExecutionWindow{Status: StatusUnknown},
		}
		if !explicit {
			graph.Unknowns = append(graph.Unknowns, UnknownFact{
				Code: "tool_attempt_number", ObjectKind: "tool_attempt",
				ObjectID: key, Sequence: envelope.Sequence,
				Observation: envelope.ID,
				Detail:      "attempt ordinal was absent; semantic attempt 1 is provisional",
			})
		}
	}
	node.Evidence = append(node.Evidence, evidence)
	switch envelope.Kind {
	case observation.KindToolStarted:
		applyStart(graph, "tool_attempt", key, &node.Window, envelope)
	case observation.KindToolFinished:
		applyEnd(
			graph, "tool_attempt", key, &node.Window, envelope, StatusCompleted,
		)
	case observation.KindToolResultProduced:
		node.RuntimeResult = evidence.Payload
	case observation.KindToolResultModelVisible:
		if evidence.Payload != nil {
			node.ModelVisible = append(node.ModelVisible, *evidence.Payload)
		}
		targetID := inferenceKey(
			identity.RuntimeID,
			string(identity.TurnID),
			identity.SampleID,
		)
		graph.Visibility = append(graph.Visibility, VisibilityEdge{
			ID:         semanticID("visibility", string(envelope.ID)),
			SourceKind: "tool_attempt", SourceID: key,
			Target: "model_request", TargetID: targetID,
			Sequence: envelope.Sequence, Observation: envelope.ID,
			Payload: evidence.Payload,
		})
	}
	graph.ToolAttempts[key] = node
}

func reduceEffect(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	identity := envelope.Identity
	key := semanticID(
		"effect",
		identity.RuntimeID,
		string(identity.EffectID),
	)
	node := graph.Effects[key]
	if node.ID == "" {
		node = EffectNode{
			ID: key, RuntimeID: identity.RuntimeID,
			EffectID: string(identity.EffectID),
			RunID:    string(identity.RunID), NodeID: string(identity.NodeID),
			AttemptID: string(identity.AttemptID),
			Window:    ExecutionWindow{Status: StatusUnknown},
		}
	}
	node.Evidence = append(node.Evidence, evidence)
	switch envelope.Kind {
	case observation.KindEffectRequested:
		if node.RequestedAt != 0 && node.RequestedAt != envelope.Sequence {
			addConflict(
				graph, "duplicate_request", "effect", key,
				node.RequestedAt, envelope,
				"effect has more than one request observation",
			)
		} else {
			node.RequestedAt = envelope.Sequence
		}
	case observation.KindEffectStarted:
		applyStart(graph, "effect", key, &node.Window, envelope)
	case observation.KindEffectResultRetained:
		node.ResultRetained = evidence.Payload
	case observation.KindEffectRequeued:
		node.Requeues = append(node.Requeues, envelope.Sequence)
	case observation.KindEffectFinished:
		applyEnd(graph, "effect", key, &node.Window, envelope, StatusCompleted)
	}
	graph.Effects[key] = node
}

func reduceApproval(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	identity := envelope.Identity
	key := semanticID(
		"approval",
		identity.RuntimeID,
		string(identity.TurnID),
		identity.CallID,
	)
	node := graph.Approvals[key]
	if node.ID == "" {
		node = ApprovalNode{
			ID: key, RuntimeID: identity.RuntimeID,
			TurnID: string(identity.TurnID), CallID: identity.CallID,
			Window: ExecutionWindow{Status: StatusUnknown},
		}
	}
	node.Evidence = append(node.Evidence, evidence)
	if envelope.Kind == observation.KindApprovalRequested {
		applyStart(graph, "approval", key, &node.Window, envelope)
	} else {
		applyEnd(graph, "approval", key, &node.Window, envelope, StatusResolved)
	}
	graph.Approvals[key] = node
}

func reduceTerminal(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	identity := envelope.Identity
	key := semanticID(
		"terminal",
		identity.RuntimeID,
		string(identity.TurnID),
	)
	node := graph.TerminalOps[key]
	if node.ID == "" {
		node = TerminalNode{
			ID: key, RuntimeID: identity.RuntimeID,
			TurnID:      string(identity.TurnID),
			OperationID: string(identity.OperationID),
			EffectID:    string(identity.EffectID),
			Window:      ExecutionWindow{Status: StatusUnknown},
		}
	}
	mergeTerminalIdentity(graph, &node, envelope)
	node.Evidence = append(node.Evidence, evidence)
	if envelope.Kind == observation.KindTurnTerminalPrepared {
		applyStart(graph, "terminal", key, &node.Window, envelope)
	} else {
		applyEnd(graph, "terminal", key, &node.Window, envelope, StatusCommitted)
	}
	graph.TerminalOps[key] = node
}

func reduceVerification(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	identity := envelope.Identity
	key := semanticID(
		"verification",
		identity.RuntimeID,
		string(identity.TurnID),
	)
	node := graph.Verifications[key]
	if node.ID == "" {
		node = VerificationNode{
			ID: key, RuntimeID: identity.RuntimeID,
			TurnID: string(identity.TurnID),
			Window: ExecutionWindow{Status: StatusUnknown},
		}
	}
	node.Evidence = append(node.Evidence, evidence)
	if envelope.Kind == observation.KindVerificationStarted {
		applyStart(graph, "verification", key, &node.Window, envelope)
	} else {
		applyEnd(
			graph, "verification", key, &node.Window, envelope, StatusCompleted,
		)
	}
	graph.Verifications[key] = node
}

func reduceAgent(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	identity := envelope.Identity
	key := semanticID("agent", identity.RuntimeID, identity.AgentID)
	node := graph.Agents[key]
	if node.ID == "" {
		node = AgentNode{
			ID: key, RuntimeID: identity.RuntimeID, AgentID: identity.AgentID,
			Window: ExecutionWindow{Status: StatusUnknown},
		}
	}
	node.Evidence = append(node.Evidence, evidence)
	switch envelope.Kind {
	case observation.KindAgentSpawned:
		applyStart(graph, "agent", key, &node.Window, envelope)
	case observation.KindAgentClosed:
		applyEnd(graph, "agent", key, &node.Window, envelope, StatusClosed)
	default:
		source, target := interactionParties(envelope)
		graph.Interactions = append(graph.Interactions, InteractionEdge{
			ID:   semanticID("interaction", string(envelope.ID)),
			Kind: envelope.Kind, AgentID: identity.AgentID,
			SourceAgent: source, TargetAgent: target,
			Sequence: envelope.Sequence, Observation: envelope.ID,
			Payload: evidence.Payload,
		})
	}
	graph.Agents[key] = node
}

func reduceContextVisibility(
	graph *Graph,
	envelope observation.Envelope,
	evidence Evidence,
) {
	identity := envelope.Identity
	targetID := inferenceKey(
		identity.RuntimeID,
		string(identity.TurnID),
		identity.SampleID,
	)
	graph.Visibility = append(graph.Visibility, VisibilityEdge{
		ID:         semanticID("visibility", string(envelope.ID)),
		SourceKind: "context_projection",
		SourceID:   turnKey(identity.RuntimeID, string(identity.TurnID)),
		Target:     "model_request", TargetID: targetID,
		Sequence: envelope.Sequence, Observation: envelope.ID,
		Payload: evidence.Payload,
	})
}

func applyStart(
	graph *Graph,
	objectKind, objectID string,
	window *ExecutionWindow,
	envelope observation.Envelope,
) {
	if window.StartedSequence != 0 {
		addConflict(
			graph, "duplicate_start", objectKind, objectID,
			window.StartedSequence, envelope,
			"object has more than one start observation",
		)
		return
	}
	startedAt := envelope.RecordedAt
	window.StartedSequence = envelope.Sequence
	window.StartedAt = &startedAt
	window.Status = StatusOpen
}

func applyEnd(
	graph *Graph,
	objectKind, objectID string,
	window *ExecutionWindow,
	envelope observation.Envelope,
	status Status,
) {
	if window.EndedSequence != 0 {
		addConflict(
			graph, "duplicate_end", objectKind, objectID,
			window.EndedSequence, envelope,
			"object has more than one end observation",
		)
		return
	}
	if window.StartedSequence == 0 {
		graph.Unknowns = append(graph.Unknowns, UnknownFact{
			Code: "missing_start", ObjectKind: objectKind, ObjectID: objectID,
			Sequence: envelope.Sequence, Observation: envelope.ID,
			Detail: "end observation has no matching start",
		})
	}
	endedAt := envelope.RecordedAt
	window.EndedSequence = envelope.Sequence
	window.EndedAt = &endedAt
	window.Status = status
}

func addConflict(
	graph *Graph,
	code, objectKind, objectID string,
	previous uint64,
	envelope observation.Envelope,
	detail string,
) {
	graph.Inconsistencies = append(graph.Inconsistencies, Inconsistency{
		Code: code, ObjectKind: objectKind, ObjectID: objectID,
		Sequences:    []uint64{previous, envelope.Sequence},
		Observations: []observation.ObservationID{envelope.ID},
		Detail:       detail,
	})
}

func addIdentityConflict(
	graph *Graph,
	objectKind, objectID string,
	envelope observation.Envelope,
	field, current, incoming string,
) {
	graph.Inconsistencies = append(graph.Inconsistencies, Inconsistency{
		Code: "identity_conflict", ObjectKind: objectKind, ObjectID: objectID,
		Sequences:    []uint64{envelope.Sequence},
		Observations: []observation.ObservationID{envelope.ID},
		Detail: fmt.Sprintf(
			"%s is %q and %q",
			field,
			current,
			incoming,
		),
	})
}

func mergeTurnIdentity(
	graph *Graph,
	turn *TurnNode,
	envelope observation.Envelope,
) {
	identity := envelope.Identity
	if turn.SessionID == "" {
		turn.SessionID = identity.SessionID
	} else if identity.SessionID != "" && turn.SessionID != identity.SessionID {
		addIdentityConflict(
			graph, "turn", turn.ID, envelope,
			"session_id", turn.SessionID, identity.SessionID,
		)
	}
	incomingThread := string(identity.ThreadID)
	if turn.ThreadID == "" {
		turn.ThreadID = incomingThread
	} else if incomingThread != "" && turn.ThreadID != incomingThread {
		addIdentityConflict(
			graph, "turn", turn.ID, envelope,
			"thread_id", turn.ThreadID, incomingThread,
		)
	}
}

func mergeTerminalIdentity(
	graph *Graph,
	node *TerminalNode,
	envelope observation.Envelope,
) {
	operationID := string(envelope.Identity.OperationID)
	if node.OperationID == "" {
		node.OperationID = operationID
	} else if operationID != "" && node.OperationID != operationID {
		addIdentityConflict(
			graph, "terminal", node.ID, envelope,
			"operation_id", node.OperationID, operationID,
		)
	}
	effectID := string(envelope.Identity.EffectID)
	if node.EffectID == "" {
		node.EffectID = effectID
	} else if effectID != "" && node.EffectID != effectID {
		addIdentityConflict(
			graph, "terminal", node.ID, envelope,
			"effect_id", node.EffectID, effectID,
		)
	}
}

func (r Reducer) validateCausality(
	graph *Graph,
	envelopes []observation.Envelope,
) {
	for _, envelope := range envelopes {
		if envelope.Causality == nil {
			continue
		}
		targets := make([]observation.ObservationID, 0, len(envelope.Causality.Links)+1)
		if envelope.Causality.ParentObservationID != "" {
			targets = append(targets, envelope.Causality.ParentObservationID)
		}
		for _, link := range envelope.Causality.Links {
			targets = append(targets, link.Target)
		}
		for _, target := range targets {
			if _, ok := graph.Observations[string(target)]; ok {
				continue
			}
			graph.Unknowns = append(graph.Unknowns, UnknownFact{
				Code: "causality_target_missing", ObjectKind: "observation",
				ObjectID: string(envelope.ID), Sequence: envelope.Sequence,
				Observation: envelope.ID,
				Detail:      "causality target " + string(target) + " is unavailable",
			})
		}
	}
}

func reduceObservedGaps(
	graph *Graph,
	observedByRuntime map[string][]uint64,
) {
	for runtimeID, sequences := range observedByRuntime {
		slices.Sort(sequences)
		var previous uint64
		for _, sequence := range sequences {
			if sequence == previous {
				graph.Inconsistencies = append(
					graph.Inconsistencies,
					Inconsistency{
						Code:       "observed_sequence_duplicate",
						ObjectKind: "runtime",
						ObjectID:   runtimeID,
						Sequences:  []uint64{sequence},
						Detail:     "two observations share one admission sequence",
					},
				)
				continue
			}
			if sequence > previous+1 {
				graph.Unknowns = append(graph.Unknowns, UnknownFact{
					Code: "observation_gap", ObjectKind: "journal",
					ObjectID: runtimeID,
					Detail: fmt.Sprintf(
						"observed_sequence gap %d..%d",
						previous+1,
						sequence-1,
					),
				})
			}
			previous = sequence
		}
	}
}

func interactionParties(envelope observation.Envelope) (string, string) {
	var summary struct {
		SourceAgentID string `json:"source_agent_id"`
		TargetAgentID string `json:"target_agent_id"`
	}
	if len(envelope.Summary) != 0 {
		_ = jsonUnmarshal(envelope.Summary, &summary)
	}
	return summary.SourceAgentID, summary.TargetAgentID
}

func semanticID(kind string, values ...string) string {
	var builder strings.Builder
	builder.WriteString(kind)
	for _, value := range values {
		builder.WriteByte('|')
		builder.WriteString(strconv.Itoa(len(value)))
		builder.WriteByte(':')
		builder.WriteString(value)
	}
	return builder.String()
}

func turnKey(runtimeID, turnID string) string {
	return semanticID("turn", runtimeID, turnID)
}

func inferenceKey(runtimeID, turnID, sampleID string) string {
	return semanticID("inference", runtimeID, turnID, sampleID)
}

func toolKey(
	runtimeID, turnID, callID string,
	attempt uint32,
) string {
	return semanticID(
		"tool",
		runtimeID,
		turnID,
		callID,
		strconv.FormatUint(uint64(attempt), 10),
	)
}

func appendUnique(values []string, value string) []string {
	if slices.Contains(values, value) {
		return values
	}
	return append(values, value)
}
