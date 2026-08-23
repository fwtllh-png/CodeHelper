package agentcontext

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	NarrativeSchemaVersion = 1
	NarrativePrivacyClass  = "conversation_content"

	NarrativeDecision   = "decision"
	NarrativeRationale  = "rationale"
	NarrativePreference = "preference"
	NarrativeUnresolved = "unresolved"
)

type NarrativeExcerpt struct {
	MessageID string        `json:"message_id"`
	Role      provider.Role `json:"role"`
	Turn      uint64        `json:"turn,omitempty"`
	Text      string        `json:"text"`
	Digest    string        `json:"digest"`
	Truncated bool          `json:"truncated,omitempty"`
	Priority  int           `json:"priority"`
}

type NarrativeInputArtifact struct {
	Version         int                `json:"version"`
	ThreadID        protocol.ThreadID  `json:"thread_id"`
	SourceWindowID  string             `json:"source_window_id"`
	AuthorityDigest string             `json:"authority_digest"`
	RouteDigest     string             `json:"route_digest"`
	PrivacyClass    string             `json:"privacy_class"`
	Excerpts        []NarrativeExcerpt `json:"excerpts"`
	Digest          string             `json:"digest"`
	ExpiresAt       time.Time          `json:"expires_at"`
}

type NarrativeItem struct {
	ID               string   `json:"id"`
	Kind             string   `json:"kind"`
	Text             string   `json:"text"`
	SourceMessageIDs []string `json:"source_message_ids"`
	SourceDigest     string   `json:"source_digest"`
	CreatedTurn      uint64   `json:"created_turn"`
}

type NarrativeArtifact struct {
	Version         int               `json:"version"`
	ThreadID        protocol.ThreadID `json:"thread_id"`
	WindowID        string            `json:"window_id"`
	AuthorityDigest string            `json:"authority_digest"`
	InputDigest     string            `json:"input_digest"`
	RouteDigest     string            `json:"route_digest"`
	Body            Narrative         `json:"body"`
	CreatedAt       time.Time         `json:"created_at"`
	ExpiresAt       time.Time         `json:"expires_at"`
	Digest          string            `json:"digest"`
}

type ContextDataBlock struct {
	Version          int             `json:"version"`
	Kind             string          `json:"kind"`
	NonAuthoritative bool            `json:"non_authoritative"`
	Provenance       string          `json:"provenance"`
	Content          json.RawMessage `json:"content"`
	Digest           string          `json:"digest"`
}

func NewContextDataBlock(
	kind string,
	nonAuthoritative bool,
	provenance string,
	value any,
) (ContextDataBlock, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return ContextDataBlock{}, err
	}
	block := ContextDataBlock{
		Version: NarrativeSchemaVersion, Kind: strings.TrimSpace(kind),
		NonAuthoritative: nonAuthoritative,
		Provenance:       strings.TrimSpace(provenance),
		Content:          content,
	}
	block.Digest = digestString(string(content))
	return block, block.Validate()
}

func (b ContextDataBlock) Validate() error {
	if b.Version != NarrativeSchemaVersion || b.Kind == "" ||
		b.Provenance == "" || len(b.Content) == 0 ||
		!json.Valid(b.Content) ||
		b.Digest != digestString(string(b.Content)) {
		return errors.New("context data block is invalid")
	}
	if b.Kind == "truth" && b.NonAuthoritative {
		return errors.New("truth data block cannot be non-authoritative")
	}
	if b.Kind == "narrative" && !b.NonAuthoritative {
		return errors.New("narrative data block must be non-authoritative")
	}
	return nil
}

