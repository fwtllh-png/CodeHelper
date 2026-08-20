package admission

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
)

const (
	H4SchemaVersion         = 1
	H4EvidenceSchemaVersion = 1
)

var h4RequiredLanes = []string{
	"h3_rc",
	"canary",
	"rollout_stop",
	"rollback",
	"incident_closure",
}

type H4Catalog struct {
	SchemaVersion int            `json:"schema_version"`
	Canary        H4CanarySpec   `json:"canary"`
	Rollback      H4RollbackSpec `json:"rollback"`
	Incident      H4IncidentSpec `json:"incident"`
}

type H4CanarySpec struct {
	PhaseSlots         []int  `json:"phase_slots"`
	TurnsPerSlot       int    `json:"turns_per_slot"`
	TurnIntervalMS     int64  `json:"turn_interval_ms"`
	TurnTimeoutSeconds int64  `json:"turn_timeout_seconds"`
	Prompt             string `json:"prompt"`
	MaxP95LatencyMS    int64  `json:"max_p95_latency_ms"`
	MaxRSSGrowthBytes  int64  `json:"max_rss_growth_bytes"`
	MaxFDGrowth        int    `json:"max_fd_growth"`
}

type H4RollbackSpec struct {
	Command []string `json:"command"`
}

type H4IncidentSpec struct {
	BatchID     string `json:"batch_id"`
	EntryPrefix string `json:"entry_prefix"`
	Reviewer    string `json:"reviewer"`
	ReviewedOn  string `json:"reviewed_on"`
}

type H4PhaseEvidence struct {
	ID                string `json:"id"`
	TargetSlots       int    `json:"target_slots"`
	ActiveSlots       int    `json:"active_slots"`
	TurnsScheduled    int    `json:"turns_scheduled"`
	TurnsCompleted    int    `json:"turns_completed"`
	TurnsFailed       int    `json:"turns_failed"`
	P95LatencyMS      int64  `json:"p95_latency_ms"`
	ExpansionDecision string `json:"expansion_decision"`
}

type H4SlotEvidence struct {
	ID                string `json:"id"`
	PID               int    `json:"pid"`
	TurnsScheduled    int    `json:"turns_scheduled"`
	TurnsCompleted    int    `json:"turns_completed"`
	TurnsFailed       int    `json:"turns_failed"`
	TerminalCompleted int    `json:"terminal_completed"`
	TerminalFailed    int    `json:"terminal_failed"`
	TerminalCanceled  int    `json:"terminal_canceled"`
	ProcessRestarts   int    `json:"process_restarts"`
	P95LatencyMS      int64  `json:"p95_latency_ms"`
	RSSGrowthBytes    int64  `json:"rss_growth_bytes"`
	FDGrowth          int    `json:"fd_growth"`
}

type H4CanaryEvidence struct {
	SchemaVersion       int               `json:"schema_version"`
	QualificationID     string            `json:"qualification_id"`
	Status              string            `json:"status"`
	SourceDigest        string            `json:"source_digest"`
	LockIdentity        string            `json:"lock_identity"`
	PackageDigest       string            `json:"package_digest"`
	DevelopmentOverride bool              `json:"development_override"`
	Phases              []H4PhaseEvidence `json:"phases"`
	Slots               []H4SlotEvidence  `json:"slots"`
	TurnsScheduled      int               `json:"turns_scheduled"`
	TurnsCompleted      int               `json:"turns_completed"`
	TurnsFailed         int               `json:"turns_failed"`
	P95LatencyMS        int64             `json:"p95_latency_ms"`
	EvidenceDigest      string            `json:"evidence_digest"`
}

type H4StopEvidence struct {
	SchemaVersion   int    `json:"schema_version"`
	Status          string `json:"status"`
	InjectedFailure string `json:"injected_failure"`
	Decision        string `json:"decision"`
	StartedSlots    int    `json:"started_slots"`
	BlockedSlots    int    `json:"blocked_slots"`
	EvidenceDigest  string `json:"evidence_digest"`
}

