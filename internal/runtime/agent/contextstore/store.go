// Package contextstore owns the typed, immutable projection used for one model
// sample. Durable world-state baselines are added in later CE stages.
package contextstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"

	adaptercontent "github.com/fwtllh-png/CodeHelper/internal/adapter/content"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

type Kind string

const (
	KindStable       Kind = "stable"
	KindHistory      Kind = "history"
	KindDynamic      Kind = "dynamic"
	KindContinuation Kind = "continuation"
)

var orderedKinds = [...]Kind{
	KindStable,
	KindHistory,
	KindDynamic,
	KindContinuation,
}

// Item is one typed model-visible message in a Snapshot.
type Item struct {
	ID      string           `json:"id"`
	Kind    Kind             `json:"kind"`
	Role    provider.Role    `json:"role"`
	Message provider.Message `json:"message"`
}

// Input is the one-way adapter from the current prompt and history owners.
type Input struct {
	Stable       []provider.Message
	History      []provider.Message
	Dynamic      []provider.Message
	Continuation []provider.Message
	Definitions  []provider.ToolDefinition
}

// Projection atomically updates the mutable portions of a Ledger.
type Projection struct {
	Stable       []provider.Message
	History      []provider.Message
	Dynamic      []provider.Message
	Continuation []provider.Message
	Definitions  []provider.ToolDefinition
}

// Ledger is the sole owner of model-sample assembly within one turn.
type Ledger struct {
	revision    uint64
	partitions  map[Kind][]provider.Message
	definitions []provider.ToolDefinition
}

// Snapshot is an immutable model-sample projection.
type Snapshot struct {
	revision    uint64
	partitions  map[Kind][]provider.Message
	definitions []provider.ToolDefinition
	items       []Item
}

func New(input Input) *Ledger {
	return &Ledger{
		revision: 1,
		partitions: map[Kind][]provider.Message{
			KindStable:       CloneMessages(input.Stable),
			KindHistory:      CloneMessages(input.History),
			KindDynamic:      CloneMessages(input.Dynamic),
			KindContinuation: CloneMessages(input.Continuation),
		},
		definitions: cloneDefinitions(input.Definitions),
	}
}

// Project replaces all mutable partitions as one revision.
func (l *Ledger) Project(value Projection) Snapshot {
	changed := l.replace(KindStable, value.Stable)
	changed = l.replace(KindHistory, value.History) || changed
	changed = l.replace(KindDynamic, value.Dynamic) || changed
	changed = l.replace(KindContinuation, value.Continuation) || changed
	if !reflect.DeepEqual(l.definitions, value.Definitions) {
		l.definitions = cloneDefinitions(value.Definitions)
		changed = true
	}
	if changed {
		l.revision++
	}
	return l.Snapshot()
}

func (l *Ledger) Snapshot() Snapshot {
	partitions := make(map[Kind][]provider.Message, len(orderedKinds))
	var items []Item
	for _, kind := range orderedKinds {
		partitions[kind] = CloneMessages(l.partitions[kind])
		occurrences := make(map[string]int)
		for _, message := range partitions[kind] {
			id := itemID(kind, message)
			occurrence := occurrences[id]
			occurrences[id]++
			if occurrence != 0 {
				id = fmt.Sprintf("%s_%d", id, occurrence)
			}
			items = append(items, Item{
				ID: id, Kind: kind,
				Role: message.Role, Message: CloneMessage(message),
			})
		}
	}
	return Snapshot{
		revision: l.revision, partitions: partitions,
		definitions: cloneDefinitions(l.definitions), items: items,
	}
}

func (s Snapshot) Revision() uint64 {
	return s.revision
}

func (s Snapshot) Items() []Item {
	result := make([]Item, len(s.items))
	for index, item := range s.items {
		result[index] = item
		result[index].Message = CloneMessage(item.Message)
	}
	return result
}

func (s Snapshot) Partition(kind Kind) []provider.Message {
	return CloneMessages(s.partitions[kind])
}

func (s Snapshot) Definitions() []provider.ToolDefinition {
	return cloneDefinitions(s.definitions)
}

func (s Snapshot) Messages() []provider.Message {
	var result []provider.Message
	for _, kind := range orderedKinds {
		result = append(result, CloneMessages(s.partitions[kind])...)
	}
	return result
}

