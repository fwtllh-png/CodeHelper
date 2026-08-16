package kernel

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestReducerLifecycleAndFactReplay(t *testing.T) {
	runID := protocol.RunID("run-lifecycle")
	now := time.Unix(100, 0).UTC()
	graph := model.Empty(runID)
	var allFacts []Fact
	apply := func(command Command) Result {
		t.Helper()
		result, err := Reduce(graph, command)
		if err != nil {
			t.Fatalf("%s: %v", command.Kind, err)
		}
		graph = result.Graph
		allFacts = append(allFacts, result.Facts...)
		return result
	}
	apply(submitCommand(runID, now))
	if graph.Run.State != protocol.RunStateActive ||
		graph.Nodes["node-a"].State != protocol.NodeStateReady ||
		graph.Nodes["node-b"].State != protocol.NodeStatePending {
		t.Fatalf("submitted graph = %+v", graph)
	}
	apply(claimCommand(runID, "node-a", "attempt-a", "effect-a", 1, now))
	apply(Command{
		ID: "bind-a", Kind: CommandBindExecution, RunID: runID,
		ExpectedRevision: 2, At: now.Add(2 * time.Second),
		AttemptID: "attempt-a", LeaseOwner: "worker", LeaseEpoch: 1,
		Execution: &model.ExecutionRef{
			Kind: "turn", EffectID: "effect-a",
			ThreadID: "thread-a", TurnID: "turn-a",
		},
	})
	apply(settleCommand(runID, "attempt-a", 3, protocol.NodeStateSucceeded, now))
	if graph.Nodes["node-b"].State != protocol.NodeStateReady {
		t.Fatalf("dependent node state = %s", graph.Nodes["node-b"].State)
	}
	apply(claimCommand(runID, "node-b", "attempt-b", "effect-b", 4, now))
	apply(Command{
		ID: "bind-b", Kind: CommandBindExecution, RunID: runID,
		ExpectedRevision: 5, At: now.Add(5 * time.Second),
		AttemptID: "attempt-b", LeaseOwner: "worker", LeaseEpoch: 4,
		Execution: &model.ExecutionRef{
			Kind: "turn", EffectID: "effect-b",
			ThreadID: "thread-b", TurnID: "turn-b",
		},
	})
	final := apply(settleCommand(
		runID,
		"attempt-b",
		6,
		protocol.NodeStateSucceeded,
		now,
	))
	if graph.Run.State != protocol.RunStateCompleted {
		t.Fatalf("run state = %s", graph.Run.State)
	}
	if len(final.Effects) != 1 ||
		final.Effects[0].Kind != model.EffectPublishTerminal {
		t.Fatalf("terminal effects = %+v", final.Effects)
	}
	published := apply(Command{
		ID: "publish-terminal", Kind: CommandPublishEffect, RunID: runID,
		EffectID:         final.Effects[0].ID,
		ExpectedRevision: graph.Run.Revision, At: now.Add(8 * time.Second),
	})
	if len(published.Effects) != 1 ||
		published.Effects[0].State != model.EffectDispatched {
		t.Fatalf("published terminal effects = %+v", published.Effects)
	}

	rebuilt := model.Empty(runID)
	for _, fact := range allFacts {
		if err := ApplyFact(&rebuilt, fact); err != nil {
			t.Fatalf("apply fact %d: %v", fact.Sequence, err)
		}
	}
	want, _ := json.Marshal(graph)
	got, _ := json.Marshal(rebuilt)
	if string(got) != string(want) {
		t.Fatalf("fact replay differs\n got: %s\nwant: %s", got, want)
	}
}

func TestReducerCancelAndRevisionConflict(t *testing.T) {
	runID := protocol.RunID("run-cancel")
	now := time.Unix(200, 0).UTC()
	submitted, err := Reduce(model.Empty(runID), submitCommand(runID, now))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Reduce(submitted.Graph, Command{
		ID: "stale", Kind: CommandCancel, RunID: runID,
		ExpectedRevision: 99, At: now.Add(time.Second), Reason: "stop",
	}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale revision error = %v", err)
	}
	canceled, err := Reduce(submitted.Graph, Command{
		ID: "cancel", Kind: CommandCancel, RunID: runID,
		ExpectedRevision: 1, At: now.Add(time.Second), Reason: "stop",
	})
	if err != nil {
		t.Fatal(err)
	}
	if canceled.Graph.Run.State != protocol.RunStateCanceled ||
		canceled.Graph.Nodes["node-a"].State != protocol.NodeStateCanceled ||
		canceled.Graph.Nodes["node-b"].State != protocol.NodeStateCanceled {
		t.Fatalf("canceled graph = %+v", canceled.Graph)
	}
}

