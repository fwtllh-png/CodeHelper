package engine

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerassembly "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/assembly"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/toolsearch"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (e *Engine) reasoningEffort(scope *Scope, reason string) string {
	scope.mu.Lock()
	if reason != promptcontext.SampleNormal {
		scope.state.reasoningEscalation = min(scope.state.reasoningEscalation+1, 3)
	}
	level := scope.state.reasoningEscalation
	scope.mu.Unlock()
	return promptcontext.ReasoningEffort(
		scope.spec.Request.Prompt,
		string(scope.spec.Request.Intent),
		level,
		scope.spec.Route.Model().Capabilities.ReasoningEffortLevels(),
		e.options.ReasoningEffort,
	)
}

func (e *Engine) modelStep(
	ctx context.Context,
	history *[]provider.Message,
	turnUsage provider.Usage,
	sampleID string,
	reason string,
	providerRetries uint32,
	finishOnly bool,
	convergenceOnly bool,
	continued *bool,
	pendingInputInjected *bool,
	capturedReplay **provider.ReplayState,
	assembly *providerassembly.ResponseAssembly,
	checkpoint func(*providerassembly.ResponseAssembly) error,
	send func(State, Event) error,
) ([]provider.ContentBlock, []provider.ToolCall, provider.Usage, uint64, error) {
	if continued != nil {
		*continued = false
	}
	if pendingInputInjected != nil {
		*pendingInputInjected = false
	}
	if capturedReplay != nil {
		*capturedReplay = nil
	}
	if assembly == nil {
		assembly = providerassembly.NewResponseAssembly(sampleID)
	}
	if err := assembly.Validate(); err != nil {
		return nil, nil, provider.Usage{}, 0,
			fmt.Errorf("restore provider response assembly: %w", err)
	}
	if assembly.State == providerassembly.ResponseComplete {
		calls, err := assembly.ExecutableToolCalls()
		if err == nil {
			if continued != nil {
				*continued = assembly.TransportCount() > 1
			}
			if capturedReplay != nil {
				*capturedReplay = assembly.CurrentReplay()
			}
			return assembly.ConfirmedBlocks(), calls,
				assembly.TotalUsage(), 0, nil
		}
	}
	scope := e.runningScope()
	if scope == nil {
		return nil, nil, provider.Usage{}, 0, errors.New("turn scope is not active")
	}
	admittedHistory, err := e.admitToolResultHistory(*history)
	if err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	*history = admittedHistory
	if err := e.emitExtensionLifecycleChanges(scope.spec.ExtensionPlan, send); err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	if err := e.emitMCPHealthChanges(scope.spec.MCP, send); err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	catalog := e.scopeCatalog(scope)
	if changed := e.catalogChange(catalog); changed != nil {
		if err := send(CallingModel, Event{CatalogChanged: changed}); err != nil {
			return nil, nil, provider.Usage{}, 0, err
		}
	}
	definitions, advertised, err := e.toolDefinitionsFromSnapshot(catalog, scope.spec.Request)
	if err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	stableContext, worldDelta, worldReceipts, worldProjection, err := e.projectWorldState(
		ctx, *history, catalog, advertised,
	)
	if err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	*history = append(*history, cloneMessages(worldDelta)...)
	scope.mu.Lock()
	if scope.state.contextLedger == nil {
		scope.state.contextLedger = agentcontext.NewMessageLedger(agentcontext.LedgerInput{
			Stable: stableContext, History: *history, Definitions: definitions,
		})
	}
	contextLedger := scope.state.contextLedger
	scope.mu.Unlock()
	totalUsage := assembly.TotalUsage()
	var lastEstimate uint64
	var providerAttempt uint32
	var continuationMessages []provider.Message
	var continuedBlocks []provider.ContentBlock
	continuations := uint32(0)
	if assembly.TransportCount() != 0 {
		continuedBlocks = assembly.ConfirmedBlocks()
		if len(continuedBlocks) != 0 {
			continuationMessages = append(
				continuationMessages,
				provider.ProducedAssistant(
					e.activeRoute(),
					cloneBlocks(continuedBlocks),
					e.turn,
					nil,
				),
			)
		}
		continuationMessages = append(
			continuationMessages,
			promptcontext.IncompleteOutputFeedback(
				provider.StopReasonIncomplete,
				assembly.IncompleteToolFragments(),
				e.turn,
			),
		)
		continuations = uint32(assembly.TransportCount())
		if continued != nil {
			*continued = true
		}
	}
	baseReasoningEffort := e.reasoningEffort(scope, reason)
	for attempt := 0; ; attempt++ {
		var turnContext []provider.Message
		turnReceipts := append([]promptcontext.Receipt(nil), worldReceipts...)
		e.recordTurnContextReceipts(turnReceipts)
		budgetMessage, budgetFinishOnly := e.budgetConvergence(
			e.BudgetSnapshot().TokensUsed + turnUsage.Total() + totalUsage.Total(),
		)
		if len(budgetMessage.Blocks) != 0 {
			turnContext = append(turnContext, budgetMessage)
		}
		route := e.activeRoute()
		requestTools := definitions
		reasoningEffort := baseReasoningEffort
		nativeSearch := e.options.NativeSearch
		if convergenceOnly {
			reasoningEffort = "low"
			nativeSearch = false
		}
		sampleReason := promptcontext.SampleReason(
			reason, attempt, continuations > 0,
		)
		project := func() agentcontext.MessageSnapshot {
			projectedHistory := *history
			if !route.Model().Capabilities.IncrementalResponses {
				projectedHistory = agentcontext.ProjectStatelessHistory(
					projectedHistory,
				)
			}
			return contextLedger.Project(agentcontext.LedgerProjection{
				Stable: stableContext, History: projectedHistory, Dynamic: turnContext,
				Continuation: continuationMessages, Definitions: requestTools,
			})
		}
		snapshot := project()
		phase := CompactionPhaseMidTurn
		if turnUsage.InputTokens+totalUsage.InputTokens == 0 {
			phase = CompactionPhasePreSampling
		}
		gateSend := deduplicateCompactionReceipts(send)
		window, err := e.runCompactGate(
			ctx,
			history, snapshot, 0, phase, true, gateSend,
		)
		if err != nil {
			return nil, nil, totalUsage, window.estimated, err
		}
		snapshot = project()
		if window.hardLimit > 0 &&
			window.active >= e.prepareCompactLimit() {
			turnContext = append(
				turnContext,
				promptcontext.ContextWindowFeedback(e.turn),
			)
			snapshot = project()
			window, err = e.runCompactGate(
				ctx,
				history, snapshot, 0, phase, true, gateSend,
			)
			if err != nil {
				return nil, nil, totalUsage, window.estimated, err
			}
		}
		finishOnly = finishOnly || convergenceOnly || budgetFinishOnly ||
			window.hardLimit > 0 &&
				window.active >= e.emergencyCompactLimit()
		if finishOnly {
			requestTools = slices.DeleteFunc(
				append([]provider.ToolDefinition(nil), requestTools...),
				func(definition provider.ToolDefinition) bool {
					if convergenceOnly {
						return !tool.ConvergenceDefinitionAllowed(definition)
					}
					return !tool.FinishOnlyDefinitionAllowed(catalog, definition)
				})
			reasoningEffort, nativeSearch = "low", false
		}
		maxOutputTokens := e.maxOutputFor(route)
		snapshot = project()
		snapshot, normalization, normalizationErr := snapshot.Normalize(
			route.Model().Capabilities,
		)
		if normalizationErr != nil {
			return nil, nil, totalUsage, lastEstimate,
				fmt.Errorf("normalize context projection: %w", normalizationErr)
		}
		requestTools = snapshot.Definitions()
		e.recordSampledTools(scope, catalog, requestTools)
		attribution, attributionErr := snapshot.Measure(
			sampleReason, reasoningEffort, e.options.TokenEstimator,
		)
		if attributionErr != nil {
			return nil, nil, totalUsage, lastEstimate, attributionErr
		}
		attribution.WorldRevision = worldProjection.Baseline.Revision
		attribution.WorldDigest = worldProjection.Baseline.Digest
		attribution.WorldMode = string(worldProjection.Mode)
		attribution.WorldChangedSections = len(worldProjection.Changed)
		attribution.PairingCalls = normalization.ToolCalls
		attribution.PairingResults = normalization.ToolResults
		attribution.PairingPairs = normalization.PairedCalls
		attribution.PairingDroppedOrphans = normalization.DroppedOrphans
		attribution.PairingVisibleOrphans = normalization.ModelVisibleOrphans
		attribution.ProjectedImages = normalization.ProjectedImages
		attribution.DroppedReasoning = normalization.DroppedReasoning
		lastEstimate = attribution.EstimatedTokens
		windowProjection := e.prepareTokenWindow(&attribution, 0)
		maxOutputTokens, err = e.checkBudget(
			windowProjection.FullActiveTokens,
			turnUsage,
			totalUsage,
			maxOutputTokens,
		)
		if err != nil {
			return nil, nil, totalUsage, lastEstimate, err
		}
		windowProjection = e.prepareTokenWindow(
			&attribution,
			maxOutputTokens,
		)
		messages := snapshot.Messages()
		providerAttempt++
		if err := send(CallingModel, Event{
			ModelExecution: &ModelExecution{
				Kind: "provider_attempt", SampleID: sampleID,
				Attempt: providerAttempt, Reason: sampleReason,
			},
		}); err != nil {
			return nil, nil, totalUsage, lastEstimate, err
		}
		call := sample{
			index: e.nextSample(), provider: route.ProviderID(),
			model: route.Model().ID, pricing: route.Model().Pricing, context: &attribution,
			observe: e.observeTokenWindow,
		}
		transport, err := providerassembly.RunTransportAttempt(
			ctx,
			e.options.Provider,
			provider.ModelRequest{
				Route: route, Messages: messages,
				LogicalRequestID: sampleID,
				TransportAttempt: assembly.NextTransportAttempt(),
				Projection: provider.ProjectionContext{
					ContextRevision: attribution.ContextRevision,
					WindowID:        attribution.WindowID,
					WindowNumber:    attribution.WindowNumber,
					Retry: providerRetries > 0 ||
						assembly.TransportCount() > 0,
					RecoveryID: providerassembly.ProjectionRecoveryID(
						scope.spec.Request.Recovery,
					),
				},
				MaxOutputTokens: maxOutputTokens, Tools: requestTools,
				ReasoningEffort: reasoningEffort, NativeSearch: nativeSearch,
				Idempotent: true,
				PromptCacheKey: provider.StickyPromptCacheKey(
					e.options.PromptCacheKey,
					route,
				),
			},
			assembly,
			providerassembly.ConsumeConfig{
				FirstOutput: e.tracer().NoteFirstOutput,
				Checkpoint:  checkpoint,
				Project: func(projected providerassembly.StreamProjection) error {
					if projected.Usage != nil {
						agentcontext.ApplyTransport(
							call.context,
							projected.Transport,
						)
						copy := *projected.Usage
						if call.observe != nil {
							call.observe(
								call.context,
								copy.InputTokens,
								copy.CachedTokens,
							)
						}
						return send(Streaming, Event{
							Usage: &copy,
							CostUSD: provider.EstimateCost(
								call.pricing,
								copy,
							),
							CostKnown: provider.PricingKnown(
								call.pricing,
								copy,
							),
							Sample: call.index, Provider: call.provider,
							Model: call.model, SampleContext: call.context,
						})
					}
					return send(Streaming, Event{
						Text: projected.Text, Block: projected.Block,
						Search: projected.Search, Citation: projected.Citation,
						Plan: projected.Plan,
					})
				},
			},
			providerassembly.TransportLifecycle{
				Activate: e.setActiveCancel,
				Clear:    e.clearActiveCancel,
				Begin: func(callCtx context.Context) (
					context.Context,
					func(error),
				) {
					span := e.tracer().Start(
						trace.NameModelCall,
						0,
						map[string]any{
							"provider": call.provider,
							"model":    call.model,
							"sample":   call.index,
							"attempt":  attempt + 1,
						},
					)
					return e.tracer().Context(callCtx, span.ID()), func(err error) {
						if err != nil {
							span.Set("error", errorText(err))
							span.End(trace.StatusError)
							return
						}
						span.End(trace.StatusOK)
					}
				},
			},
		)
		if err != nil && !transport.Opened {
			if errors.Is(err, context.Canceled) && ctx.Err() == nil && e.appendSteering(history) {
				attempt = -1
				continue
			}
			contextChanged, recoveryErr := e.recoverContextOverflow(
				err,
				false,
				history,
				snapshot,
				maxOutputTokens,
				send,
			)
			if recoveryErr != nil {
				return nil, nil, totalUsage, lastEstimate, recoveryErr
			}
			retry, retryable := e.providerRetry(
				err,
				false,
				providerRetries,
				contextChanged,
			)
			if retryable && ctx.Err() == nil {
				if sendErr := send(CallingModel, Event{
					ProviderRetry: &retry,
				}); sendErr != nil {
					return nil, nil, totalUsage, lastEstimate, sendErr
				}
				if waitErr := waitRetryDelay(
					ctx,
					retry.EffectiveDelay,
				); waitErr != nil {
					return nil, nil, totalUsage, lastEstimate, waitErr
				}
				providerRetries++
				continue
			}
			return nil, nil, totalUsage, lastEstimate, err
		}
		consumed := transport.ConsumeResult
		blocks, calls := consumed.Blocks, consumed.Calls
		usage, meaningful := consumed.Usage, consumed.Meaningful
		replay := consumed.Replay
		totalUsage.Add(usage)
		pending := e.drainPending()
		if ctx.Err() == nil && len(pending) != 0 {
			if pendingInputInjected != nil {
				*pendingInputInjected = true
			}
			pendingBlocks := providerassembly.AppendBlocks(
				continuedBlocks,
				blocks,
			)
			if len(continuedBlocks) != 0 {
				replay = nil
			}
			if len(pendingBlocks) != 0 {
				*history = append(*history, provider.ProducedAssistant(
					route, pendingBlocks, e.turn, replay,
				))
			}
			e.appendPendingInputs(history, pending)
			continuationMessages = nil
			continuedBlocks = nil
			continuations = 0
			attempt = -1
			continue
		}
		var incomplete *providerassembly.IncompleteOutputError
		if errors.As(err, &incomplete) && ctx.Err() == nil {
			if continued != nil {
				*continued = true
			}
			continuedBlocks = providerassembly.AppendBlocks(
				continuedBlocks,
				blocks,
			)
			if len(blocks) != 0 {
				continuationMessages = append(
					continuationMessages,
					provider.ProducedAssistant(
						route, cloneBlocks(blocks), e.turn, nil,
					),
				)
			}
			continuationMessages = append(
				continuationMessages,
				promptcontext.IncompleteOutputFeedback(
					incomplete.Reason,
					incomplete.ToolFragments,
					e.turn,
				),
			)
			continuations++
			attempt = -1
			continue
		}
		if err == nil {
			if len(continuedBlocks) != 0 {
				replay = nil
			}
			completeBlocks := providerassembly.AppendBlocks(
				continuedBlocks,
				blocks,
			)
			if continued != nil {
				text := strings.TrimSpace(
					providerassembly.BlocksText(completeBlocks),
				)
				*continued = strings.HasSuffix(text, ":") || strings.HasSuffix(text, "：")
			}
			if capturedReplay != nil {
				*capturedReplay = replay
			}
			for index := range calls {
				binding, known := catalog.Binding(calls[index].Name)
				entry, _ := catalog.Lookup(calls[index].Name)
				unavailable := known &&
					entry.Descriptor.Visibility == tool.VisibleModel &&
					entry.Descriptor.Availability == tool.AvailabilityUnavailable
				if !known || (!advertised[calls[index].Name] && !unavailable) {

					calls[index].CatalogID = catalog.CatalogID
					calls[index].CatalogGeneration = catalog.Generation
					continue
				}
				calls[index].CatalogID = binding.CatalogID
				calls[index].CatalogGeneration = binding.Generation
				calls[index].CatalogRevision = binding.Revision
				calls[index].CatalogAuthority = binding.Authority
			}
			return completeBlocks, calls, totalUsage, lastEstimate, nil
		}
		contextChanged, recoveryErr := e.recoverContextOverflow(
			err,
			meaningful,
			history,
			snapshot,
			maxOutputTokens,
			send,
		)
		if recoveryErr != nil {
			return nil, nil, totalUsage, lastEstimate, recoveryErr
		}
		retry, retryable := e.providerRetry(
			err,
			meaningful,
			providerRetries,
			contextChanged,
		)
		if !retryable || ctx.Err() != nil {
			if ctx.Err() != nil {
				return blocks, nil, totalUsage, lastEstimate, ctx.Err()
			}
			var problem *protocol.Problem
			if errors.As(err, &problem) {
				return blocks, nil, totalUsage, lastEstimate, err
			}
			return blocks, nil, totalUsage, lastEstimate,
				protocol.NewFault(
					protocol.CodeUnavailable,
					"provider could not complete the model sample: "+
						errorText(err),
					true,
					protocol.FaultMetadata{
						Origin:         protocol.FaultOriginProvider,
						Disposition:    protocol.FaultRetryTurn,
						SideEffects:    protocol.SideEffectUnchanged,
						RecoveryAction: "retry the turn from its durable checkpoint",
					},
					err,
				)
		}
		if sendErr := send(CallingModel, Event{
			ProviderRetry: &retry,
		}); sendErr != nil {
			return nil, nil, totalUsage, lastEstimate, sendErr
		}
		if waitErr := waitRetryDelay(
			ctx,
			retry.EffectiveDelay,
		); waitErr != nil {
			return nil, nil, totalUsage, lastEstimate, waitErr
		}
		providerRetries++
	}
}

