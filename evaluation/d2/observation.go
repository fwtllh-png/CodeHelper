package d2

import (
	"errors"
	"fmt"
	"slices"
	"time"
)

type Observation struct {
	SchemaVersion          int       `json:"schema_version"`
	ID                     string    `json:"id"`
	CampaignID             string    `json:"campaign_id"`
	CaseID                 string    `json:"case_id"`
	DiscoveryLockIdentity  string    `json:"discovery_lock_identity"`
	EnvironmentDigest      string    `json:"environment_digest"`
	Producer               string    `json:"producer"`
	Classification         string    `json:"classification"`
	Severity               string    `json:"severity"`
	Reproducibility        string    `json:"reproducibility"`
	Attempts               int       `json:"attempts"`
	EvidenceDigests        []string  `json:"evidence_digests"`
	FirstObservedAt        time.Time `json:"first_observed_at"`
	SummaryCode            string    `json:"summary_code"`
	ContainsPrivateContent bool      `json:"contains_private_content"`
}

func (o Observation) Validate() error {
	if o.SchemaVersion != SchemaVersion ||
		!validID(o.ID) ||
		!validID(o.CampaignID) ||
		!validID(o.CaseID) ||
		!validDigest(o.DiscoveryLockIdentity) ||
		!validDigest(o.EnvironmentDigest) ||
		!validID(o.Producer) ||
		!validID(o.SummaryCode) ||
		o.Attempts < 1 ||
		o.FirstObservedAt.IsZero() ||
		o.ContainsPrivateContent {
		return errors.New("D2 observation identity or privacy contract is invalid")
	}
	if !slices.Contains([]string{
		"product_candidate",
		"harness_incident",
		"environment_failure",
		"expected_variance",
		"unattributed",
	}, o.Classification) {
		return fmt.Errorf(
			"D2 observation classification %q is invalid",
			o.Classification,
		)
	}
	if !slices.Contains([]string{"p0", "p1", "p2", "p3"}, o.Severity) ||
		!slices.Contains([]string{
			"exact_seed",
			"controlled_matrix",
			"live_statistical",
			"unreproduced",
		}, o.Reproducibility) {
		return errors.New("D2 observation severity or reproducibility is invalid")
	}
	if len(o.EvidenceDigests) == 0 {
		return errors.New("D2 observation evidence is empty")
	}
	seen := make(map[string]struct{}, len(o.EvidenceDigests))
	for _, digest := range o.EvidenceDigests {
		if !validDigest(digest) {
			return errors.New("D2 observation evidence digest is invalid")
		}
		if _, duplicate := seen[digest]; duplicate {
			return errors.New("D2 observation evidence is duplicated")
		}
		seen[digest] = struct{}{}
	}
	return nil
}
