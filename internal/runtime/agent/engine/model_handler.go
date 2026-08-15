package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/toolsearch"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
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
	finishMode bool,
	continued *bool,
	pendingInputInjected *bool,
	capturedReplay **provider.ReplayState,
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
	scope := e.runningScope()
	if scope == nil {
		return nil, nil, provider.Usage{}, 0, errors.New("turn scope is not active")
	}
	admittedHistory, err := e.admitToolResultHistory(*history)
	if err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	*history = admittedHistory
	if err := e.emitExtensionLifecycleChanges(scope.spec.Extensions, send); err != nil {
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
		scope.state.contextLedger = contextstore.New(contextstore.Input{
			Stable: stableContext, History: *history, Definitions: definitions,
		})
	}
	contextLedger := scope.state.contextLedger
	scope.mu.Unlock()
	var totalUsage provider.Usage
	var lastEstimate uint64
	var providerAttempt uint32
	var continuationMessages []provider.Message
	var continuedBlocks []provider.ContentBlock
	finishAttempted, continuations := finishMode, 0
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
		if finishMode {
			requestTools = nil
			reasoningEffort = "low"
			nativeSearch = false
		}
		sampleReason := promptcontext.SampleReason(
			reason, attempt, finishMode || continuations > 0,
		)
		project := func() contextstore.Snapshot {
			return contextLedger.Project(contextstore.Projection{
				Stable: stableContext, History: *history, Dynamic: turnContext,
				Continuation: continuationMessages, Definitions: requestTools,
			})
		}
		snapshot := project()
		phase := CompactionPhaseMidTurn
		if turnUsage.InputTokens+totalUsage.InputTokens == 0 {
			phase = CompactionPhasePreSampling
		}
		window, err := e.runCompactGate(
			history, snapshot, promptcontext.OutputLimit(
				e.maxOutputFor(route), reasoningEffort, finishMode || budgetFinishOnly,
			), phase, true, send,
		)
		if err != nil {
			return nil, nil, totalUsage, window.estimated, err
		}
		snapshot = project()
		if window.hardLimit > 0 && window.active >= window.hardLimit*55/100 {
			turnContext = append(turnContext, contextWindowFeedback(e.turn))
			snapshot = project()
			window, err = e.runCompactGate(
				history, snapshot, promptcontext.OutputLimit(
					e.maxOutputFor(route), reasoningEffort, finishMode || budgetFinishOnly,
				), phase, true, send,
			)
			if err != nil {
				return nil, nil, totalUsage, window.estimated, err
			}
		}
		finishOnly := finishMode || budgetFinishOnly ||
			window.hardLimit > 0 && window.active >= window.hardLimit*85/100
		if finishOnly && !finishMode {
			research := scope.spec.Request.Intent == protocol.TurnIntentAnswer || scope.spec.Request.Intent == protocol.TurnIntentPlan
			requestTools = slices.DeleteFunc(
				append([]provider.ToolDefinition(nil), requestTools...),
				func(tool provider.ToolDefinition) bool {
					return research || tool.Name != "turn_complete" && !strings.HasPrefix(tool.Name, "quality_")
				})
			reasoningEffort, nativeSearch = "low", false
		}
		maxOutputTokens := promptcontext.OutputLimit(
			e.maxOutputFor(route), reasoningEffort, finishOnly,
		)
		snapshot = project()
		snapshot, normalization, normalizationErr := snapshot.Normalize(
			route.Model().Capabilities,
		)
		if normalizationErr != nil {
			return nil, nil, totalUsage, lastEstimate,
				fmt.Errorf("normalize context projection: %w", normalizationErr)
		}
		requestTools = snapshot.Definitions()
		attribution, attributionErr := snapshot.Measure(
			sampleReason, reasoningEffort, e.options.TokenEstimator,
		)
		if attributionErr != nil {
			return nil, nil, totalUsage, lastEstimate, attributionErr
		}
		if attribution.MaxItemTokens > maxModelVisibleItemTokens {
			return nil, nil, totalUsage, attribution.EstimatedTokens,
				protocol.NewProblem(
					protocol.CodeResourceExhausted,
					fmt.Sprintf(
						"normalized context item requires %d tokens; limit is %d",
						attribution.MaxItemTokens,
						maxModelVisibleItemTokens,
					),
					false,
					nil,
				)
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
		windowProjection := e.prepareTokenWindow(&attribution, maxOutputTokens)
		if _, err := e.checkBudget(
			windowProjection.FullActiveTokens, turnUsage, totalUsage, maxOutputTokens,
		); err != nil {
			return nil, nil, totalUsage, lastEstimate, err
		}
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
		requestContext, cancel := context.WithCancelCause(ctx)
		e.setActiveCancel(cancel)
		call := sample{
			index: e.nextSample(), provider: route.ProviderID(),
			model: route.Model().ID, pricing: route.Model().Pricing, context: &attribution,
			observe: e.observeTokenWindow,
		}
		callSpan := e.tracer().Start(trace.NameModelCall, 0, map[string]any{
			"provider": call.provider, "model": call.model,
			"sample": call.index, "attempt": attempt + 1,
		})
		stream, err := e.options.Provider.Stream(requestContext, provider.ModelRequest{
			Route: route, Messages: messages,
			MaxOutputTokens: maxOutputTokens, Tools: requestTools,
			ReasoningEffort: reasoningEffort, NativeSearch: nativeSearch,
			Idempotent:     true,
			PromptCacheKey: provider.StickyPromptCacheKey(e.options.PromptCacheKey, route),
		})
		if err != nil {
			e.clearActiveCancel()
			cancel(nil)
			callSpan.Set("error", errorText(err))
			callSpan.End(trace.StatusError)
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
				providerRetries++
				continue
			}
			return nil, nil, totalUsage, lastEstimate, err
		}
		var replay *provider.ReplayState
		blocks, calls, usage, meaningful, err := consume(
			stream, call, func(event Event) error {
				return send(Streaming, event)
			},
			e.tracer().NoteFirstOutput,
			&replay,
		)
		e.clearActiveCancel()
		cancel(nil)
		if err != nil {
			callSpan.Set("error", errorText(err))
			callSpan.End(trace.StatusError)
		} else {
			callSpan.End(trace.StatusOK)
		}
		totalUsage.Add(usage)
		pending := e.drainPending()
		if ctx.Err() == nil && len(pending) != 0 {
			if pendingInputInjected != nil {
				*pendingInputInjected = true
			}
			pendingBlocks := appendContinuedBlocks(continuedBlocks, blocks)
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
			finishAttempted = false
			finishMode = false
			attempt = -1
			continue
		}
		var incomplete *incompleteModelOutputError
		if errors.As(err, &incomplete) && ctx.Err() == nil {
			if continued != nil {
				*continued = true
			}
			continuedBlocks = appendContinuedBlocks(continuedBlocks, blocks)
			if finishMode {
				return continuedBlocks, nil, totalUsage, lastEstimate, protocol.NewProblem(
					protocol.CodeResourceExhausted,
					"model finish route remained incomplete after one bounded attempt",
					true,
					err,
				)
			}
			if incomplete.Reason == provider.StopReasonMaxTokens &&
				!incomplete.HasToolCallFragment &&
				reasoningOnlyBlocks(continuedBlocks) &&
				!finishAttempted && continuations > 0 {
				if len(blocks) != 0 {
					continuationMessages = append(
						continuationMessages,
						provider.ProducedAssistant(
							route, cloneBlocks(blocks), e.turn, nil,
						),
					)
				}
				continuationMessages = append(continuationMessages, finishOutputFeedback(e.turn))
				finishAttempted, finishMode, attempt = true, true, -1
				continue
			}
			if continuations >= maxOutputContinuations {
				return continuedBlocks, nil, totalUsage, lastEstimate, protocol.NewProblem(
					protocol.CodeResourceExhausted,
					fmt.Sprintf(
						"model output remained incomplete after %d continuation attempts (%s)",
						maxOutputContinuations,
						incomplete.Reason,
					),
					true,
					err,
				)
			}
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
				incompleteOutputFeedback(incomplete.Reason, e.turn),
			)
			continuations++
			attempt = -1
			continue
		}
		if err == nil {
			if len(continuedBlocks) != 0 {
				replay = nil
			}
			if capturedReplay != nil {
				*capturedReplay = replay
			}
			completeBlocks := appendContinuedBlocks(continuedBlocks, blocks)
			if continued != nil {
				text := strings.TrimSpace(blocksText(completeBlocks))
				*continued = strings.HasSuffix(text, ":") || strings.HasSuffix(text, "：")
			}
			if finishOnly && len(calls) != 0 {
				return continuedBlocks, nil, totalUsage, lastEstimate, protocol.NewProblem(
					protocol.CodeConflict,
					"model finish route attempted a new tool call",
					false,
					nil,
				)
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
			return blocks, nil, totalUsage, lastEstimate, err
		}
		if sendErr := send(CallingModel, Event{
			ProviderRetry: &retry,
		}); sendErr != nil {
			return nil, nil, totalUsage, lastEstimate, sendErr
		}
		providerRetries++
	}
}

