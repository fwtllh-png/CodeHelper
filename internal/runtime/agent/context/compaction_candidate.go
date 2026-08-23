package agentcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const DefaultMaxDigestEntries = 120

type SummaryRequest struct {
	Plan             Plan
	Removed          []provider.Message
	Turn             uint64
	WorkingSetLimit  int
	EvidenceLimit    int
	MaxDigestEntries int
	SummaryLineBytes int
}

type SummaryResult struct {
	Summary       Summary
	WorkingSet    []string
	CriticalPaths []string
}

func (a Authority) BuildSummary(request SummaryRequest) SummaryResult {
	summary := Summary{Window: len(request.Removed)}
	summary.Goal = request.Plan.Objective
	if summary.Goal == "" {
		summary.Goal = request.Plan.Title
	}
	open, done := request.Plan.OutstandingSteps()
	summary.DoneTodos = done
	for _, step := range open {
		summary.Todos = append(summary.Todos, Todo{
			Title: step.Title, Status: step.Status,
		})
	}
	summary.Failures = a.Failures().List()
	for _, change := range a.Evidence().Changes() {
		summary.Changes = append(summary.Changes, CompactionChange{
			Path: change.Path, Turn: change.Turn, Read: change.Read,
			Verified: change.Verified, Diagnostics: change.Diagnostics,
			Stale: change.Stale,
		})
	}
	SortChanges(summary.Changes)
	var paths, critical []string
	for _, entry := range a.WorkingSet().Select(
		request.Turn,
		request.WorkingSetLimit,
	) {
		paths = append(paths, entry.Path)
		if entry.Critical {
			critical = append(critical, entry.Path)
		}
	}
	sort.Strings(paths)
	sort.Strings(critical)
	summary.CriticalPaths = append([]string(nil), critical...)
	snapshot := a.Evidence().Snapshot(request.EvidenceLimit)
	for _, fact := range snapshot.Facts {
		summary.Facts = append(summary.Facts, CompactionFact{
			Line: fact.Describe(),
		})
	}
	summary.OmittedFacts = snapshot.OmittedFacts
	limit := request.MaxDigestEntries
	if limit <= 0 {
		limit = DefaultMaxDigestEntries
	}
	lineBytes := request.SummaryLineBytes
	if lineBytes <= 0 {
		lineBytes = 512
	}
	for index := len(request.Removed) - 1; index >= 0; index-- {
		message := request.Removed[index]
		if _, ok := Carry(message.Text()); ok {
			continue
		}
		if len(summary.Digest) == limit {
			continue
		}
		summary.Digest = append(
			summary.Digest,
			SummaryLine(message, lineBytes),
		)
	}
	return SummaryResult{
		Summary: summary, WorkingSet: paths, CriticalPaths: critical,
	}
}

type CompactionCandidate struct {
	Cut                 int
	History             []provider.Message
	Removed             []provider.Message
	ToSummarize         []provider.Message
	Rendered            string
	RetainedBytes       int
	RetainedTokens      uint64
	SummaryTruncated    bool
	Sections            []string
	Truth               MergeReceipt
	Capsule             TruthCapsule
	Authority           TruthCapsule
	CompatibilityHash   string
	AuthorityDigest     string
	NarrativeIncluded   bool
	CapsuleBytes        int
	Retention           RetentionReceipt
	SourceWindowID      string
	SourceContextDigest string
}

type CompactionCandidateInput struct {
	Cut              int
	Removed          []provider.Message
	ToSummarize      []provider.Message
	Tail             []provider.Message
	OriginalHistory  []provider.Message
	Summary          Summary
	CurrentTruth     TruthCapsule
	RetentionPolicy  RetentionPolicy
	Turn             uint64
	SummaryMaxBytes  int
	IncludeNarrative bool
}

