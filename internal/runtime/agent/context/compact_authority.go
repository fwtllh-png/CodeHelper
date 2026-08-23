package agentcontext

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

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
	}
	current.Generation = max(1, maxGeneration+1)
	for _, entity := range current.Entities {
		if entity.Kind == EntityFact || entity.Kind == EntityCriticalPath {
			receipt.CriticalEntityCount++
		}
	}
	current.Seal()
	receipt.Generation = current.Generation
	receipt.EntityCount = len(current.Entities)
	return current, receipt, nil
}

// AuthorityDigest identifies mandatory runtime facts without coupling
// equivalence to model-specific rendering metadata, optional retained facts, or
// compaction generation.
func (c TruthCapsule) AuthorityDigest() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	mandatory := make([]TruthEntity, 0, len(c.Entities))
	for _, entity := range c.Entities {
		if entity.Retention == RetentionMandatory {
			mandatory = append(mandatory, entity)
		}
	}
	encoded, err := json.Marshal(mandatory)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ContainsAuthority verifies that compaction retained every mandatory entity
// exactly. Protected and refreshable entities are governed by retention policy,
// so their omission cannot make an authority-equivalence check fail.
func (c TruthCapsule) ContainsAuthority(required TruthCapsule) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := required.Validate(); err != nil {
		return err
	}
	entities := make(map[string]TruthEntity, len(c.Entities))
	for _, entity := range c.Entities {
		entities[entity.ID] = entity
	}
	for _, entity := range required.Entities {
		if entity.Retention != RetentionMandatory {
			continue
		}
		retained, ok := entities[entity.ID]
		if !ok {
			return fmt.Errorf("compaction lost authority entity %q", entity.ID)
		}
		if retained != entity {
			return fmt.Errorf("compaction changed authority entity %q", entity.ID)
		}
	}
	return nil
}