type H4IncidentEvidence struct {
	SchemaVersion    int    `json:"schema_version"`
	Status           string `json:"status"`
	BatchID          string `json:"batch_id"`
	SourceDigest     string `json:"source_digest"`
	ReviewDigest     string `json:"review_digest"`
	FailureSignature string `json:"failure_signature"`
	Entries          int    `json:"entries"`
	SecretScan       string `json:"secret_scan"`
	ReplayLevel      string `json:"replay_level"`
	EvidenceDigest   string `json:"evidence_digest"`
}

type H4LaneEvidence struct {
	ID             string `json:"id"`
	Status         string `json:"status"`
	EvidenceDigest string `json:"evidence_digest"`
}

type H4AdmissionEvidence struct {
	SchemaVersion     int              `json:"schema_version"`
	QualificationID   string           `json:"qualification_id"`
	SourceDigest      string           `json:"source_digest"`
	LockIdentity      string           `json:"lock_identity"`
	FoundationDigest  string           `json:"foundation_digest"`
	RuntimeDigest     string           `json:"runtime_digest"`
	VSIXDigest        string           `json:"vsix_digest"`
	H3QualificationID string           `json:"h3_qualification_id"`
	H3ReleaseDigest   string           `json:"h3_release_digest"`
	PackageDigest     string           `json:"package_digest"`
	RequiredLanes     []H4LaneEvidence `json:"required_lanes"`
	Scope             string           `json:"scope"`
	PublicRelease     bool             `json:"public_release"`
	Decision          string           `json:"decision"`
}

func LoadH4(root, path string) (H4Catalog, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return H4Catalog{}, err
	}
	absolutePath := path
	if !filepath.IsAbs(absolutePath) {
		absolutePath = filepath.Join(absoluteRoot, filepath.FromSlash(path))
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return H4Catalog{}, errors.New("H4 catalog escapes repository root")
	}
	var catalog H4Catalog
	if err := decodeStrictFile(absolutePath, &catalog); err != nil {
		return H4Catalog{}, fmt.Errorf("decode H4 catalog: %w", err)
	}
	if err := catalog.Validate(); err != nil {
		return H4Catalog{}, err
	}
	return catalog, nil
}

func (c H4Catalog) Validate() error {
	if c.SchemaVersion != H4SchemaVersion {
		return fmt.Errorf("H4 catalog schema_version must be %d", H4SchemaVersion)
	}
	if !slices.Equal(c.Canary.PhaseSlots, []int{1, 2, 3}) ||
		c.Canary.TurnsPerSlot < 10 ||
		c.Canary.TurnIntervalMS < 1 ||
		c.Canary.TurnTimeoutSeconds < 1 ||
		strings.TrimSpace(c.Canary.Prompt) == "" ||
		len(c.Canary.Prompt) > 256 ||
		c.Canary.MaxP95LatencyMS < 1 ||
		c.Canary.MaxRSSGrowthBytes < 0 ||
		c.Canary.MaxFDGrowth < 0 {
		return errors.New("H4 Canary policy is invalid")
	}
	if len(c.Rollback.Command) == 0 {
		return errors.New("H4 rollback command is empty")
	}
	for _, value := range c.Rollback.Command {
		if strings.TrimSpace(value) == "" || strings.ContainsRune(value, 0) {
			return errors.New("H4 rollback command is invalid")
		}
	}
	if !validID(c.Incident.BatchID) ||
		!validID(c.Incident.EntryPrefix) ||
		!validID(c.Incident.Reviewer) ||
		strings.TrimSpace(c.Incident.ReviewedOn) == "" {
		return errors.New("H4 incident policy is invalid")
	}
	return nil
}

func digestH4Canary(evidence H4CanaryEvidence) string {
	evidence.EvidenceDigest = ""
	return digestH2(evidence)
}

func digestH4Stop(evidence H4StopEvidence) string {
	evidence.EvidenceDigest = ""
	return digestH2(evidence)
}

func digestH4Incident(evidence H4IncidentEvidence) string {
	evidence.EvidenceDigest = ""
	return digestH2(evidence)
}

func DigestH4Admission(evidence H4AdmissionEvidence) string {
	return digestH2(evidence)
}
