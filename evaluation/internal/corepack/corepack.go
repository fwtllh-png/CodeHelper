package corepack

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

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/oracle"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/replay"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

const SchemaVersion = 2

type Pack struct {
	SchemaVersion    int         `json:"schema_version"`
	MinimumFamilies  int         `json:"minimum_families"`
	MandatoryOracles []string    `json:"mandatory_oracles"`
	Scenarios        []Scenario  `json:"scenarios"`
	FaultCases       []FaultCase `json:"fault_cases"`
}

type Scenario struct {
	ID                string                `json:"id"`
	Title             string                `json:"title"`
	Family            string                `json:"family"`
	Risk              spec.Risk             `json:"risk"`
	Owner             string                `json:"owner"`
	FixtureID         string                `json:"fixture_id"`
	FixtureProfile    string                `json:"fixture_profile"`
	ExpectedFacts     []string              `json:"expected_facts"`
	RequiredOracles   []string              `json:"required_oracles"`
	RequiredMutations []replay.MutationKind `json:"required_mutations"`
	ImpactTags        []string              `json:"impact_tags"`
}

type FaultCase struct {
	ID                string           `json:"id"`
	Fault             oracle.FaultKind `json:"fault"`
	Risk              spec.Risk        `json:"risk"`
	ExpectedStatus    spec.Status      `json:"expected_status"`
	ExpectedSignature string           `json:"expected_signature"`
	ExpectedDomain    oracle.Domain    `json:"expected_domain"`
}

type ImpactMap struct {
	SchemaVersion int          `json:"schema_version"`
	Fallback      string       `json:"fallback"`
	Exclusions    []string     `json:"exclusions"`
	Rules         []ImpactRule `json:"rules"`
}

type ImpactRule struct {
	ID      string   `json:"id"`
	Pattern string   `json:"pattern"`
	Tags    []string `json:"tags"`
}

type Bundle struct {
	Root      string
	Pack      Pack
	ImpactMap ImpactMap
}

type CheckReport struct {
	SchemaVersion int `json:"schema_version"`
	Scenarios     int `json:"scenarios"`
	Families      int `json:"families"`
	P0Scenarios   int `json:"p0_scenarios"`
	OracleRuns    int `json:"oracle_runs"`
	FaultCases    int `json:"fault_cases"`
	ImpactRules   int `json:"impact_rules"`
	MutationRuns  int `json:"mutation_runs"`
}

type SelectionReport struct {
	Paths         []string            `json:"paths"`
	Scenarios     []Scenario          `json:"scenarios"`
	MatchedRules  map[string][]string `json:"matched_rules"`
	FallbackPaths []string            `json:"fallback_paths"`
	ExcludedPaths []string            `json:"excluded_paths"`
}

func Load(root, packPath, impactPath string) (Bundle, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Bundle{}, err
	}
	packFile, err := resolveWithin(absoluteRoot, packPath)
	if err != nil {
		return Bundle{}, err
	}
	impactFile, err := resolveWithin(absoluteRoot, impactPath)
	if err != nil {
		return Bundle{}, err
	}
	var pack Pack
	if decodeErr := decodeFile(packFile, &pack); decodeErr != nil {
		return Bundle{}, fmt.Errorf("decode core scenario pack: %w", decodeErr)
	}
	var impact ImpactMap
	if decodeErr := decodeFile(impactFile, &impact); decodeErr != nil {
		return Bundle{}, fmt.Errorf("decode evaluation impact map: %w", decodeErr)
	}
	bundle := Bundle{
		Root: absoluteRoot, Pack: pack, ImpactMap: impact,
	}
	if err := bundle.Validate(); err != nil {
		return Bundle{}, err
	}
	return bundle, nil
}