type CompactedContext struct {
	Version             int                `json:"version"`
	CompactionID        string             `json:"compaction_id"`
	ThreadID            protocol.ThreadID  `json:"thread_id"`
	TurnID              protocol.TurnID    `json:"turn_id"`
	SourceWindowID      string             `json:"source_window_id"`
	TargetWindowID      string             `json:"target_window_id"`
	SourceContextDigest string             `json:"source_context_digest"`
	StablePrefixDigest  string             `json:"stable_prefix_digest"`
	Truth               TruthCapsule       `json:"truth"`
	Narrative           *NarrativeArtifact `json:"narrative,omitempty"`
	Tail                []provider.Message `json:"tail"`
	TailTurns           []uint64           `json:"tail_turns,omitempty"`
	AuthorityDigest     string             `json:"authority_digest"`
	NarrativeDigest     string             `json:"narrative_digest,omitempty"`
	TailDigest          string             `json:"tail_digest"`
	Digest              string             `json:"digest"`
}

type CompactionPlan struct {
	Version             int                    `json:"version"`
	ID                  string                 `json:"id"`
	Phase               string                 `json:"phase"`
	Trigger             string                 `json:"trigger"`
	SourceWindowID      string                 `json:"source_window_id"`
	TargetWindowID      string                 `json:"target_window_id"`
	SourceContextDigest string                 `json:"source_context_digest"`
	Cut                 int                    `json:"cut"`
	RemovedMessageIDs   []string               `json:"removed_message_ids"`
	TailMessageIDs      []string               `json:"tail_message_ids"`
	Truth               TruthCapsule           `json:"truth"`
	NarrativeInput      NarrativeInputArtifact `json:"narrative_input"`
	DeterministicResult CompactedContext       `json:"deterministic_result"`
	Digest              string                 `json:"digest"`
}

func (p *CompactionPlan) Seal() error {
	if p == nil {
		return errors.New("compaction plan is nil")
	}
	p.Version = NarrativeSchemaVersion
	p.RemovedMessageIDs = append([]string(nil), p.RemovedMessageIDs...)
	if len(p.DeterministicResult.TailTurns) !=
		len(p.DeterministicResult.Tail) {
		return errors.New("compaction plan tail turn count is invalid")
	}
	p.TailMessageIDs = make(
		[]string,
		len(p.DeterministicResult.Tail),
	)
	for index, message := range p.DeterministicResult.Tail {
		message.Turn = p.DeterministicResult.TailTurns[index]
		p.TailMessageIDs[index] = StableMessageID(
			p.DeterministicResult.ThreadID,
			message,
			index,
		)
	}
	p.Digest = p.digest()
	return p.Validate()
}

func (p CompactionPlan) Validate() error {
	if p.Version != NarrativeSchemaVersion || p.ID == "" ||
		p.Phase == "" || p.Trigger == "" ||
		p.SourceWindowID == "" || p.TargetWindowID == "" ||
		p.SourceContextDigest == "" || p.Cut < 1 ||
		len(p.RemovedMessageIDs) != p.Cut ||
		p.Digest == "" || p.Digest != p.digest() {
		return errors.New("compaction plan identity or digest is invalid")
	}
	if err := p.Truth.Validate(); err != nil {
		return err
	}
	if err := p.NarrativeInput.Validate(time.Time{}); err != nil {
		return err
	}
	if err := p.DeterministicResult.Validate(); err != nil {
		return err
	}
	if p.DeterministicResult.CompactionID != p.ID ||
		p.DeterministicResult.SourceWindowID != p.SourceWindowID ||
		p.DeterministicResult.TargetWindowID != p.TargetWindowID ||
		p.DeterministicResult.SourceContextDigest != p.SourceContextDigest ||
		p.DeterministicResult.Truth.Digest != p.Truth.Digest ||
		p.NarrativeInput.ThreadID != p.DeterministicResult.ThreadID ||
		p.NarrativeInput.SourceWindowID != p.SourceWindowID {
		return errors.New("compaction plan deterministic result fence is invalid")
	}
	authorityDigest, err := p.Truth.AuthorityDigest()
	if err != nil || p.NarrativeInput.AuthorityDigest != authorityDigest ||
		p.DeterministicResult.AuthorityDigest != authorityDigest {
		return errors.New("compaction plan authority fence is invalid")
	}
	if len(p.TailMessageIDs) != len(p.DeterministicResult.Tail) {
		return errors.New("compaction plan tail identity count is invalid")
	}
	for index, message := range p.DeterministicResult.Tail {
		message.Turn = p.DeterministicResult.TailTurns[index]
		expected := StableMessageID(
			p.DeterministicResult.ThreadID,
			message,
			index,
		)
		if p.TailMessageIDs[index] != expected {
			return fmt.Errorf(
				"compaction plan tail identity %d for thread %s is invalid: got %s want %s",
				index,
				p.DeterministicResult.ThreadID,
				p.TailMessageIDs[index],
				expected,
			)
		}
	}
	return nil
}

