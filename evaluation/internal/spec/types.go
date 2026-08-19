package spec

import "time"

const SchemaVersion = 2

type Status string

const (
	StatusPassed       Status = "passed"
	StatusFailed       Status = "failed"
	StatusUnavailable  Status = "unavailable"
	StatusNotEvaluated Status = "not_evaluated"
	StatusInvalid      Status = "invalid"
)

func (s Status) Valid() bool {
	switch s {
	case StatusPassed, StatusFailed, StatusUnavailable, StatusNotEvaluated,
		StatusInvalid:
		return true
	default:
		return false
	}
}

type Risk string

const (
	RiskP0 Risk = "P0"
	RiskP1 Risk = "P1"
	RiskP2 Risk = "P2"
)

func (r Risk) Valid() bool {
	switch r {
	case RiskP0, RiskP1, RiskP2:
		return true
	default:
		return false
	}
}

type Driver string

const (
	DriverCommand Driver = "command"
	DriverACP     Driver = "acp"
	DriverVSCode  Driver = "vscode-electron"
	DriverTUI     Driver = "tui"
	DriverWorker  Driver = "worker"
)

func (d Driver) Valid() bool {
	switch d {
	case DriverCommand, DriverACP, DriverVSCode, DriverTUI, DriverWorker:
		return true
	default:
		return false
	}
}

type Manifest struct {
	SchemaVersion int     `json:"schema_version"`
	Suites        []Suite `json:"suites"`
}

type Suite struct {
	ID              string            `json:"id"`
	Owner           string            `json:"owner"`
	Risk            Risk              `json:"risk"`
	DefaultLane     string            `json:"default_lane"`
	Scenarios       []string          `json:"scenarios"`
	RequiredOracles []string          `json:"required_oracles"`
	Repetitions     int               `json:"repetitions"`
	Requirements    Requirements      `json:"requirements"`
	Budgets         Budgets           `json:"budgets"`
	ReleasePolicy   ReleasePolicy     `json:"release_policy"`
	Exceptions      []PolicyException `json:"exceptions"`
}

type Requirements struct {
	Commands     []string `json:"commands"`
	Platforms    []string `json:"platforms"`
	Capabilities []string `json:"capabilities"`
}

type Budgets struct {
	WallTimeMS     int   `json:"wall_time_ms"`
	MaxAttempts    int   `json:"max_attempts"`
	MaxOutputBytes int64 `json:"max_output_bytes"`
}

type ReleasePolicy struct {
	Blocking         bool     `json:"blocking"`
	AllowedStatuses  []Status `json:"allowed_statuses"`
	MinimumValidRuns int      `json:"minimum_valid_runs"`
}

type PolicyException struct {
	ID              string   `json:"id"`
	Owner           string   `json:"owner"`
	Reason          string   `json:"reason"`
	ExpiresOn       string   `json:"expires_on"`
	ScenarioIDs     []string `json:"scenario_ids"`
	AllowedStatuses []Status `json:"allowed_statuses"`
}

type Scenario struct {
	SchemaVersion     int               `json:"schema_version"`
	ID                string            `json:"id"`
	Title             string            `json:"title"`
	Family            string            `json:"family"`
	Owner             string            `json:"owner"`
	Risk              Risk              `json:"risk"`
	Driver            Driver            `json:"driver"`
	ProviderMode      string            `json:"provider_mode"`
	Workspace         string            `json:"workspace"`
	FixtureID         string            `json:"fixture_id"`
	ExpectedFacts     []string          `json:"expected_facts"`
	Turns             []Turn            `json:"turns"`
	Faults            []Fault           `json:"faults"`
	Oracles           []string          `json:"oracles"`
	RequiredEvidence  []string          `json:"required_evidence"`
	RequiredMutations []string          `json:"required_mutations"`
	CleanupContract   string            `json:"cleanup_contract"`
	RunPlan           RunPlan           `json:"run_plan"`
	Budgets           Budgets           `json:"budgets"`
	Requirements      Requirements      `json:"requirements"`
	Execution         ScenarioExecution `json:"execution"`
	Tags              []string          `json:"tags"`
}

type RunPlan struct {
	Attempts        int    `json:"attempts"`
	CollectAllGroup string `json:"collect_all_group"`
}

type Turn struct {
	PromptFile string `json:"prompt_file"`
}

