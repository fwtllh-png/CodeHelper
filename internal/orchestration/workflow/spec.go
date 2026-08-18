// Package workflow owns CodeHelper's restricted orchestration IR and run lifecycle.
// Side effects happen only through an injected Driver; the default permission
// posture denies filesystem, shell, and network access.
package workflow

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"time"
)

var (
	ErrInvalidSpec      = errors.New("invalid workflow spec")
	ErrPermissionDenied = errors.New("workflow permission denied")
	ErrBudgetExhausted  = errors.New("workflow budget exhausted")
	ErrSpecChanged      = errors.New("workflow spec changed since the run started")
)

type Permissions struct {
	Filesystem bool `json:"filesystem"`
	Shell      bool `json:"shell"`
	Network    bool `json:"network"`
}

type Budget struct {
	MaxTokens   uint64  `json:"max_tokens,omitempty"`
	MaxCostUSD  float64 `json:"max_cost_usd,omitempty"`
	MaxSteps    int     `json:"max_steps,omitempty"`
	MaxAgents   int     `json:"max_agents,omitempty"`
	MaxDepth    int     `json:"max_depth,omitempty"`
	MaxParallel int     `json:"max_parallel,omitempty"`
}

func (b Budget) WithDefaults() Budget {
	if b.MaxAgents <= 0 {
		b.MaxAgents = 1000
	}
	if b.MaxDepth <= 0 {
		b.MaxDepth = 5
	}
	if b.MaxParallel <= 0 {
		b.MaxParallel = 1000
	}
	return b
}

type NodeKind string

const (
	NodeTask     NodeKind = "task"
	NodeParallel NodeKind = "parallel"
	NodePhase    NodeKind = "phase"
)

// NodeStatus is what a node ended up as. It is recorded per node so that a run
// that died halfway can be resumed instead of repeated.
type NodeStatus string

const (
	NodeStatusPending   NodeStatus = "pending"
	NodeStatusRunning   NodeStatus = "running"
	NodeStatusCompleted NodeStatus = "completed"
	NodeStatusBlocked   NodeStatus = "blocked"
	NodeStatusFailed    NodeStatus = "failed"
	NodeStatusSkipped   NodeStatus = "skipped"
)

// Terminal reports whether a status is one a dependent node may act on.
func (s NodeStatus) Terminal() bool {
	switch s {
	case NodeStatusCompleted, NodeStatusBlocked, NodeStatusFailed,
		NodeStatusSkipped:
		return true
	default:
		return false
	}
}

// Condition lets a node run on an upstream outcome other than success, which is
// what makes a compensation node expressible. It is a structured predicate
// rather than an expression: an expression language would need its own sandbox,
// determinism rules and version compatibility.
type Condition struct {
	Node   string     `json:"node"`
	Status NodeStatus `json:"status"`
}

// Retry is a node's own attempt policy, shaped like the task-level one so that
// an operator reading either recognizes the other.
type Retry struct {
	MaxAttempts int `json:"max_attempts,omitempty"`
	BackoffMS   int `json:"backoff_ms,omitempty"`
	// Idempotent declares that a repeated Node attempt cannot duplicate an
	// external effect. Retry ownership remains in Workflow Runtime.
	Idempotent bool `json:"idempotent,omitempty"`
}

type Node struct {
	ID   string   `json:"id"`
	Kind NodeKind `json:"kind"`
	Role string   `json:"role,omitempty"`
	// Needs are the nodes that must reach a terminal status before this one is
	// considered. Dependencies rather than array order are what let two nodes
	// say "we can run at the same time".
	Needs       []string        `json:"needs,omitempty"`
	Prompt      string          `json:"prompt,omitempty"`
	Profile     string          `json:"profile,omitempty"`
	Schema      json.RawMessage `json:"response_schema,omitempty"`
	Children    []string        `json:"children,omitempty"`
	When        *Condition      `json:"when,omitempty"`
	Retry       *Retry          `json:"retry,omitempty"`
	TimeoutMS   int             `json:"timeout_ms,omitempty"`
	Permissions *Permissions    `json:"permissions,omitempty"`
	Budget      *Budget         `json:"budget,omitempty"`
	Metadata    json.RawMessage `json:"metadata,omitempty"`
}

// dependencies are the edges this node waits on. A parallel node joins its
// children, so its children are dependencies too: that is what turns the old
// "spawn and ignore the results" behaviour into a join.
func (n Node) dependencies() []string {
	edges := make([]string, 0, len(n.Needs)+len(n.Children))
	seen := make(map[string]bool, len(n.Needs)+len(n.Children))
	for _, group := range [][]string{n.Needs, n.Children} {
		for _, id := range group {
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			edges = append(edges, id)
		}
	}
	return edges
}

func (n Node) attempts() int {
	if n.Retry == nil || n.Retry.MaxAttempts <= 0 {
		return 1
	}
	return n.Retry.MaxAttempts
}

func (n Node) backoff() time.Duration {
	if n.Retry == nil || n.Retry.BackoffMS <= 0 {
		return 0
	}
	return time.Duration(n.Retry.BackoffMS) * time.Millisecond
}

