// Package subagent owns role routing, worktree isolation, mailbox, and takeover.
package subagent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type Role string

// Canonical roles aligned with the reference SUBAGENTS taxonomy.
const (
	RoleGeneral     Role = "general"
	RoleExplore     Role = "explore"
	RolePlan        Role = "plan"
	RoleReview      Role = "review"
	RoleImplementer Role = "implementer"
	RoleVerifier    Role = "verifier"
	RoleAwaiter     Role = "awaiter"
	RoleCustom      Role = "custom"
)

type Stance string

const (
	StanceWrite        Stance = "write"
	StanceReadOnly     Stance = "read_only"
	StanceMinimalWrite Stance = "minimal_write"
	StanceTestFocused  Stance = "test_focused"
	StanceCustom       Stance = "custom"
)

type Budget struct {
	MaxTokens   uint64
	MaxCostUSD  float64
	MaxDepth    int
	MaxParallel int
}

func (b Budget) WithDefaults() Budget {
	if b.MaxDepth <= 0 {
		b.MaxDepth = 5
	}
	if b.MaxParallel <= 0 {
		b.MaxParallel = 8
	}
	return b
}

type Route struct {
	Role    Role
	Profile string
	Stance  Stance
}

// ParseRole maps canonical names and compatibility aliases to a Role.
func ParseRole(raw string) (Role, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "general", "worker":
		return RoleGeneral, nil
	case "explore", "explorer", "exploration":
		return RoleExplore, nil
	case "plan", "planner":
		return RolePlan, nil
	case "review", "reviewer":
		return RoleReview, nil
	case "implementer", "implement", "implementation", "builder":
		return RoleImplementer, nil
	case "verifier", "verify", "verification", "validator", "tester":
		return RoleVerifier, nil
	case "awaiter", "await":
		return RoleAwaiter, nil
	case "custom":
		return RoleCustom, nil
	default:
		return "", fmt.Errorf("unsupported role %q", raw)
	}
}

type ToolGate interface {
	Execute(ctx context.Context, callID, name string, raw json.RawMessage) (tool.Result, error)
}

type RuntimeHost interface {
	StartTurn(ctx context.Context, agentID, prompt string) (string, error)
	CancelTurn(ctx context.Context, agentID, turnID string) error
}

type Options struct {
	Root      string
	Budget    Budget
	Gate      ToolGate
	Runtime   RuntimeHost
	Worktrees WorktreeProvider
	Roles     RoleCatalog
}

type Manager struct {
	mu        sync.Mutex
	wait      *sync.Cond
	root      string
	budget    Budget
	gate      ToolGate
	runtime   RuntimeHost
	trees     WorktreeProvider
	roles     RoleCatalog
	agents    map[string]*Agent
	mailbox   *Mailbox
	worktrees map[string]*Worktree
	claims    map[string]string // workspace-relative path -> owning agent id
	active    atomic.Int32
	graph     Graph
	workspace string
	sessionID string
	nextID    int
}

type Agent struct {
	ID        string
	Workspace string
	SessionID string
	Role      Role
	Profile   string
	Stance    Stance
	Depth     int
	// Worktree is where this agent works, and Isolated says whether writing
	// there is safe. A writing agent without isolation must not run.
	Worktree string
	Isolated bool
	// Serialized means this child deliberately shares the host workspace and
	// may run only while holding the session's whole-turn workspace gate.
	Serialized bool
	// BaseRev is the spawn-time git revision of an isolated worktree; empty for
	// scratch / read-only children that never get a checkout.
	BaseRev           string
	Parent            string
	Closed            bool
	Status            Status
	TurnID            string
	LastMessage       string
	Result            *Result
	TaskName          string
	ExpectedOutput    string
	OwnedPaths        []string
	DelegationTrigger DelegationTrigger
	RoleInstructions  string
}