// BuildCompactionCandidate performs the deterministic portion of compaction:
// merging prior truth, applying retention, rendering the structured summary and
// rebuilding a tool-pair-safe history window.
func BuildCompactionCandidate(
	input CompactionCandidateInput,
) (CompactionCandidate, error) {
	previous, err := truthCapsules(input.ToSummarize)
	if err != nil {
		return CompactionCandidate{}, err
	}
	authority := MandatoryCapsule(input.CurrentTruth)
	capsule, mergeReceipt, err := MergeTruthCapsules(
		input.CurrentTruth,
		previous...,
	)
	if err != nil {
		return CompactionCandidate{}, err
	}
	capsule, retention, err := PlanRetention(
		capsule,
		input.RetentionPolicy,
		input.Turn,
	)
	if err != nil {
		return CompactionCandidate{}, err
	}
	mergeReceipt.EntityCount = len(capsule.Entities)
	mergeReceipt.CriticalEntityCount = 0
	for _, entity := range capsule.Entities {
		if entity.Kind == EntityFact || entity.Kind == EntityCriticalPath {
			mergeReceipt.CriticalEntityCount++
		}
	}
	narrative := Narrative{}
	renderSummary := input.Summary
	if !input.IncludeNarrative {
		renderSummary = Summary{Window: input.Summary.Window}
	}
	rendered, err := RenderStructured(
		renderSummary,
		capsule,
		narrative,
		input.SummaryMaxBytes,
	)
	if err != nil {
		return CompactionCandidate{}, err
	}
	compacted := provider.TextMessage(provider.RoleSystem, rendered.Text)
	history := append(
		[]provider.Message{compacted},
		CloneMessages(input.Tail)...,
	)
	if HistoryBytes(history) >= HistoryBytes(input.OriginalHistory) &&
		(rendered.NarrativeIncluded || len(rendered.Sections) > 1) {
		rendered, err = RenderStructured(
			Summary{Window: input.Summary.Window},
			capsule,
			Narrative{},
			input.SummaryMaxBytes,
		)
		if err != nil {
			return CompactionCandidate{}, err
		}
		compacted = provider.TextMessage(provider.RoleSystem, rendered.Text)
		history = append(
			[]provider.Message{compacted},
			CloneMessages(input.Tail)...,
		)
	}
	return CompactionCandidate{
		Cut: input.Cut, History: history,
		Removed:     CloneMessages(input.Removed),
		ToSummarize: CloneMessages(input.ToSummarize),
		Rendered:    rendered.Text, RetainedBytes: HistoryBytes(history),
		SummaryTruncated: rendered.Truncated,
		Sections:         append([]string(nil), rendered.Sections...),
		Truth:            mergeReceipt, Capsule: capsule, Authority: authority,
		CompatibilityHash: capsule.CompatibilityHash,
		NarrativeIncluded: rendered.NarrativeIncluded,
		CapsuleBytes:      rendered.CapsuleBytes,
		Retention:         retention,
	}, nil
}

func truthCapsules(messages []provider.Message) ([]TruthCapsule, error) {
	var result []TruthCapsule
	for _, message := range messages {
		capsule, found, err := ParseTruthCapsule(message.Text())
		if err != nil {
			return nil, err
		}
		if found {
			result = append(result, capsule)
		}
	}
	return result, nil
}

type CompactionPreparation struct {
	Candidate          CompactionCandidate
	Previous           *CompactionState
	ThreadID           protocol.ThreadID
	TurnID             protocol.TurnID
	TargetWindowID     string
	StablePrefixDigest string
	RouteDigest        string
	RouteFailure       string
	Trigger            string
	NarrativeLimits    NarrativeLimits
	Now                time.Time
	InputTTL           time.Duration
}

