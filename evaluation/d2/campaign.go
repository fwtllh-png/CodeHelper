package d2

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

const SchemaVersion = 1

var requiredAxisIDs = []string{
	"dependency_behavior",
	"lifecycle",
	"model_variability",
	"session_state",
	"topology",
	"workload",
}

var requiredFamilyIDs = []string{
	"composed_faults",
	"concurrency_cancellation",
	"differential_host",
	"live_model_variability",
	"scale_long_tail",
	"stateful_journey",
	"upgrade_recovery",
}

type Campaign struct {
	SchemaVersion         int           `json:"schema_version"`
	ID                    string        `json:"id"`
	SelectionStrategy     string        `json:"selection_strategy"`
	Adaptive              bool          `json:"adaptive"`
	AdaptivePolicy        string        `json:"adaptive_policy,omitempty"`
	ProductionEntryPoints []string      `json:"production_entry_points"`
	Axes                  []Axis        `json:"axes"`
	Families              []Family      `json:"families"`
	Seeds                 []uint64      `json:"seeds"`
	Budgets               Budgets       `json:"budgets"`
	Cleanup               CleanupPolicy `json:"cleanup"`
	Privacy               PrivacyPolicy `json:"privacy"`
	StopPolicy            StopPolicy    `json:"stop_policy"`
}

type Axis struct {
	ID     string      `json:"id"`
	Values []AxisValue `json:"values"`
}

type AxisValue struct {
	ID           string `json:"id"`
	Boundary     bool   `json:"boundary"`
	FaultTrigger bool   `json:"fault_trigger"`
}

type Family struct {
	ID                   string        `json:"id"`
	Axes                 []string      `json:"axes"`
	RequiredCombinations []Combination `json:"required_combinations"`
}

type Combination struct {
	Values map[string]string `json:"values"`
}

type Budgets struct {
	MaxRuns                int    `json:"max_runs"`
	WallTimeMS             int64  `json:"wall_time_ms"`
	MaxModelCostMicrounits uint64 `json:"max_model_cost_microunits"`
	MaxParallelism         int    `json:"max_parallelism"`
}

type CleanupPolicy struct {
	Owner     string   `json:"owner"`
	Resources []string `json:"resources"`
	TimeoutMS int64    `json:"timeout_ms"`
}

type PrivacyPolicy struct {
	CaptureRetentionHours   int  `json:"capture_retention_hours"`
	PersistUserContent      bool `json:"persist_user_content"`
	PromotionReviewRequired bool `json:"promotion_review_required"`
}

type StopPolicy struct {
	MaxInvalid            int  `json:"max_invalid"`
	MaxCleanupOutstanding int  `json:"max_cleanup_outstanding"`
	StopOnIdentityDrift   bool `json:"stop_on_identity_drift"`
	StopOnSystemicPattern bool `json:"stop_on_systemic_pattern"`
}

type CampaignBundle struct {
	Root     string
	Path     string
	Raw      []byte
	Digest   string
	Campaign Campaign
}

func LoadCampaign(root, path string) (CampaignBundle, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return CampaignBundle{}, err
	}
	absolutePath, relativePath, err := resolveWithin(absoluteRoot, path)
	if err != nil {
		return CampaignBundle{}, err
	}
	raw, err := os.ReadFile(absolutePath)
	if err != nil {
		return CampaignBundle{}, err
	}
	var campaign Campaign
	if err := decodeStrict(raw, &campaign); err != nil {
		return CampaignBundle{}, fmt.Errorf("decode D2 campaign: %w", err)
	}
	if err := campaign.Validate(); err != nil {
		return CampaignBundle{}, err
	}
	return CampaignBundle{
		Root:     absoluteRoot,
		Path:     relativePath,
		Raw:      raw,
		Digest:   spec.DigestString(string(raw)),
		Campaign: campaign,
	}, nil
}

func (c Campaign) Validate() error {
	if c.SchemaVersion != SchemaVersion || !validID(c.ID) ||
		c.SelectionStrategy != "pairwise" {
		return errors.New("D2 campaign identity or strategy is invalid")
	}
	if c.Adaptive != (c.AdaptivePolicy != "") ||
		c.AdaptivePolicy != "" && !validID(c.AdaptivePolicy) {
		return errors.New("D2 adaptive selection policy is invalid")
	}
	entryPoints := append([]string(nil), c.ProductionEntryPoints...)
	slices.Sort(entryPoints)
	if !slices.Equal(entryPoints, []string{"acp", "cli", "vscode"}) {
		return errors.New("D2 production entry points are incomplete")
	}
	axes, err := validateAxes(c.Axes)
	if err != nil {
		return err
	}
	if err := validateFamilies(c.Families, axes); err != nil {
		return err
	}
	if len(c.Seeds) < 3 {
		return errors.New("D2 campaign requires at least three seeds")
	}
	seenSeeds := make(map[uint64]struct{}, len(c.Seeds))
	for _, seed := range c.Seeds {
		if seed == 0 {
			return errors.New("D2 campaign seed is zero")
		}
		if _, duplicate := seenSeeds[seed]; duplicate {
			return errors.New("D2 campaign seeds are not unique")
		}
		seenSeeds[seed] = struct{}{}
	}
	if c.Budgets.MaxRuns < len(requiredFamilyIDs) ||
		c.Budgets.MaxRuns > 10_000 ||
		c.Budgets.WallTimeMS < 1_000 ||
		c.Budgets.WallTimeMS > 86_400_000 ||
		c.Budgets.MaxModelCostMicrounits == 0 ||
		c.Budgets.MaxModelCostMicrounits > 1_000_000_000 ||
		c.Budgets.MaxParallelism < 1 ||
		c.Budgets.MaxParallelism > 64 {
		return errors.New("D2 campaign budgets are invalid or unbounded")
	}
	if !validID(c.Cleanup.Owner) ||
		len(c.Cleanup.Resources) == 0 ||
		c.Cleanup.TimeoutMS < 1 ||
		c.Cleanup.TimeoutMS > 600_000 ||
		!uniqueValidIDs(c.Cleanup.Resources) {
		return errors.New("D2 cleanup contract is invalid")
	}
	if c.Privacy.CaptureRetentionHours < 1 ||
		c.Privacy.CaptureRetentionHours > 168 ||
		c.Privacy.PersistUserContent ||
		!c.Privacy.PromotionReviewRequired {
		return errors.New("D2 privacy contract is invalid")
	}
	if c.StopPolicy.MaxInvalid != 0 ||
		c.StopPolicy.MaxCleanupOutstanding != 0 ||
		!c.StopPolicy.StopOnIdentityDrift ||
		!c.StopPolicy.StopOnSystemicPattern {
		return errors.New("D2 stop policy is not fail closed")
	}
	return nil
}