func Open(options Options) (*Manager, error) {
	if strings.TrimSpace(options.Root) == "" {
		return nil, errors.New("subagent root is required")
	}
	if options.Gate == nil {
		return nil, errors.New("tool gate is required")
	}
	if err := os.MkdirAll(options.Root, 0o700); err != nil {
		return nil, err
	}
	trees := options.Worktrees
	if trees == nil {
		trees = scratchWorktrees{root: options.Root}
	}
	roles := options.Roles
	if len(roles.specs) == 0 {
		roles = DefaultRoleCatalog()
	}
	manager := &Manager{
		root: options.Root, budget: options.Budget.WithDefaults(),
		gate: options.Gate, runtime: options.Runtime, trees: trees,
		roles:  roles,
		agents: make(map[string]*Agent), mailbox: NewMailbox(),
		worktrees: make(map[string]*Worktree),
		claims:    make(map[string]string),
	}
	manager.wait = sync.NewCond(&manager.mu)
	manager.mailbox.onDeliver = func(msg Message) {
		manager.recordMessage(msg.From, msg.To, msg.Sequence, msg.Body)
	}
	return manager, nil
}

func (m *Manager) Route(role Role) Route {
	spec, err := m.roles.Resolve(role)
	if err != nil {
		spec, _ = m.roles.Resolve(RoleGeneral)
	}
	return Route{Role: spec.Role, Profile: spec.Profile, Stance: spec.Stance}
}

// Spawn is retained for internal compatibility. Model-facing and worker paths
// use AgentControl, which adds delegation admission and structured intent.
func (m *Manager) Spawn(parentID string, role Role, prompt string) (*Agent, error) {
	spec, err := m.roles.Resolve(role)
	if err != nil {
		return nil, err
	}
	return m.spawn(DelegationIntent{
		TaskName:       "internal_task",
		Role:           role,
		Objective:      prompt,
		ExpectedOutput: "Return a concise result with supporting evidence.",
		ParentID:       parentID,
		Trigger:        TriggerSystem,
	}, spec)
}

func (m *Manager) spawn(intent DelegationIntent, spec RoleSpec) (*Agent, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	depth := 0
	if intent.ParentID != "" {
		parent, ok := m.agents[intent.ParentID]
		if !ok || parent.Closed {
			return nil, errors.New("parent agent unavailable")
		}
		depth = parent.Depth + 1
	}
	if depth > m.budget.MaxDepth {
		return nil, fmt.Errorf("recursion depth %d exceeds limit %d", depth, m.budget.MaxDepth)
	}
	if int(m.active.Load()) >= m.budget.MaxParallel {
		return nil, errors.New("subagent concurrency budget exhausted")
	}
	m.nextID++
	id := fmt.Sprintf("agent-%d", m.nextID)
	// The stance decides what kind of directory the agent needs, so routing has
	// to happen before provisioning: an explore child must not pay for a checkout.
	wt, err := m.trees.Provision(id, spec.Stance)
	if err != nil {
		return nil, err
	}
	agent := &Agent{
		ID: id, Workspace: m.workspace, SessionID: m.sessionID,
		Role: spec.Role, Profile: spec.Profile, Stance: spec.Stance,
		Depth: depth, Worktree: wt.Path, Isolated: wt.Isolated,
		Serialized: wt.Serialized, BaseRev: wt.BaseRev,
		Parent: intent.ParentID, Status: StatusPendingInit,
		TaskName:          strings.TrimSpace(intent.TaskName),
		ExpectedOutput:    strings.TrimSpace(intent.ExpectedOutput),
		OwnedPaths:        append([]string(nil), intent.OwnedPaths...),
		DelegationTrigger: intent.Trigger,
		RoleInstructions:  spec.Instructions,
	}
	if err := m.recordSpawnLocked(agent); err != nil {
		_ = m.trees.Discard(wt)
		return nil, fmt.Errorf("record agent spawn: %w", err)
	}
	m.agents[id] = agent
	m.worktrees[id] = &wt
	m.active.Add(1)
	m.wait.Broadcast()
	return agent, nil
}

