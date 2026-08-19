package admission

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

const H2SchemaVersion = 1

type H2Catalog struct {
	SchemaVersion int          `json:"schema_version"`
	Provider      string       `json:"provider"`
	Model         string       `json:"model"`
	Protocol      string       `json:"protocol"`
	Policy        H2Policy     `json:"policy"`
	Scenarios     []H2Scenario `json:"scenarios"`
}

type H2Policy struct {
	MaxTotalCostMicrounits uint64 `json:"max_total_cost_microunits"`
	RequireCostKnown       bool   `json:"require_cost_known"`
	PricingContract        string `json:"pricing_contract"`
}

type H2Scenario struct {
	ID                         string   `json:"id"`
	Mode                       string   `json:"mode"`
	Repetitions                int      `json:"repetitions"`
	Command                    []string `json:"command"`
	ExpectedTextSHA256         string   `json:"expected_text_sha256"`
	MinConfidenceLowerBasisPts int      `json:"min_confidence_lower_basis_points"`
	MaxP95LatencyMS            int64    `json:"max_p95_latency_ms"`
	MaxCostMicrounits          uint64   `json:"max_cost_microunits"`
	MaxEventShapeVariants      int      `json:"max_event_shape_variants"`
}

type H2LiveEvidence struct {
	SchemaVersion      int    `json:"schema_version"`
	Stage              string `json:"stage"`
	QualificationID    string `json:"qualification_id"`
	ScenarioID         string `json:"scenario_id"`
	SampleIndex        int    `json:"sample_index"`
	SourceDigest       string `json:"source_digest"`
	LockIdentity       string `json:"lock_identity"`
	EndpointHostSHA256 string `json:"endpoint_host_sha256"`
	Provider           string `json:"provider"`
	Model              string `json:"model"`
	Protocol           string `json:"protocol"`
	PricingWindow      string `json:"pricing_window"`
	ConfigSHA256       string `json:"config_sha256"`
	MultiAgent         bool   `json:"multi_agent"`
	TerminalEvent      string `json:"terminal_event"`
	TerminalCount      int    `json:"terminal_count"`
	TextAssertionSHA   string `json:"text_assertion_sha256"`
	EventShapeSHA256   string `json:"event_shape_sha256"`
	UsageSamples       int    `json:"usage_samples"`
	InputTokens        uint64 `json:"input_tokens"`
	OutputTokens       uint64 `json:"output_tokens"`
	ReasoningTokens    uint64 `json:"reasoning_tokens"`
	CachedTokens       uint64 `json:"cached_tokens"`
	CostMicrounits     uint64 `json:"cost_microunits"`
	CostKnown          bool   `json:"cost_known"`
	DurationMS         int64  `json:"duration_ms"`
	AgentSpawnCount    int    `json:"agent_spawn_count"`
	AgentTerminalCount int    `json:"agent_terminal_count"`
	AgentCompleted     int    `json:"agent_completed_count"`
}

type H2ScenarioSummary struct {
	ID                      string   `json:"id"`
	Mode                    string   `json:"mode"`
	Scheduled               int      `json:"scheduled"`
	Passed                  int      `json:"passed"`
	ConfidenceLowerBasisPts int      `json:"confidence_lower_basis_points"`
	P95LatencyMS            int64    `json:"p95_latency_ms"`
	CostMicrounits          uint64   `json:"cost_microunits"`
	EventShapeVariants      int      `json:"event_shape_variants"`
	EvidenceDigests         []string `json:"evidence_digests"`
}