func deduplicateCompactionReceipts(
	send func(State, Event) error,
) func(State, Event) error {
	var previous *CompactionReceipt
	return func(state State, event Event) error {
		if state == Compacting && event.Compaction != nil {
			current := observableCompactionReceipt(event.Compaction)
			if previous != nil && reflect.DeepEqual(previous, &current) {
				return nil
			}
			previous = &current
		}
		return send(state, event)
	}
}

func observableCompactionReceipt(receipt *CompactionReceipt) CompactionReceipt {
	value := *receipt
	value.OriginalMessages = 0
	value.OriginalTokens = 0
	value.RetainedTokens = 0
	value.SummaryOriginalBytes = 0
	value.SummaryRetainedBytes = 0
	value.TruncationReason = ""
	value.ContextReceipts = nil
	value.WorkingSet = nil
	value.CriticalPaths = nil
	return value
}

func (e *Engine) emitExtensionLifecycleChanges(
	plan runtimeextension.Plan,
	send func(State, Event) error,
) error {
	scope := e.executionScope()
	if scope == nil {
		return errors.New("turn scope is not active")
	}
	scope.mu.Lock()
	if scope.state.extensionsProjected {
		scope.mu.Unlock()
		return nil
	}
	scope.state.extensionsProjected = true
	scope.mu.Unlock()
	for _, change := range runtimeextension.ProjectLifecycle(plan) {
		value := change
		if err := send(CallingModel, Event{
			ExtensionLifecycle: &value,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) emitMCPHealthChanges(
	current []MCPHealthSnapshot,
	send func(State, Event) error,
) error {
	scope := e.executionScope()
	if scope == nil {
		return errors.New("turn scope is not active")
	}
	scope.mu.Lock()
	if scope.state.mcpProjected {
		scope.mu.Unlock()
		return nil
	}
	scope.state.mcpProjected = true
	scope.mu.Unlock()
	for _, change := range mcp.ProjectHealth(current) {
		value := change
		if err := send(CallingModel, Event{
			MCPHealthChanged: &value,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *Engine) scopeCatalog(scope *Scope) tool.CatalogSnapshot {
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.state.catalog.Digest != "" {
		return scope.state.catalog
	}
	return scope.spec.Catalog
}

func (e *Engine) refreshScopeCatalog() error {
	current, err := e.options.Tools.Snapshot()
	if err != nil {
		return err
	}
	scope := e.runningScope()
	if scope == nil {
		return errors.New("turn scope is not active")
	}
	scope.mu.Lock()
	scope.state.catalog = current
	scope.mu.Unlock()
	return nil
}

func (e *Engine) catalogChange(current tool.CatalogSnapshot) *CatalogChanged {
	scope := e.executionScope()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	previous := scope.state.catalogProjected
	if previous.Digest == current.Digest {
		return nil
	}
	scope.state.catalogProjected = current
	diff := tool.DiffCatalog(previous, current)
	changed := &CatalogChanged{
		CatalogID: current.CatalogID, Generation: current.Generation, Digest: current.Digest,
		Added: diff.Added, Replaced: diff.Replaced, Revoked: diff.Revoked,
	}
	return changed
}

// sample attributes one provider call and its usage.
type sample struct {
	index    uint32
	provider string
	model    string
	pricing  model.Pricing
	context  *protocol.SampleContextData
	observe  func(*protocol.SampleContextData, uint64, uint64)
}

func (e *Engine) toolDefinitionsFromSnapshot(
	snapshot tool.CatalogSnapshot,
	request TurnRequest,
) ([]provider.ToolDefinition, map[string]bool, error) {
	return toolsearch.ProjectDefinitions(toolsearch.ProjectionRequest{
		Catalog: snapshot, Prompt: request.Prompt, Intent: request.Intent,
		MaxDefinitions: e.options.MaxToolDefinitions,
		MaxSchemaBytes: e.options.MaxToolSchemaBytes,
		Enabled:        e.toolEnabled,
	})
}

func (e *Engine) recordSampledTools(
	scope *Scope,
	catalog tool.CatalogSnapshot,
	definitions []provider.ToolDefinition,
) {
	if scope == nil {
		return
	}
	advertised := make(map[string]bool, len(definitions))
	for _, definition := range definitions {
		advertised[definition.Name] = true
	}
	scope.mu.Lock()
	scope.state.sampledCatalog = catalog
	scope.state.sampledTools = advertised
	scope.mu.Unlock()
}

// maxOutputFor clamps the session ceiling to the active route.
func (e *Engine) maxOutputFor(route model.ReadyRoute) uint64 {
	modelLimit := route.Model().Limits.MaxOutputTokens
	if e.options.MaxOutputTokens == 0 {
		return min(modelLimit, 16_384)
	}
	if e.options.MaxOutputTokens > modelLimit {
		return modelLimit
	}
	return e.options.MaxOutputTokens
}