func (n Node) timeout() time.Duration {
	if n.TimeoutMS <= 0 {
		return 0
	}
	return time.Duration(n.TimeoutMS) * time.Millisecond
}

type Spec struct {
	ID          string      `json:"id,omitempty"`
	Goal        string      `json:"goal"`
	Description string      `json:"description,omitempty"`
	Budget      Budget      `json:"budget"`
	Permissions Permissions `json:"permissions"`
	Nodes       []Node      `json:"nodes"`
}

func (s Spec) Validate() error {
	if strings.TrimSpace(s.Goal) == "" {
		return fmt.Errorf("%w: goal is required", ErrInvalidSpec)
	}
	seen := make(map[string]bool, len(s.Nodes))
	for _, node := range s.Nodes {
		if strings.TrimSpace(node.ID) == "" {
			return fmt.Errorf("%w: node id is required", ErrInvalidSpec)
		}
		if seen[node.ID] {
			return fmt.Errorf("%w: duplicate node id %q", ErrInvalidSpec, node.ID)
		}
		seen[node.ID] = true
		switch node.Kind {
		case NodeTask, NodeParallel, NodePhase:
		default:
			return fmt.Errorf("%w: unsupported node kind %q", ErrInvalidSpec, node.Kind)
		}
		if node.Kind != NodeTask &&
			(strings.TrimSpace(node.Profile) != "" || len(node.Schema) != 0) {
			return fmt.Errorf(
				"%w: node %q uses task-only profile/response_schema",
				ErrInvalidSpec,
				node.ID,
			)
		}
		if node.Kind == NodeTask {
			if err := ValidateTaskRequest(TaskRequest{
				Prompt:  firstNonEmpty(node.Prompt, s.Goal),
				Profile: node.Profile,
				Schema:  node.Schema,
			}); err != nil {
				return fmt.Errorf("%w: node %q: %w", ErrInvalidSpec, node.ID, err)
			}
		}
		for _, child := range node.Children {
			if child == "" {
				return fmt.Errorf("%w: empty child id", ErrInvalidSpec)
			}
		}
	}
	for _, node := range s.Nodes {
		for _, child := range node.Children {
			if !seen[child] {
				return fmt.Errorf("%w: unknown child %q", ErrInvalidSpec, child)
			}
		}
		if err := s.validateEdges(node, seen); err != nil {
			return err
		}
	}
	if _, err := s.order(); err != nil {
		return err
	}
	return nil
}

func (s Spec) validateEdges(node Node, known map[string]bool) error {
	for _, need := range node.Needs {
		if need == "" {
			return fmt.Errorf("%w: node %q has an empty dependency", ErrInvalidSpec, node.ID)
		}
		if !known[need] {
			return fmt.Errorf("%w: node %q needs unknown node %q", ErrInvalidSpec, node.ID, need)
		}
		if need == node.ID {
			return fmt.Errorf("%w: node %q depends on itself", ErrInvalidSpec, node.ID)
		}
	}
	if node.When != nil {
		// A condition on a node this one does not wait for would be read before
		// that node has an outcome, which is a race rather than a policy.
		if !slices.Contains(node.dependencies(), node.When.Node) {
			return fmt.Errorf("%w: node %q conditions on %q without depending on it",
				ErrInvalidSpec, node.ID, node.When.Node)
		}
		switch node.When.Status {
		case NodeStatusCompleted, NodeStatusBlocked, NodeStatusFailed,
			NodeStatusSkipped:
		default:
			return fmt.Errorf("%w: node %q conditions on unsupported status %q",
				ErrInvalidSpec, node.ID, node.When.Status)
		}
	}
	if node.Retry != nil {
		if node.Retry.MaxAttempts < 0 || node.Retry.BackoffMS < 0 {
			return fmt.Errorf("%w: node %q has a negative retry policy", ErrInvalidSpec, node.ID)
		}
	}
	if node.TimeoutMS < 0 {
		return fmt.Errorf("%w: node %q has a negative timeout", ErrInvalidSpec, node.ID)
	}
	return nil
}

// order returns the nodes in dependency order, keeping spec order between nodes
// that do not depend on each other so that a run is reproducible.
func (s Spec) order() ([]Node, error) {
	pending := make(map[string]int, len(s.Nodes))
	dependents := make(map[string][]string, len(s.Nodes))
	byID := make(map[string]Node, len(s.Nodes))
	for _, node := range s.Nodes {
		byID[node.ID] = node
		edges := node.dependencies()
		pending[node.ID] = len(edges)
		for _, edge := range edges {
			dependents[edge] = append(dependents[edge], node.ID)
		}
	}
	ready := make([]string, 0, len(s.Nodes))
	for _, node := range s.Nodes {
		if pending[node.ID] == 0 {
			ready = append(ready, node.ID)
		}
	}
	sorted := make([]Node, 0, len(s.Nodes))
	for len(ready) > 0 {
		id := ready[0]
		ready = ready[1:]
		sorted = append(sorted, byID[id])
		for _, dependent := range dependents[id] {
			pending[dependent]--
			if pending[dependent] == 0 {
				ready = append(ready, dependent)
			}
		}
	}
	if len(sorted) != len(s.Nodes) {
		stuck := make([]string, 0, len(s.Nodes)-len(sorted))
		for _, node := range s.Nodes {
			if pending[node.ID] > 0 {
				stuck = append(stuck, node.ID)
			}
		}
		sort.Strings(stuck)
		return nil, fmt.Errorf("%w: dependency cycle among %s",
			ErrInvalidSpec, strings.Join(stuck, ", "))
	}
	return sorted, nil
}

