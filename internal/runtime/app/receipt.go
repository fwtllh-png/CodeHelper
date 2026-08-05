package app

import (
	"strings"
	"time"

	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// receiptRecorder accumulates the per-turn execution receipt from the engine
// event stream. It only records what it observes, so a receipt never claims a
// check that did not run.
type receiptRecorder struct {
	started         time.Time
	goal            string
	plan            string
	mode            string
	posture         string
	sandbox         string
	workspace       string
	toolsSucceeded  []string
	toolsFailed     []string
	approvals       int
	diagnosticCount int
	diagnosticsRan  bool
	usage           protocol.UsageData
	costKnown       bool
	// routes is which model answered for which purpose, in the order the turn
	// used them.
	routes []protocol.ReceiptRoute
	// usageFinal marks that the terminal event supplied the turn-cumulative
	// usage, which supersedes anything accumulated from streaming events.
	usageFinal bool
	// samples holds the last streaming report per provider call, which is what
	// the fallback path sums. It is dropped once a terminal total arrives.
	samples       map[uint32]protocol.UsageData
	issues        []string
	skills        []protocol.ReceiptSkill
	editorContext []protocol.EditorContextReceipt
	// verification is the last gate evaluation of the turn; repair rounds
	// deliberately overwrite earlier ones so the receipt reports the verdict the
	// turn ended on.
	verification *agentengine.VerificationReceipt
	// turn is the turn the observed events belong to, which a caller needs to ask
	// the engine what that turn read.
	turn uint64
}

// turnObservations is what the engine knows at the end of a turn that the event
// stream does not carry.
type turnObservations struct {
	// changes come from the turn-diff tracker: the writes the guard observed.
	changes []agentengine.TurnDiffEntry
	// readPaths are the files the turn read.
	readPaths []string
	// context is the prompt context as it was assembled for the turn.
	context []promptcontext.Receipt
	// catalog is the exact snapshot used by the turn's latest model sample.
	catalog *protocol.ReceiptCatalog
	// evidence is what the session has established and what it has not proved.
	evidence evidence.Snapshot
	// budget is how much of the compaction threshold the history occupies.
	budget *protocol.ReceiptContextBudget
	// conflicts are paths an automatic rollback could not restore, which the turn
	// leaves behind for a human.
	conflicts []string
	// latency is where the turn spent its wall clock, and nil when the engine
	// does not measure it.
	latency *trace.Latency
	// spend is the thread's pool as the engine sees it, before this turn's own
	// usage is folded in.
	spend agentengine.BudgetSnapshot
}

func newReceiptRecorder(goal string) *receiptRecorder {
	return &receiptRecorder{started: time.Now(), goal: goal}
}

// observe folds one engine event into the receipt.
func (r *receiptRecorder) observe(event agentengine.Event) {
	if r == nil {
		return
	}
	switch event.State {
	case agentengine.Preparing:
		r.mode, r.posture = event.Mode, event.Posture
		r.sandbox, r.workspace = event.Sandbox, event.Workspace
	case agentengine.AwaitingApproval:
		if event.Approval != nil {
			r.approvals++
		}
	case agentengine.RunningTools:
		r.observeTool(event)
	case agentengine.Failed:
		if event.Error != "" {
			r.issues = append(r.issues, event.Error)
		}
	}
	if event.Verification != nil {
		r.verification = event.Verification
	}
	if event.Plan != nil && event.Plan.Body != "" {
		r.plan = event.Plan.Body
	}
	if event.Turn > r.turn {
		r.turn = event.Turn
	}
	// Any event that names a purpose contributes to the route summary, not just
	// the turn's opening one: a tool that samples a model of its own reports it
	// with its usage, and a receipt that listed only the turn's own route would
	// omit the model that produced part of the bill.
	r.observeRoute(event)
	r.observeUsage(event)
}

// observeRoute records which route a purpose sampled on. A purpose is recorded
// once: a turn that resamples does so on the route it started with, and repeating
// it would read as two models having answered.
func (r *receiptRecorder) observeRoute(event agentengine.Event) {
	if event.Purpose == "" || event.Provider == "" {
		return
	}
	for _, existing := range r.routes {
		if existing.Purpose == event.Purpose {
			return
		}
	}
	r.routes = append(r.routes, protocol.ReceiptRoute{
		Purpose: event.Purpose, Provider: event.Provider, Model: event.Model,
	})
}

// observeUsage folds usage into the receipt. Terminal events carry the
// turn-cumulative total, so they replace anything accumulated from streaming
// events; streaming events are only a fallback for turns that end without one.
//
// The fallback keeps the last report per sample rather than adding every report
// up, because streaming usage is cumulative within its provider call: a
// provider that reports input and output separately sends two snapshots of the
// same call, and summing them counts the input twice.
func (r *receiptRecorder) observeUsage(event agentengine.Event) {
	if event.Usage == nil {
		return
	}
	terminal := event.State == agentengine.Completed || event.State == agentengine.Failed
	if r.usageFinal && !terminal {
		return
	}
	// Pricing is a property of the model, not of any one call, so it is known
	// or unknown regardless of whether this call happened to cost anything.
	r.costKnown = event.CostKnown
	usage := protocol.UsageData{
		InputTokens: event.Usage.InputTokens, OutputTokens: event.Usage.OutputTokens,
		ReasoningTokens: event.Usage.ReasoningTokens, CachedTokens: event.Usage.CachedTokens,
		CostMicrounits: costMicrounits(event.CostUSD),
	}
	if terminal {
		r.usage = usage
		r.usageFinal = true
		r.samples = nil
		return
	}
	if r.samples == nil {
		r.samples = make(map[uint32]protocol.UsageData)
	}
	r.samples[event.Sample] = usage
	r.usage = protocol.UsageData{}
	for _, sample := range r.samples {
		r.usage.InputTokens += sample.InputTokens
		r.usage.OutputTokens += sample.OutputTokens
		r.usage.ReasoningTokens += sample.ReasoningTokens
		r.usage.CachedTokens += sample.CachedTokens
		r.usage.CostMicrounits += sample.CostMicrounits
	}
}

func (r *receiptRecorder) observeTool(event agentengine.Event) {
	if event.ToolCall == nil || event.Result == nil {
		return
	}
	if event.Result.IsError {
		r.toolsFailed = appendUniqueString(r.toolsFailed, event.ToolCall.Name)
	} else {
		r.toolsSucceeded = appendUniqueString(r.toolsSucceeded, event.ToolCall.Name)
	}
	if event.ToolCall.Name == "load_skill" {
		if resolved, ok := event.Result.Metadata["resolved_skills"].([]skillruntime.ResolvedSkill); ok {
			for _, item := range resolved {
				duplicate := false
				for _, existing := range r.skills {
					if existing.Name == item.Name && existing.Digest == item.Digest {
						duplicate = true
						break
					}
				}
				if duplicate {
					continue
				}
				r.skills = append(r.skills, protocol.ReceiptSkill{
					Name: item.Name, Version: item.Version, Source: string(item.Source),
					Plugin: item.Plugin, Digest: item.Digest, Locked: item.Locked,
				})
			}
		}
	}
	for _, receipt := range event.Diagnostics {
		// An unavailable runner means nothing was checked, so it must not count
		// as a diagnostics run.
		if receipt.Status == "unavailable" {
			continue
		}
		r.diagnosticsRan = true
		r.diagnosticCount += len(receipt.Diagnostics)
	}
}

// build renders the receipt from the events it folded plus what the engine
// observed outside the stream.
func (r *receiptRecorder) build(observed turnObservations) *protocol.ExecutionReceiptData {
	if r == nil {
		return nil
	}
	receipt := &protocol.ExecutionReceiptData{
		Goal: r.goal, Plan: r.plan, Mode: r.mode, Posture: r.posture,
		Sandbox: r.sandbox, Workspace: r.workspace,
		Routes:         append([]protocol.ReceiptRoute(nil), r.routes...),
		ToolsSucceeded: r.toolsSucceeded, ToolsFailed: r.toolsFailed,
		Skills:             append([]protocol.ReceiptSkill(nil), r.skills...),
		ApprovalsRequested: r.approvals,
		Verification: protocol.ReceiptVerification{
			Diagnostics: diagnosticsOutcome(r.diagnosticsRan, r.diagnosticCount),
			Tests:       r.testsOutcome(),
			Verify:      r.verifyOutcome(),
		},
		DiagnosticCount: r.diagnosticCount,
		ContextSections: contextSections(observed.context),
		EditorContext:   append([]protocol.EditorContextReceipt(nil), r.editorContext...),
		Catalog:         observed.catalog,
		ContextBudget:   observed.budget,
		Evidence:        receiptEvidence(observed.evidence),
		ReadPaths:       append([]string(nil), observed.readPaths...),
		InputTokens:     r.usage.InputTokens, OutputTokens: r.usage.OutputTokens,
		ReasoningTokens: r.usage.ReasoningTokens, CachedTokens: r.usage.CachedTokens,
		CostMicrounits: r.usage.CostMicrounits, CostKnown: r.costKnown,
		LatencyMS:        time.Since(r.started).Milliseconds(),
		Latency:          receiptLatency(observed.latency),
		Budget:           r.receiptBudget(observed.spend),
		UnresolvedIssues: append(append([]string(nil), r.issues...), observed.conflicts...),
		NotCollected:     protocol.UncollectedReceiptSections,
	}
	// The engine's clock is the better boundary when there is one: it starts when
	// the turn starts rather than when this recorder was constructed, and it is the
	// same clock the phases were measured against, so the flat number and the
	// partition cannot disagree.
	if receipt.Latency != nil {
		receipt.LatencyMS = receipt.Latency.TotalMS
	}
	for _, change := range observed.changes {
		if change.Path == "" {
			continue
		}
		receipt.Changes = append(receipt.Changes, protocol.ReceiptChange{
			Path: change.Path, Tool: change.Tool,
			Kind: change.Kind, Added: change.Added, Removed: change.Removed,
			Summary: change.Summary,
		})
	}
	return receipt
}

// receiptLatency renders the measured phases. Everything but the first token is
// reported even when it is zero: a zero says the phase cost nothing, which is a
// fact about the turn, while an absent partition says nobody measured.
func receiptLatency(latency *trace.Latency) *protocol.ReceiptLatency {
	if latency == nil {
		return nil
	}
	rendered := &protocol.ReceiptLatency{
		TotalMS:        latency.Total.Milliseconds(),
		ProviderMS:     latency.Provider.Milliseconds(),
		ToolMS:         latency.Tool.Milliseconds(),
		ApprovalWaitMS: latency.ApprovalWait.Milliseconds(),
		VerifyMS:       latency.Verify.Milliseconds(),
	}
	if latency.FirstToken != nil {
		first := latency.FirstToken.Milliseconds()
		rendered.FirstTokenMS = &first
	}
	return rendered
}

// receiptBudget adds this turn's spend to the pool the engine reports, because
// the engine only folds a turn in once it completes and a receipt that excluded
// its own turn would overstate what is left.
func (r *receiptRecorder) receiptBudget(spend agentengine.BudgetSnapshot) *protocol.ReceiptBudget {
	if spend == (agentengine.BudgetSnapshot{}) {
		return nil
	}
	return &protocol.ReceiptBudget{
		TokensUsed:        spend.TokensUsed + r.usage.InputTokens + r.usage.OutputTokens,
		MaxTokens:         spend.MaxTokens,
		CostMicrounits:    costMicrounits(spend.CostUSD) + r.usage.CostMicrounits,
		MaxCostMicrounits: costMicrounits(spend.MaxCostUSD),
	}
}

// contextSections renders the prompt context receipts for the audit record. An
// empty section is reported too: knowing a partition carried nothing is what
// distinguishes it from a partition that was never assembled.
func contextSections(receipts []promptcontext.Receipt) []protocol.ReceiptContextSection {
	if len(receipts) == 0 {
		return nil
	}
	sections := make([]protocol.ReceiptContextSection, 0, len(receipts))
	for _, receipt := range receipts {
		sections = append(sections, protocol.ReceiptContextSection{
			Kind: receipt.Kind, Digest: receipt.Digest,
			OriginalBytes: receipt.OriginalBytes, RetainedBytes: receipt.RetainedBytes,
			Truncated: receipt.Truncated, TruncationReason: receipt.TruncationReason,
		})
	}
	return sections
}

// receiptEvidence renders the evidence set for the audit record. A set with
// nothing in it produces no section: an absent section says the session found
// nothing worth recording, which an empty one would not.
func receiptEvidence(snapshot evidence.Snapshot) *protocol.ReceiptEvidence {
	if snapshot.Empty() {
		return nil
	}
	rendered := &protocol.ReceiptEvidence{OmittedFacts: snapshot.OmittedFacts}
	for _, fact := range snapshot.Facts {
		rendered.Facts = append(rendered.Facts, protocol.ReceiptEvidenceFact{
			Kind: string(fact.Kind), Path: fact.Path, Line: fact.Line,
			Symbol: fact.Symbol, Tool: fact.Tool, Turn: fact.Turn,
		})
	}
	for _, risk := range snapshot.Risks {
		rendered.Risks = append(rendered.Risks, protocol.ReceiptEvidenceRisk{
			Kind: risk.Kind, Path: risk.Path, Turn: risk.Turn,
		})
	}
	for _, reminder := range snapshot.Reminders {
		rendered.Reminders = append(rendered.Reminders, reminder.Detail)
	}
	return rendered
}

// verificationData renders one gate evaluation as a protocol event. Check output
// is trimmed to the failing streams so the event stays readable in a log.
func verificationData(receipt *agentengine.VerificationReceipt) *protocol.TurnVerificationData {
	data := &protocol.TurnVerificationData{
		Scope: string(receipt.Scope), Mode: receipt.Mode, Action: receipt.Action,
		Status: receipt.Status, RepairSteps: receipt.RepairSteps,
		Errors: receipt.Errors, Warnings: receipt.Warnings,
		Paths: append([]string(nil), receipt.Paths...), Message: receipt.Message,
	}
	for _, check := range receipt.Checks {
		output := strings.TrimSpace(check.Stdout + "\n" + check.Stderr)
		data.Checks = append(data.Checks, protocol.VerificationCheck{
			Name: check.Name, Command: check.Command, Status: check.Status,
			ExitCode: check.ExitCode, Output: output,
		})
	}
	return data
}

// verifyOutcome reports the verification gate's verdict for the turn.
func (r *receiptRecorder) verifyOutcome() string {
	if r.verification == nil {
		return protocol.ReceiptNotEvaluated
	}
	switch r.verification.Status {
	case verify.StatusPassed:
		return protocol.ReceiptPassed
	case verify.StatusFailed:
		return protocol.ReceiptFailed
	case verify.StatusUnavailable:
		return protocol.ReceiptUnavailable
	default:
		return protocol.ReceiptNotEvaluated
	}
}

// testsOutcome only reports a verdict when the gate actually ran the
// repository's own commands; the diagnostics scope runs no tests.
func (r *receiptRecorder) testsOutcome() string {
	if r.verification == nil || r.verification.Scope != verify.ScopeRepository {
		return protocol.ReceiptNotEvaluated
	}
	return r.verifyOutcome()
}

func diagnosticsOutcome(ran bool, count int) string {
	switch {
	case !ran:
		return protocol.ReceiptNotEvaluated
	case count > 0:
		return protocol.ReceiptFailed
	default:
		return protocol.ReceiptPassed
	}
}

func appendUniqueString(values []string, value string) []string {
	if value == "" {
		return values
	}
	for _, candidate := range values {
		if candidate == value {
			return values
		}
	}
	return append(values, value)
}
