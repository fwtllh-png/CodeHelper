// Package protocol defines the storage- and runner-independent evaluation
// contract. Adapters may translate benchmark fixtures and runtime receipts into
// these types, but this package deliberately performs no I/O.
package protocol

const Version = 1

type ResultStatus string

const (
	ResultPassed      ResultStatus = "passed"
	ResultFailed      ResultStatus = "failed"
	ResultUnavailable ResultStatus = "unavailable"
)

type TerminalStatus string

const (
	TerminalCompleted  TerminalStatus = "completed"
	TerminalFailed     TerminalStatus = "failed"
	TerminalCanceled   TerminalStatus = "canceled"
	TerminalIncomplete TerminalStatus = "incomplete"
)

// Case is a versioned evaluation input and its observable success conditions.
type Case struct {
	Version     int         `json:"version"`
	ID          string      `json:"id"`
	Revision    uint64      `json:"revision"`
	Digest      string      `json:"digest"`
	Category    string      `json:"category"`
	Prompt      string      `json:"prompt"`
	Fixture     string      `json:"fixture,omitempty"`
	Execution   Execution   `json:"execution"`
	Expectation Expectation `json:"expectation"`
}

type Execution struct {
	Tools     bool   `json:"tools"`
	Posture   string `json:"posture,omitempty"`
	Mode      string `json:"mode,omitempty"`
	MaxSteps  int    `json:"max_steps,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type Expectation struct {
	Terminal       TerminalStatus    `json:"terminal"`
	Files          map[string]string `json:"files,omitempty"`
	Unchanged      []string          `json:"unchanged,omitempty"`
	Absent         []string          `json:"absent,omitempty"`
	ToolsSucceeded []string          `json:"tools_succeeded,omitempty"`
	ToolsFailed    []string          `json:"tools_failed,omitempty"`
	OutputContains []string          `json:"output_contains,omitempty"`
	ChangedPaths   []string          `json:"changed_paths,omitempty"`
}

// Result is the normalized, comparable outcome of running one Case revision.
type Result struct {
	Version      int            `json:"version"`
	ID           string         `json:"id"`
	Revision     uint64         `json:"revision"`
	Digest       string         `json:"digest"`
	CaseID       string         `json:"case_id"`
	CaseRevision uint64         `json:"case_revision"`
	CaseDigest   string         `json:"case_digest"`
	Status       ResultStatus   `json:"status"`
	Terminal     TerminalStatus `json:"terminal,omitempty"`
	Failures     []string       `json:"failures,omitempty"`

	ToolsSucceeded []string      `json:"tools_succeeded,omitempty"`
	ToolsFailed    []string      `json:"tools_failed,omitempty"`
	ChangedPaths   []string      `json:"changed_paths,omitempty"`
	Verification   *Verification `json:"verification,omitempty"`
	Usage          Usage         `json:"usage"`
	RetryAttempts  int           `json:"retry_attempts"`
}

type Verification struct {
	Status      string `json:"status"`
	Action      string `json:"action"`
	RepairSteps int    `json:"repair_steps"`
}

type Usage struct {
	InputTokens         uint64 `json:"input_tokens"`
	UncachedInputTokens uint64 `json:"uncached_input_tokens"`
	OutputTokens        uint64 `json:"output_tokens"`
	ReasoningTokens     uint64 `json:"reasoning_tokens"`
	CachedTokens        uint64 `json:"cached_tokens"`
	CostMicrounits      int64  `json:"cost_microunits"`
	Calls               int    `json:"calls"`
	UnpricedCalls       int    `json:"unpriced_calls"`
}