type H2Summary struct {
	SchemaVersion       int                 `json:"schema_version"`
	QualificationID     string              `json:"qualification_id"`
	Provider            string              `json:"provider"`
	Model               string              `json:"model"`
	Protocol            string              `json:"protocol"`
	PricingWindow       string              `json:"pricing_window"`
	SourceDigest        string              `json:"source_digest"`
	LockIdentity        string              `json:"lock_identity"`
	EndpointHostSHA256  string              `json:"endpoint_host_sha256"`
	ConfigSHA256        string              `json:"config_sha256"`
	Scheduled           int                 `json:"scheduled"`
	Passed              int                 `json:"passed"`
	TotalCostMicrounits uint64              `json:"total_cost_microunits"`
	Scenarios           []H2ScenarioSummary `json:"scenarios"`
	EvidenceDigest      string              `json:"evidence_digest"`
}

func LoadH2(root, path string) (H2Catalog, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return H2Catalog{}, err
	}
	absolutePath := path
	if !filepath.IsAbs(absolutePath) {
		absolutePath = filepath.Join(absoluteRoot, filepath.FromSlash(path))
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return H2Catalog{}, errors.New("H2 catalog escapes repository root")
	}
	var catalog H2Catalog
	if err := decodeStrictFile(absolutePath, &catalog); err != nil {
		return H2Catalog{}, fmt.Errorf("decode H2 catalog: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return H2Catalog{}, err
	}
	return catalog, nil
}

func (c H2Catalog) Validate() error {
	if c.SchemaVersion != H2SchemaVersion {
		return fmt.Errorf("H2 catalog schema_version must be %d", H2SchemaVersion)
	}
	if !validID(c.Provider) || !validID(c.Model) ||
		!slices.Contains([]string{"openai_chat", "openai_responses"}, c.Protocol) ||
		len(c.Scenarios) < 2 || c.Policy.MaxTotalCostMicrounits == 0 ||
		!c.Policy.RequireCostKnown ||
		c.Policy.PricingContract != "deepseek_v4_2026_08_17" {
		return errors.New("H2 catalog identity or policy is invalid")
	}
	seen := make(map[string]struct{}, len(c.Scenarios))
	modes := make(map[string]bool)
	total := 0
	for _, scenario := range c.Scenarios {
		if !validID(scenario.ID) ||
			!slices.Contains([]string{"single", "multi_agent"}, scenario.Mode) ||
			scenario.Repetitions < 1 ||
			len(scenario.Command) == 0 ||
			!digestValidH2(scenario.ExpectedTextSHA256) ||
			scenario.MinConfidenceLowerBasisPts < 1 ||
			scenario.MinConfidenceLowerBasisPts > 10_000 ||
			scenario.MaxP95LatencyMS < 1 ||
			scenario.MaxCostMicrounits < 1 ||
			scenario.MaxEventShapeVariants < 1 {
			return fmt.Errorf("H2 scenario %q is invalid", scenario.ID)
		}
		for _, value := range scenario.Command {
			if strings.TrimSpace(value) == "" || strings.ContainsRune(value, '\x00') {
				return fmt.Errorf("H2 scenario %q contains invalid command text", scenario.ID)
			}
		}
		if _, duplicate := seen[scenario.ID]; duplicate {
			return fmt.Errorf("duplicate H2 scenario %q", scenario.ID)
		}
		seen[scenario.ID] = struct{}{}
		modes[scenario.Mode] = true
		total += scenario.Repetitions
	}
	if total < 12 || !modes["single"] || !modes["multi_agent"] {
		return errors.New("H2 catalog has an incomplete live denominator")
	}
	return nil
}

func ReadH2Evidence(path string) (H2LiveEvidence, error) {
	var evidence H2LiveEvidence
	if err := decodeStrictFile(path, &evidence); err != nil {
		return evidence, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return evidence, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return evidence, errors.New("H2 evidence permissions are not private")
	}
	if err := evidence.Validate(); err != nil {
		return evidence, err
	}
	return evidence, nil
}

