package admission

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const (
	H3SchemaVersion          = 1
	H3EnduranceSchemaVersion = 1
	H3ReleaseSchemaVersion   = 3
)

var h3RequiredLanes = []string{
	"foundation",
	"integration",
	"chaos",
	"live",
	"endurance",
	"release",
	"vscode_rc",
	"package",
}

type H3Catalog struct {
	SchemaVersion int             `json:"schema_version"`
	Endurance     H3EnduranceSpec `json:"endurance"`
	Release       H3ReleaseSpec   `json:"release"`
}

type H3EnduranceSpec struct {
	DurationSeconds                     int64  `json:"duration_seconds"`
	TurnIntervalSeconds                 int64  `json:"turn_interval_seconds"`
	TurnTimeoutSeconds                  int64  `json:"turn_timeout_seconds"`
	WarmupTurns                         int    `json:"warmup_turns"`
	MinCompletedTurns                   int    `json:"min_completed_turns"`
	Prompt                              string `json:"prompt"`
	MaxRSSSlopeBytesPerTurn             int64  `json:"max_rss_slope_bytes_per_turn"`
	MaxRSSGrowthBytes                   int64  `json:"max_rss_growth_bytes"`
	MaxFDSlopeMilliPerTurn              int64  `json:"max_fd_slope_milli_per_turn"`
	MaxFDGrowth                         int    `json:"max_fd_growth"`
	MaxPersistenceSlopeBytesPerTurn     int64  `json:"max_persistence_slope_bytes_per_turn"`
	MaxLatencySlopeMilliMSPerTurn       int64  `json:"max_latency_slope_milli_ms_per_turn"`
	MaxP95LatencyMS                     int64  `json:"max_p95_latency_ms"`
	MaxLateEarlyLatencyRatioBasisPoints int    `json:"max_late_early_latency_ratio_basis_points"`
}

type H3ReleaseSpec struct {
	RequiredLanes   []string `json:"required_lanes"`
	ReleaseCommand  []string `json:"release_command"`
	VSCodeRCCommand []string `json:"vscode_rc_command"`
	PackageCommand  []string `json:"package_command"`
	PackageTargets  []string `json:"package_targets"`
}

type H3ResourceSample struct {
	Turn             int   `json:"turn"`
	ElapsedMS        int64 `json:"elapsed_ms"`
	RSSBytes         int64 `json:"rss_bytes"`
	FDs              int   `json:"fds"`
	PersistenceBytes int64 `json:"persistence_bytes"`
	LatencyMS        int64 `json:"latency_ms"`
}

type H3SlopeSummary struct {
	RSSBytesPerTurn         int64 `json:"rss_bytes_per_turn"`
	FDMilliPerTurn          int64 `json:"fd_milli_per_turn"`
	PersistenceBytesPerTurn int64 `json:"persistence_bytes_per_turn"`
	LatencyMilliMSPerTurn   int64 `json:"latency_milli_ms_per_turn"`
}

type H3EnduranceEvidence struct {
	SchemaVersion       int                `json:"schema_version"`
	QualificationID     string             `json:"qualification_id"`
	Status              string             `json:"status"`
	SourceDigest        string             `json:"source_digest"`
	LockIdentity        string             `json:"lock_identity"`
	RuntimeDigest       string             `json:"runtime_digest"`
	ConfiguredDuration  int64              `json:"configured_duration_seconds"`
	EffectiveDurationMS int64              `json:"effective_duration_ms"`
	TurnIntervalMS      int64              `json:"turn_interval_ms"`
	ObservedDurationMS  int64              `json:"observed_duration_ms"`
	DevelopmentOverride bool               `json:"development_override"`
	TurnsScheduled      int                `json:"turns_scheduled"`
	TurnsCompleted      int                `json:"turns_completed"`
	TurnsFailed         int                `json:"turns_failed"`
	TerminalCompleted   int                `json:"terminal_completed"`
	TerminalFailed      int                `json:"terminal_failed"`
	TerminalCanceled    int                `json:"terminal_canceled"`
	ProcessRestarts     int                `json:"process_restarts"`
	P95LatencyMS        int64              `json:"p95_latency_ms"`
	EarlyLatencyP50MS   int64              `json:"early_latency_p50_ms"`
	LateLatencyP50MS    int64              `json:"late_latency_p50_ms"`
	LateEarlyRatioBPS   int                `json:"late_early_ratio_basis_points"`
	RSSGrowthBytes      int64              `json:"rss_growth_bytes"`
	FDGrowth            int                `json:"fd_growth"`
	Slopes              H3SlopeSummary     `json:"slopes"`
	Samples             []H3ResourceSample `json:"samples"`
	EvidenceDigest      string             `json:"evidence_digest"`
}