const maxOutputContinuations = 2

type incompleteModelOutputError struct {
	Reason              provider.StopReason
	HasToolCallFragment bool
}

func (e *incompleteModelOutputError) Error() string {
	return fmt.Sprintf("model output stopped before completion (%s)", e.Reason)
}

func incompleteOutputFeedback(
	reason provider.StopReason,
	turn uint64,
) provider.Message {
	message := provider.TextMessage(provider.RoleUser, fmt.Sprintf(
		`[continue_after_incomplete stop_reason=%s]
The provider stopped the previous response before completion. Continue exactly
from the captured response. Do not repeat completed content. Finish the pending
tool call or user-facing answer.`,
		reason,
	))
	message.Turn = turn
	return message
}

func finishOutputFeedback(turn uint64) provider.Message {
	message := provider.TextMessage(provider.RoleUser, `[finish_after_reasoning_limit]
The reasoning phase reached its output limit. Do not call tools or start new
analysis. Produce one concise user-facing final answer from the evidence already
present. If the requested operation is not complete, report that blocked outcome
and its structured failure instead of claiming success.`)
	message.Turn = turn
	return message
}

func reasoningOnlyBlocks(blocks []provider.ContentBlock) bool {
	meaningful := false
	for _, block := range blocks {
		if block.Type != provider.ContentReasoning {
			return false
		}
		if block.Text != "" {
			meaningful = true
		}
	}
	return meaningful
}

