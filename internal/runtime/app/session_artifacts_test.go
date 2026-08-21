package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/sessiondelta"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type memoryArtifactStore struct {
	checkpoint protocol.SessionCheckpoint
	history    []protocol.CompactedMessage
	profile    protocol.SessionProfile
	plan       protocol.SessionPlanArtifact
	context    sessiondelta.ContextSnapshot
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

func (s *memoryArtifactStore) SaveContextCheckpoint(
	context.Context,
	protocol.SessionCheckpoint,
	[]protocol.CompactedMessage,
	sessiondelta.ContextSnapshot,
	protocol.SessionProfile,
) (protocol.SessionCheckpoint, error) {
	return protocol.SessionCheckpoint{}, errors.New("unexpected Context Checkpoint save")
}

func (s *memoryArtifactStore) GetContextCheckpoint(
	context.Context,
	string,
) (
	protocol.SessionCheckpoint,
	sessiondelta.ContextSnapshot,
	protocol.SessionProfile,
	error,
) {
	return s.checkpoint, sessiondelta.CloneContextSnapshot(s.context), s.profile, nil
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
	history      []provider.Message
	restores     int
	restoreErrAt int
	restoreErr   error
	forks        map[protocol.ThreadID][]provider.Message
	contexts     map[protocol.ThreadID]sessiondelta.ContextSnapshot
}

type artifactFailingEventStore struct {
	EventStore
	kind protocol.EventKind
	err  error
}

func (s artifactFailingEventStore) Append(
	ctx context.Context,
	event protocol.Event,
) error {
	if event.Kind == s.kind {
		return s.err
	}
	return s.EventStore.Append(ctx, event)
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
	e.restores++
	if e.restoreErrAt == e.restores {
		return e.restoreErr
	}
	e.history = append([]provider.Message(nil), history...)
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
	delete(e.contexts, threadID)
}

func (e *artifactTestEngine) ContextSnapshot(
	threadID protocol.ThreadID,
) (sessiondelta.ContextSnapshot, error) {
	snapshot, ok := e.contexts[threadID]
	if !ok {
		return sessiondelta.ContextSnapshot{}, errors.New("context snapshot is unavailable")
	}
	return sessiondelta.CloneContextSnapshot(snapshot), nil
}

func (e *artifactTestEngine) RestoreContext(
	threadID protocol.ThreadID,
	snapshot sessiondelta.ContextSnapshot,
) (sessiondelta.ReconciliationReceipt, error) {
	if e.contexts == nil {
		e.contexts = make(map[protocol.ThreadID]sessiondelta.ContextSnapshot)
	}
	e.contexts[threadID] = sessiondelta.CloneContextSnapshot(snapshot)
	e.history = append([]provider.Message(nil), snapshot.History...)
	return sessiondelta.ReconciliationReceipt{BindingMatch: true}, nil
}

func (e *artifactTestEngine) ForkContext(
	_ protocol.ThreadID,
	threadID protocol.ThreadID,
	snapshot sessiondelta.ContextSnapshot,
) (sessiondelta.ReconciliationReceipt, error) {
	if e.contexts == nil {
		e.contexts = make(map[protocol.ThreadID]sessiondelta.ContextSnapshot)
	}
	e.contexts[threadID] = sessiondelta.CloneContextSnapshot(snapshot)
	if e.forks == nil {
		e.forks = make(map[protocol.ThreadID][]provider.Message)
	}
	e.forks[threadID] = append([]provider.Message(nil), snapshot.History...)
	return sessiondelta.ReconciliationReceipt{BindingMatch: true}, nil
}

type artifactCurrentContextStore struct {
	current map[protocol.ThreadID]sessiondelta.CurrentContextCommit
}

func (s *artifactCurrentContextStore) CommitContextRebase(
	context.Context,
	sessiondelta.ContextRebaseEnvelope,
) error {
	return nil
}

func (s *artifactCurrentContextStore) LatestContextSnapshot(
	_ context.Context,
	threadID protocol.ThreadID,
) (sessiondelta.ContextSnapshot, bool, error) {
	commit, ok := s.current[threadID]
	return sessiondelta.CloneContextSnapshot(commit.Snapshot), ok, nil
}