func (b Bundle) Validate() error {
	if b.Pack.SchemaVersion != SchemaVersion ||
		b.ImpactMap.SchemaVersion != SchemaVersion {
		return fmt.Errorf(
			"core pack and impact map schema_version must be %d",
			SchemaVersion,
		)
	}
	if b.Pack.MinimumFamilies < 30 {
		return errors.New("core pack minimum_families cannot be lower than 30")
	}
	if err := uniqueOracleIDs(b.Pack.MandatoryOracles); err != nil {
		return fmt.Errorf("mandatory oracles: %w", err)
	}
	if !sameStrings(b.Pack.MandatoryOracles, oracle.AllIDs) {
		return errors.New("core pack mandatory_oracles must contain all oracles")
	}
	ids := make(map[string]struct{}, len(b.Pack.Scenarios))
	families := make(map[string]struct{}, len(b.Pack.Scenarios))
	fixtures := make(map[string]struct{}, len(b.Pack.Scenarios))
	factSets := make(map[string]struct{}, len(b.Pack.Scenarios))
	mutationCoverage := make(map[replay.MutationKind]bool)
	for _, scenario := range b.Pack.Scenarios {
		if err := scenario.Validate(b.Pack.MandatoryOracles); err != nil {
			return err
		}
		if _, exists := ids[scenario.ID]; exists {
			return fmt.Errorf("duplicate core scenario id %q", scenario.ID)
		}
		ids[scenario.ID] = struct{}{}
		if _, exists := fixtures[scenario.FixtureID]; exists {
			return fmt.Errorf("duplicate core fixture id %q", scenario.FixtureID)
		}
		fixtures[scenario.FixtureID] = struct{}{}
		facts := append([]string(nil), scenario.ExpectedFacts...)
		slices.Sort(facts)
		factSet := strings.Join(facts, "\x00")
		if _, exists := factSets[factSet]; exists {
			return fmt.Errorf("duplicate core expected-fact set for %q", scenario.ID)
		}
		factSets[factSet] = struct{}{}
		for _, mutation := range scenario.RequiredMutations {
			mutationCoverage[mutation] = true
		}
		if _, exists := families[scenario.Family]; exists {
			return fmt.Errorf("duplicate core scenario family %q", scenario.Family)
		}
		families[scenario.Family] = struct{}{}
	}
	if len(families) < b.Pack.MinimumFamilies {
		return fmt.Errorf(
			"core pack has %d families, minimum is %d",
			len(families),
			b.Pack.MinimumFamilies,
		)
	}
	for _, mutation := range []replay.MutationKind{
		replay.MutationSplit,
		replay.MutationDelay,
		replay.MutationDuplicate,
		replay.MutationTruncate,
		replay.MutationInterrupt,
		replay.MutationUnknown,
		replay.MutationMalformed,
	} {
		if !mutationCoverage[mutation] {
			return fmt.Errorf("required mutation %q has no Scenario coverage", mutation)
		}
	}
	faultIDs := make(map[string]struct{}, len(b.Pack.FaultCases))
	faultCoverage := make(map[string]bool, len(oracle.AllIDs))
	for _, fault := range b.Pack.FaultCases {
		if err := fault.Validate(); err != nil {
			return err
		}
		if _, exists := faultIDs[fault.ID]; exists {
			return fmt.Errorf("duplicate fault case id %q", fault.ID)
		}
		faultIDs[fault.ID] = struct{}{}
		if oracleID, _, found := strings.Cut(fault.ExpectedSignature, ":"); found {
			faultCoverage[oracleID] = true
		}
	}
	for _, oracleID := range oracle.AllIDs {
		if !faultCoverage[oracleID] {
			return fmt.Errorf("oracle %q has no fault-case coverage", oracleID)
		}
	}
	if err := b.ImpactMap.Validate(b.Pack.Scenarios); err != nil {
		return err
	}
	return nil
}

func (s Scenario) Validate(mandatory []string) error {
	if !validID(s.ID) || !validID(s.Family) ||
		!validID(s.FixtureID) || !validID(s.FixtureProfile) ||
		strings.TrimSpace(s.Title) == "" ||
		strings.TrimSpace(s.Owner) == "" || !s.Risk.Valid() {
		return fmt.Errorf("core scenario %q identity is invalid", s.ID)
	}
	if err := uniqueOracleIDs(s.RequiredOracles); err != nil {
		return fmt.Errorf("core scenario %q: %w", s.ID, err)
	}
	if err := uniqueIDs(s.ExpectedFacts, "expected fact"); err != nil {
		return fmt.Errorf("core scenario %q: %w", s.ID, err)
	}
	if err := uniqueMutations(s.RequiredMutations); err != nil {
		return fmt.Errorf("core scenario %q: %w", s.ID, err)
	}
	for _, id := range s.RequiredOracles {
		if !slices.Contains(mandatory, id) {
			return fmt.Errorf("core scenario %q uses non-mandatory oracle %q", s.ID, id)
		}
	}
	if err := uniqueIDs(s.ImpactTags, "impact tag"); err != nil {
		return fmt.Errorf("core scenario %q: %w", s.ID, err)
	}
	return nil
}

