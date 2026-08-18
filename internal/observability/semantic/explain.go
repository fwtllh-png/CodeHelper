package semantic

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
)

var (
	ErrNotFound  = errors.New("semantic object not found")
	ErrAmbiguous = errors.New("semantic object identity is ambiguous")
)

type Explanation struct {
	SubjectKind     string                 `json:"subject_kind"`
	SubjectID       string                 `json:"subject_id"`
	Conclusion      string                 `json:"conclusion"`
	AuthoritySource []string               `json:"authority_sources"`
	Observations    []Evidence             `json:"observations"`
	Payloads        []PayloadEvidence      `json:"payloads,omitempty"`
	Visibility      []VisibilityEdge       `json:"visibility,omitempty"`
	Unknowns        []UnknownFact          `json:"unknowns,omitempty"`
	Inconsistencies []Inconsistency        `json:"inconsistencies,omitempty"`
	Failure         *FailureReconstruction `json:"failure,omitempty"`
	NextEvidence    string                 `json:"next_evidence,omitempty"`
}

type FailureAttempt struct {
	Kind      string `json:"kind"`
	ID        string `json:"id"`
	Attempt   uint32 `json:"attempt,omitempty"`
	AttemptID string `json:"attempt_id,omitempty"`
	Status    Status `json:"status"`
}

type FailureCorrelation struct {
	RuntimeID   string   `json:"runtime_id"`
	SessionID   string   `json:"session_id,omitempty"`
	ThreadID    string   `json:"thread_id,omitempty"`
	TurnID      string   `json:"turn_id"`
	OperationID string   `json:"operation_id,omitempty"`
	EffectID    string   `json:"effect_id,omitempty"`
	ResumeIDs   []string `json:"resume_ids,omitempty"`
	LeaseOwner  string   `json:"lease_owner,omitempty"`
	LeaseEpoch  uint64   `json:"lease_epoch,omitempty"`
}

type FailureReconstruction struct {
	StoppedAt      string             `json:"stopped_at"`
	ReasonCode     string             `json:"reason_code"`
	Attempts       []FailureAttempt   `json:"attempts"`
	CanContinue    bool               `json:"can_continue"`
	RecoveryAction string             `json:"recovery_action"`
	Correlation    FailureCorrelation `json:"correlation"`
}

func (g Graph) ExplainTurn(turnID string) (Explanation, error) {
	key, node, err := findTurn(g.Turns, turnID)
	if err != nil {
		return Explanation{}, err
	}
	explanation := g.explain(
		"turn",
		key,
		node.Evidence,
		fmt.Sprintf("turn status is %s", node.Window.Status),
	)
	if node.Window.Status == StatusOpen {
		explanation.NextEvidence = "turn terminal committed observation"
	}
	return explanation, nil
}

func (g Graph) ExplainTool(callID string) (Explanation, error) {
	key, node, err := findTool(g.ToolAttempts, callID)
	if err != nil {
		return Explanation{}, err
	}
	explanation := g.explain(
		"tool_attempt",
		key,
		node.Evidence,
		fmt.Sprintf(
			"tool call %s attempt %d status is %s",
			node.CallID,
			node.Attempt,
			node.Window.Status,
		),
	)
	for _, edge := range g.Visibility {
		if edge.SourceKind == "tool_attempt" && edge.SourceID == key {
			explanation.Visibility = append(explanation.Visibility, edge)
		}
	}
	if node.RuntimeResult == nil {
		explanation.NextEvidence = "tool runtime result observation"
	} else if len(explanation.Visibility) == 0 {
		explanation.NextEvidence = "model-visible tool result observation"
	}
	return explanation, nil
}

func (g Graph) ExplainFailure(turnID string) (Explanation, error) {
	explanation, err := g.ExplainTurn(turnID)
	if err != nil {
		return Explanation{}, err
	}
	explanation.SubjectKind = "failure"
	switch {
	case len(explanation.Inconsistencies) != 0:
		explanation.Conclusion = "turn evidence is inconsistent"
	case len(explanation.Unknowns) != 0:
		explanation.Conclusion = "turn outcome is indeterminate"
	default:
		turn := g.Turns[explanation.SubjectID]
		terminal := g.TerminalOps[semanticID(
			"terminal",
			turn.RuntimeID,
			turn.TurnID,
		)]
		if terminal.Window.Status != StatusCommitted ||
			terminal.Outcome == nil ||
			terminal.Outcome.Status == observation.TerminalCompleted {
			explanation.Conclusion = "no failed terminal observation is present"
			explanation.NextEvidence =
				"committed structured terminal outcome observation"
			return explanation, nil
		}
		explanation.Failure = g.reconstructFailure(turn, terminal)
		explanation.Conclusion = "turn " + string(terminal.Outcome.Status)
	}
	return explanation, nil
}

