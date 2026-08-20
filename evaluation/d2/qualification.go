package d2

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var requiredQualificationChecks = []string{
	"adaptive-declaration-negative",
	"bounded-budget-negative",
	"campaign-schema",
	"campaign-semantics",
	"cleanup-negative",
	"empty-axis-negative",
	"input-identity",
	"mixed-identity-negative",
	"planner-determinism",
	"privacy-negative",
	"unknown-field-negative",
	"zero-case-negative",
}

type QualificationReport struct {
	SchemaVersion         int                  `json:"schema_version"`
	ID                    string               `json:"id"`
	DiscoveryLockIdentity string               `json:"discovery_lock_identity"`
	Status                string               `json:"status"`
	Scheduled             int                  `json:"scheduled"`
	Settled               int                  `json:"settled"`
	Passed                int                  `json:"passed"`
	Failed                int                  `json:"failed"`
	Invalid               int                  `json:"invalid"`
	Checks                []QualificationCheck `json:"checks"`
	EvidenceDigest        string               `json:"evidence_digest"`
}

type QualificationCheck struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	EvidenceDigest string `json:"evidence_digest"`
}

func RunQualification(
	root, id string,
	bundle CampaignBundle,
	plan Plan,
	lock DiscoveryLock,
) (QualificationReport, error) {
	if !validID(id) {
		return QualificationReport{}, errors.New("D2 qualification ID is invalid")
	}
	checks := []struct {
		id  string
		run func() error
	}{
		{"campaign-schema", func() error {
			if err := validateSchemaFile(
				root,
				"evaluation/schema/discovery-campaign.schema.json",
				bundle.Raw,
			); err != nil {
				return err
			}
			planRaw, err := json.Marshal(plan)
			if err != nil {
				return err
			}
			if err := validateSchemaFile(
				root,
				"evaluation/schema/discovery-plan.schema.json",
				planRaw,
			); err != nil {
				return err
			}
			raw, err := json.Marshal(lock)
			if err != nil {
				return err
			}
			return validateSchemaFile(
				root,
				"evaluation/schema/discovery-lock.schema.json",
				raw,
			)
		}},
		{"campaign-semantics", bundle.Campaign.Validate},
		{"planner-determinism", func() error {
			first, err := BuildPlan(bundle.Campaign)
			if err != nil {
				return err
			}
			second, err := BuildPlan(bundle.Campaign)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(first, second) ||
				first.EvidenceDigest != plan.EvidenceDigest {
				return errors.New("D2 planner is not deterministic")
			}
			return nil
		}},
		{"unknown-field-negative", func() error {
			var value map[string]any
			if err := json.Unmarshal(bundle.Raw, &value); err != nil {
				return err
			}
			value["undeclared"] = true
			raw, err := json.Marshal(value)
			if err != nil {
				return err
			}
			var campaign Campaign
			return expectRejected(decodeStrict(raw, &campaign))
		}},
		{"empty-axis-negative", func() error {
			campaign := cloneCampaign(bundle.Campaign)
			campaign.Axes = nil
			return expectRejected(campaign.Validate())
		}},
		{"zero-case-negative", func() error {
			campaign := cloneCampaign(bundle.Campaign)
			campaign.Budgets.MaxRuns = 0
			return expectRejected(campaign.Validate())
		}},
		{"mixed-identity-negative", func() error {
			drifted := lock
			drifted.RuntimeDigest = spec.DigestString("drifted-runtime")
			return expectRejected(drifted.Validate())
		}},
		{"adaptive-declaration-negative", func() error {
			campaign := cloneCampaign(bundle.Campaign)
			campaign.Adaptive = true
			campaign.AdaptivePolicy = ""
			return expectRejected(campaign.Validate())
		}},
		{"cleanup-negative", func() error {
			campaign := cloneCampaign(bundle.Campaign)
			campaign.Cleanup.Resources = nil
			return expectRejected(campaign.Validate())
		}},
		{"bounded-budget-negative", func() error {
			campaign := cloneCampaign(bundle.Campaign)
			campaign.Budgets.WallTimeMS = 86_400_001
			return expectRejected(campaign.Validate())
		}},
		{"privacy-negative", func() error {
			campaign := cloneCampaign(bundle.Campaign)
			campaign.Privacy.PersistUserContent = true
			return expectRejected(campaign.Validate())
		}},
		{"input-identity", func() error {
			_, err := VerifyDiscoveryInputs(root, lock)
			return err
		}},
	}
	report := QualificationReport{
		SchemaVersion:         SchemaVersion,
		ID:                    id,
		DiscoveryLockIdentity: lock.LockIdentity,
		Status:                "passed",
		Scheduled:             len(checks),
	}
	for _, item := range checks {
		result := QualificationCheck{ID: item.id, Status: "passed"}
		err := item.run()
		if err != nil {
			result.Status = "failed"
			report.Status = "failed"
			report.Failed++
			result.EvidenceDigest = spec.DigestString(
				item.id + "\x00failed\x00" + sanitizeError(err),
			)
		} else {
			report.Passed++
			result.EvidenceDigest = spec.DigestString(
				item.id + "\x00passed\x00" + lock.LockIdentity,
			)
		}
		report.Checks = append(report.Checks, result)
		report.Settled++
	}
	slices.SortFunc(report.Checks, func(left, right QualificationCheck) int {
		return strings.Compare(left.ID, right.ID)
	})
	report.EvidenceDigest = digestQualification(report)
	return report, report.Validate()
}