func (e H2LiveEvidence) Validate() error {
	for name, value := range map[string]string{
		"source_digest": e.SourceDigest, "lock_identity": e.LockIdentity,
		"endpoint_host_sha256":  e.EndpointHostSHA256,
		"config_sha256":         e.ConfigSHA256,
		"text_assertion_sha256": e.TextAssertionSHA,
		"event_shape_sha256":    e.EventShapeSHA256,
	} {
		if !digestValidH2(value) {
			return fmt.Errorf("H2 evidence %s is invalid", name)
		}
	}
	if e.SchemaVersion != H2SchemaVersion || e.Stage != "h2_live" ||
		!validID(e.QualificationID) || !validID(e.ScenarioID) ||
		e.SampleIndex < 1 || !validID(e.Provider) || !validID(e.Model) ||
		!slices.Contains([]string{"openai_chat", "openai_responses"}, e.Protocol) ||
		!slices.Contains([]string{"peak", "off_peak"}, e.PricingWindow) ||
		e.TerminalEvent != "turn.completed" || e.TerminalCount != 1 ||
		e.UsageSamples < 1 || e.InputTokens == 0 || e.OutputTokens == 0 ||
		e.DurationMS < 1 || !e.CostKnown {
		return errors.New("H2 live evidence is incomplete")
	}
	if e.MultiAgent &&
		(e.AgentSpawnCount != 2 ||
			e.AgentTerminalCount != 2 ||
			e.AgentCompleted != 2) {
		return errors.New("H2 Multi-Agent evidence is incomplete")
	}
	return nil
}

func AggregateH2(
	output string,
	catalog H2Catalog,
	qualificationID, sourceDigest, lockIdentity string,
) (H2Summary, error) {
	summary := H2Summary{
		SchemaVersion: H2SchemaVersion, QualificationID: qualificationID,
		Provider: catalog.Provider, Model: catalog.Model, Protocol: catalog.Protocol,
		SourceDigest: sourceDigest, LockIdentity: lockIdentity,
	}
	var endpoint, config, pricingWindow string
	for _, scenario := range catalog.Scenarios {
		scenarioSummary := H2ScenarioSummary{
			ID: scenario.ID, Mode: scenario.Mode,
			Scheduled: scenario.Repetitions,
		}
		shapes := make(map[string]struct{})
		var latencies []int64
		for sample := 1; sample <= scenario.Repetitions; sample++ {
			path := H2EvidencePath(output, scenario.ID, sample)
			evidence, err := ReadH2Evidence(path)
			if err != nil {
				return summary, fmt.Errorf(
					"H2 evidence %s sample %d: %w",
					scenario.ID, sample, err,
				)
			}
			if evidence.QualificationID != qualificationID ||
				evidence.ScenarioID != scenario.ID ||
				evidence.SampleIndex != sample ||
				evidence.Provider != catalog.Provider ||
				evidence.Model != catalog.Model ||
				evidence.Protocol != catalog.Protocol ||
				evidence.SourceDigest != sourceDigest ||
				evidence.LockIdentity != lockIdentity ||
				evidence.MultiAgent != (scenario.Mode == "multi_agent") ||
				evidence.TextAssertionSHA != scenario.ExpectedTextSHA256 {
				return summary, fmt.Errorf(
					"H2 evidence identity drifted for %s sample %d",
					scenario.ID, sample,
				)
			}
			if endpoint == "" {
				endpoint, config, pricingWindow = evidence.EndpointHostSHA256,
					evidence.ConfigSHA256, evidence.PricingWindow
			} else if evidence.EndpointHostSHA256 != endpoint ||
				evidence.ConfigSHA256 != config ||
				evidence.PricingWindow != pricingWindow {
				return summary, errors.New("H2 route or configuration partition drifted")
			}
			if evidence.CostMicrounits > scenario.MaxCostMicrounits {
				return summary, fmt.Errorf(
					"H2 scenario %s sample %d exceeded cost ceiling",
					scenario.ID, sample,
				)
			}
			scenarioSummary.Passed++
			scenarioSummary.CostMicrounits += evidence.CostMicrounits
			summary.TotalCostMicrounits += evidence.CostMicrounits
			latencies = append(latencies, evidence.DurationMS)
			shapes[evidence.EventShapeSHA256] = struct{}{}
			scenarioSummary.EvidenceDigests = append(
				scenarioSummary.EvidenceDigests,
				digestH2(evidence),
			)
		}
		scenarioSummary.ConfidenceLowerBasisPts = wilsonLowerBasisPoints(
			scenarioSummary.Passed,
			scenarioSummary.Scheduled,
		)
		scenarioSummary.P95LatencyMS = nearestRank(latencies, 95)
		scenarioSummary.EventShapeVariants = len(shapes)
		if scenarioSummary.ConfidenceLowerBasisPts <
			scenario.MinConfidenceLowerBasisPts {
			return summary, fmt.Errorf(
				"H2 scenario %s confidence lower bound %d is below %d",
				scenario.ID,
				scenarioSummary.ConfidenceLowerBasisPts,
				scenario.MinConfidenceLowerBasisPts,
			)
		}
		if scenarioSummary.P95LatencyMS > scenario.MaxP95LatencyMS {
			return summary, fmt.Errorf(
				"H2 scenario %s p95 latency %d exceeded %d",
				scenario.ID,
				scenarioSummary.P95LatencyMS,
				scenario.MaxP95LatencyMS,
			)
		}
		if scenarioSummary.EventShapeVariants > scenario.MaxEventShapeVariants {
			return summary, fmt.Errorf(
				"H2 scenario %s event-shape variants %d exceeded %d",
				scenario.ID,
				scenarioSummary.EventShapeVariants,
				scenario.MaxEventShapeVariants,
			)
		}
		summary.Scheduled += scenarioSummary.Scheduled
		summary.Passed += scenarioSummary.Passed
		summary.Scenarios = append(summary.Scenarios, scenarioSummary)
	}
	if summary.TotalCostMicrounits > catalog.Policy.MaxTotalCostMicrounits {
		return summary, errors.New("H2 total cost exceeded the policy ceiling")
	}
	summary.EndpointHostSHA256, summary.ConfigSHA256 = endpoint, config
	summary.PricingWindow = pricingWindow
	summary.EvidenceDigest = digestH2(struct {
		QualificationID string
		SourceDigest    string
		LockIdentity    string
		Scenarios       []H2ScenarioSummary
	}{
		qualificationID, sourceDigest, lockIdentity, summary.Scenarios,
	})
	if err := writePrivateJSON(
		filepath.Join(output, "h2-summary.json"),
		summary,
	); err != nil {
		return summary, err
	}
	return summary, nil
}