func TestReducerRetryFailedNodeReactivatesRun(t *testing.T) {
	runID := protocol.RunID("run-retry")
	now := time.Unix(300, 0).UTC()
	graph := mustReduce(t, model.Empty(runID), submitCommand(runID, now)).Graph
	graph = mustReduce(
		t,
		graph,
		claimCommand(runID, "node-a", "attempt-a", "effect-a", 1, now),
	).Graph
	graph = mustReduce(t, graph, Command{
		ID: "bind", Kind: CommandBindExecution, RunID: runID,
		ExpectedRevision: 2, At: now.Add(time.Second),
		AttemptID: "attempt-a", LeaseOwner: "worker", LeaseEpoch: 1,
		Execution: &model.ExecutionRef{
			Kind: "process", EffectID: "effect-a", ProcessID: "process-a",
		},
	}).Graph
	graph = mustReduce(
		t,
		graph,
		settleCommand(runID, "attempt-a", 3, protocol.NodeStateFailed, now),
	).Graph
	if graph.Run.State != protocol.RunStateFailed {
		t.Fatalf("failed run state = %s", graph.Run.State)
	}
	retried := mustReduce(t, graph, Command{
		ID: "retry", Kind: CommandRetryNode, RunID: runID,
		NodeID: "node-a", ExpectedRevision: 4, At: now.Add(4 * time.Second),
	})
	if retried.Graph.Run.State != protocol.RunStateActive ||
		retried.Graph.Nodes["node-a"].State != protocol.NodeStateReady {
		t.Fatalf("retried graph = %+v", retried.Graph)
	}
}

func TestReducerFollowUpRetriesSucceededNode(t *testing.T) {
	runID := protocol.RunID("run-follow-up")
	now := time.Unix(350, 0).UTC()
	graph := mustReduce(t, model.Empty(runID), submitCommand(runID, now)).Graph
	graph = mustReduce(
		t,
		graph,
		claimCommand(runID, "node-a", "attempt-a", "effect-a", 1, now),
	).Graph
	graph = mustReduce(t, graph, Command{
		ID: "bind", Kind: CommandBindExecution, RunID: runID,
		ExpectedRevision: 2, At: now.Add(time.Second),
		AttemptID: "attempt-a", LeaseOwner: "worker", LeaseEpoch: 1,
		Execution: &model.ExecutionRef{
			Kind: "process", EffectID: "effect-a", ProcessID: "process-a",
		},
	}).Graph
	graph = mustReduce(
		t,
		graph,
		settleCommand(runID, "attempt-a", 3, protocol.NodeStateSucceeded, now),
	).Graph
	retried := mustReduce(t, graph, Command{
		ID: "follow-up", Kind: CommandRetryNode, RunID: runID,
		NodeID: "node-a", ExpectedRevision: graph.Run.Revision,
		At: now.Add(4 * time.Second),
	})
	if retried.Graph.Run.State != protocol.RunStateActive ||
		retried.Graph.Nodes["node-a"].State != protocol.NodeStateReady ||
		len(retried.Graph.Attempts) != 1 {
		t.Fatalf("follow-up graph = %+v", retried.Graph)
	}
}