func appendContinuedBlocks(
	current []provider.ContentBlock,
	next []provider.ContentBlock,
) []provider.ContentBlock {
	for _, block := range cloneBlocks(next) {
		current = appendStreamBlock(current, -1, block)
	}
	return current
}

func (e *Engine) emitExtensionLifecycleChanges(
	current []ExtensionSnapshot,
	send func(State, Event) error,
) error {
	sort.Slice(current, func(i, j int) bool {
		if current[i].Kind != current[j].Kind {
			return current[i].Kind < current[j].Kind
		}
		return current[i].Name < current[j].Name
	})
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
	for _, snapshot := range current {
		if snapshot.Kind == "" || snapshot.Name == "" {
			continue
		}
		action := "active"
		if !snapshot.Enabled {
			action = "disabled"
		}
		if err := send(CallingModel, Event{
			ExtensionLifecycle: &ExtensionLifecycleChanged{
				Action: action, Current: snapshot,
			},
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
	sort.Slice(current, func(i, j int) bool { return current[i].Server < current[j].Server })
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
	for _, snapshot := range current {
		if snapshot.Server == "" {
			continue
		}
		if err := send(CallingModel, Event{
			MCPHealthChanged: &MCPHealthChanged{Current: snapshot},
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

func consume(
	stream provider.Stream,
	call sample,
	emit func(Event) error,
	firstOutput func(),
	capturedReplay **provider.ReplayState,
) ([]provider.ContentBlock, []provider.ToolCall, provider.Usage, bool, error) {
	stream = newDeltaCoalescingStream(stream)
	defer stream.Close()
	var blocks []provider.ContentBlock
	var usage provider.Usage
	var replay *provider.ReplayState
	fragments := make(map[int]provider.ToolCall)
	meaningful := false

	output := func() {
		meaningful = true
		if firstOutput != nil {
			firstOutput()
		}
	}
	var planParser ProposedPlanParser
	for {
		event, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return blocks, nil, usage, meaningful, protocol.NewProblem(
				protocol.CodeUnavailable,
				"model stream ended without a valid stop event",
				true,
				io.ErrUnexpectedEOF,
			)
		}
		if err != nil {
			return blocks, nil, usage, meaningful, err
		}
		switch event.Type {
		case provider.EventMessageStart:
		case provider.EventTextDelta:
			output()
			block := eventBlock(event, provider.ContentText)
			blocks = appendStreamBlock(blocks, event.Index, block)
			visible := block
			visible.Text = event.Text
			if err := emit(Event{Text: event.Text, Block: &visible}); err != nil {
				return nil, nil, usage, meaningful, err
			}
			for _, update := range planParser.Feed(event.Text) {
				copy := update
				if err := emit(Event{Plan: &copy}); err != nil {
					return nil, nil, usage, meaningful, err
				}
			}
		case provider.EventReasoningDelta:
			output()
			block := eventBlock(event, provider.ContentReasoning)
			blocks = appendStreamBlock(blocks, event.Index, block)
			visible := block
			visible.Text = event.Text
			if visible.Text == "" {
				continue
			}
			if err := emit(Event{Text: event.Text, Block: &visible}); err != nil {
				return nil, nil, usage, meaningful, err
			}
		case provider.EventReasoningSignature:
			return nil, nil, usage, meaningful, errors.New(
				"provider signature was not captured as replay state",
			)
		case provider.EventSearchResult, provider.EventCitation:
			output()
			block := eventBlock(event, "")
			blocks = append(blocks, block)
			engineEvent := Event{Block: &block, Search: event.Search, Citation: event.Citation}
			if err := emit(engineEvent); err != nil {
				return nil, nil, usage, meaningful, err
			}
		case provider.EventUsage:
			usage.Add(*event.Usage)
			contextstore.ApplyTransport(call.context, event.Usage.Transport)
			copy := usage
			if call.observe != nil {
				call.observe(call.context, copy.InputTokens, copy.CachedTokens)
			}

			cost := estimateCost(call.pricing, copy)
			if err := emit(Event{
				Usage: &copy, CostUSD: cost, CostKnown: pricingKnown(call.pricing, copy),
				Sample: call.index, Provider: call.provider, Model: call.model,
				SampleContext: call.context,
			}); err != nil {
				return nil, nil, usage, meaningful, err
			}
		case provider.EventReplayState:
			if replay != nil || event.Replay == nil {
				return nil, nil, usage, meaningful, errors.New(
					"provider emitted duplicate or empty replay state",
				)
			}
			copy := *event.Replay
			copy.Data = append(json.RawMessage(nil), event.Replay.Data...)
			replay = &copy
		case provider.EventToolCallDelta:
			output()
			call := fragments[event.ToolCall.Index]
			if event.ToolCall.ID != "" {
				call.ID = event.ToolCall.ID
			}
			if event.ToolCall.Name != "" {
				call.Name = event.ToolCall.Name
			}
			call.Arguments += event.ToolCall.Arguments
			fragments[event.ToolCall.Index] = call
		case provider.EventMessageStop:
			if capturedReplay != nil {
				*capturedReplay = replay
			}
			switch event.StopReason {
			case provider.StopReasonMaxTokens, provider.StopReasonIncomplete:
				return blocks, nil, usage, meaningful, &incompleteModelOutputError{
					Reason:              event.StopReason,
					HasToolCallFragment: len(fragments) != 0,
				}
			case provider.StopReasonContentFilter:
				return blocks, nil, usage, meaningful, protocol.NewProblem(
					protocol.CodeInvalidArgument,
					"model output was blocked by the provider content filter",
					false,
					nil,
				)
			}
			if event.StopReason == provider.StopReasonUnknown {
				return blocks, nil, usage, meaningful, protocol.NewProblem(
					protocol.CodeUnavailable,
					"provider returned an unknown model stop reason",
					true,
					nil,
				)
			}
			if event.StopReason == provider.StopReasonToolUse && len(fragments) == 0 {
				return blocks, nil, usage, meaningful, protocol.NewProblem(
					protocol.CodeUnavailable,
					"provider stopped for tool use without emitting a tool call",
					true,
					nil,
				)
			}
			indexes := make([]int, 0, len(fragments))
			for index := range fragments {
				indexes = append(indexes, index)
			}
			sort.Ints(indexes)
			calls := make([]provider.ToolCall, 0, len(indexes))
			for _, index := range indexes {
				call := fragments[index]
				if call.ID == "" {
					call.ID = fmt.Sprintf("call_%d", index)
				}
				calls = append(calls, call)
			}
			return blocks, calls, usage, meaningful, nil
		default:
			return nil, nil, usage, meaningful, errors.New("unknown provider event")
		}
	}
}

const (
	commonToolSet  = ",tool_search,result_get,handle_read,request_user_input,update_plan,turn_complete,"
	readToolSet    = ",search_text,search_files,search_definition,search_references,file_read,file_list,file_write,file_edit,file_apply,shell_read,shell_run,quality_test,project_map,"
	writeToolSet   = ",search_related_tests,quality_diagnostics,quality_verify,"
	operateToolSet = ",terminal_run,background_shell_start,background_shell_wait," +
		"background_shell_interact,background_shell_cancel,"
	maxRelevantTools = 4
)

func (e *Engine) toolDefinitionsFromSnapshot(
	snapshot tool.CatalogSnapshot,
	request TurnRequest,
) ([]provider.ToolDefinition, map[string]bool, error) {
	var descriptors []tool.Descriptor
	var entries []tool.CatalogEntrySnapshot
	for _, entry := range snapshot.Entries() {
		if entry.Descriptor.Visibility == tool.VisibleModel &&
			entry.Descriptor.Availability != tool.AvailabilityUnavailable &&
			e.toolEnabled(entry) {
			entries = append(entries, entry)
			descriptors = append(descriptors, entry.Descriptor)
		}
	}
	if onlyRetrievalHelpers(descriptors) {
		return nil, map[string]bool{}, nil
	}
	selected := make(map[string]bool)
	var search *tool.CatalogEntrySnapshot
	relevant := 0
	for index := range entries {
		entry := entries[index]
		if entry.Descriptor.Name == toolsearch.ToolName {
			search = &entry
			continue
		}
		if entry.State == tool.CatalogEntryDeferred {
			continue
		}
		if coreTool(request.Intent, entry.Name) || entry.State == tool.CatalogEntryMaterialized ||
			entry.Name == "image_analyze" && strings.Contains(strings.ToLower(request.Prompt), "screenshot") {
			selected[entry.Name] = true
			continue
		}
		if relevant < maxRelevantTools &&
			toolsearch.ScoreDescriptor(entry.Descriptor, request.Prompt) > 0 {
			selected[entry.Name] = true
			relevant++
		}
	}
	if search == nil {
		for _, entry := range entries {
			if entry.State != tool.CatalogEntryDeferred {
				selected[entry.Name] = true
			}
		}
	} else if len(selected) < len(entries)-1 {
		selected[toolsearch.ToolName] = true
	}
	result := make([]provider.ToolDefinition, 0, len(descriptors))
	advertised := make(map[string]bool)
	schemaBytes := 0
	add := func(entry tool.CatalogEntrySnapshot, required bool) error {
		descriptor := entry.Descriptor
		data, _ := json.Marshal(descriptor.InputSchema)
		if len(result)+1 > e.options.MaxToolDefinitions ||
			schemaBytes+len(data) > e.options.MaxToolSchemaBytes {
			if required {
				return fmt.Errorf(
					"%w: provider tools[] cannot fit required tool %q",
					tool.ErrCatalogLimit, descriptor.Name,
				)
			}
			return nil
		}
		result = append(result, provider.ToolDefinition{
			Name: descriptor.Name, Description: descriptor.Description,
			InputSchema: descriptor.InputSchema,
		})
		advertised[descriptor.Name] = true
		schemaBytes += len(data)
		return nil
	}
	if search != nil && selected[toolsearch.ToolName] {
		if err := add(*search, true); err != nil {
			return nil, nil, err
		}
	}
	for _, entry := range entries {
		if !selected[entry.Name] || entry.Name == toolsearch.ToolName {
			continue
		}
		required := coreTool(request.Intent, entry.Name) ||
			entry.State == tool.CatalogEntryMaterialized
		if err := add(entry, required); err != nil {
			return nil, nil, err
		}
	}
	return result, advertised, nil
}

func coreTool(intent protocol.TurnIntent, name string) bool {
	in := func(set string) bool { return strings.Contains(set, ","+name+",") }
	if in(commonToolSet) || in(readToolSet) {
		return true
	}
	switch protocol.NormalizeTurnIntent(intent) {
	case protocol.TurnIntentWorkspaceChange:
		return in(writeToolSet)
	case protocol.TurnIntentOperation:
		return in(writeToolSet) || in(operateToolSet)
	default:
		return false
	}
}

func onlyRetrievalHelpers(descriptors []tool.Descriptor) bool {
	if len(descriptors) == 0 {
		return false
	}
	for _, descriptor := range descriptors {
		switch descriptor.Name {
		case "result_get", "handle_read":
		default:
			return false
		}
	}
	return true
}

// maxOutputFor clamps the session ceiling to the active route.
func (e *Engine) maxOutputFor(route model.ReadyRoute) uint64 {
	limit := route.Model().Limits.MaxOutputTokens
	if limit == 0 || e.options.MaxOutputTokens <= limit {
		return e.options.MaxOutputTokens
	}
	return limit
}

func eventBlock(event provider.StreamEvent, fallback provider.ContentType) provider.ContentBlock {
	if event.Block != nil {
		return cloneBlocks([]provider.ContentBlock{*event.Block})[0]
	}
	switch event.Type {
	case provider.EventTextDelta:
		return provider.ContentBlock{Type: provider.ContentText, Text: event.Text}
	case provider.EventReasoningDelta:
		return provider.ContentBlock{Type: provider.ContentReasoning, Text: event.Text}
	case provider.EventSearchResult:
		return provider.ContentBlock{Type: provider.ContentSearch, Search: event.Search}
	case provider.EventCitation:
		return provider.ContentBlock{Type: provider.ContentCitation, Citation: event.Citation}
	default:
		return provider.ContentBlock{Type: fallback, Text: event.Text}
	}
}

func appendStreamBlock(
	blocks []provider.ContentBlock,
	_ int,
	block provider.ContentBlock,
) []provider.ContentBlock {
	if len(blocks) != 0 && block.Type == blocks[len(blocks)-1].Type {
		last := &blocks[len(blocks)-1]
		if block.Type == provider.ContentText {
			last.Text += block.Text
			return blocks
		}
		if block.Type == provider.ContentReasoning &&
			(last.ID == "" || block.ID == "" || last.ID == block.ID) {
			switch {
			case last.Text == "":
				last.Text = block.Text
			case block.Text == "":

			case strings.Contains(block.Text, last.Text) && len(block.Text) >= len(last.Text):
				last.Text = block.Text
			case strings.Contains(last.Text, block.Text):

			default:
				last.Text += block.Text
			}
			if last.ID == "" {
				last.ID = block.ID
			}
			return blocks
		}
	}
	return append(blocks, block)
}

func blocksText(blocks []provider.ContentBlock) string {
	var result string
	for _, block := range blocks {
		if block.Type == provider.ContentText {
			result += block.Text
		}
	}
	return result
}

func blocksReasoning(blocks []provider.ContentBlock) string {
	var result string
	for _, block := range blocks {
		if block.Type == provider.ContentReasoning {
			result += block.Text
		}
	}
	return result
}

func messageToolCalls(message provider.Message) []provider.ToolCall {
	var calls []provider.ToolCall
	for _, block := range message.Blocks {
		if block.Type == provider.ContentToolCall && block.ToolCall != nil {
			calls = append(calls, *block.ToolCall)
		}
	}
	return calls
}

func messageToolResultID(message provider.Message) string {
	for _, block := range message.Blocks {
		if block.Type == provider.ContentToolResult && block.ToolResult != nil {
			return block.ToolResult.CallID
		}
	}
	return ""
}