func (f FaultCase) Validate() error {
	if !validID(f.ID) || !f.Risk.Valid() || !f.ExpectedStatus.Valid() ||
		strings.TrimSpace(f.ExpectedSignature) == "" ||
		!f.ExpectedDomain.Valid() {
		return fmt.Errorf("fault case %q is invalid", f.ID)
	}
	if f.ExpectedStatus != spec.StatusFailed &&
		f.ExpectedStatus != spec.StatusInvalid {
		return fmt.Errorf(
			"fault case %q expected_status must be failed or invalid",
			f.ID,
		)
	}
	switch f.Fault {
	case oracle.FaultDuplicateEffect, oracle.FaultDoubleTerminal,
		oracle.FaultStuckRunning, oracle.FaultReceiptDrift,
		oracle.FaultReplayDrift, oracle.FaultGuardBypass,
		oracle.FaultSecurityBypass,
		oracle.FaultWorkspaceEscape, oracle.FaultVerificationFail,
		oracle.FaultResourceLeak, oracle.FaultHostDrift,
		oracle.FaultTaskQuality, oracle.FaultMalformedEvidence:
	default:
		return fmt.Errorf("fault case %q has unsupported fault %q", f.ID, f.Fault)
	}
	return nil
}

func (m ImpactMap) Validate(scenarios []Scenario) error {
	if len(m.Rules) == 0 {
		return errors.New("evaluation impact map has no rules")
	}
	if m.Fallback != "all_p0" {
		return errors.New("evaluation impact fallback must be all_p0")
	}
	for _, pattern := range m.Exclusions {
		if !validPattern(pattern) {
			return fmt.Errorf("impact exclusion %q is invalid", pattern)
		}
	}
	seen := make(map[string]struct{}, len(m.Rules))
	covered := make(map[string]bool, len(scenarios))
	for _, rule := range m.Rules {
		if !validID(rule.ID) || !validPattern(rule.Pattern) {
			return fmt.Errorf("impact rule %q is invalid", rule.ID)
		}
		if _, exists := seen[rule.ID]; exists {
			return fmt.Errorf("duplicate impact rule id %q", rule.ID)
		}
		seen[rule.ID] = struct{}{}
		if err := uniqueIDs(rule.Tags, "impact rule tag"); err != nil {
			return fmt.Errorf("impact rule %q: %w", rule.ID, err)
		}
		selected := selectByTags(scenarios, rule.Tags)
		if len(selected) == 0 {
			return fmt.Errorf("impact rule %q selects no core scenarios", rule.ID)
		}
		for _, scenario := range selected {
			covered[scenario.ID] = true
		}
	}
	for _, scenario := range scenarios {
		if !covered[scenario.ID] {
			return fmt.Errorf(
				"core scenario %q is not selected by any impact rule",
				scenario.ID,
			)
		}
	}
	return nil
}

