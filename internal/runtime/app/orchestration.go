package app

import (
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/kernel"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type OrchestrationHandler struct{ *Runtime }

func (h OrchestrationHandler) Submit(
	operation protocol.Operation,
	payload *protocol.SubmitRunPayload,
) OperationOutcome {
	nodes := make([]model.NodeSpec, len(payload.Nodes))
	for index, node := range payload.Nodes {
		nodes[index] = model.NodeSpec{
			ID: node.ID, Kind: model.NodeKind(node.Kind),
			Dependencies:    append([]protocol.NodeID(nil), node.Dependencies...),
			AuthorityDigest: node.AuthorityDigest,
		}
	}
	return h.execute(kernel.Command{
		ID: string(operation.ID), Kind: kernel.CommandSubmit,
		RunID: payload.RunID, ExpectedRevision: 0, At: operation.CreatedAt,
		Submit: &kernel.SubmitData{
			Kind: model.RunKind(payload.Kind), Source: payload.Source,
			SessionID: payload.SessionID, Workspace: payload.Workspace,
			RootThreadID:    payload.RootThreadID,
			AuthorityDigest: payload.AuthorityDigest, Nodes: nodes,
		},
	})
}

func (h OrchestrationHandler) Cancel(
	operation protocol.Operation,
	payload *protocol.CancelRunPayload,
) OperationOutcome {
	return h.execute(kernel.Command{
		ID: string(operation.ID), Kind: kernel.CommandCancel,
		RunID: payload.RunID, ExpectedRevision: payload.ExpectedRevision,
		At: operation.CreatedAt, Reason: payload.Reason,
	})
}

func (h OrchestrationHandler) Resume(
	operation protocol.Operation,
	payload *protocol.ResumeRunPayload,
) OperationOutcome {
	return h.execute(kernel.Command{
		ID: string(operation.ID), Kind: kernel.CommandResume,
		RunID: payload.RunID, ExpectedRevision: payload.ExpectedRevision,
		At: operation.CreatedAt,
	})
}

func (h OrchestrationHandler) RetryNode(
	operation protocol.Operation,
	payload *protocol.RetryNodePayload,
) OperationOutcome {
	return h.execute(kernel.Command{
		ID: string(operation.ID), Kind: kernel.CommandRetryNode,
		RunID: payload.RunID, NodeID: payload.NodeID,
		ExpectedRevision: payload.ExpectedRevision, At: operation.CreatedAt,
	})
}

func (h OrchestrationHandler) SkipNode(
	operation protocol.Operation,
	payload *protocol.SkipNodePayload,
) OperationOutcome {
	return h.execute(kernel.Command{
		ID: string(operation.ID), Kind: kernel.CommandSkipNode,
		RunID: payload.RunID, NodeID: payload.NodeID,
		ExpectedRevision: payload.ExpectedRevision, At: operation.CreatedAt,
		Reason: payload.Reason,
	})
}

func (h OrchestrationHandler) execute(command kernel.Command) OperationOutcome {
	if h.Runtime == nil || h.orchestration == nil {
		return finishOutcome(protocol.NewProblem(
			protocol.CodeUnavailable,
			"work graph orchestration is unavailable",
			false,
			nil,
		))
	}
	result, err := h.orchestration.Execute(h.ctx, command)
	if err != nil {
		return finishOutcome(err)
	}
	events, err := orchestrationEvents(result.Facts)
	if err != nil {
		return finishOutcome(err)
	}
	return OperationOutcome{
		Kind: OutcomeCommitted, Events: events, CommitMode: CommitNow,
	}
}

func orchestrationEvents(facts []kernel.Fact) ([]protocol.EventData, error) {
	events := make([]protocol.EventData, 0, len(facts))
	for _, fact := range facts {
		switch fact.Kind {
		case kernel.FactRunSubmitted:
			if fact.Run == nil {
				return nil, errors.New("run submitted fact has no run")
			}
			events = append(events, &protocol.RunStartedData{
				Run:  protocol.RunReference{RunID: fact.Run.ID},
				Kind: string(fact.Run.Kind), Source: fact.Run.Source,
				Revision:        fact.Revision,
				AuthorityDigest: fact.Run.AuthorityDigest,
			})
		case kernel.FactRunStatus:
			if fact.Run == nil {
				return nil, errors.New("run status fact has no run")
			}
			reference := protocol.RunReference{RunID: fact.Run.ID}
			switch fact.Run.State {
			case protocol.RunStateCompleted:
				continue
			case protocol.RunStateFailed:
				continue
			case protocol.RunStateCanceled:
				continue
			default:
				events = append(events, &protocol.RunStatusData{
					Run: reference, State: fact.Run.State,
					Revision: fact.Revision, Reason: fact.Run.Reason,
				})
			}
		case kernel.FactNodeDeclared, kernel.FactNodeStatus:
			if fact.Node == nil {
				return nil, errors.New("node fact has no node")
			}
			events = append(events, &protocol.NodeStatusData{
				Node: protocol.NodeReference{
					RunID: fact.Node.RunID, NodeID: fact.Node.ID,
				},
				State: fact.Node.State, Revision: fact.Revision,
				Reason: fact.Node.Reason,
			})
		case kernel.FactAttemptCreated, kernel.FactAttemptStatus:
			if fact.Attempt == nil {
				return nil, errors.New("attempt fact has no attempt")
			}
			events = append(events, &protocol.AttemptStatusData{
				Attempt: protocol.AttemptReference{
					RunID: fact.Attempt.RunID, NodeID: fact.Attempt.NodeID,
					AttemptID: fact.Attempt.ID,
				},
				State: fact.Attempt.State, Revision: fact.Revision,
				LeaseOwner: fact.Attempt.LeaseOwner,
				LeaseEpoch: fact.Attempt.LeaseEpoch,
				Reason:     fact.Attempt.Reason,
			})
		case kernel.FactExecutionBound:
			if fact.Attempt == nil || fact.Attempt.Execution == nil {
				return nil, errors.New("execution bound fact has no execution")
			}
			execution := fact.Attempt.Execution
			events = append(events, &protocol.ExecutionBoundData{
				Correlation: protocol.OrchestrationCorrelation{
					RunID: fact.Attempt.RunID, NodeID: fact.Attempt.NodeID,
					AttemptID: fact.Attempt.ID, EffectID: execution.EffectID,
				},
				Kind: execution.Kind, ThreadID: execution.ThreadID,
				TurnID: execution.TurnID, ProcessID: execution.ProcessID,
				LaneID: execution.LaneID,
			})
		case kernel.FactEffectQueued, kernel.FactEffectStatus:
			continue
		default:
			return nil, errors.New("unknown work graph fact")
		}
	}
	return events, nil
}
