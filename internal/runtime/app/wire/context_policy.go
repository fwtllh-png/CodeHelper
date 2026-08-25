package wire

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"

	memorystore "github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	turnstate "github.com/fwtllh-png/CodeHelper/internal/persist/state/turnstate"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	apppersistence "github.com/fwtllh-png/CodeHelper/internal/runtime/app/persistence"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

type contextRuntimeBinding struct {
	durable         *durableCoordinatorRuntime
	coordinator     turnkernel.CoordinatorRuntime
	commit          func(context.Context, agentcontext.ContextRebaseEnvelope) error
	commitWithFacts func(
		context.Context,
		agentcontext.ContextRebaseEnvelope,
		turnkernel.DomainFactBatch,
	) error
}

func bindMemoryScopes(
	store *memorystore.Store,
	workspace string,
	rootID string,
) (string, error) {
	workspaceID, repositoryID, err :=
		memorystore.CanonicalScopeIdentities(workspace)
	if err != nil {
		return "", err
	}
	if rootID != "" {
		workspaceID = "workspace:" + rootID
	}
	if store != nil {
		if err := store.BindScopes(workspaceID, repositoryID); err != nil {
			return "", fmt.Errorf("bind memory scopes: %w", err)
		}
	}
	return workspaceID, nil
}

func engineSecurityPolicy(
	state *buildState,
) (*agentengine.WorkspaceTurnGate, policy.Permission) {
	var workspaceTurnGate *agentengine.WorkspaceTurnGate
	if state.security.journal != nil {
		workspaceTurnGate = agentengine.NewWorkspaceTurnGate()
	}
	posture := policy.PermissionBypass
	if state.security.runtime != nil {
		posture = state.security.runtime.PermissionValue()
	} else if state.options.Permission != "" {
		posture = policy.Permission(state.options.Permission)
	}
	return workspaceTurnGate, posture
}

func validateRouteReasoning(
	route model.ReadyRoute,
	effort string,
) error {
	if effort != "" && !route.Model().Capabilities.Reasoning {
		return errors.New("execution.reasoning_effort requires a reasoning model")
	}
	return nil
}

func effectiveReasoningEffort(route model.ReadyRoute, configured string) string {
	if configured != "" {
		return configured
	}
	return route.Model().Capabilities.DefaultReasoningEffort
}

func buildContextRuntime(
	store *state.Store,
	sessionID string,
) (contextRuntimeBinding, error) {
	if store == nil {
		return contextRuntimeBinding{
			coordinator: turnkernel.NewEphemeralCoordinatorRuntime(),
		}, nil
	}
	durable, err := newDurableCoordinatorRuntime(
		turnstate.NewSQLiteRepository(store.SQLite()),
		sessionID,
		defaultTurnCoordinatorLease,
	)
	if err != nil {
		return contextRuntimeBinding{}, err
	}
	rebases := apppersistence.NewContextRebaseRepository(store)
	return contextRuntimeBinding{
		durable: durable, coordinator: durable,
		commit:          rebases.CommitContextRebase,
		commitWithFacts: rebases.CommitContextRebaseWithFacts,
	}, nil
}

func engineContextPolicy(
	configuration config.Compact,
	commit func(context.Context, agentcontext.ContextRebaseEnvelope) error,
	commitWithFacts func(
		context.Context,
		agentcontext.ContextRebaseEnvelope,
		turnkernel.DomainFactBatch,
	) error,
) agentengine.ContextPolicy {
	return agentengine.ContextPolicy{
		Window: agentengine.CompactWindowPolicy{
			PrepareTokens:   uint64(configuration.PrepareTokens),
			AutoTokens:      uint64(configuration.AutoCompactTokens),
			EmergencyTokens: uint64(configuration.EmergencyTokens),
			Scope:           configuration.Scope,
		},
		TruthRetention: agentcontext.RetentionPolicy{
			TruthMaxBytes:        configuration.TruthMaxBytes,
			TruthMaxEntities:     configuration.TruthMaxEntities,
			MandatoryMaxEntities: configuration.MandatoryMaxEntities,
			FactMaxEntities:      configuration.FactMaxEntities,
			FailureMaxEntities:   configuration.FailureMaxEntities,
			HandleMaxEntities:    configuration.HandleMaxEntities,
			OmissionSampleMaxEntities: configuration.
				OmissionSampleMaxEntities,
			VerifiedChangeRetentionTurns: uint64(
				configuration.VerifiedChangeRetentionTurns,
			),
		},
		SemanticNarrative: configuration.SemanticNarrative,
		NarrativeLimits: agentcontext.NarrativeLimits{
			MaxInputBytes:  configuration.SemanticNarrativeMaxInputTokens * 4,
			MaxOutputBytes: configuration.SemanticNarrativeMaxOutputTokens * 4,
			MaxItems:       configuration.SemanticNarrativeMaxItems,
			ItemMaxBytes:   configuration.SemanticNarrativeItemMaxBytes,
		},
		NarrativeTimeout:      configuration.SemanticNarrativeTimeout,
		NarrativeRetryLimit:   configuration.SemanticNarrativeRetryLimit,
		OwnerDeltaMaxSegments: configuration.OwnerDeltaMaxSegments,
		OwnerDeltaMaxBytes:    configuration.OwnerDeltaMaxBytes,
		RecentTailTurns:       configuration.RecentTailTurns,
		RecentTailMaxTokens:   uint64(configuration.RecentTailMaxTokens),
		CommitRebase:          commit,
		CommitRebaseWithFacts: commitWithFacts,
	}
}

func memorySnapshotSource(
	store *memorystore.Store,
	configuration config.Memory,
	budget promptcontext.Budget,
) func(string) (agentengine.MemorySnapshot, error) {
	return func(query string) (agentengine.MemorySnapshot, error) {
		if store == nil {
			return agentengine.MemorySnapshot{}, nil
		}
		maxBytes := configuration.MaxPromptBytes
		if budget.MaxBytes > 0 {
			maxBytes = min(maxBytes, budget.MaxBytes)
		}
		if budget.MaxTokens > 0 {
			maxBytes = min(maxBytes, int(budget.MaxTokens*4))
		}
		block, selection, err := store.SelectBlock(memorystore.Query{
			Text:          query,
			MaxCandidates: configuration.MaxCandidates,
			MaxBytes:      maxBytes,
		})
		if err != nil {
			return agentengine.MemorySnapshot{}, err
		}
		digest := sha256.Sum256([]byte(block))
		return agentengine.MemorySnapshot{
			Generation:     selection.Generation,
			Body:           block,
			Source:         store.Path(),
			Digest:         fmt.Sprintf("sha256:%x", digest[:]),
			CandidateCount: selection.CandidateCount,
			SelectedIDs: append(
				[]string(nil),
				selection.SelectedIDs...,
			),
			Truncated: selection.Truncated,
		}, nil
	}
}
