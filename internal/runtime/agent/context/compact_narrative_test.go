package agentcontext

import (
	"encoding/json"
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
	if len(artifact.Body.Items) != 2 ||
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
	for name, raw := range map[string]string{
		"unknown source": `{"decisions":[{"text":"x","source_message_ids":["msg_unknown"]}],"rationale":[],"preferences":[],"unresolved":[]}`,
		"unknown field":  `{"decisions":[],"rationale":[],"preferences":[],"unresolved":[],"verified":true}`,
		"missing array":  `{"decisions":[],"rationale":[],"preferences":[]}`,
		"trailing":       `{"decisions":[],"rationale":[],"preferences":[],"unresolved":[]} true`,
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

func TestNarrativeInputLabelsConversationAndExcludesToolResults(t *testing.T) {
	now := time.Now().UTC()
	input, err := BuildNarrativeInput(
		"thread-1",
		"window-1",
		"sha256:authority",
		"sha256:route",
		[]provider.Message{
			messageAt(provider.RoleUser, "retain this preference", 1),
			messageAt(provider.RoleSystem, "old generated narrative", 1),
			messageAt(provider.RoleTool, "untrusted tool output", 1),
		},
		DefaultNarrativeLimits(),
		now,
		time.Hour,
	)
	if err != nil {
		t.Fatal(err)
	}
	if input.PrivacyClass != NarrativePrivacyClass ||
		len(input.Excerpts) != 1 ||
		input.Excerpts[0].Role != provider.RoleUser {
		t.Fatalf("input=%+v", input)
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