func (s *artifactCurrentContextStore) CommitCurrentContext(
	_ context.Context,
	commit sessiondelta.CurrentContextCommit,
) error {
	if err := commit.Validate(); err != nil {
		return err
	}
	if s.current == nil {
		s.current = make(map[protocol.ThreadID]sessiondelta.CurrentContextCommit)
	}
	s.current[commit.ThreadID] = commit
	return nil
}

func (s *artifactCurrentContextStore) DeleteCurrentContext(
	_ context.Context,
	threadID protocol.ThreadID,
	commitID string,
	_ bool,
) error {
	if current, ok := s.current[threadID]; ok && current.ID == commitID {
		delete(s.current, threadID)
	}
	return nil
}

func TestExactContextRestoreAndForkPersistCurrentBaselines(t *testing.T) {
	profile := runtimeTestProfile()
	window, err := contextstore.NewWindowLedger("checkpoint-window", 1)
	if err != nil {
		t.Fatal(err)
	}
	binding := sessiondelta.WorkspaceBinding{
		WorkspaceIdentity: "workspace:test",
	}
	binding.Seal()
	message := provider.TextMessage(provider.RoleUser, "checkpoint context")
	message.Turn = 1
	checkpointContext := sessiondelta.ContextSnapshot{
		Version: sessiondelta.ContextSnapshotVersion,
		Epoch:   1, Revision: 1, Turn: 1,
		History:   []provider.Message{message},
		Workspace: binding,
		Window:    window,
	}
	if err := checkpointContext.Seal(); err != nil {
		t.Fatal(err)
	}
	encoded, err := EncodeCompactedHistory(checkpointContext.History)
	if err != nil {
		t.Fatal(err)
	}
	artifacts := &memoryArtifactStore{
		checkpoint: protocol.SessionCheckpoint{
			Version: protocol.CheckpointProtocolVersion,
			ID:      "checkpoint-context", SessionID: "session-profile",
			ThreadID: "thread-profile", TurnID: "turn-checkpoint",
			Cursor: 8, Status: protocol.CheckpointCompleted,
			ProfileRevision: profile.Revision, CanRestore: true, CanFork: true,
			ContextDigest: checkpointContext.Digest,
		},
		profile: profile,
		context: checkpointContext,
		history: encoded,
	}
	engine := &artifactTestEngine{
		contexts: map[protocol.ThreadID]sessiondelta.ContextSnapshot{
			"thread-profile": checkpointContext,
		},
	}
	current := &artifactCurrentContextStore{}
	lifecycle := artifactLifecycle()
	runtime := NewRuntime(Options{
		Engine:              engine,
		SessionProfiles:     &memoryProfileStore{profile: profile},
		DefaultProfile:      profile,
		ProfileCapabilities: runtimeTestCapabilities(profile),
		SessionLifecycle:    lifecycle,
		SessionArtifacts:    artifacts,
		ContextRebaseStore:  current,
	})
	runtime.durable = true
	t.Cleanup(func() { closeRuntime(t, runtime) })

	restored, err := runtime.RestoreCheckpoint(
		t.Context(),
		"session-profile",
		"checkpoint-context",
	)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.ExactContext ||
		current.current["thread-profile"].Snapshot.Digest != checkpointContext.Digest {
		t.Fatalf("restore=%+v current=%+v", restored, current.current)
	}
	forked, err := runtime.ForkCheckpoint(
		t.Context(),
		"session-profile",
		"checkpoint-context",
		"Child",
	)
	if err != nil {
		t.Fatal(err)
	}
	commit, ok := current.current[forked.ThreadID]
	if !forked.ExactContext || !ok ||
		commit.ParentThreadID != "thread-profile" ||
		commit.SessionID != "session-profile" ||
		commit.Snapshot.Digest != checkpointContext.Digest {
		t.Fatalf("fork=%+v commit=%+v", forked, commit)
	}
	events, _, err := runtime.ReplayEvents(t.Context(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var restoreReferenced, forkReferenced bool
	for _, event := range events {
		switch data := event.Data.(type) {
		case *protocol.CheckpointRestoredData:
			saved := current.current["thread-profile"]
			restoreReferenced = data.ContextCommitID == saved.ID &&
				data.ContextDigest == saved.Snapshot.Digest &&
				data.ContextRevision == saved.Snapshot.Revision &&
				data.StateEpoch == saved.Snapshot.Epoch
		case *protocol.CheckpointForkedData:
			forkReferenced = data.ContextCommitID == commit.ID &&
				data.ContextDigest == commit.Snapshot.Digest &&
				data.ContextRevision == commit.Snapshot.Revision &&
				data.StateEpoch == commit.Snapshot.Epoch
		}
	}
	if !restoreReferenced || !forkReferenced {
		t.Fatalf(
			"context references restore=%t fork=%t events=%+v",
			restoreReferenced,
			forkReferenced,
			events,
		)
	}
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

func TestCheckpointRestoreJoinsPublicationAndRollbackFailures(t *testing.T) {
	profile := runtimeTestProfile()
	encoded, err := EncodeCompactedHistory([]provider.Message{
		provider.TextMessage(provider.RoleUser, "checkpoint prompt"),
		provider.TextMessage(provider.RoleAssistant, "checkpoint result"),
	})
	if err != nil {
		t.Fatal(err)
	}
	rollbackErr := errors.New("injected checkpoint rollback failure")
	publishErr := errors.New("injected checkpoint publication failure")
	engine := &artifactTestEngine{
		profileTestEngine: profileTestEngine{},
		history: []provider.Message{
			provider.TextMessage(provider.RoleUser, "current history"),
		},
		restoreErrAt: 2,
		restoreErr:   rollbackErr,
	}
	events := artifactFailingEventStore{
		EventStore: NewMemoryEventStore(16),
		kind:       protocol.EventCheckpointRestored,
		err:        publishErr,
	}
	runtime := NewRuntime(Options{
		Engine:              engine,
		EventStore:          events,
		SessionProfiles:     &memoryProfileStore{profile: profile},
		DefaultProfile:      profile,
		ProfileCapabilities: runtimeTestCapabilities(profile),
		SessionLifecycle:    artifactLifecycle(),
		SessionArtifacts: &memoryArtifactStore{
			checkpoint: protocol.SessionCheckpoint{
				Version: protocol.CheckpointProtocolVersion,
				ID:      "checkpoint-rollback", SessionID: "session-profile",
				ThreadID: "thread-profile", TurnID: "turn-checkpoint",
				Cursor: 8, Status: protocol.CheckpointCompleted,
				ProfileRevision: profile.Revision, CanRestore: true,
			},
			history: encoded,
			profile: profile,
		},
	})
	t.Cleanup(func() { closeRuntime(t, runtime) })
	_, err = runtime.RestoreCheckpoint(
		t.Context(),
		"session-profile",
		"checkpoint-rollback",
	)
	if err == nil ||
		!errors.Is(err, publishErr) ||
		!errors.Is(err, rollbackErr) {
		t.Fatalf("RestoreCheckpoint() error = %v", err)
	}
	if engine.restores != 2 {
		t.Fatalf("RestoreCheckpoint() calls = %d, want 2", engine.restores)
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
		prepared.Intent != protocol.TurnIntentWorkspaceChange ||
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
	if destination.Intent != protocol.TurnIntentWorkspaceChange ||
		!strings.Contains(destination.Prompt, artifacts.plan.Body) ||
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
		Intent:   protocol.TurnIntentWorkspaceChange,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), started); err != nil {
		t.Fatal(err)
	}
	meta.Sequence = 2
	output, err := protocol.NewEvent(meta, &protocol.OutputDeltaData{
		Text: "I inspected the parser and reached validation.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), output); err != nil {
		t.Fatal(err)
	}
	meta.Sequence = 3
	toolStarted, err := protocol.NewEvent(meta, &protocol.ToolStartData{
		Tool: "file_read", CallID: "call-read",
		Arguments: json.RawMessage(`{"path":"parser.go"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), toolStarted); err != nil {
		t.Fatal(err)
	}
	meta.Sequence = 4
	toolResult, err := protocol.NewEvent(meta, &protocol.ToolResultData{
		Tool: "file_read", CallID: "call-read", Output: "package parser",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), toolResult); err != nil {
		t.Fatal(err)
	}
	meta.Sequence = 5
	receipt, err := protocol.NewEvent(meta, &protocol.ExecutionReceiptData{
		Intent:       protocol.TurnIntentWorkspaceChange,
		Outcome:      protocol.TurnOutcomeChanged,
		ReadPaths:    []string{"parser.go"},
		Verification: protocol.ReceiptVerification{},
		WorkspaceOutcome: &protocol.ReceiptWorkspaceOutcome{
			Status: "unchanged",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), receipt); err != nil {
		t.Fatal(err)
	}
	meta.Sequence = 6
	failed, err := protocol.NewEvent(meta, &protocol.TurnFailedData{
		Code: protocol.CodeConflict, Message: "validation failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), failed); err != nil {
		t.Fatal(err)
	}
	profile := runtimeTestProfile()
	profile.Revision = 2
	profile.ApprovalPosture = "bypass"
	profiles := &memoryProfileStore{profile: profile}
	engine := &profileTestEngine{}
	runtime := NewRuntime(Options{
		EventStore:          events,
		SessionLifecycle:    artifactLifecycle(),
		Engine:              engine,
		SessionProfiles:     profiles,
		DefaultProfile:      profile,
		ProfileCapabilities: runtimeTestCapabilities(profile),
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
		retry.DisplayPrompt != "Fix the parser" ||
		retry.Intent != protocol.TurnIntentWorkspaceChange ||
		retry.IdempotencyKey != "retry-source" {
		t.Fatalf("Retry preparation = %+v", retry)
	}
	engine.mu.Lock()
	applied := engine.applied
	engine.mu.Unlock()
	if applied.Revision != profile.Revision ||
		applied.ApprovalPosture != "bypass" {
		t.Fatalf("Recovery applied profile = %+v, want %+v", applied, profile)
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
		continued.Intent != protocol.TurnIntentWorkspaceChange ||
		continued.Recovery.Action != protocol.TurnRecoveryContinue ||
		continued.Recovery.SourceTurnID != "turn-source" ||
		!strings.Contains(continued.Prompt, "Source Turn ID: turn-source") ||
		!strings.Contains(continued.Prompt, "<source_request>\nFix the parser") ||
		!strings.Contains(continued.Prompt, "failed (conflict): validation failed") ||
		!strings.Contains(continued.Prompt, "I inspected the parser") ||
		!strings.Contains(continued.Prompt, "<recovery_evidence>") ||
		!strings.Contains(continued.Prompt, `"intent":"workspace_change"`) ||
		!strings.Contains(continued.Prompt, `"tool":"file_read"`) ||
		!strings.Contains(continued.Prompt, `"read_paths":["parser.go"]`) ||
		strings.Contains(continued.Prompt, `"outcome":"changed"`) ||
		!strings.Contains(continued.Prompt, "Run focused tests") {
		t.Fatalf("Continue preparation = %+v", continued)
	}
	if continued.DisplayPrompt !=
		"Continue: Fix the parser\n\nGuidance: Run focused tests" {
		t.Fatalf("Continue display prompt = %q", continued.DisplayPrompt)
	}
	for _, internal := range []string{
		"Source Turn ID",
		"<source_request>",
		"<recovery_evidence>",
		`"call_id"`,
		`"arguments_digest"`,
	} {
		if strings.Contains(continued.DisplayPrompt, internal) {
			t.Fatalf(
				"Continue display prompt leaked %q: %q",
				internal,
				continued.DisplayPrompt,
			)
		}
	}
	replayed, err := events.Replay(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(replayed) != 6 {
		t.Fatalf("Recovery preparation emitted historical operations: %+v", replayed)
	}
}

func TestTurnRecoveryDefaultsLegacyEmptyIntentToAnswer(t *testing.T) {
	events := NewMemoryEventStore(4)
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
		Prompt:   "Explain the parser",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), started); err != nil {
		t.Fatal(err)
	}
	meta.Sequence++
	completed, err := protocol.NewEvent(meta, &protocol.TurnCompletedData{
		Text: "The parser validates input.",
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

	prepared, err := runtime.PrepareTurnRecovery(
		t.Context(),
		protocol.TurnRecoveryRequest{
			Version:        protocol.WorkflowIntentVersion,
			Action:         protocol.TurnRecoveryContinue,
			SessionID:      "session-profile",
			SourceTurnID:   "turn-source",
			IdempotencyKey: "continue-source",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Intent != protocol.TurnIntentAnswer {
		t.Fatalf("Recovery intent = %q, want answer", prepared.Intent)
	}
}

func TestRecoveryDisplayPromptUnwrapsLegacyInternalPrompt(t *testing.T) {
	legacy := turnRecoveryPromptPrefix + ` Do not infer the task.

Original model-visible request:
<source_request>
Fix the parser
</source_request>

<recovery_evidence>
{"source_turn_id":"turn-source","closed_tools":[{"call_id":"call-read"}]}
</recovery_evidence>`
	if got := recoveryDisplayPrompt(legacy, legacy); got != "Fix the parser" {
		t.Fatalf("legacy recovery display prompt = %q", got)
	}
	nested := turnRecoveryPromptPrefix + ` Do not infer the task.

Original model-visible request:
<source_request>
` + legacy + `
</source_request>`
	if got := recoverySourcePrompt(nested); got != "Fix the parser" {
		t.Fatalf("nested recovery source prompt = %q", got)
	}
	withQuotedClose := turnRecoveryPromptPrefix + ` Do not infer the task.

Original model-visible request:
<source_request>
Fix the parser
</source_request>

Recovery guidance:
<guidance>
Explain the literal </source_request> tag.
</guidance>`
	if got := recoverySourcePrompt(withQuotedClose); got != "Fix the parser" {
		t.Fatalf("quoted close recovery source prompt = %q", got)
	}
	if got := recoveryDisplayPrompt(
		"internal recovery context",
		"Continue: Continue: Fix the parser",
	); got != "Fix the parser" {
		t.Fatalf("nested recovery display prompt = %q", got)
	}
}

func TestRecoveryEvidenceIsCanonicalAndBounded(t *testing.T) {
	first := recoveryDigestJSON(
		[]byte(`{"b":9223372036854775807,"a":1}`),
	)
	second := recoveryDigestJSON(
		[]byte(`{"a":1,"b":9223372036854775807}`),
	)
	if first == "" || first != second {
		t.Fatalf("canonical argument digests = %q and %q", first, second)
	}
	tools := make([]recoveryToolEvidence, 200)
	for index := range tools {
		tools[index] = recoveryToolEvidence{
			Tool:            "file_read",
			CallID:          fmt.Sprintf("call-%03d", index),
			ArgumentsDigest: first,
			OutputDigest:    recoveryDigest([]byte(strings.Repeat("x", index+1))),
		}
	}
	rendered := renderRecoveryEvidence(
		"turn-source",
		protocol.TurnIntentWorkspaceChange,
		"failed (conflict): no changes",
		tools,
		&protocol.ExecutionReceiptData{
			ReadPaths:    []string{"parser.go", "parser_test.go"},
			Verification: protocol.ReceiptVerification{},
			WorkspaceOutcome: &protocol.ReceiptWorkspaceOutcome{
				Status: "unchanged",
			},
		},
	)
	if rendered == "" || len(rendered) > turnRecoveryEvidenceLimit {
		t.Fatalf("rendered recovery evidence bytes = %d", len(rendered))
	}
	var capsule recoveryEvidenceCapsule
	if err := json.Unmarshal([]byte(rendered), &capsule); err != nil {
		t.Fatal(err)
	}
	if capsule.Intent != protocol.TurnIntentWorkspaceChange ||
		capsule.SourceTurnID != "turn-source" ||
		capsule.OmittedTools == 0 ||
		len(capsule.Tools)+capsule.OmittedTools != len(tools) ||
		capsule.Receipt == nil ||
		len(capsule.Receipt.ReadPaths) != 2 {
		t.Fatalf("recovery evidence = %+v", capsule)
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
