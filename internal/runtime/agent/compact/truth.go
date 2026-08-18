package compact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

const (
	TruthSchemaVersion        = 1
	TruthMarkerStart          = "<codehelper_truth_capsule>"
	TruthMarkerEnd            = "</codehelper_truth_capsule>"
	DownshiftRuntimeTruthOnly = "runtime_truth_only"

	EntityGoal          = "goal"
	EntityTodo          = "todo"
	EntityFailure       = "failure"
	EntityChange        = "change"
	EntityCriticalPath  = "critical_path"
	EntityFact          = "fact"
	EntityPendingInput  = "pending_input"
	EntityContentHandle = "content_handle"
)

var entityKinds = map[string]struct{}{
	EntityGoal: {}, EntityTodo: {}, EntityFailure: {}, EntityChange: {},
	EntityCriticalPath: {}, EntityFact: {}, EntityPendingInput: {},
	EntityContentHandle: {},
}

type TruthEntity struct {
	ID                 string `json:"i"`
	Kind               string `json:"k"`
	Key                string `json:"x"`
	Value              string `json:"v"`
	Source             string `json:"s"`
	Status             string `json:"a,omitempty"`
	Turn               uint64 `json:"t,omitempty"`
	Count              int    `json:"n,omitempty"`
	Read               bool   `json:"r,omitempty"`
	Verified           bool   `json:"q,omitempty"`
	Diagnostics        bool   `json:"f,omitempty"`
	Consumed           bool   `json:"o,omitempty"`
	VerificationSource string `json:"z,omitempty"`
}

func NewTruthEntity(
	kind string,
	key string,
	value string,
	source string,
) TruthEntity {
	key = strings.TrimSpace(key)
	return TruthEntity{
		ID: StableEntityID(kind, key), Kind: kind, Key: key,
		Value: strings.TrimSpace(value), Source: strings.TrimSpace(source),
	}
}

func StableEntityID(kind, key string) string {
	sum := sha256.Sum256([]byte(kind + "\x00" + strings.TrimSpace(key)))
	return kind + ":" + hex.EncodeToString(sum[:12])
}

func (e TruthEntity) Validate() error {
	if _, ok := entityKinds[e.Kind]; !ok {
		return fmt.Errorf("unknown truth entity kind %q", e.Kind)
	}
	if e.Key == "" || e.Value == "" || e.Source == "" ||
		e.ID != StableEntityID(e.Kind, e.Key) {
		return fmt.Errorf("invalid %s truth entity identity", e.Kind)
	}
	if e.Verified && e.VerificationSource != "runtime.evidence" {
		return errors.New("verified truth requires runtime evidence")
	}
	if !e.Verified && e.VerificationSource != "" {
		return errors.New("unverified truth cannot name verification evidence")
	}
	return nil
}

type TruthCapsule struct {
	SchemaVersion     int           `json:"v"`
	Generation        uint64        `json:"g"`
	CompatibilityHash string        `json:"c"`
	ModelID           string        `json:"m"`
	ContextTokens     uint64        `json:"w"`
	DownshiftPolicy   string        `json:"p"`
	Entities          []TruthEntity `json:"e,omitempty"`
	Digest            string        `json:"d"`
}

func (c *TruthCapsule) Seal() {
	entities := make(map[string]TruthEntity, len(c.Entities))
	for _, entity := range c.Entities {
		entities[entity.ID] = entity
	}
	c.Entities = c.Entities[:0]
	for _, entity := range entities {
		c.Entities = append(c.Entities, entity)
	}
	sort.Slice(c.Entities, func(i, j int) bool {
		if c.Entities[i].Kind != c.Entities[j].Kind {
			return c.Entities[i].Kind < c.Entities[j].Kind
		}
		return c.Entities[i].ID < c.Entities[j].ID
	})
	c.Digest = c.digest()
}

func (c TruthCapsule) Validate() error {
	if c.SchemaVersion != TruthSchemaVersion || c.Generation == 0 ||
		c.CompatibilityHash == "" || c.ModelID == "" ||
		c.DownshiftPolicy != DownshiftRuntimeTruthOnly ||
		c.Digest == "" || c.Digest != c.digest() {
		return errors.New("invalid truth capsule header or digest")
	}
	seen := make(map[string]struct{}, len(c.Entities))
	for _, entity := range c.Entities {
		if err := entity.Validate(); err != nil {
			return err
		}
		if _, exists := seen[entity.ID]; exists {
			return fmt.Errorf("duplicate truth entity %q", entity.ID)
		}
		seen[entity.ID] = struct{}{}
	}
	return nil
}