func (m *Manager) ExecuteTool(ctx context.Context, agentID, callID, name string, raw json.RawMessage) (tool.Result, error) {
	m.mu.Lock()
	agent, ok := m.agents[agentID]
	gate := m.gate
	m.mu.Unlock()
	if !ok || agent.Closed {
		return tool.Result{}, errors.New("agent not found")
	}
	if gate == nil {
		return tool.Result{}, errors.New("tool gate unavailable")
	}
	return gate.Execute(ctx, callID, name, raw)
}

func (m *Manager) Takeover(ctx context.Context, agentID, prompt string) (string, error) {
	m.mu.Lock()
	agent, ok := m.agents[agentID]
	if !ok || agent.Closed || agent.Status == StatusShutdown {
		m.mu.Unlock()
		return "", errors.New("agent not found")
	}
	if agent.Status == StatusRunning {
		m.mu.Unlock()
		return "", errors.New("agent is busy")
	}
	runtime := m.runtime
	m.mu.Unlock()
	return m.startTurn(ctx, agentID, prompt, runtime)
}

func (m *Manager) Mailbox() *Mailbox { return m.mailbox }

// Agent returns a snapshot of an open agent, or false if missing/closed.
func (m *Manager) Agent(id string) (Agent, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	agent, ok := m.agents[id]
	if !ok || agent.Closed {
		return Agent{}, false
	}
	return cloneAgent(agent), true
}

func cloneAgent(agent *Agent) Agent {
	if agent == nil {
		return Agent{}
	}
	cloned := *agent
	cloned.OwnedPaths = append([]string(nil), agent.OwnedPaths...)
	if agent.Result != nil {
		result := *agent.Result
		cloned.Result = &result
	}
	return cloned
}

func (m *Manager) Close(agentID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	agent, ok := m.agents[agentID]
	if !ok {
		return errors.New("agent not found")
	}
	if agent.Closed {
		return nil
	}
	agent.Closed = true
	agent.Status = StatusShutdown
	m.recordStatusLocked(agentID, StatusShutdown, "")
	m.releaseClaimsLocked(agentID)
	m.active.Add(-1)
	m.wait.Broadcast()
	wt := m.worktrees[agentID]
	if wt == nil {
		return nil
	}
	if err := m.checkWorktreeOverlapLocked(wt); err != nil {
		return err
	}
	return m.trees.Discard(*wt)
}

type Message struct {
	Sequence uint64          `json:"sequence"`
	From     string          `json:"from"`
	To       string          `json:"to"`
	Body     json.RawMessage `json:"body"`
}

type Mailbox struct {
	mu        sync.Mutex
	seq       uint64
	pending   []Message
	closed    bool
	onDeliver func(Message)
}

func NewMailbox() *Mailbox { return &Mailbox{} }

func (m *Mailbox) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.closed = true
}

func (m *Mailbox) Closed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

func (m *Mailbox) Deliver(from, to string, body json.RawMessage) (Message, error) {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return Message{}, errors.New("mailbox closed")
	}
	m.seq++
	msg := Message{Sequence: m.seq, From: from, To: to, Body: append(json.RawMessage(nil), body...)}
	m.pending = append(m.pending, msg)
	hook := m.onDeliver
	m.mu.Unlock()
	if hook != nil {
		hook(msg)
	}
	return msg, nil
}

func (m *Mailbox) Drain(to string) []Message {
	m.mu.Lock()
	defer m.mu.Unlock()
	var kept, out []Message
	for _, msg := range m.pending {
		if msg.To == to {
			out = append(out, msg)
			continue
		}
		kept = append(kept, msg)
	}
	m.pending = kept
	sort.SliceStable(out, func(i, j int) bool { return out[i].Sequence < out[j].Sequence })
	return out
}