func (p CompactionPlan) digest() string {
	p.Digest = ""
	encoded, _ := json.Marshal(p)
	return digestString(string(encoded))
}

type NarrativeLimits struct {
	MaxInputBytes   int
	MaxOutputBytes  int
	MaxItems        int
	ItemMaxBytes    int
	ExcerptMaxBytes int
}

func DefaultNarrativeLimits() NarrativeLimits {
	return NarrativeLimits{
		MaxInputBytes: 16 << 10, MaxOutputBytes: 8 << 10,
		MaxItems: 32, ItemMaxBytes: 512, ExcerptMaxBytes: 1024,
	}
}

func BuildNarrativeInput(
	threadID protocol.ThreadID,
	sourceWindowID string,
	authorityDigest string,
	routeDigest string,
	removed []provider.Message,
	limits NarrativeLimits,
	now time.Time,
	ttl time.Duration,
) (NarrativeInputArtifact, error) {
	limits = normalizeNarrativeLimits(limits)
	if threadID == "" || sourceWindowID == "" ||
		authorityDigest == "" || routeDigest == "" {
		return NarrativeInputArtifact{}, errors.New("narrative input identity is incomplete")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	type ranked struct {
		excerpt NarrativeExcerpt
		index   int
	}
	var candidates []ranked
	for index, message := range removed {
		if message.Role != provider.RoleUser &&
			message.Role != provider.RoleAssistant {
			continue
		}
		text := strings.TrimSpace(message.Text())
		if text == "" {
			continue
		}
		truncated := false
		if len(text) > limits.ExcerptMaxBytes {
			text = utf8Prefix(text, limits.ExcerptMaxBytes)
			truncated = true
		}
		id := StableMessageID(threadID, message, index)
		candidates = append(candidates, ranked{
			index: index,
			excerpt: NarrativeExcerpt{
				MessageID: id, Role: message.Role, Turn: message.Turn,
				Text: text, Digest: digestString(text),
				Truncated: truncated,
				Priority:  narrativePriority(message, text),
			},
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].excerpt.Priority != candidates[j].excerpt.Priority {
			return candidates[i].excerpt.Priority <
				candidates[j].excerpt.Priority
		}
		if candidates[i].excerpt.Turn != candidates[j].excerpt.Turn {
			return candidates[i].excerpt.Turn >
				candidates[j].excerpt.Turn
		}
		return candidates[i].excerpt.MessageID <
			candidates[j].excerpt.MessageID
	})
	artifact := NarrativeInputArtifact{
		Version: NarrativeSchemaVersion, ThreadID: threadID,
		SourceWindowID: sourceWindowID, AuthorityDigest: authorityDigest,
		RouteDigest: routeDigest, PrivacyClass: NarrativePrivacyClass,
		ExpiresAt: now.UTC().Add(ttl),
	}
	selected := make([]ranked, 0, len(candidates))
	for _, candidate := range candidates {
		trial := append(
			append([]ranked(nil), selected...),
			candidate,
		)
		sort.Slice(trial, func(i, j int) bool {
			return trial[i].index < trial[j].index
		})
		artifact.Excerpts = artifact.Excerpts[:0]
		for _, value := range trial {
			artifact.Excerpts = append(
				artifact.Excerpts,
				value.excerpt,
			)
		}
		artifact.Digest = artifact.digest()
		encoded, err := json.Marshal(artifact)
		if err != nil {
			return NarrativeInputArtifact{}, err
		}
		if len(encoded) > limits.MaxInputBytes {
			continue
		}
		selected = append(selected, candidate)
	}
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].index < selected[j].index
	})
	artifact.Excerpts = artifact.Excerpts[:0]
	for _, value := range selected {
		artifact.Excerpts = append(artifact.Excerpts, value.excerpt)
	}
	artifact.Digest = artifact.digest()
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return NarrativeInputArtifact{}, err
	}
	if len(encoded) > limits.MaxInputBytes {
		return NarrativeInputArtifact{},
			errors.New("narrative input metadata exceeds byte limit")
	}
	return artifact, artifact.Validate(now)
}

