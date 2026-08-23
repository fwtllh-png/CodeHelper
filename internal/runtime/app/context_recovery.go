package app

import (
	"context"
	"encoding/json"
	"fmt"

	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func configureThreadManager(options Options) {
	manager, ok := options.Engine.(*ThreadManager)
	if !ok {
		return
	}
	if store, ok := options.TerminalStore.(turnkernel.SessionDeltaRecoveryStore); ok {
		manager.SetSessionDeltaRestorer(contextSessionDeltaRestorer(
			store,
			options.ContentStore,
			options.ContextRebaseStore,
		))
	}
	if options.SessionLifecycle != nil {
		manager.SetSessionResolver(options.SessionLifecycle.SessionForThread)
	}
}

func contextSessionDeltaRestorer(
	store turnkernel.SessionDeltaRecoveryStore,
	content ContentStore,
	rebases ContextRebaseStore,
) SessionDeltaRestorer {
	return func(
		ctx context.Context,
		threadID protocol.ThreadID,
	) (json.RawMessage, error) {
		raw, err := store.LatestSessionDelta(ctx, threadID)
		if err != nil {
			return nil, err
		}
		var expanded []byte
		if len(raw) != 0 {
			var probe struct {
				Manifest json.RawMessage `json:"manifest"`
			}
			expanded = []byte(raw)
			if json.Unmarshal(raw, &probe) == nil && len(probe.Manifest) != 0 {
				expanded, err = agentcontext.ExpandContextEnvelope(
					ctx,
					content,
					raw,
				)
				if err != nil {
					return nil, fmt.Errorf("expand context manifest: %w", err)
				}
			}
		}
		if rebases == nil {
			return expanded, nil
		}
		rebase, found, err := rebases.LatestContextSnapshot(ctx, threadID)
		if err != nil || !found {
			return expanded, err
		}
		if len(expanded) == 0 {
			accounting := agentcontext.AccountingDelta{
				TurnID: "context-recovery:" + string(threadID),
			}
			accounting.Seal()
			replaced, replaceErr := agentcontext.NewSessionDelta(rebase, accounting)
			if replaceErr != nil {
				return nil, replaceErr
			}
			return json.Marshal(replaced)
		}
		var delta agentcontext.SessionDelta
		if err := json.Unmarshal(expanded, &delta); err != nil {
			return nil, err
		}
		current, err := delta.ContextSnapshot()
		if err != nil {
			return nil, err
		}
		if rebase.Revision <= current.Revision {
			return expanded, nil
		}
		replaced, err := agentcontext.NewSessionDelta(
			rebase,
			delta.AccountingDelta(),
		)
		if err != nil {
			return nil, err
		}
		return json.Marshal(replaced)
	}
}
