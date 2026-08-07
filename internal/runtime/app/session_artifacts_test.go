package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type memoryArtifactStore struct {
	checkpoint protocol.SessionCheckpoint
	history    []protocol.CompactedMessage
	profile    protocol.SessionProfile
	plan       protocol.SessionPlanArtifact
}

func (s *memoryArtifactStore) SaveCheckpoint(
	context.Context,
	protocol.SessionCheckpoint,
	[]protocol.CompactedMessage,
	protocol.SessionProfile,
) (protocol.SessionCheckpoint, error) {
	return protocol.SessionCheckpoint{}, errors.New("unexpected Checkpoint save")
}

func (s *memoryArtifactStore) GetCheckpoint(
	context.Context,
	string,
) (
	protocol.SessionCheckpoint,
	[]protocol.CompactedMessage,
	protocol.SessionProfile,
	error,
) {
	return s.checkpoint,
		append([]protocol.CompactedMessage(nil), s.history...),
		s.profile,
		nil
}

func (s *memoryArtifactStore) ListCheckpoints(
	context.Context,
	string,
	int,
) ([]protocol.SessionCheckpoint, error) {
	return []protocol.SessionCheckpoint{s.checkpoint}, nil
}

func (s *memoryArtifactStore) CountCheckpoints(
	context.Context,
	string,
) (int, error) {
	return 1, nil
}

func (s *memoryArtifactStore) SavePlan(
	context.Context,
	protocol.SessionPlanArtifact,
) (protocol.SessionPlanArtifact, error) {
	return protocol.SessionPlanArtifact{}, errors.New("unexpected Plan save")
}

func (s *memoryArtifactStore) GetPlan(
	context.Context,
	string,
) (protocol.SessionPlanArtifact, error) {
	return s.plan, nil
}

func (s *memoryArtifactStore) LatestPlan(
	context.Context,
	string,
	protocol.ThreadID,
) (protocol.SessionPlanArtifact, bool, error) {
	return s.plan, s.plan.ID != "", nil
}

type artifactTestEngine struct {
	profileTestEngine
	history  []provider.Message
	restores int
	forks    map[protocol.ThreadID][]provider.Message
}

func (e *artifactTestEngine) History(
	protocol.ThreadID,
) ([]provider.Message, error) {
	return append([]provider.Message(nil), e.history...), nil
}

func (e *artifactTestEngine) RestoreCheckpoint(
	_ protocol.ThreadID,
	history []provider.Message,
) error {
	e.history = append([]provider.Message(nil), history...)
	e.restores++
	return nil
}

func (e *artifactTestEngine) ForkCheckpoint(
	_ protocol.ThreadID,
	child protocol.ThreadID,
	history []provider.Message,
) error {
	if e.forks == nil {
		e.forks = make(map[protocol.ThreadID][]provider.Message)
	}
	e.forks[child] = append([]provider.Message(nil), history...)
	return nil
}

func (e *artifactTestEngine) Release(threadID protocol.ThreadID) {
	delete(e.forks, threadID)
}