func (g Graph) reconstructFailure(
	turn TurnNode,
	terminal TerminalNode,
) *FailureReconstruction {
	outcome := terminal.Outcome
	result := &FailureReconstruction{
		StoppedAt:  "terminal",
		ReasonCode: outcome.Code,
		Correlation: FailureCorrelation{
			RuntimeID: turn.RuntimeID, SessionID: turn.SessionID,
			ThreadID: turn.ThreadID, TurnID: turn.TurnID,
			OperationID: terminal.OperationID, EffectID: terminal.EffectID,
			ResumeIDs: append([]string(nil), turn.ResumeIDs...),
		},
	}
	for _, inference := range g.InferenceCalls {
		if inference.RuntimeID == turn.RuntimeID &&
			inference.TurnID == turn.TurnID {
			result.Attempts = append(result.Attempts, FailureAttempt{
				Kind: "provider", ID: inference.SampleID,
				Attempt: inference.Attempt, Status: inference.Window.Status,
			})
		}
	}
	for _, tool := range g.ToolAttempts {
		if tool.RuntimeID == turn.RuntimeID && tool.TurnID == turn.TurnID {
			result.Attempts = append(result.Attempts, FailureAttempt{
				Kind: "tool", ID: tool.CallID,
				Attempt: tool.Attempt, Status: tool.Window.Status,
			})
		}
	}
	for _, effect := range g.Effects {
		if effect.RuntimeID == turn.RuntimeID &&
			effect.EffectID == terminal.EffectID {
			result.Attempts = append(result.Attempts, FailureAttempt{
				Kind: "effect", ID: effect.EffectID,
				AttemptID: effect.AttemptID, Status: effect.Window.Status,
			})
			result.Correlation.LeaseOwner = effect.LeaseOwner
			result.Correlation.LeaseEpoch = effect.LeaseEpoch
		}
	}
	sort.Slice(result.Attempts, func(left, right int) bool {
		if result.Attempts[left].Kind != result.Attempts[right].Kind {
			return result.Attempts[left].Kind < result.Attempts[right].Kind
		}
		return result.Attempts[left].ID < result.Attempts[right].ID
	})
	if outcome.Fault != nil {
		fault := outcome.Fault
		if fault.Stage != "" {
			result.StoppedAt = string(fault.Stage)
		} else if fault.Origin != "" {
			result.StoppedAt = string(fault.Origin)
		}
		result.RecoveryAction = fault.RecoveryAction
		if result.RecoveryAction == "" {
			result.RecoveryAction = string(fault.ResumeHint)
		}
		if result.RecoveryAction == "" {
			result.RecoveryAction = string(fault.Disposition)
		}
		switch fault.Disposition {
		case "retry_step", "retry_turn", "resume_turn":
			result.CanContinue = true
		}
		switch fault.ResumeHint {
		case "retry_step", "retry_turn", "resume_turn", "wait":
			result.CanContinue = true
		}
	} else {
		switch outcome.Code {
		case "unavailable", "deadline_exceeded", "resource_exhausted":
			result.CanContinue = true
			result.RecoveryAction = "retry_or_resume"
		}
	}
	return result
}

func (g Graph) ExplainCost(turnID string) (Explanation, error) {
	explanation, err := g.ExplainTurn(turnID)
	if err != nil {
		return Explanation{}, err
	}
	explanation.SubjectKind = "cost"
	explanation.Conclusion = "cost is unknown in the SO3 observation graph"
	explanation.Unknowns = append(explanation.Unknowns, UnknownFact{
		Code: "cost_observation_missing", ObjectKind: "turn",
		ObjectID: explanation.SubjectID,
		Detail:   "usage remains authoritative outside the SO3 reducer",
	})
	explanation.NextEvidence = "usage observation or SO4 terminal measurement"
	return explanation, nil
}

func (g Graph) ExplainVisibility(sampleID string) (Explanation, error) {
	var edges []VisibilityEdge
	for _, edge := range g.Visibility {
		if strings.HasSuffix(edge.TargetID, semanticSuffix(sampleID)) {
			edges = append(edges, edge)
		}
	}
	if len(edges) == 0 {
		return Explanation{}, fmt.Errorf("%w: sample %q", ErrNotFound, sampleID)
	}
	sort.Slice(edges, func(left, right int) bool {
		return edges[left].Sequence < edges[right].Sequence
	})
	explanation := Explanation{
		SubjectKind: "visibility",
		SubjectID:   sampleID,
		Conclusion: fmt.Sprintf(
			"%d model-visible evidence edge(s)",
			len(edges),
		),
		Visibility: edges,
	}
	for _, edge := range edges {
		if summary, ok := g.Observations[string(edge.Observation)]; ok {
			evidence := Evidence{
				ObservationID: summary.ID,
				Kind:          summary.Kind,
				Sequence:      summary.Sequence,
				Payload:       edge.Payload,
			}
			explanation.Observations = append(
				explanation.Observations,
				evidence,
			)
		}
	}
	enrichExplanation(&explanation)
	return explanation, nil
}