// Fingerprint identifies the spec a run was started from. Resuming against a
// different fingerprint would silently apply a new graph to old node records,
// so callers compare it and refuse rather than guess.
func (s Spec) Fingerprint() string {
	canonical, err := json.Marshal(s)
	if err != nil {
		// Spec is plain data with json tags, so this cannot fail; if it somehow
		// does, a fingerprint nothing matches is the fail-closed answer.
		return "unfingerprintable"
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

func (s Spec) EffectivePermissions(node Node) Permissions {
	if node.Permissions != nil {
		return *node.Permissions
	}
	return s.Permissions
}

func (s Spec) AssertAllowed(node Node, capability string) error {
	perms := s.EffectivePermissions(node)
	switch capability {
	case "filesystem":
		if !perms.Filesystem {
			return fmt.Errorf("%w: filesystem", ErrPermissionDenied)
		}
	case "shell":
		if !perms.Shell {
			return fmt.Errorf("%w: shell", ErrPermissionDenied)
		}
	case "network":
		if !perms.Network {
			return fmt.Errorf("%w: network", ErrPermissionDenied)
		}
	default:
		return fmt.Errorf("%w: unknown capability %q", ErrPermissionDenied, capability)
	}
	return nil
}

type RunStatus string

const (
	RunQueued    RunStatus = "queued"
	RunRunning   RunStatus = "running"
	RunCompleted RunStatus = "completed"
	RunBlocked   RunStatus = "blocked"
	RunFailed    RunStatus = "failed"
	RunCanceled  RunStatus = "canceled"
)

type Run struct {
	ID        string          `json:"id"`
	SpecID    string          `json:"spec_id,omitempty"`
	Goal      string          `json:"goal"`
	Status    RunStatus       `json:"status"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     string          `json:"error,omitempty"`
	Nodes     []NodeResult    `json:"nodes,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	UpdatedAt time.Time       `json:"updated_at"`
}

// NodeResult is one node's outcome within a run, in dependency order.
type NodeResult struct {
	ID                string     `json:"id"`
	Status            NodeStatus `json:"status"`
	Attempt           int        `json:"attempt,omitempty"`
	Reason            string     `json:"reason,omitempty"`
	Content           string     `json:"content,omitempty"`
	Usage             WorkUsage  `json:"usage,omitempty"`
	PermissionDigests []string   `json:"permission_digests,omitempty"`
	// Resumed marks a node whose outcome was read from the checkpoint instead of
	// executed again, which is the difference an operator asks about first.
	Resumed   bool `json:"resumed,omitempty"`
	retryable bool
}

type ProgressKind string

const (
	ProgressLog   ProgressKind = "log"
	ProgressPhase ProgressKind = "phase"
)

type ProgressEvent struct {
	Kind    ProgressKind `json:"kind"`
	Message string       `json:"message"`
}

// Driver is the only host seam Workflow scripts may use for side effects.
type Driver interface {
	SpawnTask(ctx context.Context, req TaskRequest) (TaskResult, error)
	CancelAll() error
	Budget() BudgetSnapshot
	Progress(ProgressEvent) error
}

type TaskRequest struct {
	RunID       string          `json:"run_id,omitempty"`
	NodeID      string          `json:"node_id,omitempty"`
	Attempt     int             `json:"attempt,omitempty"`
	TraceParent string          `json:"traceparent,omitempty"`
	TraceState  string          `json:"tracestate,omitempty"`
	Role        string          `json:"role,omitempty"`
	Prompt      string          `json:"prompt"`
	Profile     string          `json:"profile,omitempty"`
	Schema      json.RawMessage `json:"response_schema,omitempty"`
}

type TaskResult struct {
	Success           bool            `json:"success"`
	Content           string          `json:"content"`
	Data              json.RawMessage `json:"data,omitempty"`
	Error             string          `json:"error,omitempty"`
	Usage             WorkUsage       `json:"usage,omitempty"`
	PermissionDigests []string        `json:"permission_digests,omitempty"`
}

type WorkUsage struct {
	Tokens     uint64 `json:"tokens,omitempty"`
	CostMicros uint64 `json:"cost_micros,omitempty"`
	CostKnown  bool   `json:"cost_known,omitempty"`
}

type BudgetSnapshot struct {
	TotalTokens     *uint64  `json:"total_tokens,omitempty"`
	SpentTokens     uint64   `json:"spent_tokens"`
	TotalCostUSD    *float64 `json:"total_cost_usd,omitempty"`
	SpentCostUSD    float64  `json:"spent_cost_usd"`
	RemainingTokens *uint64  `json:"remaining_tokens,omitempty"`
}