func RebindNarrativeInput(
	previous NarrativeInputArtifact,
	sourceWindowID string,
	authorityDigest string,
	routeDigest string,
	limits NarrativeLimits,
	now time.Time,
	ttl time.Duration,
) (NarrativeInputArtifact, error) {
	if err := previous.Validate(now); err != nil {
		return NarrativeInputArtifact{}, err
	}
	if sourceWindowID == "" || authorityDigest == "" || routeDigest == "" {
		return NarrativeInputArtifact{},
			errors.New("narrative input identity is incomplete")
	}
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	artifact := previous
	artifact.SourceWindowID = sourceWindowID
	artifact.AuthorityDigest = authorityDigest
	artifact.RouteDigest = routeDigest
	artifact.Excerpts = append([]NarrativeExcerpt(nil), previous.Excerpts...)
	artifact.ExpiresAt = now.UTC().Add(ttl)
	artifact.Digest = artifact.digest()
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return NarrativeInputArtifact{}, err
	}
	if len(encoded) > normalizeNarrativeLimits(limits).MaxInputBytes {
		return NarrativeInputArtifact{},
			errors.New("narrative input metadata exceeds byte limit")
	}
	return artifact, artifact.Validate(now)
}

func (a NarrativeInputArtifact) Validate(now time.Time) error {
	if a.Version != NarrativeSchemaVersion || a.ThreadID == "" ||
		a.SourceWindowID == "" || a.AuthorityDigest == "" ||
		a.RouteDigest == "" ||
		a.PrivacyClass != NarrativePrivacyClass ||
		a.Digest == "" || a.Digest != a.digest() ||
		a.ExpiresAt.IsZero() {
		return errors.New("narrative input artifact identity or digest is invalid")
	}
	if !now.IsZero() && !a.ExpiresAt.After(now) {
		return errors.New("narrative input artifact is stale")
	}
	seen := make(map[string]struct{}, len(a.Excerpts))
	for _, excerpt := range a.Excerpts {
		if excerpt.MessageID == "" || excerpt.Text == "" ||
			excerpt.Digest != digestString(excerpt.Text) ||
			!utf8.ValidString(excerpt.Text) ||
			excerpt.Role != provider.RoleUser &&
				excerpt.Role != provider.RoleAssistant {
			return errors.New("narrative excerpt is invalid")
		}
		if _, duplicate := seen[excerpt.MessageID]; duplicate {
			return errors.New("narrative excerpt source is duplicated")
		}
		seen[excerpt.MessageID] = struct{}{}
	}
	return nil
}

func (a NarrativeInputArtifact) digest() string {
	a.Digest = ""
	encoded, _ := json.Marshal(a)
	return digestString(string(encoded))
}