// Digest identifies the complete model-visible message and definition content.
func (s Snapshot) Digest() (string, error) {
	encoded, err := json.Marshal(struct {
		Messages    []provider.Message        `json:"messages"`
		Definitions []provider.ToolDefinition `json:"definitions,omitempty"`
	}{
		Messages: s.Messages(), Definitions: s.Definitions(),
	})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// WithHistory returns an ephemeral rewrite used while measuring compaction.
func (s Snapshot) WithHistory(history []provider.Message) Snapshot {
	if reflect.DeepEqual(s.partitions[KindHistory], history) {
		return s
	}
	partitions := make(map[Kind][]provider.Message, len(s.partitions))
	for _, kind := range orderedKinds {
		partitions[kind] = CloneMessages(s.partitions[kind])
	}
	partitions[KindHistory] = CloneMessages(history)
	ledger := &Ledger{
		revision: s.revision + 1, partitions: partitions,
		definitions: cloneDefinitions(s.definitions),
	}
	return ledger.Snapshot()
}

func (l *Ledger) replace(kind Kind, messages []provider.Message) bool {
	if reflect.DeepEqual(l.partitions[kind], messages) {
		return false
	}
	l.partitions[kind] = CloneMessages(messages)
	return true
}

func itemID(kind Kind, message provider.Message) string {
	encoded, _ := json.Marshal(struct {
		Kind    Kind             `json:"kind"`
		Turn    uint64           `json:"turn"`
		Message provider.Message `json:"message"`
	}{
		Kind: kind, Turn: message.Turn, Message: message,
	})
	sum := sha256.Sum256(encoded)
	return "ctx_" + hex.EncodeToString(sum[:])
}

// CloneMessages isolates mutable nested provider content.
func CloneMessages(messages []provider.Message) []provider.Message {
	if messages == nil {
		return nil
	}
	result := make([]provider.Message, len(messages))
	for index, message := range messages {
		result[index] = CloneMessage(message)
	}
	return result
}

// CloneMessage isolates one provider message.
func CloneMessage(message provider.Message) provider.Message {
	result := message
	result.Blocks = CloneBlocks(message.Blocks)
	if message.Provenance != nil {
		value := *message.Provenance
		if message.Provenance.Replay != nil {
			replay := *message.Provenance.Replay
			replay.Data = append([]byte(nil), message.Provenance.Replay.Data...)
			value.Replay = &replay
		}
		result.Provenance = &value
	}
	return result
}

// CloneBlocks isolates nested block payloads, including attachment bytes.
func CloneBlocks(blocks []provider.ContentBlock) []provider.ContentBlock {
	if blocks == nil {
		return nil
	}
	result := make([]provider.ContentBlock, len(blocks))
	for index, block := range blocks {
		result[index] = block
		if block.ToolCall != nil {
			value := *block.ToolCall
			result[index].ToolCall = &value
		}
		if block.ToolResult != nil {
			value := *block.ToolResult
			value.Admission = adaptercontent.CloneAdmissionReceipt(
				block.ToolResult.Admission,
			)
			result[index].ToolResult = &value
		}
		if block.Search != nil {
			value := *block.Search
			value.Sources = append([]provider.Source(nil), block.Search.Sources...)
			result[index].Search = &value
		}
		if block.Citation != nil {
			value := *block.Citation
			result[index].Citation = &value
		}
		if block.Attachment != nil {
			value := *block.Attachment
			value.Data = append([]byte(nil), block.Attachment.Data...)
			result[index].Attachment = &value
		}
	}
	return result
}

func cloneDefinitions(
	definitions []provider.ToolDefinition,
) []provider.ToolDefinition {
	if definitions == nil {
		return nil
	}
	result := make([]provider.ToolDefinition, len(definitions))
	for index, definition := range definitions {
		result[index] = definition
		result[index].InputSchema = cloneMap(definition.InputSchema)
	}
	return result
}

func cloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = cloneValue(item)
	}
	return result
}

func cloneValue(value any) any {
	switch item := value.(type) {
	case map[string]any:
		return cloneMap(item)
	case []any:
		result := make([]any, len(item))
		for index, child := range item {
			result[index] = cloneValue(child)
		}
		return result
	case []string:
		return append([]string(nil), item...)
	case json.RawMessage:
		return append(json.RawMessage(nil), item...)
	default:
		return value
	}
}