func validateAxes(values []Axis) (map[string]Axis, error) {
	axes := make(map[string]Axis, len(values))
	ids := make([]string, 0, len(values))
	for _, axis := range values {
		if !validID(axis.ID) || len(axis.Values) < 2 {
			return nil, fmt.Errorf("D2 axis %q is invalid", axis.ID)
		}
		if _, duplicate := axes[axis.ID]; duplicate {
			return nil, fmt.Errorf("duplicate D2 axis %q", axis.ID)
		}
		seenValues := make(map[string]struct{}, len(axis.Values))
		hasBoundary := false
		for _, value := range axis.Values {
			if !validID(value.ID) {
				return nil, fmt.Errorf("D2 axis %q has invalid value", axis.ID)
			}
			if _, duplicate := seenValues[value.ID]; duplicate {
				return nil, fmt.Errorf(
					"D2 axis %q has duplicate value %q",
					axis.ID,
					value.ID,
				)
			}
			seenValues[value.ID] = struct{}{}
			hasBoundary = hasBoundary || value.Boundary
		}
		if !hasBoundary {
			return nil, fmt.Errorf("D2 axis %q has no boundary value", axis.ID)
		}
		axes[axis.ID] = axis
		ids = append(ids, axis.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, requiredAxisIDs) {
		return nil, errors.New("D2 campaign axis catalog is incomplete")
	}
	return axes, nil
}

func validateFamilies(families []Family, axes map[string]Axis) error {
	ids := make([]string, 0, len(families))
	seen := make(map[string]struct{}, len(families))
	usedAxes := make(map[string]struct{}, len(axes))
	for _, family := range families {
		if !validID(family.ID) || len(family.Axes) < 2 ||
			len(family.RequiredCombinations) == 0 ||
			!uniqueValidIDs(family.Axes) {
			return fmt.Errorf("D2 family %q is invalid", family.ID)
		}
		if _, duplicate := seen[family.ID]; duplicate {
			return fmt.Errorf("duplicate D2 family %q", family.ID)
		}
		seen[family.ID] = struct{}{}
		ids = append(ids, family.ID)
		familyAxes := make(map[string]struct{}, len(family.Axes))
		for _, axisID := range family.Axes {
			if _, exists := axes[axisID]; !exists {
				return fmt.Errorf(
					"D2 family %q references unknown axis %q",
					family.ID,
					axisID,
				)
			}
			familyAxes[axisID] = struct{}{}
			usedAxes[axisID] = struct{}{}
		}
		for _, combination := range family.RequiredCombinations {
			if len(combination.Values) < 2 {
				return fmt.Errorf(
					"D2 family %q has an underspecified combination",
					family.ID,
				)
			}
			for axisID, valueID := range combination.Values {
				if _, exists := familyAxes[axisID]; !exists ||
					!axisHasValue(axes[axisID], valueID) {
					return fmt.Errorf(
						"D2 family %q has invalid combination %s=%s",
						family.ID,
						axisID,
						valueID,
					)
				}
			}
		}
	}
	slices.Sort(ids)
	if !slices.Equal(ids, requiredFamilyIDs) || len(usedAxes) != len(axes) {
		return errors.New("D2 campaign family portfolio is incomplete")
	}
	return nil
}

func axisHasValue(axis Axis, valueID string) bool {
	return slices.ContainsFunc(axis.Values, func(value AxisValue) bool {
		return value.ID == valueID
	})
}

func decodeStrict(raw []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON contains multiple values")
	}
	return nil
}

func resolveWithin(root, path string) (string, string, error) {
	absolute := path
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(root, filepath.FromSlash(path))
	}
	absolute = filepath.Clean(absolute)
	relative, err := filepath.Rel(root, absolute)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", "", errors.New("D2 path escapes repository root")
	}
	return absolute, filepath.ToSlash(relative), nil
}

func uniqueValidIDs(values []string) bool {
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validID(value) {
			return false
		}
		if _, duplicate := seen[value]; duplicate {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func validID(value string) bool {
	if value == "" || len(value) > 96 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			index > 0 && (character == '-' || character == '_') {
			continue
		}
		return false
	}
	return true
}