// PrepareCompactionState creates the durable, deterministic context work item
// that can later generate and rebase an optional semantic narrative.
func PrepareCompactionState(input CompactionPreparation) *CompactionState {
	candidate := input.Candidate
	state := &CompactionState{
		ID: stableCompactionID(
			input.ThreadID,
			input.TurnID,
			candidate.SourceWindowID,
			candidate.AuthorityDigest,
		),
		ThreadID:            input.ThreadID,
		TurnID:              input.TurnID,
		Phase:               "prepared",
		Truth:               candidate.Capsule,
		SourceWindowID:      candidate.SourceWindowID,
		TargetWindowID:      input.TargetWindowID,
		SourceContextDigest: candidate.SourceContextDigest,
	}
	if input.RouteFailure != "" {
		state.Phase = "fallback"
		state.FallbackReason = input.RouteFailure
		state.PlanDigest = FallbackCompactionPlanDigest(state)
		return state
	}
	narrativeInput, err := BuildNarrativeInput(
		input.ThreadID,
		candidate.SourceWindowID,
		candidate.AuthorityDigest,
		input.RouteDigest,
		candidate.Removed,
		input.NarrativeLimits,
		input.Now,
		input.InputTTL,
	)
	if err == nil && len(narrativeInput.Excerpts) == 0 {
		previous := input.Previous
		if previous != nil && previous.Phase == "prepared" &&
			previous.TurnID == input.TurnID &&
			previous.NarrativeInput != nil &&
			len(previous.NarrativeInput.Excerpts) != 0 &&
			previous.NarrativeInput.AuthorityDigest ==
				candidate.AuthorityDigest &&
			previous.NarrativeInput.RouteDigest == input.RouteDigest {
			narrativeInput, err = RebindNarrativeInput(
				*previous.NarrativeInput,
				candidate.SourceWindowID,
				candidate.AuthorityDigest,
				input.RouteDigest,
				input.NarrativeLimits,
				input.Now,
				input.InputTTL,
			)
		}
	}
	if err != nil {
		state.Phase = "fallback"
		state.FallbackReason = err.Error()
		state.PlanDigest = FallbackCompactionPlanDigest(state)
		return state
	}
	state.NarrativeInput = &narrativeInput
	compacted := CompactedContext{
		CompactionID:        state.ID,
		ThreadID:            input.ThreadID,
		TurnID:              input.TurnID,
		SourceWindowID:      candidate.SourceWindowID,
		TargetWindowID:      input.TargetWindowID,
		SourceContextDigest: candidate.SourceContextDigest,
		StablePrefixDigest:  input.StablePrefixDigest,
		Truth:               candidate.Capsule,
		Tail:                CloneMessages(candidate.History[1:]),
	}
	digestErr := compacted.Seal()
	plan := CompactionPlan{
		ID: state.ID, Phase: "prepared",
		Trigger:             input.Trigger,
		SourceWindowID:      candidate.SourceWindowID,
		TargetWindowID:      input.TargetWindowID,
		SourceContextDigest: candidate.SourceContextDigest,
		Cut:                 candidate.Cut,
		Truth:               candidate.Capsule,
		NarrativeInput:      narrativeInput,
		DeterministicResult: compacted,
	}
	for index, message := range candidate.Removed {
		plan.RemovedMessageIDs = append(
			plan.RemovedMessageIDs,
			StableMessageID(input.ThreadID, message, index),
		)
	}
	if digestErr == nil {
		digestErr = plan.Seal()
	}
	if digestErr != nil {
		state.Phase = "fallback"
		state.FallbackReason = digestErr.Error()
		state.PlanDigest = FallbackCompactionPlanDigest(state)
		return state
	}
	state.Plan = &plan
	state.PlanDigest = plan.Digest
	return state
}

func FallbackCompactionPlanDigest(state *CompactionState) string {
	if state == nil {
		return ""
	}
	sum := sha256.Sum256([]byte(
		state.ID + "\x00" +
			string(state.ThreadID) + "\x00" +
			string(state.TurnID) + "\x00" +
			state.SourceWindowID + "\x00" +
			state.TargetWindowID + "\x00" +
			state.SourceContextDigest + "\x00" +
			state.Truth.Digest,
	))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stableCompactionID(
	threadID protocol.ThreadID,
	turnID protocol.TurnID,
	windowID string,
	authorityDigest string,
) string {
	sum := sha256.Sum256([]byte(
		string(threadID) + "\x00" + string(turnID) + "\x00" +
			windowID + "\x00" + authorityDigest,
	))
	return "compact_" + hex.EncodeToString(sum[:16])
}
