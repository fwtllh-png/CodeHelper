package agentcontext

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestNarrativeArtifactIsSourceBoundAndNonAuthoritative(t *testing.T) {
	now := time.Date(2026, 8, 21, 12, 0, 0, 0, time.UTC)
	removed := []provider.Message{
		messageAt(provider.RoleUser, "I prefer deterministic ledgers", 1),
		messageAt(provider.RoleAssistant, "Decision: retain structured truth because it is verifiable", 1),
	}
	input, err := BuildNarrativeInput(
		"thread-1",
		"window-1",
		"sha256:authority",
		"sha256:route",
		removed,
		DefaultNarrativeLimits(),
		now,
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"technical_concepts": []any{},
		"files_and_code": []map[string]any{{
			"text":               "parser/lex.go exposes Lex.",
			"source_message_ids": []string{input.Excerpts[1].MessageID},
		}},
		"errors_and_fixes": []any{},
		"pending_jobs":     []any{},
		"current_work":     []any{},
		"next_steps":       []any{},
		"critical_context": []any{},
		"decisions": []map[string]any{{
			"text":               "Use a deterministic truth ledger.",
			"source_message_ids": []string{input.Excerpts[1].MessageID},
		}},
		"rationale": []any{},
		"preferences": []map[string]any{{
			"text":               "Prefer deterministic state.",
			"source_message_ids": []string{input.Excerpts[0].MessageID},
		}},
		"unresolved": []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := ValidateNarrativeJSON(
		raw,
		input,
		DefaultNarrativeLimits(),
		2,
		now,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(artifact.Body.Items) != 3 ||
		artifact.AuthorityDigest != input.AuthorityDigest ||
		artifact.Digest == "" ||
		artifact.Body.Items[0].ID == artifact.Body.Items[1].ID {
		t.Fatalf("artifact=%+v", artifact)
	}
}

func TestNarrativeValidatorRejectsUnknownSourceAndFields(t *testing.T) {
	now := time.Now().UTC()
	input, err := BuildNarrativeInput(
		"thread-1",
		"window-1",
		"sha256:authority",
		"sha256:route",
		[]provider.Message{messageAt(provider.RoleUser, "constraint", 1)},
		DefaultNarrativeLimits(),
		now,
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	valid := `{"technical_concepts":[],"files_and_code":[],"errors_and_fixes":[],"pending_jobs":[],"current_work":[],"next_steps":[],"critical_context":[],"decisions":[],"rationale":[],"preferences":[],"unresolved":[]}`
	for name, raw := range map[string]string{
		"unknown source": strings.Replace(
			valid,
			`"files_and_code":[]`,
			`"files_and_code":[{"text":"x","source_message_ids":["msg_unknown"]}]`,
			1,
		),
		"unknown field": strings.TrimSuffix(valid, "}") + `,"verified":true}`,
		"missing array": strings.Replace(valid, `"next_steps":[],`, "", 1),
		"trailing":      valid + ` true`,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ValidateNarrativeJSON(
				[]byte(raw),
				input,
				DefaultNarrativeLimits(),
				2,
				now,
			); err == nil {
				t.Fatal("invalid narrative was accepted")
			}
		})
	}
}

func TestStableMessageIDDoesNotContainMessageText(t *testing.T) {
	message := messageAt(provider.RoleUser, "private content", 7)
	id := StableMessageID("thread-1", message, 3)
	if id == "" || id == "private content" {
		t.Fatalf("message id = %q", id)
	}
	if id != StableMessageID("thread-1", message, 3) {
		t.Fatal("message id changed across retry")
	}
	if message.Turn != 7 {
		t.Fatalf("message turn mutated to %d", message.Turn)
	}
	withoutTurn := message
	withoutTurn.Turn = 0
	if id == StableMessageID("thread-1", withoutTurn, 3) {
		t.Fatalf("message id %q ignored the turn", id)
	}
}

func TestNarrativeInputIncludesPairedToolResults(t *testing.T) {
	now := time.Now().UTC()
	input, err := BuildNarrativeInput(
		"thread-1",
		"window-1",
		"sha256:authority",
		"sha256:route",
		[]provider.Message{
			messageAt(provider.RoleUser, "retain this preference", 1),
			messageAt(provider.RoleSystem, "old generated narrative", 1),
			{
				Role: provider.RoleAssistant,
				Blocks: []provider.ContentBlock{{
					Type: provider.ContentToolCall,
					ToolCall: &provider.ToolCall{
						ID: "call-read", Name: "file_read",
						Arguments: `{"path":"parser.go"}`,
					},
				}},
				Turn: 1,
			},
			{
				Role: provider.RoleTool,
				Blocks: []provider.ContentBlock{{
					Type: provider.ContentToolResult,
					ToolResult: &provider.ToolResult{
						CallID: "call-read", Content: "func Parse() {}",
					},
				}},
				Turn: 1,
			},
		},
		DefaultNarrativeLimits(),
		now,
		time.Hour,
		[]string{
			NarrativeCurrent,
			NarrativeFileCode,
			NarrativeNextStep,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if input.PrivacyClass != NarrativePrivacyClass ||
		len(input.Excerpts) != 2 ||
		len(input.RequiredKinds) != 3 ||
		input.Excerpts[0].Role != provider.RoleUser ||
		input.Excerpts[1].Role != provider.RoleTool ||
		!strings.Contains(input.Excerpts[1].Text, "file_read") ||
		!strings.Contains(input.Excerpts[1].Text, "parser.go") ||
		!strings.Contains(input.Excerpts[1].Text, "func Parse()") {
		t.Fatalf("input=%+v", input)
	}
	source := input.Excerpts[1].MessageID
	raw, err := json.Marshal(map[string]any{
		"technical_concepts": []any{},
		"files_and_code": []map[string]any{{
			"text": "parser.go defines Parse.", "source_message_ids": []string{source},
		}},
		"errors_and_fixes": []any{},
		"pending_jobs":     []any{},
		"current_work": []map[string]any{{
			"text": "Implement the parser.", "source_message_ids": []string{source},
		}},
		"next_steps": []map[string]any{{
			"text": "Write parser.go.", "source_message_ids": []string{source},
		}},
		"critical_context": []any{},
		"decisions":        []any{},
		"rationale":        []any{},
		"preferences":      []any{},
		"unresolved":       []any{},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = ValidateNarrativeJSON(
		raw, input, DefaultNarrativeLimits(), 2, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	missing, _ := json.Marshal(map[string]any{
		"technical_concepts": []any{},
		"files_and_code":     []any{},
		"errors_and_fixes":   []any{},
		"pending_jobs":       []any{},
		"current_work":       []any{},
		"next_steps":         []any{},
		"critical_context":   []any{},
		"decisions":          []any{},
		"rationale":          []any{},
		"preferences":        []any{},
		"unresolved":         []any{},
	})
	if _, err := ValidateNarrativeJSON(
		missing, input, DefaultNarrativeLimits(), 2, now,
	); err == nil {
		t.Fatal("tool-heavy narrative accepted without continuation context")
	}
}

func TestNarrativeInputBudgetCoversCanonicalArtifact(t *testing.T) {
	now := time.Now().UTC()
	var removed []provider.Message
	for index := 0; index < 20; index++ {
		removed = append(
			removed,
			messageAt(provider.RoleUser, "short preference", uint64(index+1)),
		)
	}
	limits := DefaultNarrativeLimits()
	limits.MaxInputBytes = 900
	input, err := BuildNarrativeInput(
		"thread-1",
		"window-1",
		"sha256:authority",
		"sha256:route",
		removed,
		limits,
		now,
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > limits.MaxInputBytes ||
		len(input.Excerpts) == 0 ||
		len(input.Excerpts) == len(removed) {
		t.Fatalf(
			"artifact bytes=%d excerpts=%d",
			len(raw),
			len(input.Excerpts),
		)
	}
}

func TestContinuationCheckpointMustReduceSourceContext(t *testing.T) {
	now := time.Now().UTC()
	compatibility := Compatibility{
		SchemaVersion: TruthSchemaVersion,
		Adapter:       "openai", Provider: "test", Model: "test",
		ContextTokens: 4096, ToolCalls: true,
		SummaryMaxBytes: 4096, MaxDigestEntries: 10,
		DownshiftPolicy: DownshiftRuntimeTruthOnly,
	}
	truth := BuildTruthCapsule(TruthProjection{
		Compatibility: compatibility,
		ModelID:       "test",
		ContextTokens: 4096,
		Summary:       Summary{Goal: "continue"},
	})
	authority, err := truth.AuthorityDigest()
	if err != nil {
		t.Fatal(err)
	}
	source := []provider.Message{
		messageAt(provider.RoleUser, "continue", 1),
	}
	candidate, err := BuildCompactionCandidate(CompactionCandidateInput{
		Cut: 1, Removed: source, ToSummarize: source,
		OriginalHistory: source,
		Summary:         Summary{Window: 1, Goal: "continue"},
		CurrentTruth:    truth,
		RetentionPolicy: DefaultRetentionPolicy(),
		Turn:            1,
		SummaryMaxBytes: 4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	candidate.SourceWindowID = "window-1"
	candidate.SourceContextDigest = "sha256:context"
	candidate.AuthorityDigest = authority
	state := PrepareCompactionState(CompactionPreparation{
		Candidate: candidate, ThreadID: "thread-1", TurnID: "turn-1",
		TargetWindowID: "window-2", StablePrefixDigest: "sha256:stable",
		RouteDigest: "sha256:route", Trigger: "inline",
		NarrativeLimits: DefaultNarrativeLimits(), Now: now, InputTTL: time.Hour,
	})
	if state.Plan == nil || state.NarrativeInput == nil {
		t.Fatalf("prepared state = %+v", state)
	}
	raw := strings.Replace(
		`{"technical_concepts":[],"files_and_code":[],"errors_and_fixes":[],"pending_jobs":[],"current_work":[],"next_steps":[],"critical_context":[],"decisions":[],"rationale":[],"preferences":[],"unresolved":[]}`,
		`"preferences":[]`,
		`"preferences":[{"text":"continue","source_message_ids":["`+
			state.NarrativeInput.Excerpts[0].MessageID+`"]}]`,
		1,
	)
	artifact, err := ValidateNarrativeJSON(
		[]byte(raw), *state.NarrativeInput, DefaultNarrativeLimits(), 2, now,
	)
	if err != nil {
		t.Fatal(err)
	}
	state.Plan.SourceBytes = 1
	state.Plan.Digest = state.Plan.digest()
	state.PlanDigest = state.Plan.Digest
	if _, err := CompleteCompaction(
		*state,
		&artifact,
		source,
		4096,
	); err == nil || !strings.Contains(err.Error(), "did not reduce") {
		t.Fatalf("CompleteCompaction() error = %v", err)
	}
}

func TestRebindNarrativeInputCarriesVerifiedExcerptsToLatestWindow(t *testing.T) {
	now := time.Now().UTC()
	input, err := BuildNarrativeInput(
		"thread-1",
		"window-1",
		"sha256:authority",
		"sha256:route",
		[]provider.Message{
			messageAt(provider.RoleUser, "retain this decision", 1),
		},
		DefaultNarrativeLimits(),
		now,
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	rebased, err := RebindNarrativeInput(
		input,
		"window-2",
		input.AuthorityDigest,
		input.RouteDigest,
		DefaultNarrativeLimits(),
		now.Add(time.Minute),
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if rebased.SourceWindowID != "window-2" ||
		len(rebased.Excerpts) != 1 ||
		rebased.Excerpts[0] != input.Excerpts[0] ||
		rebased.Digest == input.Digest {
		t.Fatalf("rebased input = %+v", rebased)
	}
}

func messageAt(role provider.Role, text string, turn uint64) provider.Message {
	message := provider.TextMessage(role, text)
	message.Turn = turn
	return message
}