func (g Graph) ExplainRecovery(turnID string) (Explanation, error) {
	explanation, err := g.ExplainTurn(turnID)
	if err != nil {
		return Explanation{}, err
	}
	explanation.SubjectKind = "recovery"
	recovered := false
	for _, evidence := range explanation.Observations {
		if evidence.Kind == observation.KindTurnRecovered {
			recovered = true
			break
		}
	}
	if recovered {
		explanation.Conclusion = "turn recovery was observed"
	} else {
		explanation.Conclusion = "no turn recovery observation is present"
		explanation.NextEvidence = "turn.recovered observation"
	}
	return explanation, nil
}

func (g Graph) explain(
	kind, id string,
	evidence []Evidence,
	conclusion string,
) Explanation {
	explanation := Explanation{
		SubjectKind:  kind,
		SubjectID:    id,
		Conclusion:   conclusion,
		Observations: append([]Evidence(nil), evidence...),
	}
	for _, unknown := range g.Unknowns {
		if unknown.ObjectID == id ||
			evidenceContains(evidence, unknown.Observation) {
			explanation.Unknowns = append(explanation.Unknowns, unknown)
		}
	}
	for _, inconsistency := range g.Inconsistencies {
		if inconsistency.ObjectID == id ||
			observationsIntersect(evidence, inconsistency.Observations) {
			explanation.Inconsistencies = append(
				explanation.Inconsistencies,
				inconsistency,
			)
		}
	}
	enrichExplanation(&explanation)
	return explanation
}

func enrichExplanation(explanation *Explanation) {
	owners := make(map[string]bool)
	payloads := make(map[string]PayloadEvidence)
	for _, evidence := range explanation.Observations {
		if traits, ok := observation.TraitsFor(evidence.Kind); ok {
			owners[string(traits.Owner)] = true
		}
		if evidence.Payload != nil {
			payloads[evidence.Payload.Reference.Digest] = *evidence.Payload
		}
	}
	for owner := range owners {
		explanation.AuthoritySource = append(
			explanation.AuthoritySource,
			owner,
		)
	}
	sort.Strings(explanation.AuthoritySource)
	for _, payload := range payloads {
		explanation.Payloads = append(explanation.Payloads, payload)
	}
	sort.Slice(explanation.Payloads, func(left, right int) bool {
		return explanation.Payloads[left].Reference.Digest <
			explanation.Payloads[right].Reference.Digest
	})
	sortEvidence(explanation.Observations)
}

func findTurn(
	values map[string]TurnNode,
	id string,
) (string, TurnNode, error) {
	if value, ok := values[id]; ok {
		return id, value, nil
	}
	var key string
	var result TurnNode
	for candidate, value := range values {
		if value.TurnID != id {
			continue
		}
		if key != "" {
			return "", TurnNode{}, fmt.Errorf(
				"%w: turn %q",
				ErrAmbiguous,
				id,
			)
		}
		key, result = candidate, value
	}
	if key == "" {
		return "", TurnNode{}, fmt.Errorf("%w: turn %q", ErrNotFound, id)
	}
	return key, result, nil
}

func findTool(
	values map[string]ToolNode,
	id string,
) (string, ToolNode, error) {
	if value, ok := values[id]; ok {
		return id, value, nil
	}
	var key string
	var result ToolNode
	for candidate, value := range values {
		if value.CallID != id {
			continue
		}
		if key != "" {
			return "", ToolNode{}, fmt.Errorf(
				"%w: tool call %q",
				ErrAmbiguous,
				id,
			)
		}
		key, result = candidate, value
	}
	if key == "" {
		return "", ToolNode{}, fmt.Errorf(
			"%w: tool call %q",
			ErrNotFound,
			id,
		)
	}
	return key, result, nil
}

func evidenceContains(
	values []Evidence,
	id observation.ObservationID,
) bool {
	for _, evidence := range values {
		if evidence.ObservationID == id {
			return true
		}
	}
	return false
}

func observationsIntersect(
	evidence []Evidence,
	ids []observation.ObservationID,
) bool {
	for _, id := range ids {
		if evidenceContains(evidence, id) {
			return true
		}
	}
	return false
}

func semanticSuffix(value string) string {
	return "|" + fmt.Sprintf("%d:%s", len(value), value)
}