func TestClaimRejectsAuthorityDriftAndSettlementBindsPermissionDigests(t *testing.T) {
	runID := protocol.RunID("run-authority")
	now := time.Unix(375, 0).UTC()
	submit := submitCommand(runID, now)
	authorityDigest := strings.Repeat("a", 64)
	submit.Submit.AuthorityDigest = authorityDigest
	for index := range submit.Submit.Nodes {
		submit.Submit.Nodes[index].AuthorityDigest = authorityDigest
	}
	graph := mustReduce(t, model.Empty(runID), submit).Graph
	drifted := claimCommand(
		runID,
		"node-a",
		"attempt-a",
		"effect-a",
		1,
		now,
	)
	drifted.ExpectedAuthorityDigest = strings.Repeat("b", 64)
	if _, err := Reduce(graph, drifted); !errors.Is(err, ErrConflict) {
		t.Fatalf("authority drift error = %v", err)
	}
	claim := claimCommand(
		runID,
		"node-a",
		"attempt-a",
		"effect-a",
		1,
		now,
	)
	claim.ExpectedAuthorityDigest = authorityDigest
	graph = mustReduce(t, graph, claim).Graph
	graph = mustReduce(t, graph, Command{
		ID: "bind-authority", Kind: CommandBindExecution, RunID: runID,
		AttemptID: "attempt-a", ExpectedRevision: graph.Run.Revision,
		At: now.Add(time.Second), LeaseOwner: "worker", LeaseEpoch: 1,
		Execution: &model.ExecutionRef{
			Kind: "process", EffectID: "effect-a", ProcessID: "process-a",
		},
	}).Graph
	permissionDigest := strings.Repeat("c", 64)
	settled := mustReduce(t, graph, Command{
		ID: "settle-authority", Kind: CommandSettleExecution, RunID: runID,
		AttemptID: "attempt-a", ExpectedRevision: graph.Run.Revision,
		At: now.Add(2 * time.Second), LeaseOwner: "worker", LeaseEpoch: 1,
		Settlement: &SettlementData{
			State:             protocol.NodeStateSucceeded,
			PermissionDigests: []string{permissionDigest},
		},
	})
	attempt := settled.Graph.Attempts["attempt-a"]
	if attempt.AuthorityDigest != authorityDigest ||
		len(attempt.PermissionDigests) != 1 ||
		attempt.PermissionDigests[0] != permissionDigest {
		t.Fatalf("settled attempt = %+v", attempt)
	}
}

func submitCommand(runID protocol.RunID, now time.Time) Command {
	return Command{
		ID: "submit-" + string(runID), Kind: CommandSubmit, RunID: runID,
		At: now,
		Submit: &SubmitData{
			Kind: model.RunKindWorkflow, Source: "test",
			SessionID: "session", RootThreadID: "thread",
			Nodes: []model.NodeSpec{
				{ID: "node-a", Kind: model.NodeKindAgentTurn},
				{
					ID: "node-b", Kind: model.NodeKindVerify,
					Dependencies: []protocol.NodeID{"node-a"},
				},
			},
		},
	}
}

func claimCommand(
	runID protocol.RunID,
	nodeID protocol.NodeID,
	attemptID protocol.AttemptID,
	effectID protocol.EffectID,
	revision uint64,
	now time.Time,
) Command {
	return Command{
		ID: "claim-" + string(attemptID), Kind: CommandClaimNode,
		RunID: runID, NodeID: nodeID, AttemptID: attemptID,
		EffectID: effectID, ExpectedRevision: revision,
		At:         now.Add(time.Duration(revision) * time.Second),
		LeaseOwner: "worker", LeaseEpoch: revision,
		LeaseExpiresAt: timePointer(
			now.Add(time.Duration(revision)*time.Second + time.Minute),
		),
	}
}

func settleCommand(
	runID protocol.RunID,
	attemptID protocol.AttemptID,
	revision uint64,
	state protocol.NodeState,
	now time.Time,
) Command {
	return Command{
		ID: "settle-" + string(attemptID), Kind: CommandSettleExecution,
		RunID: runID, AttemptID: attemptID, ExpectedRevision: revision,
		At:         now.Add(time.Duration(revision) * time.Second),
		LeaseOwner: "worker", LeaseEpoch: leaseEpoch(attemptID),
		Settlement: &SettlementData{State: state},
	}
}

func leaseEpoch(attemptID protocol.AttemptID) uint64 {
	if attemptID == "attempt-b" {
		return 4
	}
	return 1
}

func timePointer(value time.Time) *time.Time { return &value }

func mustReduce(
	t *testing.T,
	graph model.Graph,
	command Command,
) Result {
	t.Helper()
	result, err := Reduce(graph, command)
	if err != nil {
		t.Fatalf("%s: %v", command.Kind, err)
	}
	return result
}