type H3PackageEvidence struct {
	SchemaVersion   int      `json:"schema_version"`
	QualificationID string   `json:"qualification_id"`
	Status          string   `json:"status"`
	Version         string   `json:"version"`
	Stage           string   `json:"stage"`
	ManifestDigest  string   `json:"manifest_digest"`
	ChecksumsDigest string   `json:"checksums_digest"`
	SBOMDigest      string   `json:"sbom_digest"`
	Targets         []string `json:"targets"`
	EvidenceDigest  string   `json:"evidence_digest"`
}

type H3LaneEvidence struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	EvidenceDigest string `json:"evidence_digest"`
}

type H3ReleasePackage struct {
	ManifestDigest  string   `json:"manifest_digest"`
	ChecksumsDigest string   `json:"checksums_digest"`
	SBOMDigest      string   `json:"sbom_digest"`
	Targets         []string `json:"targets"`
}

type H3ReleaseEvidence struct {
	SchemaVersion    int              `json:"schema_version"`
	QualificationID  string           `json:"qualification_id"`
	SourceDigest     string           `json:"source_digest"`
	LockIdentity     string           `json:"lock_identity"`
	FoundationDigest string           `json:"foundation_digest"`
	RuntimeDigest    string           `json:"runtime_digest"`
	VSIXDigest       string           `json:"vsix_digest"`
	HostDigest       string           `json:"host_digest"`
	ProviderDigest   string           `json:"provider_digest"`
	ModelDigest      string           `json:"model_digest"`
	ConfigDigest     string           `json:"config_digest"`
	RequiredLanes    []H3LaneEvidence `json:"required_lanes"`
	Package          H3ReleasePackage `json:"package"`
	Decision         string           `json:"decision"`
}

func LoadH3(root, path string) (H3Catalog, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return H3Catalog{}, err
	}
	absolutePath := path
	if !filepath.IsAbs(absolutePath) {
		absolutePath = filepath.Join(absoluteRoot, filepath.FromSlash(path))
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return H3Catalog{}, errors.New("H3 catalog escapes repository root")
	}
	var catalog H3Catalog
	if err := decodeStrictFile(absolutePath, &catalog); err != nil {
		return H3Catalog{}, fmt.Errorf("decode H3 catalog: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return H3Catalog{}, err
	}
	return catalog, nil
}

func (c H3Catalog) Validate() error {
	if c.SchemaVersion != H3SchemaVersion {
		return fmt.Errorf("H3 catalog schema_version must be %d", H3SchemaVersion)
	}
	e := c.Endurance
	if e.DurationSeconds < 14_400 ||
		e.TurnIntervalSeconds < 1 ||
		e.TurnTimeoutSeconds < 1 ||
		e.WarmupTurns < 1 ||
		e.MinCompletedTurns < 1 ||
		e.MinCompletedTurns+e.WarmupTurns >
			int(e.DurationSeconds/e.TurnIntervalSeconds)+1 ||
		strings.TrimSpace(e.Prompt) == "" ||
		len(e.Prompt) > 256 ||
		e.MaxRSSSlopeBytesPerTurn < 0 ||
		e.MaxRSSGrowthBytes < 0 ||
		e.MaxFDSlopeMilliPerTurn < 0 ||
		e.MaxFDGrowth < 0 ||
		e.MaxPersistenceSlopeBytesPerTurn < 1 ||
		e.MaxLatencySlopeMilliMSPerTurn < 0 ||
		e.MaxP95LatencyMS < 1 ||
		e.MaxLateEarlyLatencyRatioBasisPoints < 10_000 {
		return errors.New("H3 Endurance policy is invalid")
	}
	r := c.Release
	if !slices.Equal(r.RequiredLanes, h3RequiredLanes) ||
		len(r.PackageTargets) != 5 ||
		!slices.Equal(r.PackageTargets, []string{
			"linux/amd64", "linux/arm64", "darwin/amd64",
			"darwin/arm64", "windows/amd64",
		}) {
		return errors.New("H3 release inventory is invalid")
	}
	for name, command := range map[string][]string{
		"release":   r.ReleaseCommand,
		"vscode_rc": r.VSCodeRCCommand,
		"package":   r.PackageCommand,
	} {
		if len(command) == 0 {
			return fmt.Errorf("H3 %s command is empty", name)
		}
		for _, value := range command {
			if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
				return fmt.Errorf("H3 %s command is invalid", name)
			}
		}
	}
	return nil
}