func (c TruthCapsule) digest() string {
	c.Digest = ""
	encoded, _ := json.Marshal(c)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

type Compatibility struct {
	SchemaVersion    int    `json:"schema_version"`
	Adapter          string `json:"adapter"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	ContextTokens    uint64 `json:"context_tokens"`
	ToolCalls        bool   `json:"tool_calls"`
	Reasoning        bool   `json:"reasoning"`
	ImageInput       bool   `json:"image_input"`
	SummaryMaxBytes  int    `json:"summary_max_bytes"`
	MaxDigestEntries int    `json:"max_digest_entries"`
	DownshiftPolicy  string `json:"downshift_policy"`
}

func (c Compatibility) Hash() string {
	encoded, _ := json.Marshal(c)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

type MergeReceipt struct {
	Generation           uint64
	PreviousCapsules     int
	CompatibilityMatched bool
	ModelDownshifted     bool
	EntityCount          int
	CriticalEntityCount  int
}

func MergeTruthCapsules(
	current TruthCapsule,
	previous ...TruthCapsule,
) (TruthCapsule, MergeReceipt, error) {
	if err := current.Validate(); err != nil {
		return TruthCapsule{}, MergeReceipt{}, err
	}
	current.Entities = append([]TruthEntity(nil), current.Entities...)
	entities := make(map[string]TruthEntity)
	maxGeneration := uint64(0)
	receipt := MergeReceipt{
		PreviousCapsules: len(previous), CompatibilityMatched: true,
	}
	for _, capsule := range previous {
		if err := capsule.Validate(); err != nil {
			return TruthCapsule{}, MergeReceipt{}, err
		}
		maxGeneration = max(maxGeneration, capsule.Generation)
		if capsule.CompatibilityHash != current.CompatibilityHash {
			receipt.CompatibilityMatched = false
		}
		if capsule.ContextTokens > current.ContextTokens {
			receipt.ModelDownshifted = true
		}
		for _, entity := range capsule.Entities {
			entities[entity.ID] = entity
		}
	}
	for _, entity := range current.Entities {
		entities[entity.ID] = entity
	}
	current.Generation = max(1, maxGeneration+1)
	current.Entities = current.Entities[:0]
	for _, entity := range entities {
		current.Entities = append(current.Entities, entity)
		if entity.Kind == EntityFact || entity.Kind == EntityCriticalPath {
			receipt.CriticalEntityCount++
		}
	}
	sort.Slice(current.Entities, func(i, j int) bool {
		if current.Entities[i].Kind != current.Entities[j].Kind {
			return current.Entities[i].Kind < current.Entities[j].Kind
		}
		return current.Entities[i].ID < current.Entities[j].ID
	})
	current.Seal()
	receipt.Generation = current.Generation
	receipt.EntityCount = len(current.Entities)
	return current, receipt, nil
}

func ParseTruthCapsule(text string) (TruthCapsule, bool, error) {
	start := strings.Index(text, TruthMarkerStart)
	if start < 0 {
		return TruthCapsule{}, false, nil
	}
	rest := text[start+len(TruthMarkerStart):]
	end := strings.Index(rest, TruthMarkerEnd)
	if end < 0 {
		return TruthCapsule{}, true, errors.New("truth capsule is not closed")
	}
	var capsule TruthCapsule
	if err := json.Unmarshal([]byte(strings.TrimSpace(rest[:end])), &capsule); err != nil {
		return TruthCapsule{}, true, fmt.Errorf("decode truth capsule: %w", err)
	}
	if err := capsule.Validate(); err != nil {
		return TruthCapsule{}, true, err
	}
	return capsule, true, nil
}

type Narrative struct {
	Lines []string
}

type StructuredRender struct {
	Text              string
	Truncated         bool
	Sections          []string
	NarrativeIncluded bool
	CapsuleBytes      int
}

func RenderStructured(
	summary Summary,
	capsule TruthCapsule,
	narrative Narrative,
	budget int,
) (StructuredRender, error) {
	if err := capsule.Validate(); err != nil {
		return StructuredRender{}, err
	}
	encoded, err := json.Marshal(capsule)
	if err != nil {
		return StructuredRender{}, err
	}
	header := fmt.Sprintf(
		"Compacted truth; replaces %d message(s).\n",
		summary.Window,
	)
	truthBlock := TruthMarkerStart + "\n" + string(encoded) + "\n" + TruthMarkerEnd + "\n"
	mandatory := MarkerStart + "\n" + header + truthBlock
	minimum := len(mandatory) + len(MarkerEnd)
	if budget > 0 && minimum > budget {
		return StructuredRender{}, fmt.Errorf(
			"truth capsule requires %d bytes; summary budget is %d",
			minimum,
			budget,
		)
	}
	summary.Digest = nil
	blocks := summary.blocks()
	room := unbounded
	if budget > 0 {
		room = max(0, budget-minimum-len(truncationNotice))
	}
	var optional strings.Builder
	sections := []string{SectionTruth}
	truncated := false
	for _, block := range blocks {
		text := block.render(remaining(room, optional.Len()))
		if text == "" {
			truncated = true
			continue
		}
		optional.WriteString(text)
		sections = append(sections, block.name)
		if block.partial {
			truncated = true
		}
	}
	narrativeIncluded := false
	if len(narrative.Lines) != 0 {
		text, partial := renderNarrative(
			narrative.Lines,
			remaining(room, optional.Len()),
		)
		if text == "" {
			truncated = true
		} else {
			optional.WriteString(text)
			sections = append(sections, SectionNarrative)
			narrativeIncluded = true
			truncated = truncated || partial
		}
	}
	var output strings.Builder
	output.WriteString(mandatory)
	output.WriteString(optional.String())
	if truncated {
		output.WriteString(truncationNotice)
	}
	output.WriteString(MarkerEnd)
	return StructuredRender{
		Text: output.String(), Truncated: truncated, Sections: sections,
		NarrativeIncluded: narrativeIncluded, CapsuleBytes: len(truthBlock),
	}, nil
}

func renderNarrative(lines []string, room int) (string, bool) {
	header := "Narrative context (non-authoritative, newest first):\n"
	if room != unbounded && len(header) >= room {
		return "", true
	}
	var output strings.Builder
	output.WriteString(header)
	for index, line := range lines {
		entry := "  " + collapse(line) + "\n"
		if room != unbounded && output.Len()+len(entry) > room {
			return output.String(), index != len(lines)
		}
		output.WriteString(entry)
	}
	return output.String(), false
}