func H2EvidencePath(output, scenarioID string, sample int) string {
	return filepath.Join(
		output,
		"live-evidence",
		fmt.Sprintf("%s-%02d.json", scenarioID, sample),
	)
}

func wilsonLowerBasisPoints(successes, total int) int {
	if successes < 0 || total < 1 || successes > total {
		return 0
	}
	const z = 1.959963984540054
	n := float64(total)
	p := float64(successes) / n
	z2 := z * z
	center := p + z2/(2*n)
	margin := z * math.Sqrt((p*(1-p)+z2/(4*n))/n)
	lower := (center - margin) / (1 + z2/n)
	return int(math.Floor(max(0, lower)*10_000 + 1e-9))
}

func nearestRank(values []int64, percentile int) int64 {
	if len(values) == 0 {
		return 0
	}
	sorted := append([]int64(nil), values...)
	slices.Sort(sorted)
	index := (len(sorted)*percentile + 99) / 100
	return sorted[max(1, index)-1]
}

func decodeStrictFile(path string, destination any) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("JSON file contains multiple values")
	}
	return nil
}

func writePrivateJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".h2-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := temporary.Write(raw); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func digestH2(value any) string {
	raw, _ := json.Marshal(value)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func digestValidH2(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != 71 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimPrefix(value, "sha256:"))
	return err == nil
}