func TestCheckpointRestoreIsStateOnlyAndForkPreservesLineage(t *testing.T) {
	profile := runtimeTestProfile()
	profile.Mode = "plan"
	encoded, err := EncodeCompactedHistory([]provider.Message{
		provider.TextMessage(provider.RoleUser, "checkpoint prompt"),
		provider.TextMessage(provider.RoleAssistant, "checkpoint result"),
	})
	if err != nil {
		t.Fatal(err)
	}
	artifacts := &memoryArtifactStore{
		checkpoint: protocol.SessionCheckpoint{
			Version:         protocol.CheckpointProtocolVersion,
			ID:              "checkpoint-1",
			SessionID:       "session-profile",
			ThreadID:        "thread-profile",
			TurnID:          "turn-checkpoint",
			Cursor:          8,
			Status:          protocol.CheckpointCompleted,
			Summary:         "Checkpoint result",
			ProfileRevision: profile.Revision,
			CanRestore:      true,
			CanFork:         true,
			CreatedAt:       time.Now().UTC(),
		},
		history: encoded,
		profile: profile,
	}
	engine := &artifactTestEngine{
		profileTestEngine: profileTestEngine{},
		history: []provider.Message{
			provider.TextMessage(provider.RoleUser, "later work"),
		},
	}
	lifecycle := artifactLifecycle()
	runtime := NewRuntime(Options{
		Engine:              engine,
		SessionProfiles:     &memoryProfileStore{profile: profile},
		DefaultProfile:      profile,
		ProfileCapabilities: runtimeTestCapabilities(profile),
		SessionLifecycle:    lifecycle,
		SessionArtifacts:    artifacts,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	result, err := runtime.RestoreCheckpoint(
		t.Context(),
		"session-profile",
		"checkpoint-1",
	)
	if err != nil {
		t.Fatal(err)
	}
	if result.SideEffectsReplayed || engine.restores != 1 ||
		len(engine.history) != 2 {
		t.Fatalf("Restore result=%+v history=%+v", result, engine.history)
	}
	events, _, err := runtime.ReplayEvents(t.Context(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		if event.Kind == protocol.EventToolStart ||
			event.Kind == protocol.EventToolResult {
			t.Fatalf("Restore replayed Tool event %+v", event)
		}
	}
	forked, err := runtime.ForkCheckpoint(
		t.Context(),
		"session-profile",
		"checkpoint-1",
		"Child",
	)
	if err != nil {
		t.Fatal(err)
	}
	if forked.ParentID != "thread-profile" ||
		len(engine.forks[forked.ThreadID]) != 2 ||
		lifecycle.summary.ThreadID != forked.ThreadID ||
		lifecycle.summary.ParentThreadID != "thread-profile" {
		t.Fatalf("Fork result=%+v lifecycle=%+v", forked, lifecycle.summary)
	}
}

func TestPlanTransitionUsesANewGuardedTurnProfile(t *testing.T) {
	profile := runtimeTestProfile()
	profile.Mode = "plan"
	profiles := &memoryProfileStore{profile: profile}
	artifacts := &memoryArtifactStore{
		plan: protocol.SessionPlanArtifact{
			Version:         protocol.CheckpointProtocolVersion,
			ID:              "plan-1",
			SessionID:       "session-profile",
			ThreadID:        "thread-profile",
			TurnID:          "turn-plan",
			Cursor:          7,
			Status:          protocol.PlanArtifactReady,
			Body:            "1. Update parser",
			ProfileRevision: profile.Revision,
			CanImplement:    true,
			CanAutopilot:    true,
			CreatedAt:       time.Now().UTC(),
		},
	}
	lifecycle := artifactLifecycle()
	runtime := NewRuntime(Options{
		Engine:              &profileTestEngine{},
		SessionProfiles:     profiles,
		DefaultProfile:      profile,
		ProfileCapabilities: runtimeTestCapabilities(profile),
		SessionLifecycle:    lifecycle,
		SessionArtifacts:    artifacts,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	prepared, err := runtime.PreparePlanTransition(
		t.Context(),
		"session-profile",
		"plan-1",
		protocol.PlanTransitionAutopilot,
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.ProfileUpdate.Profile.Mode != "act" ||
		prepared.ProfileUpdate.Profile.ApprovalPosture != "auto" ||
		!strings.Contains(prepared.Prompt, artifacts.plan.Body) ||
		prepared.IdempotencyKey != "plan:plan-1:autopilot" {
		t.Fatalf("Plan transition = %+v", prepared)
	}
	if _, err := runtime.PreparePlanTransition(
		t.Context(),
		"session-profile",
		"plan-1",
		protocol.PlanTransitionImplement,
	); protocol.CodeOf(err) != protocol.CodeConflict {
		t.Fatalf("stale Plan transition error = %v", err)
	}
}

func TestPlanTransitionToRequiresMatchingProfileAndForkLineage(t *testing.T) {
	profile := runtimeTestProfile()
	profile.Mode = "plan"
	profiles := &memoryProfileStore{profile: profile}
	artifacts := &memoryArtifactStore{
		plan: protocol.SessionPlanArtifact{
			Version:         protocol.CheckpointProtocolVersion,
			ID:              "plan-1",
			SessionID:       "session-profile",
			ThreadID:        "thread-profile",
			TurnID:          "turn-plan",
			Cursor:          7,
			Status:          protocol.PlanArtifactReady,
			Body:            "1. Update parser",
			ProfileRevision: profile.Revision,
			CanImplement:    true,
			CanAutopilot:    true,
			CreatedAt:       time.Now().UTC(),
		},
	}
	lifecycle := artifactLifecycle()
	lifecycle.summary.ThreadID = "thread-plan-fork"
	lifecycle.summary.ParentThreadID = "thread-profile"
	runtime := NewRuntime(Options{
		Engine:              &profileTestEngine{},
		SessionProfiles:     profiles,
		DefaultProfile:      profile,
		ProfileCapabilities: runtimeTestCapabilities(profile),
		SessionLifecycle:    lifecycle,
		SessionArtifacts:    artifacts,
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	destination, err := runtime.PreparePlanTransitionTo(
		t.Context(),
		"session-profile",
		"session-profile",
		"plan-1",
		protocol.PlanTransitionImplement,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(destination.Prompt, artifacts.plan.Body) ||
		destination.IdempotencyKey !=
			"plan:plan-1:implement:session-profile" {
		t.Fatalf("Plan destination = %+v", destination)
	}
	if _, err := runtime.PreparePlanTransitionTo(
		t.Context(),
		"session-profile",
		"session-profile",
		"plan-1",
		protocol.PlanTransitionImplement,
	); protocol.CodeOf(err) != protocol.CodeConflict {
		t.Fatalf("stale source Profile error = %v", err)
	}
	artifacts.plan.ProfileRevision = profiles.profile.Revision
	lifecycle.summary.ParentThreadID = "thread-unrelated"
	if _, err := runtime.PreparePlanTransitionTo(
		t.Context(),
		"session-profile",
		"session-profile",
		"plan-1",
		protocol.PlanTransitionImplement,
	); protocol.CodeOf(err) != protocol.CodeInvalidArgument {
		t.Fatalf("unrelated Fork lineage error = %v", err)
	}
}

func TestTurnRecoveryCreatesANewPromptWithoutReplayingOperations(t *testing.T) {
	events := NewMemoryEventStore(16)
	meta := protocol.EventMeta{
		Sequence:    1,
		OperationID: "operation-source",
		ThreadID:    "thread-profile",
		TurnID:      "turn-source",
		ItemID:      "item-source",
	}
	started, err := protocol.NewEvent(meta, &protocol.TurnStartedData{
		Provider: "fixture",
		Model:    "fixture-model",
		Prompt:   "Fix the parser",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), started); err != nil {
		t.Fatal(err)
	}
	meta.Sequence = 2
	completed, err := protocol.NewEvent(meta, &protocol.TurnCompletedData{
		Text: "done",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), completed); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(Options{
		EventStore:       events,
		SessionLifecycle: artifactLifecycle(),
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	retry, err := runtime.PrepareTurnRecovery(
		t.Context(),
		protocol.TurnRecoveryRequest{
			Version:   protocol.WorkflowIntentVersion,
			Action:    protocol.TurnRecoveryRetry,
			SessionID: "session-profile", SourceTurnID: "turn-source",
			IdempotencyKey: "retry-source",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if retry.Prompt != "Fix the parser" ||
		retry.IdempotencyKey != "retry-source" {
		t.Fatalf("Retry preparation = %+v", retry)
	}
	continued, err := runtime.PrepareTurnRecovery(
		t.Context(),
		protocol.TurnRecoveryRequest{
			Version:   protocol.WorkflowIntentVersion,
			Action:    protocol.TurnRecoveryContinue,
			SessionID: "session-profile", SourceTurnID: "turn-source",
			Guidance: "Run focused tests", IdempotencyKey: "continue-source",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(continued.Prompt, "do not repeat completed Tool") ||
		!strings.Contains(continued.Prompt, "Run focused tests") {
		t.Fatalf("Continue preparation = %+v", continued)
	}
	replayed, err := events.Replay(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 2 {
		t.Fatalf("Recovery preparation emitted historical operations: %+v", replayed)
	}
}

func artifactLifecycle() *memorySessionLifecycleStore {
	now := time.Now().UTC()
	return &memorySessionLifecycleStore{summary: protocol.SessionSummary{
		Version:         protocol.SessionLifecycleVersion,
		Revision:        1,
		SessionID:       "session-profile",
		ThreadID:        "thread-profile",
		Title:           "Artifacts",
		Status:          protocol.SessionStatusIdle,
		Isolation:       "shared",
		WorkspaceRoot:   "/workspace",
		WorkspaceLabel:  "workspace",
		ExecutionTarget: "local",
		CreatedAt:       now,
		UpdatedAt:       now,
	}}
}