func ValidateNarrativeJSON(
	raw []byte,
	input NarrativeInputArtifact,
	limits NarrativeLimits,
	createdTurn uint64,
	now time.Time,
) (NarrativeArtifact, error) {
	if err := input.Validate(now); err != nil {
		return NarrativeArtifact{}, err
	}
	limits = normalizeNarrativeLimits(limits)
	if len(raw) == 0 || len(raw) > limits.MaxOutputBytes ||
		!utf8.Valid(raw) {
		return NarrativeArtifact{}, errors.New("narrative output size or encoding is invalid")
	}
	var payload struct {
		Decisions   *[]narrativeJSONItem `json:"decisions"`
		Rationale   *[]narrativeJSONItem `json:"rationale"`
		Preferences *[]narrativeJSONItem `json:"preferences"`
		Unresolved  *[]narrativeJSONItem `json:"unresolved"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&payload); err != nil {
		return NarrativeArtifact{}, fmt.Errorf("decode narrative: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return NarrativeArtifact{}, errors.New("narrative has trailing JSON")
		}
		return NarrativeArtifact{}, err
	}
	if payload.Decisions == nil || payload.Rationale == nil ||
		payload.Preferences == nil || payload.Unresolved == nil {
		return NarrativeArtifact{},
			errors.New("narrative output is missing a required array")
	}
	sources := make(map[string]NarrativeExcerpt, len(input.Excerpts))
	for _, excerpt := range input.Excerpts {
		sources[excerpt.MessageID] = excerpt
	}
	var items []NarrativeItem
	appendItems := func(kind string, values []narrativeJSONItem) error {
		for _, value := range values {
			text := strings.TrimSpace(value.Text)
			if text == "" || len(text) > limits.ItemMaxBytes ||
				!utf8.ValidString(text) || len(value.SourceMessageIDs) == 0 {
				return errors.New("narrative item is invalid")
			}
			ids := append([]string(nil), value.SourceMessageIDs...)
			sort.Strings(ids)
			ids = deduplicateStrings(ids)
			var sourceDigests []string
			for _, id := range ids {
				source, ok := sources[id]
				if !ok {
					return fmt.Errorf("narrative source %q is unknown", id)
				}
				sourceDigests = append(sourceDigests, source.Digest)
			}
			sourceDigest := digestString(strings.Join(sourceDigests, "\x00"))
			item := NarrativeItem{
				Kind: kind, Text: text, SourceMessageIDs: ids,
				SourceDigest: sourceDigest, CreatedTurn: createdTurn,
			}
			item.ID = stableNarrativeItemID(item)
			items = append(items, item)
			if len(items) > limits.MaxItems {
				return errors.New("narrative item count exceeds limit")
			}
		}
		return nil
	}
	for _, values := range []struct {
		kind  string
		items []narrativeJSONItem
	}{
		{NarrativeDecision, *payload.Decisions},
		{NarrativeRationale, *payload.Rationale},
		{NarrativePreference, *payload.Preferences},
		{NarrativeUnresolved, *payload.Unresolved},
	} {
		if err := appendItems(values.kind, values.items); err != nil {
			return NarrativeArtifact{}, err
		}
	}
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if _, duplicate := seen[item.ID]; duplicate {
			return NarrativeArtifact{}, errors.New("narrative item is duplicated")
		}
		seen[item.ID] = struct{}{}
	}
	artifact := NarrativeArtifact{
		Version: NarrativeSchemaVersion, ThreadID: input.ThreadID,
		WindowID:        input.SourceWindowID,
		AuthorityDigest: input.AuthorityDigest,
		InputDigest:     input.Digest, RouteDigest: input.RouteDigest,
		Body: Narrative{Items: items}, CreatedAt: now.UTC(),
		ExpiresAt: input.ExpiresAt,
	}
	artifact.Digest = artifact.digest()
	return artifact, artifact.Validate(now)
}

type narrativeJSONItem struct {
	Text             string   `json:"text"`
	SourceMessageIDs []string `json:"source_message_ids"`
}

func (a NarrativeArtifact) Validate(now time.Time) error {
	if a.Version != NarrativeSchemaVersion || a.ThreadID == "" ||
		a.WindowID == "" || a.AuthorityDigest == "" ||
		a.InputDigest == "" || a.RouteDigest == "" ||
		a.CreatedAt.IsZero() || a.ExpiresAt.IsZero() ||
		a.ExpiresAt.Before(a.CreatedAt) ||
		a.Digest == "" || a.Digest != a.digest() {
		return errors.New("narrative artifact identity or digest is invalid")
	}
	if !now.IsZero() && !a.ExpiresAt.After(now) {
		return errors.New("narrative artifact is stale")
	}
	for _, item := range a.Body.Items {
		if !validNarrativeKind(item.Kind) || item.ID == "" ||
			item.ID != stableNarrativeItemID(item) ||
			item.Text == "" || len(item.SourceMessageIDs) == 0 ||
			item.SourceDigest == "" ||
			!sort.StringsAreSorted(item.SourceMessageIDs) {
			return errors.New("narrative artifact item is invalid")
		}
		for index := 1; index < len(item.SourceMessageIDs); index++ {
			if item.SourceMessageIDs[index] ==
				item.SourceMessageIDs[index-1] {
				return errors.New("narrative artifact source is duplicated")
			}
		}
	}
	return nil
}

func (a NarrativeArtifact) digest() string {
	a.Digest = ""
	encoded, _ := json.Marshal(a)
	return digestString(string(encoded))
}

func (c *CompactedContext) Seal() error {
	if c == nil {
		return errors.New("compacted context is nil")
	}
	c.Version = NarrativeSchemaVersion
	authority, err := c.Truth.AuthorityDigest()
	if err != nil {
		return err
	}
	c.AuthorityDigest = authority
	if c.Narrative != nil {
		if err := c.Narrative.Validate(time.Time{}); err != nil {
			return err
		}
		if c.Narrative.AuthorityDigest != authority ||
			c.Narrative.WindowID != c.SourceWindowID {
			return errors.New("narrative artifact fence does not match compacted context")
		}
		c.NarrativeDigest = c.Narrative.Digest
	} else {
		c.NarrativeDigest = ""
	}
	c.Tail = CloneMessages(c.Tail)
	if len(c.TailTurns) == 0 {
		c.TailTurns = make([]uint64, len(c.Tail))
		for index, message := range c.Tail {
			c.TailTurns[index] = message.Turn
		}
	} else {
		c.TailTurns = append([]uint64(nil), c.TailTurns...)
		if len(c.TailTurns) != len(c.Tail) {
			return errors.New("compacted context tail turn count is invalid")
		}
		for index, turn := range c.TailTurns {
			c.Tail[index].Turn = turn
		}
	}
	encodedTail, _ := json.Marshal(c.Tail)
	c.TailDigest = digestString(string(encodedTail))
	c.Digest = c.digest()
	return c.Validate()
}

func (c CompactedContext) Validate() error {
	if c.Version != NarrativeSchemaVersion || c.CompactionID == "" ||
		c.ThreadID == "" || c.TurnID == "" || c.SourceWindowID == "" ||
		c.TargetWindowID == "" || c.SourceContextDigest == "" ||
		c.StablePrefixDigest == "" || c.AuthorityDigest == "" ||
		c.TailDigest == "" || c.Digest == "" || c.Digest != c.digest() {
		return errors.New("compacted context identity or digest is invalid")
	}
	if len(c.TailTurns) != len(c.Tail) {
		return errors.New("compacted context tail turn count is invalid")
	}
	if err := c.Truth.Validate(); err != nil {
		return err
	}
	authorityDigest, err := c.Truth.AuthorityDigest()
	if err != nil || c.AuthorityDigest != authorityDigest {
		return errors.New("compacted context authority digest is invalid")
	}
	if c.Narrative == nil {
		if c.NarrativeDigest != "" {
			return errors.New("compacted context narrative digest is unexpected")
		}
	} else {
		if err := c.Narrative.Validate(time.Time{}); err != nil {
			return err
		}
		if c.NarrativeDigest != c.Narrative.Digest ||
			c.Narrative.AuthorityDigest != c.AuthorityDigest ||
			c.Narrative.WindowID != c.SourceWindowID {
			return errors.New("compacted context narrative fence is invalid")
		}
	}
	encodedTail, err := json.Marshal(c.Tail)
	if err != nil || c.TailDigest != digestString(string(encodedTail)) {
		return errors.New("compacted context tail digest is invalid")
	}
	if !toolPairsClosedForContext(c.Tail) {
		return errors.New("compacted context tail has an open tool pair")
	}
	return nil
}

func (c CompactedContext) digest() string {
	c.Digest = ""
	encoded, _ := json.Marshal(c)
	return digestString(string(encoded))
}

func StableMessageID(
	threadID protocol.ThreadID,
	message provider.Message,
	index int,
) string {
	turn := message.Turn
	message.Turn = 0
	encoded, _ := json.Marshal(message)
	sum := sha256.Sum256([]byte(
		string(threadID) + "\x00" +
			fmt.Sprint(turn) + "\x00" +
			fmt.Sprint(index) + "\x00" +
			string(message.Role) + "\x00" +
			digestString(string(encoded)),
	))
	return "msg_" + hex.EncodeToString(sum[:16])
}

func stableNarrativeItemID(item NarrativeItem) string {
	ids := append([]string(nil), item.SourceMessageIDs...)
	sort.Strings(ids)
	sum := sha256.Sum256([]byte(
		item.Kind + "\x00" +
			strings.Join(strings.Fields(item.Text), " ") + "\x00" +
			strings.Join(ids, "\x00"),
	))
	return "narr_" + hex.EncodeToString(sum[:16])
}

func validNarrativeKind(kind string) bool {
	return kind == NarrativeDecision || kind == NarrativeRationale ||
		kind == NarrativePreference || kind == NarrativeUnresolved
}

func normalizeNarrativeLimits(limits NarrativeLimits) NarrativeLimits {
	defaults := DefaultNarrativeLimits()
	if limits.MaxInputBytes <= 0 {
		limits.MaxInputBytes = defaults.MaxInputBytes
	}
	if limits.MaxOutputBytes <= 0 {
		limits.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if limits.MaxItems <= 0 {
		limits.MaxItems = defaults.MaxItems
	}
	if limits.ItemMaxBytes <= 0 {
		limits.ItemMaxBytes = defaults.ItemMaxBytes
	}
	if limits.ExcerptMaxBytes <= 0 {
		limits.ExcerptMaxBytes = defaults.ExcerptMaxBytes
	}
	return limits
}

func (l NarrativeLimits) Normalized() NarrativeLimits {
	return normalizeNarrativeLimits(l)
}

func narrativePriority(message provider.Message, text string) int {
	lower := strings.ToLower(text)
	switch {
	case message.Role == provider.RoleUser &&
		(strings.Contains(lower, "must") ||
			strings.Contains(lower, "prefer")):
		return 0
	case strings.Contains(lower, "decision") ||
		strings.Contains(lower, "because"):
		return 1
	case strings.Contains(lower, "unresolved") ||
		strings.Contains(lower, "todo"):
		return 2
	case message.Role == provider.RoleUser:
		return 3
	case message.Role == provider.RoleAssistant:
		return 5
	default:
		return 6
	}
}

func utf8Prefix(value string, maximum int) string {
	if maximum <= 0 || len(value) <= maximum {
		return value
	}
	value = value[:maximum]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}

func digestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func deduplicateStrings(values []string) []string {
	if len(values) < 2 {
		return values
	}
	result := values[:1]
	for _, value := range values[1:] {
		if value != result[len(result)-1] {
			result = append(result, value)
		}
	}
	return result
}

func toolPairsClosedForContext(messages []provider.Message) bool {
	calls := make(map[string]int)
	results := make(map[string]int)
	for _, message := range messages {
		for _, block := range message.Blocks {
			if block.ToolCall != nil {
				calls[block.ToolCall.ID]++
			}
			if block.ToolResult != nil {
				results[block.ToolResult.CallID]++
			}
		}
	}
	if len(calls) != len(results) {
		return false
	}
	for id, count := range calls {
		if count != 1 || results[id] != 1 {
			return false
		}
	}
	return true
}