func (b Bundle) Check() (CheckReport, error) {
	if err := b.Validate(); err != nil {
		return CheckReport{}, err
	}
	report := CheckReport{
		SchemaVersion: SchemaVersion,
		Scenarios:     len(b.Pack.Scenarios),
		FaultCases:    len(b.Pack.FaultCases),
		ImpactRules:   len(b.ImpactMap.Rules),
	}
	families := make(map[string]struct{}, len(b.Pack.Scenarios))
	for _, scenario := range b.Pack.Scenarios {
		input := oracle.NewBaseline(scenario.ID, scenario.FixtureID)
		input, err := oracle.Specialize(
			input,
			scenario.FixtureProfile,
			scenario.ExpectedFacts,
		)
		if err != nil {
			return CheckReport{}, fmt.Errorf(
				"build core scenario %q fixture: %w",
				scenario.ID,
				err,
			)
		}
		result := oracle.Evaluate(input, scenario.RequiredOracles, scenario.Risk)
		if result.Status != spec.StatusPassed {
			return CheckReport{}, fmt.Errorf(
				"core scenario %q status = %s, signature = %s",
				scenario.ID,
				result.Status,
				result.FailureSignature,
			)
		}
		families[scenario.Family] = struct{}{}
		report.OracleRuns += len(result.Results)
		if scenario.Risk == spec.RiskP0 {
			report.P0Scenarios++
		}
		events, err := mutationFixture(scenario)
		if err != nil {
			return CheckReport{}, err
		}
		coverage, err := replay.VerifyMutationCoverage(
			events,
			scenario.RequiredMutations,
		)
		if err != nil {
			return CheckReport{}, fmt.Errorf(
				"core scenario %q mutation coverage: %w",
				scenario.ID,
				err,
			)
		}
		for _, item := range coverage {
			report.MutationRuns += item.Executed
		}
	}
	report.Families = len(families)
	for _, fault := range b.Pack.FaultCases {
		baseline := oracle.NewBaseline(fault.ID, "fault-"+fault.ID)
		input, err := oracle.Inject(baseline, fault.Fault)
		if err != nil {
			return CheckReport{}, fmt.Errorf("inject fault case %q: %w", fault.ID, err)
		}
		input.ScenarioID = fault.ID
		result := oracle.Evaluate(input, b.Pack.MandatoryOracles, fault.Risk)
		if result.Status != fault.ExpectedStatus ||
			result.FailureSignature != fault.ExpectedSignature ||
			result.Primary == nil ||
			result.Primary.Domain != fault.ExpectedDomain {
			return CheckReport{}, fmt.Errorf(
				"fault case %q got status=%s signature=%s primary=%+v",
				fault.ID,
				result.Status,
				result.FailureSignature,
				result.Primary,
			)
		}
	}
	return report, nil
}

func mutationFixture(scenario Scenario) ([]evidence.Envelope, error) {
	source := evidence.SourceRuntime
	if slices.Contains(scenario.RequiredMutations, replay.MutationSplit) {
		source = evidence.SourceProvider
	}
	events := []evidence.Envelope{{
		OffsetMS: 0,
		Source:   source,
		Kind:     "provider.frame",
		Identity: evidence.Identity{Capture: "capture-001"},
		Policy: evidence.Policy{
			Class:     evidence.DataOperational,
			Redaction: evidence.RedactionNotRequired,
		},
		Data: []byte(`{"wire_sequence":1,"metadata":true}`),
	}}
	events = append(events, evidence.Envelope{
		OffsetMS: 1,
		Source:   evidence.SourceRuntime,
		Kind:     "turn.failed",
		Identity: evidence.Identity{Capture: "capture-001", Turn: "turn-001"},
		Policy: evidence.Policy{
			Class:     evidence.DataOperational,
			Redaction: evidence.RedactionNotRequired,
		},
		Data: []byte(`{"metadata":true}`),
	})
	return evidence.Seal(events)
}

func (b Bundle) Select(paths []string) ([]Scenario, error) {
	report, err := b.SelectDetailed(paths)
	return report.Scenarios, err
}

