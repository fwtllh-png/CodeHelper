package compact

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
)

// AuthorityDigest identifies authoritative runtime facts without coupling
// equivalence to model-specific rendering metadata or compaction generation.
func (c TruthCapsule) AuthorityDigest() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(c.Entities)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

// ContainsAuthority verifies that compaction retained every authoritative
// entity exactly. Additional entities from previous capsules are allowed.
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
