package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"syscall"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/toolsearch"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (e *Engine) modelStep(
	ctx context.Context,
	history *[]provider.Message,
	turnUsage provider.Usage,
	continued *bool,
	pendingInputInjected *bool,
	send func(State, Event) error,
) ([]provider.ContentBlock, []provider.ToolCall, provider.Usage, uint64, error) {
	if continued != nil {
		*continued = false
	}
	if pendingInputInjected != nil {
		*pendingInputInjected = false
	}
	scope := e.runningScope()
	if scope == nil {
		return nil, nil, provider.Usage{}, 0, errors.New("turn scope is not active")
	}
	if err := e.emitExtensionLifecycleChanges(scope.spec.Extensions, send); err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	if err := e.emitMCPHealthChanges(scope.spec.MCP, send); err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	catalog := scope.spec.Catalog
	if changed := e.catalogChange(catalog); changed != nil {
		if err := send(CallingModel, Event{CatalogChanged: changed}); err != nil {
			return nil, nil, provider.Usage{}, 0, err
		}
	}
	definitions, advertised, err := e.toolDefinitionsFromSnapshot(catalog)
	if err != nil {
		return nil, nil, provider.Usage{}, 0, err
	}
	var totalUsage provider.Usage
	var lastEstimate uint64
	var continuationMessages []provider.Message
	var continuedBlocks []provider.ContentBlock
	continuations := 0
	finishAttempted := false
	finishMode := false
	for attempt := 0; ; attempt++ {
		messages := append(
			cloneMessages(scope.spec.Context.Messages),
			cloneMessages(*history)...,
		)
		turnContext, turnReceipts := e.turnContextMessagesForCatalog(ctx, catalog, advertised)
		messages = append(messages, turnContext...)
		messages = append(messages, cloneMessages(continuationMessages)...)
		e.recordTurnContextReceipts(turnReceipts)

		estimatedInput, err := e.checkBudget(messages, turnUsage, totalUsage)
		if err != nil {
			return nil, nil, totalUsage, estimatedInput, err
		}
		e.maybeInjectBudgetReminder(&messages)
		lastEstimate = estimatedInput
		requestContext, cancel := context.WithCancelCause(ctx)
		e.setActiveCancel(cancel)

		route := e.activeRoute()
		call := sample{
			index: e.nextSample(), provider: route.ProviderID(),
			model: route.Model().ID, pricing: route.Model().Pricing,
		}

		callSpan := e.tracer().Start(trace.NameModelCall, 0, map[string]any{
			"provider": call.provider, "model": call.model,
			"sample": call.index, "attempt": attempt + 1,
		})
		maxOutputTokens := e.maxOutputFor(route)
		requestTools := definitions
		reasoningEffort := e.options.ReasoningEffort
		nativeSearch := e.options.NativeSearch
		if finishMode {
			maxOutputTokens = min(maxOutputTokens, uint64(4096))
			requestTools = nil
			reasoningEffort = "low"
			nativeSearch = false
		}
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
			if attempt < providerRetryLimit(e.options.MaxRetries, err) &&
				ctx.Err() == nil {
				if sendErr := send(CallingModel, Event{
					ProviderRetry: providerRetryEvent(attempt+1, err),
				}); sendErr != nil {
					return nil, nil, totalUsage, lastEstimate, sendErr
				}
				continue
			}
			return nil, nil, totalUsage, lastEstimate, err
		}
		blocks, calls, usage, meaningful, err := consume(
			stream, call, func(event Event) error {
				return send(Streaming, event)
			},
			e.tracer().NoteFirstOutput,
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
			if len(pendingBlocks) != 0 {
				*history = append(*history, provider.Message{
					Role: provider.RoleAssistant, Blocks: pendingBlocks, Turn: e.turn,
				})
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
				!finishAttempted {
				if len(blocks) != 0 {
					continuationMessages = append(continuationMessages, provider.Message{
						Role: provider.RoleAssistant, Blocks: cloneBlocks(blocks), Turn: e.turn,
					})
				}
				continuationMessages = append(
					continuationMessages,
					finishOutputFeedback(e.turn),
				)
				finishAttempted = true
				finishMode = true
				attempt = -1
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
				continuationMessages = append(continuationMessages, provider.Message{
					Role: provider.RoleAssistant, Blocks: cloneBlocks(blocks), Turn: e.turn,
				})
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
			if finishMode && len(calls) != 0 {
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
			return appendContinuedBlocks(continuedBlocks, blocks),
				calls, totalUsage, lastEstimate, nil
		}
		if meaningful ||
			attempt >= providerRetryLimit(e.options.MaxRetries, err) ||
			ctx.Err() != nil {
			return blocks, nil, totalUsage, lastEstimate, err
		}
		if sendErr := send(CallingModel, Event{
			ProviderRetry: providerRetryEvent(attempt+1, err),
		}); sendErr != nil {
			return nil, nil, totalUsage, lastEstimate, sendErr
		}
	}
}

func providerRetryEvent(attempt int, err error) *ProviderRetry {
	category := "provider_unavailable"
	switch {
	case errors.Is(err, syscall.ECONNRESET):
		category = "connection_reset"
	case errors.Is(err, syscall.EPIPE):
		category = "broken_pipe"
	case errors.Is(err, io.ErrUnexpectedEOF):
		category = "unexpected_eof"
	case errors.Is(err, context.DeadlineExceeded):
		category = "deadline_exceeded"
	}
	return &ProviderRetry{
		Attempt: attempt, Code: protocol.CodeOf(err), Category: category,
	}
}

func providerRetryLimit(configured int, err error) int {
	if configured < 1 && protocol.IsRetryable(err) {
		return 1
	}
	return configured
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
		if block.Text != "" || block.Signature != "" || len(block.ProviderData) != 0 {
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

func (e *Engine) catalogChange(current tool.CatalogSnapshot) *CatalogChanged {
	scope := e.executionScope()
	if scope == nil {
		return nil
	}
	scope.mu.Lock()
	defer scope.mu.Unlock()
	if scope.state.catalogProjected {
		return nil
	}
	scope.state.catalogProjected = true
	changed := &CatalogChanged{
		CatalogID: current.CatalogID, Generation: current.Generation, Digest: current.Digest,
	}
	for _, entry := range current.Entries() {
		changed.Added = append(changed.Added, tool.CatalogChange{
			Name: entry.Name, Source: entry.Source, Revision: entry.Revision,
		})
	}
	return changed
}

// sample names one provider call within a turn: which call it is, who answered
// it, and what its tokens cost. It travels with every usage report so a
// consumer can tell a second report about this call from the first report about
// the next one.
type sample struct {
	index    uint32
	provider string
	model    string
	pricing  model.Pricing
}

func consume(
	stream provider.Stream,
	call sample,
	emit func(Event) error,
	firstOutput func(),
) ([]provider.ContentBlock, []provider.ToolCall, provider.Usage, bool, error) {
	stream = newDeltaCoalescingStream(stream)
	defer stream.Close()
	var blocks []provider.ContentBlock
	var usage provider.Usage
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
			if visible.Text == "" && visible.Signature == "" {
				continue
			}
			if err := emit(Event{Text: event.Text, Block: &visible}); err != nil {
				return nil, nil, usage, meaningful, err
			}
		case provider.EventReasoningSignature:
			output()
			block := eventBlock(event, provider.ContentReasoning)
			blocks = appendStreamBlock(blocks, event.Index, block)
			if err := emit(Event{Block: &block}); err != nil {
				return nil, nil, usage, meaningful, err
			}
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
			copy := usage

			cost := estimateCost(call.pricing, copy)
			if err := emit(Event{
				Usage: &copy, CostUSD: cost, CostKnown: call.pricing.Known,
				Sample: call.index, Provider: call.provider, Model: call.model,
			}); err != nil {
				return nil, nil, usage, meaningful, err
			}
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

func (e *Engine) toolDefinitionsFromSnapshot(
	snapshot tool.CatalogSnapshot,
) ([]provider.ToolDefinition, map[string]bool, error) {
	var descriptors []tool.Descriptor
	for _, entry := range snapshot.Entries() {
		if entry.Descriptor.Visibility == tool.VisibleModel &&
			entry.Descriptor.Availability != tool.AvailabilityUnavailable &&
			e.toolEnabled(entry) {
			descriptors = append(descriptors, entry.Descriptor)
		}
	}
	if onlyRetrievalHelpers(descriptors) {
		return nil, map[string]bool{}, nil
	}
	threshold := e.options.ToolSearchThreshold
	if threshold <= 0 {
		threshold = toolsearch.DefaultThresh
	}
	useSearch := toolsearch.ShouldEnable(descriptors, threshold)
	for _, entry := range snapshot.Entries() {
		if entry.State == tool.CatalogEntryDeferred {
			useSearch = true
			break
		}
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
	var search *tool.CatalogEntrySnapshot
	for _, entry := range snapshot.Entries() {
		descriptor := entry.Descriptor
		if descriptor.Visibility != tool.VisibleModel ||
			descriptor.Availability == tool.AvailabilityUnavailable ||
			!e.toolEnabled(entry) {
			continue
		}
		if descriptor.Name == toolsearch.ToolName {
			copy := entry
			search = &copy
			continue
		}
		if entry.State != tool.CatalogEntryMaterialized {
			continue
		}
		if err := add(entry, true); err != nil {
			return nil, nil, err
		}
	}
	for _, entry := range snapshot.Entries() {
		descriptor := entry.Descriptor
		if descriptor.Visibility != tool.VisibleModel ||
			descriptor.Availability == tool.AvailabilityUnavailable ||
			!e.toolEnabled(entry) ||
			descriptor.Name == toolsearch.ToolName ||
			entry.State == tool.CatalogEntryDeferred ||
			entry.State == tool.CatalogEntryMaterialized {
			continue
		}

		if err := add(entry, true); err != nil {
			return nil, nil, err
		}
	}
	if search != nil {
		if err := add(*search, useSearch); err != nil {
			return nil, nil, err
		}
	}
	return result, advertised, nil
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

// maxOutputFor is the output ceiling to ask this route for. The configured
// ceiling is a session-level number, so a turn routed to a model with a smaller
// output limit is clamped to it: sending the larger number would be refused by
// the provider, which reads as a routing failure rather than as a ceiling.
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
	case provider.EventReasoningSignature:
		return provider.ContentBlock{Type: provider.ContentReasoning, Signature: event.Signature}
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
			(len(last.ProviderData) == 0 || len(block.ProviderData) == 0 ||
				(last.ID != "" && last.ID == block.ID)) {

			if block.Text == "" && len(block.ProviderData) != 0 {
				var item map[string]any
				if json.Unmarshal(block.ProviderData, &item) == nil {
					block.Text = reasoningTextFromProviderData(item)
				}
			}
			switch {
			case last.Text == "":
				last.Text = block.Text
			case block.Text == "":

			case strings.Contains(block.Text, last.Text) && len(block.Text) >= len(last.Text):
				last.Text = block.Text
			case strings.Contains(last.Text, block.Text):

			case len(last.ProviderData) == 0:
				last.Text += block.Text
			}
			last.Signature += block.Signature
			if len(last.ProviderData) == 0 {
				last.ProviderType = block.ProviderType
				last.ProviderData = append([]byte(nil), block.ProviderData...)
				last.ID = block.ID
			} else if last.ID == "" {
				last.ID = block.ID
			}
			return blocks
		}
	}
	return append(blocks, block)
}

func reasoningTextFromProviderData(item map[string]any) string {
	if item == nil {
		return ""
	}
	for _, key := range []string{"content", "summary"} {
		switch content := item[key].(type) {
		case string:
			if content != "" {
				return content
			}
		case []any:
			var parts []string
			for _, raw := range content {
				part, _ := raw.(map[string]any)
				if part == nil {
					continue
				}
				switch typ, _ := part["type"].(string); typ {
				case "reasoning_text", "output_text", "summary_text", "text", "":
					if text, _ := part["text"].(string); text != "" {
						parts = append(parts, text)
					}
				}
			}
			if joined := strings.Join(parts, ""); joined != "" {
				return joined
			}
		}
	}
	return ""
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

func blocksSignature(blocks []provider.ContentBlock) string {
	var result string
	for _, block := range blocks {
		if block.Type == provider.ContentReasoning {
			result += block.Signature
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