func (b Bundle) SelectDetailed(paths []string) (SelectionReport, error) {
	if len(paths) == 0 {
		return SelectionReport{}, errors.New("changed path list is empty")
	}
	report := SelectionReport{
		Paths:        append([]string(nil), paths...),
		MatchedRules: make(map[string][]string),
	}
	selected := make(map[string]Scenario)
	for _, changed := range paths {
		if !validRelativePath(changed) {
			return SelectionReport{}, fmt.Errorf("changed path %q is invalid", changed)
		}
		excluded := false
		for _, pattern := range b.ImpactMap.Exclusions {
			if matchPath(pattern, changed) {
				excluded = true
				break
			}
		}
		if excluded {
			report.ExcludedPaths = append(report.ExcludedPaths, changed)
			continue
		}
		matched := false
		for _, rule := range b.ImpactMap.Rules {
			if !matchPath(rule.Pattern, changed) {
				continue
			}
			matched = true
			report.MatchedRules[changed] = append(
				report.MatchedRules[changed],
				rule.ID,
			)
			for _, scenario := range selectByTags(b.Pack.Scenarios, rule.Tags) {
				selected[scenario.ID] = scenario
			}
		}
		if !matched {
			report.FallbackPaths = append(report.FallbackPaths, changed)
			for _, scenario := range b.Pack.Scenarios {
				if scenario.Risk == spec.RiskP0 {
					selected[scenario.ID] = scenario
				}
			}
		}
	}
	result := make([]Scenario, 0, len(selected))
	for _, scenario := range selected {
		result = append(result, scenario)
	}
	slices.SortFunc(result, func(left, right Scenario) int {
		return strings.Compare(left.ID, right.ID)
	})
	report.Scenarios = result
	slices.Sort(report.FallbackPaths)
	slices.Sort(report.ExcludedPaths)
	for path := range report.MatchedRules {
		slices.Sort(report.MatchedRules[path])
	}
	return report, nil
}

func uniqueMutations(values []replay.MutationKind) error {
	seen := make(map[replay.MutationKind]struct{}, len(values))
	for _, value := range values {
		switch value {
		case replay.MutationSplit, replay.MutationDelay,
			replay.MutationDuplicate, replay.MutationTruncate,
			replay.MutationInterrupt, replay.MutationUnknown,
			replay.MutationMalformed:
		default:
			return fmt.Errorf("unsupported mutation %q", value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate mutation %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func selectByTags(scenarios []Scenario, tags []string) []Scenario {
	var selected []Scenario
	for _, scenario := range scenarios {
		for _, tag := range tags {
			if slices.Contains(scenario.ImpactTags, tag) {
				selected = append(selected, scenario)
				break
			}
		}
	}
	return selected
}

func matchPath(pattern, changed string) bool {
	if strings.HasSuffix(pattern, "/**") {
		return strings.HasPrefix(changed, strings.TrimSuffix(pattern, "**"))
	}
	return changed == pattern
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	leftCopy := append([]string(nil), left...)
	rightCopy := append([]string(nil), right...)
	slices.Sort(leftCopy)
	slices.Sort(rightCopy)
	return slices.Equal(leftCopy, rightCopy)
}

func uniqueOracleIDs(values []string) error {
	if len(values) == 0 {
		return errors.New("oracle list is empty")
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !oracle.IsOracleID(value) {
			return fmt.Errorf("unknown oracle %q", value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate oracle %q", value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func uniqueIDs(values []string, label string) error {
	if len(values) == 0 {
		return fmt.Errorf("%s list is empty", label)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if !validID(value) {
			return fmt.Errorf("%s %q is invalid", label, value)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate %s %q", label, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}

func validID(value string) bool {
	if value == "" || len(value) > 96 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			(index > 0 && (character == '-' || character == '_')) {
			continue
		}
		return false
	}
	return true
}

func validPattern(value string) bool {
	if prefix, found := strings.CutSuffix(value, "/**"); found {
		return validRelativePath(prefix + "/x")
	}
	return validRelativePath(value)
}

func validRelativePath(value string) bool {
	if strings.TrimSpace(value) == "" || filepath.IsAbs(value) ||
		value == ".." || strings.HasPrefix(value, "../") ||
		strings.Contains(value, "\\") {
		return false
	}
	return filepath.ToSlash(filepath.Clean(value)) == value
}

func resolveWithin(root, relative string) (string, error) {
	if !validRelativePath(relative) {
		return "", fmt.Errorf("path %q is not a clean repository-relative path", relative)
	}
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	back, err := filepath.Rel(root, absolute)
	if err != nil || back == ".." ||
		strings.HasPrefix(filepath.ToSlash(back), "../") {
		return "", fmt.Errorf("path %q escapes repository root", relative)
	}
	return absolute, nil
}

func decodeFile(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("document must contain exactly one JSON value")
	}
	return nil
}