func (r QualificationReport) Validate() error {
	if r.SchemaVersion != SchemaVersion || !validID(r.ID) ||
		!validDigest(r.DiscoveryLockIdentity) ||
		(r.Status != "passed" && r.Status != "failed" && r.Status != "invalid") ||
		r.Scheduled < 1 || r.Settled != r.Scheduled ||
		len(r.Checks) != r.Scheduled ||
		r.Passed+r.Failed+r.Invalid != r.Settled ||
		!validDigest(r.EvidenceDigest) {
		return errors.New("D2 qualification inventory is invalid")
	}
	ids := make([]string, 0, len(r.Checks))
	seen := make(map[string]struct{}, len(r.Checks))
	for _, check := range r.Checks {
		if !validID(check.ID) ||
			(check.Status != "passed" &&
				check.Status != "failed" &&
				check.Status != "invalid") ||
			!validDigest(check.EvidenceDigest) {
			return fmt.Errorf("D2 qualification check %q is invalid", check.ID)
		}
		if _, duplicate := seen[check.ID]; duplicate {
			return fmt.Errorf("duplicate D2 qualification check %q", check.ID)
		}
		seen[check.ID] = struct{}{}
		ids = append(ids, check.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, requiredQualificationChecks) {
		return errors.New("D2 qualification check inventory is incomplete")
	}
	if r.Status == "passed" && (r.Passed != r.Scheduled ||
		r.Failed != 0 || r.Invalid != 0) {
		return errors.New("passed D2 qualification has non-passing checks")
	}
	if r.EvidenceDigest != digestQualification(r) {
		return errors.New("D2 qualification digest does not match report")
	}
	return nil
}

func QualifyDiscoveryLock(
	lock DiscoveryLock,
	report QualificationReport,
) (DiscoveryLock, error) {
	if err := lock.Validate(); err != nil {
		return DiscoveryLock{}, err
	}
	if err := report.Validate(); err != nil {
		return DiscoveryLock{}, err
	}
	if lock.Status != "candidate" ||
		report.Status != "passed" ||
		report.DiscoveryLockIdentity != lock.LockIdentity {
		return DiscoveryLock{}, errors.New(
			"D2 qualification does not match candidate Discovery Lock",
		)
	}
	lock.Status = "qualified"
	lock.QualificationDigest = report.EvidenceDigest
	return lock, lock.Validate()
}

func digestQualification(report QualificationReport) string {
	report.EvidenceDigest = ""
	raw, _ := json.Marshal(report)
	return spec.DigestString(string(raw))
}

func validateSchemaFile(root, schemaPath string, raw []byte) error {
	path := schemaPath
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(schemaPath))
	}
	schemaRaw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var schemaValue any
	if err := json.Unmarshal(schemaRaw, &schemaValue); err != nil {
		return err
	}
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	resource := filepath.Base(path)
	if err := compiler.AddResource(resource, schemaValue); err != nil {
		return err
	}
	observationResource := "discovery-observation.schema.json"
	if resource != observationResource {
		observationRaw, readErr := os.ReadFile(
			filepath.Join(filepath.Dir(path), observationResource),
		)
		if readErr == nil {
			var observationValue any
			if err := json.Unmarshal(observationRaw, &observationValue); err != nil {
				return err
			}
			if err := compiler.AddResource(
				"https://codehelper.dev/schemas/"+observationResource,
				observationValue,
			); err != nil {
				return err
			}
		}
	}
	compiled, err := compiler.Compile(resource)
	if err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	return compiled.Validate(value)
}

func ValidateSchema(root, schemaPath string, raw []byte) error {
	return validateSchemaFile(root, schemaPath, raw)
}

func expectRejected(err error) error {
	if err == nil {
		return errors.New("D2 negative control was accepted")
	}
	return nil
}

func cloneCampaign(campaign Campaign) Campaign {
	raw, _ := json.Marshal(campaign)
	var clone Campaign
	_ = json.Unmarshal(raw, &clone)
	return clone
}

func sanitizeError(err error) string {
	value := err.Error()
	if len(value) > 160 {
		value = value[:160]
	}
	var output strings.Builder
	for _, character := range value {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '-',
			character == '_',
			character == ' ',
			character == ':':
			output.WriteRune(character)
		default:
			output.WriteByte('_')
		}
	}
	return output.String()
}