type Fault struct {
	At     string `json:"at"`
	Action string `json:"action"`
}

type ScenarioExecution struct {
	Command          []string `json:"command"`
	WorkingDirectory string   `json:"working_directory"`
}

type SourceIdentity struct {
	Commit      string `json:"commit"`
	Dirty       bool   `json:"dirty"`
	DirtyDigest string `json:"dirty_digest"`
}

type ArtifactIdentity struct {
	HarnessDigest  string `json:"harness_digest"`
	RuntimeDigest  string `json:"runtime_digest"`
	HostDigest     string `json:"host_digest"`
	ScenarioDigest string `json:"scenario_digest"`
	FixtureDigest  string `json:"fixture_digest"`
	ProviderDigest string `json:"provider_digest"`
	ModelDigest    string `json:"model_digest"`
	ConfigDigest   string `json:"config_digest"`
}

type Environment struct {
	Host      string `json:"host"`
	OS        string `json:"os"`
	Arch      string `json:"arch"`
	GoVersion string `json:"go_version"`
}

type ExecutionResult struct {
	Command      []string `json:"command"`
	Directory    string   `json:"directory"`
	ExitCode     *int     `json:"exit_code"`
	Signal       string   `json:"signal,omitempty"`
	TimedOut     bool     `json:"timed_out"`
	StdoutBytes  int64    `json:"stdout_bytes"`
	StderrBytes  int64    `json:"stderr_bytes"`
	StdoutDigest string   `json:"stdout_digest"`
	StderrDigest string   `json:"stderr_digest"`
	Truncated    bool     `json:"truncated"`
	ReasonCode   string   `json:"reason_code"`
}

type EvidenceRecord struct {
	SchemaVersion int    `json:"schema_version"`
	RunPartition  string `json:"run_partition"`
	RunID         string `json:"run_id"`
	ScenarioID    string `json:"scenario_id"`
	Attempt       int    `json:"attempt"`
	Producer      string `json:"producer"`
	Kind          string `json:"kind"`
	Digest        string `json:"digest"`
	Ref           string `json:"ref,omitempty"`
}

type OracleResult struct {
	ID       string   `json:"id"`
	Status   Status   `json:"status"`
	Severity Risk     `json:"severity"`
	Summary  string   `json:"summary"`
	Evidence []string `json:"evidence"`
}

type RunRecord struct {
	SchemaVersion int              `json:"schema_version"`
	RunID         string           `json:"run_id"`
	RunPartition  string           `json:"run_partition"`
	SuiteID       string           `json:"suite_id"`
	ScenarioID    string           `json:"scenario_id"`
	Variant       string           `json:"variant"`
	Attempt       int              `json:"attempt"`
	Seed          int64            `json:"seed"`
	Status        Status           `json:"status"`
	StartedAt     time.Time        `json:"started_at"`
	EndedAt       time.Time        `json:"ended_at"`
	DurationMS    int64            `json:"duration_ms"`
	Source        SourceIdentity   `json:"source"`
	Artifacts     ArtifactIdentity `json:"artifacts"`
	Environment   Environment      `json:"environment"`
	Execution     ExecutionResult  `json:"execution"`
	OracleResults []OracleResult   `json:"oracle_results"`
	Evidence      []EvidenceRecord `json:"evidence"`
}

type Summary struct {
	Total                 int `json:"total"`
	Passed                int `json:"passed"`
	Failed                int `json:"failed"`
	Unavailable           int `json:"unavailable"`
	NotEvaluated          int `json:"not_evaluated"`
	Invalid               int `json:"invalid"`
	FirstAttemptTotal     int `json:"first_attempt_total"`
	FirstAttemptPassed    int `json:"first_attempt_passed"`
	RecoveredAttemptTotal int `json:"recovered_attempt_total"`
	RecoveredPassed       int `json:"recovered_passed"`
}

type Report struct {
	SchemaVersion int               `json:"schema_version"`
	Status        Status            `json:"status"`
	GeneratedAt   time.Time         `json:"generated_at"`
	Source        SourceIdentity    `json:"source"`
	Summary       Summary           `json:"summary"`
	Runs          []RunRecord       `json:"runs"`
	Admission     AdmissionDecision `json:"admission"`
}

type AdmissionDecision struct {
	Allowed  bool     `json:"allowed"`
	Blocking bool     `json:"blocking"`
	Reasons  []string `json:"reasons"`
}
