// Package tui implements the Bubble Tea interactive host over app.Runtime.
package tui

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	skillruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/host/review"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/fleet"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/lane"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	toml "github.com/pelletier/go-toml/v2"
)

type Mode string

const (
	ModeChat       Mode = "chat"
	ModePlan       Mode = "plan"
	ModeApprove    Mode = "approve"
	ModeInput      Mode = "input"
	ModePicker     Mode = "picker"
	ModePanel      Mode = "panel"
	ModeTranscript Mode = "transcript"
)

type PanelKind string

const (
	PanelNone     PanelKind = ""
	PanelMCP      PanelKind = "mcp"
	PanelFleet    PanelKind = "fleet"
	PanelWorkflow PanelKind = "workflow"
	PanelSettings PanelKind = "settings"
	PanelHotbar   PanelKind = "hotbar"
	PanelLane     PanelKind = "lane"
	PanelPlugin   PanelKind = "plugin"
	PanelSkill    PanelKind = "skill"
	// The three panels below observe work that outlives a single turn: child
	// agents, durable tasks and background jobs. Without them that work is only
	// visible from the CLI, which is not where the person running the turn is.
	PanelAgents PanelKind = "agents"
	PanelTasks  PanelKind = "tasks"
	PanelJobs   PanelKind = "jobs"
	// PanelCost reports tokens, money and latency for the turn, the thread and the
	// session. It is a panel rather than a status line because three scopes and a
	// budget do not fit on one.
	PanelCost PanelKind = "cost"
)

type PickerKind string

const (
	PickerNone     PickerKind = ""
	PickerModel    PickerKind = "model"
	PickerProvider PickerKind = "provider"
	PickerSession  PickerKind = "session"
)

// ToolCard is a structured streaming tool widget (not plain View text).
type ToolCard struct {
	ID     string
	Name   string
	Status string
	Detail string
	// Output is the tail of what a still-running tool has printed, kept bounded
	// because a build's output is longer than any transcript wants to hold. It is
	// live commentary only; the settled receipt comes from the tool result.
	Output string
}

// maxToolCardOutput bounds the live tail a card keeps. Two kilobytes is a couple
// of screens: enough to see where a command is, small enough to throw away.
const maxToolCardOutput = 2 << 10

// appendOutput keeps the newest bytes of a running tool's output.
func (c *ToolCard) appendOutput(chunk string) {
	if chunk == "" {
		return
	}
	c.Output += chunk
	if len(c.Output) > maxToolCardOutput {
		c.Output = c.Output[len(c.Output)-maxToolCardOutput:]
	}
}

// OutputTail returns the last non-empty line of live output, which is what a
// progress line in a one-line live row can honestly show.
func (c ToolCard) OutputTail() string {
	lines := strings.Split(strings.ReplaceAll(c.Output, "\r", "\n"), "\n")
	for index := len(lines) - 1; index >= 0; index-- {
		if line := strings.TrimSpace(lines[index]); line != "" {
			return line
		}
	}
	return ""
}

// ApprovalCard is a structured approval widget driven by Runtime events.
type ApprovalCard struct {
	ID        string
	Message   string
	Status    string
	Decision  string
	Tool      string
	Arguments json.RawMessage
	Preview   string
	Kind      approvalKind
}

func (c ApprovalCard) Render() string {
	base := fmt.Sprintf("[approval:%s status=%s] %s", c.ID, c.Status, c.Message)
	if c.Decision != "" {
		base += " decision=" + c.Decision
	}
	return base
}

// enqueueApproval focuses the first pending card and FIFO-queues the rest (N6).
func (m Model) enqueueApproval(card ApprovalCard) Model {
	if card.ID == "" {
		return m
	}
	if m.approvalCard != nil && m.approvalCard.ID == card.ID {
		return m
	}
	for _, queued := range m.approvalQueue {
		if queued.ID == card.ID {
			return m
		}
	}
	if m.approvalCard == nil {
		copy := card
		m.approvalCard = &copy
	} else {
		m.approvalQueue = append(m.approvalQueue, card)
	}
	m.mode = ModeApprove
	m.phase = PhaseApproval
	return m
}

// resolveFrontApproval records the decision for the focused card and advances the queue.
func (m Model) resolveFrontApproval(decision, status string) Model {
	if m.approvalCard != nil {
		m.approvalCard.Status = status
		m.approvalCard.Decision = decision
		m = m.noteStatus(m.approvalCard.Render())
	}
	m = m.noteStatus("approval:" + decision)
	if len(m.approvalQueue) > 0 {
		next := m.approvalQueue[0]
		m.approvalQueue = m.approvalQueue[1:]
		m.approvalCard = &next
		m.mode = ModeApprove
		m.phase = PhaseApproval
		return m
	}
	m.approvalCard = nil
	m.mode = ModeChat
	return m
}

// pendingApprovalCount is focused card plus queued cards.
func (m Model) pendingApprovalCount() int {
	n := len(m.approvalQueue)
	if m.approvalCard != nil && m.approvalCard.Status == "pending" {
		n++
	}
	return n
}

// PlanCard is a structured streaming proposed_plan widget (W5.1).
type PlanCard struct {
	Body   string
	Status string // streaming | ready
}

func (c PlanCard) Render() string {
	status := c.Status
	if status == "" {
		status = "streaming"
	}
	return fmt.Sprintf("[plan status=%s]\n%s", status, strings.TrimSpace(c.Body))
}

// InputCard is a structured user-input widget driven by Runtime events.
type InputCard struct {
	ID       string
	Prompt   string
	Options  []string
	Selected int // index into Options when choosing by arrow keys
	Status   string
	Answer   string
}

func (c InputCard) Render() string {
	var b strings.Builder
	fmt.Fprintf(&b, "[input:%s status=%s] %s", c.ID, c.Status, c.Prompt)
	if len(c.Options) > 0 {
		b.WriteByte('\n')
		for i, opt := range c.Options {
			marker := "  "
			if i == c.Selected {
				marker = "> "
			}
			fmt.Fprintf(&b, "%s%d. %s\n", marker, i+1, opt)
		}
	}
	if c.Answer != "" {
		fmt.Fprintf(&b, " answer=%s", c.Answer)
	}
	return strings.TrimRight(b.String(), "\n")
}

type Options struct {
	ConfigPath  string
	DataDir     string
	FleetRoot   string
	MCPConfig   string
	FixturePath string
	Workspace   string
	Jobs        process.JobCenter
	Host        RuntimeHost // tests inject; when nil Run opens SessionHost or fake
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	// DisableAltScreen skips the alternate screen buffer (hermetic / pipe tests).
	// Interactive CLI must leave this false — otherwise the launch command and
	// prior shell output remain visible and transcript scrolling looks broken.
	DisableAltScreen bool
	// Program is optional; tests inject a custom tea.Program via NewModel.
	WithoutProgram bool

	// Live provider wiring (same contract as `codehelper exec` custom/catalog routes).
	// When FixturePath is empty and Provider+Model are set (or BaseURL is set),
	// Run binds a real wire.Session instead of the offline fakeRuntime.
	Provider              string
	Model                 string
	BaseURL               string
	Protocol              string
	APIKeyEnv             string
	EnableTools           bool
	Mode                  string
	Permission            string
	MaxSteps              int // 0 = config/default; interactive coding usually needs >16
	ContextTokens         uint64
	ModelMaxOutputTokens  uint64
	ModelCapabilities     string
	InputPricePerMillion  float64
	OutputPricePerMillion float64
	PricingCurrency       string
}

type RuntimeHost interface {
	StartTurn(ctx context.Context, prompt string) error
	DecideApproval(ctx context.Context, requestID, decision string) error
	ReplyInput(ctx context.Context, requestID, answer string) error
	Cancel(ctx context.Context) error
	Close(ctx context.Context) error
	WaitMsg() tea.Cmd
}

type fakeRuntime struct {
	mu        sync.Mutex
	Prompts   []string
	Approvals []string
	Inputs    []string
	Canceled  int
}

func (f *fakeRuntime) StartTurn(_ context.Context, prompt string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Prompts = append(f.Prompts, prompt)
	return nil
}

func (f *fakeRuntime) DecideApproval(_ context.Context, requestID, decision string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Approvals = append(f.Approvals, requestID+":"+decision)
	return nil
}

func (f *fakeRuntime) ReplyInput(_ context.Context, requestID, answer string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Inputs = append(f.Inputs, requestID+":"+answer)
	return nil
}

func (f *fakeRuntime) Cancel(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Canceled++
	return nil
}

func (f *fakeRuntime) Close(context.Context) error { return nil }

func (f *fakeRuntime) WaitMsg() tea.Cmd {
	return func() tea.Msg { return streamDoneMsg{} }
}

type Model struct {
	width, height int
	mode          Mode
	input         string // mirrored composer text for tests / slash paths
	cells         []transcriptCell
	settledTools  []ToolCard
	activeTool    *ToolCard
	approvalCard  *ApprovalCard
	approvalQueue []ApprovalCard // FIFO behind the focused card (N6)
	planCard      *PlanCard
	inputCard     *InputCard
	picker        PickerKind
	panel         PanelKind
	provider      string
	modelID       string
	session       string
	pickerItems   []string
	pickerIndex   int
	panelBody     string
	restored      bool
	quitting      bool
	busy          bool
	// selectMode releases terminal mouse capture (Ctrl+S) so text can be
	// selected/copied with the terminal's native mechanism.
	selectMode bool
	// mouseCapture records whether the program started with alt-screen mouse
	// capture; the select-mode toggle is a no-op without it.
	mouseCapture bool
	runtime      RuntimeHost
	configPath   string
	dataDir      string
	fleetRoot    string
	mcpConfig    string
	mcpHealth    map[string]protocol.MCPHealthChangedData
	lastSlash    string
	posture      string // suggest | auto | bypass (Ask→Auto→Full)
	toolMode     policy.Mode
	granular     policy.Granular
	parentFork   string
	lastPlanText string
	// lastContextLine is the prompt-context summary of the most recent turn.
	lastContextLine string
	jobs            process.JobCenter
	pendingImages   []string
	workspaceRoot   string
	// streamOutIdx / streamReasonIdx index into cells for in-place streaming.
	streamOutIdx    int
	streamReasonIdx int
	mdStream        *streamMD // assistant stable/tail markdown while streaming

	viewport       viewport.Model
	transcriptVP   viewport.Model // Ctrl+T full transcript pager
	composer       textarea.Model
	followTail     bool
	showWelcome    bool
	viewportDirty  bool
	pendingFollow  bool // force-follow when coalesced paint flushes
	lastPaint      time.Time
	transcriptBase string // cached transcript without live spinner suffix
	ready          bool
	exploring      *exploringGroup // live Exploring merge for read/find tools
	overlayYOffset int
	pendingResizeW int
	pendingResizeH int
	resizeSeq      uint64

	phase             ShellPhase
	motion            MotionMode
	spinnerTick       int
	doneBreathUntil   time.Time
	tokenPrompt       uint64
	tokenCompletion   uint64
	contextWindow     uint64
	contextTokensUsed uint64
	costGlance        string
	// turn is the current turn's money, latency and budget as the runtime reported
	// them. The token counters above are the header's, kept separate because they
	// are also fed by paths that carry no accounting.
	turn             turnAccounting
	thinkingExpanded bool
	expandedToolID   string
	sidebarTasks     []string // optional classic activity echo
}

const maxPendingImages = 3

func NewModel(options Options, host RuntimeHost) Model {
	if host == nil {
		host = &fakeRuntime{}
	}
	provider, modelID := "openai", "gpt-4.1"
	if options.Provider != "" {
		provider = options.Provider
	}
	if options.Model != "" {
		modelID = options.Model
	}
	if sessionHost, ok := host.(*SessionHost); ok {
		if id := sessionHost.ProviderID(); id != "" {
			provider = id
		}
		if id := sessionHost.ModelID(); id != "" {
			modelID = id
		}
	}
	if loaded, err := config.Load(config.LoadOptions{Path: options.ConfigPath}); err == nil {
		if options.Provider == "" && loaded.Config.Execution.Provider != "" {
			provider = loaded.Config.Execution.Provider
		}
		if options.Model == "" && loaded.Config.Execution.Model != "" {
			modelID = loaded.Config.Execution.Model
		}
		if options.DataDir == "" && loaded.Config.State.DataDir != "" {
			options.DataDir = loaded.Config.State.DataDir
		}
	}
	session := "thread-local"
	if options.DataDir != "" {
		if active, err := os.ReadFile(filepath.Join(options.DataDir, "active-thread")); err == nil {
			if value := strings.TrimSpace(string(active)); value != "" {
				session = value
			}
		}
	}
	fleetRoot := options.FleetRoot
	if fleetRoot == "" && options.DataDir != "" {
		fleetRoot = filepath.Join(options.DataDir, "fleet")
	}
	posture := "auto"
	posturePinned := strings.TrimSpace(options.Permission) != ""
	switch strings.ToLower(strings.TrimSpace(options.Permission)) {
	case "suggest", "ask":
		posture = "suggest"
	case "bypass", "full":
		posture = "bypass"
	case "never":
		posture = "suggest" // UI cycles Ask/Auto/Full; never maps to strictest ask
	case "auto":
		posture = "auto"
	case "":
		posture = "auto"
	default:
		posture = strings.ToLower(strings.TrimSpace(options.Permission))
	}
	toolMode := policy.ModeAct
	modePinned := strings.TrimSpace(options.Mode) != ""
	switch strings.ToLower(strings.TrimSpace(options.Mode)) {
	case "plan":
		toolMode = policy.ModePlan
	case "operate":
		toolMode = policy.ModeOperate
	case "act":
		toolMode = policy.ModeAct
	case "":
		toolMode = policy.ModeAct
	default:
		toolMode = policy.Mode(strings.ToLower(strings.TrimSpace(options.Mode)))
	}
	uiMode := ModeChat
	if toolMode == policy.ModePlan {
		uiMode = ModePlan
	}
	model := Model{
		width: 80, height: 24, mode: uiMode, toolMode: toolMode, posture: posture,
		runtime:    host,
		configPath: options.ConfigPath, provider: provider, modelID: modelID,
		session: session, dataDir: options.DataDir, fleetRoot: fleetRoot,
		mcpConfig: options.MCPConfig, jobs: options.Jobs, workspaceRoot: options.Workspace,
		streamOutIdx: -1, streamReasonIdx: -1,
		showWelcome: true, followTail: true,
		composer: newComposer(), viewport: newTranscriptViewport(80, 20),
		phase: PhaseIdle, motion: detectMotionMode(),
		contextWindow: options.ContextTokens,
		mouseCapture:  !options.DisableAltScreen,
	}
	if model.workspaceRoot == "" {
		model.workspaceRoot = "."
	}
	model = model.refreshViewport(true)
	if model.jobs == nil {
		if sessionHost, ok := host.(*SessionHost); ok {
			model.jobs = sessionHost.Jobs()
		}
	}
	if options.DataDir != "" {
		if snap, err := ux.LoadSnapshot(options.DataDir, "session-local"); err == nil {
			if snap.Provider != "" {
				model.provider = snap.Provider
			}
			if snap.Model != "" {
				model.modelID = snap.Model
			}
			if snap.ThreadID != "" {
				model.session = snap.ThreadID
			}
			if snap.ParentFork != "" {
				model.parentFork = snap.ParentFork
			}
			if snap.Mode == "plan" {
				model.mode = ModePlan
				model.toolMode = policy.ModePlan
			} else if snap.Mode == "operate" {
				model.toolMode = policy.ModeOperate
			} else if snap.Mode == "act" {
				model.toolMode = policy.ModeAct
			}
			model = model.applySnapshotSecurity(snap)
			// Explicit CLI --posture / --mode win over restored snapshot (common footgun:
			// ~/.codehelper snapshot has auto and silently undoes --posture bypass).
			if posturePinned {
				model.posture = posture
			}
			if modePinned {
				model.toolMode = toolMode
				if toolMode == policy.ModePlan {
					model.mode = ModePlan
				} else if model.mode == ModePlan {
					model.mode = ModeChat
				}
			}
			if len(snap.Messages) > 0 {
				model = model.noteStatus("snapshot:restored messages=" + fmt.Sprint(len(snap.Messages)))
			}
		}
	}
	// Live route / CLI provider always win over stale snapshot labels in the status strip.
	if sessionHost, ok := host.(*SessionHost); ok {
		if id := sessionHost.ProviderID(); id != "" {
			model.provider = id
		}
		if id := sessionHost.ModelID(); id != "" {
			model.modelID = id
		}
	} else {
		if options.Provider != "" {
			model.provider = options.Provider
		}
		if options.Model != "" {
			model.modelID = options.Model
		}
	}
	return model.syncSecurity()
}

type tickMsg time.Time

type clearOnceMsg struct{}

type resizeSettleMsg struct {
	seq    uint64
	width  int
	height int
}

const resizeDebounce = 50 * time.Millisecond

func (m Model) Init() tea.Cmd {
	// Alt-screen is entered via tea.WithAltScreen in Run; only clear here.
	cmds := []tea.Cmd{tea.ClearScreen}
	if m.motion != MotionStill {
		cmds = append(cmds, tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) }))
	}
	return tea.Batch(cmds...)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		first := !m.ready
		m.pendingResizeW, m.pendingResizeH = msg.Width, msg.Height
		m.width, m.height = msg.Width, msg.Height
		m.ready = true
		if m.width < 40 || m.height < 10 {
			m = m.noteStatus("small-screen layout")
		}
		if first {
			m = m.invalidateAssistantMarkdown()
			m = m.refreshViewport(m.followTail)
			if m.mode == ModeTranscript {
				m = m.syncOverlayContent()
			}
			return m, tea.ClearScreen
		}
		m.resizeSeq++
		seq := m.resizeSeq
		w, h := msg.Width, msg.Height
		return m, tea.Tick(resizeDebounce, func(t time.Time) tea.Msg {
			return resizeSettleMsg{seq: seq, width: w, height: h}
		})
	case resizeSettleMsg:
		if msg.seq != m.resizeSeq {
			return m, nil
		}
		m.width, m.height = msg.width, msg.height
		m.pendingResizeW, m.pendingResizeH = msg.width, msg.height
		m = m.invalidateAssistantMarkdown()
		m = m.refreshViewport(m.followTail)
		if m.mode == ModeTranscript {
			m = m.syncOverlayContent()
		}
		return m, nil
	case clearOnceMsg:
		return m, tea.ClearScreen
	case tickMsg:
		m = m.flushViewportIfDirty()
		dripped := false
		if m.mdStream != nil && m.motion == MotionFull && m.mdStream.hasPendingCommit() {
			m = m.dripMDStream()
			dripped = true
		}
		animating := m.motion != MotionStill && (m.busy || m.activeTool != nil || m.exploring != nil || m.inDoneBreath() ||
			(m.mdStream != nil && m.mdStream.hasPendingCommit()))
		if dripped {
			m.spinnerTick++
			m = m.recomputePhase()
			m = m.refreshViewport(false)
		} else if animating {
			m.spinnerTick++
			m = m.recomputePhase()
			m = m.patchLiveViewport()
		}
		if m.motion == MotionStill && !(m.mdStream != nil && m.mdStream.hasPendingCommit()) {
			return m, nil
		}
		if m.motion == MotionStill {
			return m, nil
		}
		return m, tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
	case tea.KeyMsg:
		m = m.flushViewportIfDirty()
		return m.handleKey(msg)
	case tea.MouseMsg:
		m.ensureChrome()
		m = m.flushViewportIfDirty()
		if m.mode == ModeTranscript {
			m = m.syncOverlayContent()
			var cmd tea.Cmd
			m.transcriptVP, cmd = m.transcriptVP.Update(msg)
			m.overlayYOffset = m.transcriptVP.YOffset
			return m, cmd
		}
		m = m.syncViewportContent()
		var cmd tea.Cmd
		m.viewport, cmd = m.viewport.Update(msg)
		if !m.viewport.AtBottom() {
			m.followTail = false
		} else {
			m.followTail = true
		}
		return m, cmd
	case streamMsg:
		if msg.mcpHealth != nil {
			if m.mcpHealth == nil {
				m.mcpHealth = make(map[string]protocol.MCPHealthChangedData)
			}
			if msg.mcpHealth.State == "removed" {
				delete(m.mcpHealth, msg.mcpHealth.Server)
			} else {
				m.mcpHealth[msg.mcpHealth.Server] = *msg.mcpHealth
			}
			if m.mode == ModePanel && m.panel == PanelMCP {
				m.panelBody = m.renderPanel(PanelMCP)
			}
		}
		if msg.phaseHint != "" {
			switch msg.phaseHint {
			case "running_tools", "feeding_results", "calling_model", "streaming":
				m.phase = PhaseWorking
			case "awaiting_approval":
				m.phase = PhaseApproval
			case "awaiting_input":
				m.phase = PhaseWaiting
			case "failed":
				m.phase = PhaseFailed
			default:
				if m.busy {
					m.phase = PhaseWorking
				}
			}
		}
		if msg.toolOutput != "" {
			m = m.appendToolOutput(msg.toolID, msg.toolOutput)
			m.phase = PhaseWorking
		} else if msg.tool != "" {
			status := "running"
			if msg.toolStatus != "" {
				status = msg.toolStatus
			} else if msg.toolDone {
				status = "done"
				if strings.HasPrefix(msg.text, "error:") {
					status = "failed"
				}
			}
			card := ToolCard{ID: msg.toolID, Name: msg.tool, Status: status, Detail: msg.text}
			m = m.upsertActiveTool(card)
			m.phase = PhaseWorking
		} else if msg.kind == streamKindOutput {
			m = m.appendStreamCell(cellAssistant, msg.text)
			m.phase = PhaseWorking
		} else if msg.kind == streamKindReasoning {
			m = m.appendStreamCell(cellThinking, msg.text)
			m.phase = PhaseWorking
		} else if msg.kind == streamKindPlan {
			body := msg.planBody
			if body == "" {
				body = msg.text
			}
			status := "streaming"
			if msg.planDone {
				status = "ready"
			}
			m.planCard = &PlanCard{Body: body, Status: status}
			if msg.planDone && body != "" {
				m.lastPlanText = body
			}
			m.phase = PhaseWorking
		} else if msg.text != "" && msg.phaseHint == "" {
			if strings.HasPrefix(msg.text, "turn.failed") || strings.HasPrefix(msg.text, "rejected:") {
				if m.activeTool != nil {
					m.activeTool.Status = "failed"
					m = m.flushActiveTool()
				}
				m = m.appendCell(cellError, msg.text)
				m.phase = PhaseFailed
			} else if strings.HasPrefix(msg.text, "— turn.completed") {
				m = m.appendSystem(msg.text)
			} else {
				m = m.appendSystem(msg.text)
			}
		}
		if msg.promptTokens > 0 || msg.completionTokens > 0 || msg.contextWindow > 0 {
			m = m.noteUsage(msg.promptTokens, msg.completionTokens, msg.contextWindow)
		}
		if msg.usage != nil {
			m = m.noteUsageCost(msg.usage.costMicrounits, msg.usage.costKnown, msg.usage.cachedTokens)
		}
		if msg.receipt != nil {
			m = m.noteReceiptAccounting(*msg.receipt)
		}
		if msg.contextSummary != "" {
			m.lastContextLine = msg.contextSummary
		}
		if msg.approvalID != "" {
			kind, preview := buildApprovalPreview(msg.approvalTool, msg.approvalArgs)
			summary := msg.text
			if msg.approvalTool != "" {
				compact := compactToolArgs(msg.approvalArgs)
				if compact != "" {
					summary = msg.approvalTool + " · " + compact
				} else {
					summary = msg.approvalTool
				}
			}
			card := ApprovalCard{
				ID: msg.approvalID, Message: summary, Status: "pending",
				Tool: msg.approvalTool, Arguments: msg.approvalArgs,
				Preview: preview, Kind: kind,
			}
			m = m.enqueueApproval(card)
		}
		if msg.inputID != "" {
			m.inputCard = &InputCard{
				ID: msg.inputID, Prompt: msg.text, Options: append([]string(nil), msg.inputOptions...),
				Status: "pending",
			}
			m.mode = ModeInput
			m.phase = PhaseWaiting
		}
		m = m.requestPaint(false)
		if m.mode == ModeTranscript {
			m = m.syncOverlayContent()
		}
		return m, tea.Batch(m.runtime.WaitMsg())
	case streamDoneMsg:
		m.busy = false
		m.streamOutIdx = -1
		m.streamReasonIdx = -1
		failed := m.phase == PhaseFailed
		m = m.finishPendingTurnCard()
		if m.exploring != nil {
			m = m.flushExploring()
		}
		if m.activeTool != nil {
			if failed {
				m.activeTool.Status = "failed"
			}
			m = m.flushActiveTool()
		}
		m = m.renderAssistantMarkdown()
		if failed {
			m.phase = PhaseFailed
			m.doneBreathUntil = time.Time{}
		} else {
			m = m.beginDoneBreath()
		}
		m = m.refreshViewport(true)
		if m.mode == ModeTranscript {
			m = m.syncOverlayContent()
		}
		if host, ok := m.runtime.(*SessionHost); ok && m.dataDir != "" {
			_ = host.WriteCheckpoint(m.dataDir, "session-local")
		}
		if m.dataDir != "" {
			items, err := ux.DrainQueue(m.dataDir)
			if err == nil && len(items) > 0 {
				prompt := items[0].Prompt
				for _, item := range items[1:] {
					_ = ux.Enqueue(m.dataDir, item)
				}
				m = m.noteStatus("offline-queue:drain " + prompt)
				m = m.refreshViewport(true)
				if err := m.runtime.StartTurn(context.Background(), prompt); err != nil {
					m = m.noteStatus("offline-queue:error:" + err.Error())
					m = m.refreshViewport(true)
					return m, nil
				}
				m.busy = true
				return m, m.runtime.WaitMsg()
			}
		}
		return m, nil
	default:
		m.ensureChrome()
		var cmd tea.Cmd
		m.composer, cmd = m.composer.Update(msg)
		m.input = m.composer.Value()
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return m, tea.Batch(cmds...)
}

type streamKind string

const (
	streamKindPlain     streamKind = ""
	streamKindOutput    streamKind = "output"
	streamKindReasoning streamKind = "reasoning"
	streamKindPlan      streamKind = "plan"
)

type streamMsg struct {
	text, tool, toolID, approvalID, inputID string
	inputOptions                            []string
	kind                                    streamKind
	toolDone                                bool
	toolStatus                              string // optional override (from tool.result)
	// toolOutput is a chunk a still-running tool printed. It updates the live row
	// rather than minting a receipt: the call has not finished.
	toolOutput                     string
	phaseHint                      string // engine lifecycle (running_tools, …)
	promptTokens, completionTokens uint64
	contextWindow                  uint64
	planBody                       string
	planDone                       bool
	approvalTool                   string
	approvalArgs                   json.RawMessage
	// contextSummary is the prompt-context line from a turn receipt, which is what
	// /context reports.
	contextSummary string
	mcpHealth      *protocol.MCPHealthChangedData
	// usage is what a usage event said about the call it covers, and receipt is
	// what the turn receipt settled on. Both carry money and the receipt also
	// carries latency and the thread's budget, none of which the token counters
	// above can express.
	usage   *turnAccounting
	receipt *turnAccounting
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.ensureChrome()
	if updated, handled, cmd := m.handleTranscriptOverlayKey(msg); handled {
		return updated, cmd
	}
	if updated, handled, cmd := m.handleOverlayHotkeys(msg); handled {
		return updated, cmd
	}
	switch msg.Type {
	case tea.KeyCtrlS:
		// Toggle select mode: release the mouse back to the terminal so the
		// user can drag-select and copy text natively, then re-acquire it.
		if !m.mouseCapture {
			m = m.noteStatus("select: mouse not captured — native selection already available")
			return m, nil
		}
		m.selectMode = !m.selectMode
		if m.selectMode {
			m = m.noteStatus("select-mode: mouse released — drag to select, copy with Cmd/Ctrl+C, Ctrl+S to resume")
			return m, tea.DisableMouse
		}
		m = m.noteStatus("select-mode: mouse re-enabled")
		return m, tea.EnableMouseCellMotion
	case tea.KeyCtrlT:
		if m.mode == ModeChat || m.mode == ModePlan {
			return m.openTranscriptOverlay(), nil
		}
		return m, nil
	case tea.KeyCtrlC, tea.KeyEsc:
		if m.mode == ModeApprove {
			id := ""
			if m.approvalCard != nil {
				id = m.approvalCard.ID
			}
			_ = m.runtime.DecideApproval(context.Background(), id, "cancel")
			m = m.resolveFrontApproval("cancel", "canceled")
			m = m.clearComposer()
			m.picker = PickerNone
			m.panel = PanelNone
			m = m.recomputePhase()
			m = m.refreshViewport(true)
			if m.mode == ModeApprove {
				return m, nil
			}
			return m, m.continueTurnStream()
		}
		if m.mode == ModeInput || m.mode == ModePicker || m.mode == ModePanel {
			m.mode = ModeChat
			m.picker = PickerNone
			m.panel = PanelNone
			m.approvalCard = nil
			m.inputCard = nil
			m = m.recomputePhase()
			m = m.refreshViewport(false)
			return m, nil
		}
		m.quitting = true
		m.restored = true
		_ = m.runtime.Cancel(context.Background())
		return m, tea.Quit
	case tea.KeyPgUp:
		return m.scrollTranscript(func(model *Model) { model.viewport.PageUp() }), nil
	case tea.KeyPgDown:
		return m.scrollTranscript(func(model *Model) { model.viewport.PageDown() }), nil
	case tea.KeyCtrlU:
		return m.scrollTranscript(func(model *Model) { model.viewport.HalfViewUp() }), nil
	case tea.KeyCtrlD:
		return m.scrollTranscript(func(model *Model) { model.viewport.HalfViewDown() }), nil
	case tea.KeyEnd:
		return m.scrollTranscript(func(model *Model) { model.viewport.GotoBottom() }), nil
	case tea.KeyHome:
		return m.scrollTranscript(func(model *Model) { model.viewport.GotoTop() }), nil
	case tea.KeyUp, tea.KeyDown:
		if m.mode == ModeInput && m.inputCard != nil && len(m.inputCard.Options) > 0 {
			if msg.Type == tea.KeyUp {
				if m.inputCard.Selected > 0 {
					m.inputCard.Selected--
				}
			} else if m.inputCard.Selected+1 < len(m.inputCard.Options) {
				m.inputCard.Selected++
			}
			return m, nil
		}
		if m.mode == ModePicker && len(m.pickerItems) > 0 {
			if msg.Type == tea.KeyUp {
				m.pickerIndex = (m.pickerIndex - 1 + len(m.pickerItems)) % len(m.pickerItems)
			} else {
				m.pickerIndex = (m.pickerIndex + 1) % len(m.pickerItems)
			}
			return m, nil
		}
		if m.composerText() == "" && (m.mode == ModeChat || m.mode == ModePanel) {
			if msg.Type == tea.KeyUp {
				return m.scrollTranscript(func(model *Model) { model.viewport.ScrollUp(3) }), nil
			}
			return m.scrollTranscript(func(model *Model) { model.viewport.ScrollDown(3) }), nil
		}
	case tea.KeyEnter:
		if msg.Alt {
			m.composer.InsertString("\n")
			m.input = m.composer.Value()
			return m, nil
		}
		return m.handleEnter()
	case tea.KeyRunes:
		if m.mode == ModePicker {
			return m, nil
		}
		text := m.composerText()
		if text == "" && len(msg.Runes) == 1 {
			key := strings.ToLower(string(msg.Runes))
			if m.mode == ModeChat || m.mode == ModePanel {
				switch key {
				case "m":
					return m.openPicker(PickerModel), nil
				case "p":
					return m.openPicker(PickerProvider), nil
				case "s":
					return m.openPicker(PickerSession), nil
				case "1", "2", "3", "4", "5", "6", "7":
					// Digits must type normally (e.g. "2. fix the bug"). Panels
					// stay on Alt+digit or slash commands like /fleet.
					if !msg.Alt {
						break
					}
					switch key {
					case "1":
						return m.openPanel(PanelMCP), nil
					case "2":
						return m.openPanel(PanelFleet), nil
					case "3":
						return m.openPanel(PanelWorkflow), nil
					case "4":
						return m.openPanel(PanelSettings), nil
					case "5":
						return m.openPanel(PanelAgents), nil
					case "6":
						return m.openPanel(PanelTasks), nil
					case "7":
						return m.openPanel(PanelJobs), nil
					}
				case "a":
					if m.mode == ModeChat {
						m = m.cyclePosture()
						m = m.refreshViewport(false)
						return m, nil
					}
				case "v":
					if m.mode == ModeChat {
						m = m.toggleToolExpand()
						m = m.refreshViewport(false)
						return m, nil
					}
				case "t":
					if m.mode == ModeChat {
						m = m.toggleThinkingExpand()
						m = m.refreshViewport(false)
						return m, nil
					}
				}
			}
		}
		incoming := string(msg.Runes)
		m.composer.InsertString(incoming)
		if cleaned, dropped := stripMouseReportArtifacts(m.composer.Value(), incoming); dropped {
			m.composer.SetValue(cleaned)
		}
		m.input = m.composer.Value()
		m = m.recomputePhase()
		return m, nil
	}
	var cmd tea.Cmd
	m.composer, cmd = m.composer.Update(msg)
	m.input = m.composer.Value()
	m = m.recomputePhase()
	return m, cmd
}

// StreamOutputMessage injects an assistant output delta (appended in-place).
func StreamOutputMessage(text string) tea.Msg {
	return streamMsg{kind: streamKindOutput, text: text}
}

// StreamReasoningMessage injects a reasoning delta (appended in-place).
func StreamReasoningMessage(text string) tea.Msg {
	return streamMsg{kind: streamKindReasoning, text: text}
}

// StreamDoneMessage marks the end of a turn stream (triggers markdown render).
func StreamDoneMessage() tea.Msg {
	return streamDoneMsg{}
}

// StreamApprovalMessage injects an approval prompt into the TUI update loop.
func StreamApprovalMessage(id, text string) tea.Msg {
	return streamMsg{text: text, approvalID: id}
}

// StreamInputMessage injects a request_user_input card into the TUI update loop.
func StreamInputMessage(id, text string, options ...string) tea.Msg {
	return streamMsg{text: text, inputID: id, inputOptions: append([]string(nil), options...)}
}

// StreamToolMessage injects a structured tool card.
func StreamToolMessage(id, name, detail string) tea.Msg {
	return streamMsg{toolID: id, tool: name, text: detail}
}

// continueTurnStream resumes Bubble Tea subscription to the active turn pump.
// Approval / input replies must call this; dropping WaitMsg leaves busy stuck
// and subsequent prompts land in offline-queue.
func (m Model) continueTurnStream() tea.Cmd {
	if !m.busy || m.runtime == nil {
		return nil
	}
	return m.runtime.WaitMsg()
}

func (m Model) handleEnter() (tea.Model, tea.Cmd) {
	m.ensureChrome()
	m.input = m.composerText()
	switch m.mode {
	case ModeApprove:
		decision := "allow"
		typed := strings.ToLower(strings.TrimSpace(m.input))
		switch typed {
		case "deny", "n":
			decision = "deny"
		case "always", "a":
			decision = "always"
		case "session":
			decision = "session"
		case "cancel":
			decision = "cancel"
		}
		id := ""
		if m.approvalCard != nil {
			id = m.approvalCard.ID
		}
		_ = m.runtime.DecideApproval(context.Background(), id, decision)
		status := "decided"
		if decision == "cancel" {
			status = "canceled"
		}
		m = m.resolveFrontApproval(decision, status)
		m = m.clearComposer()
		m = m.refreshViewport(true)
		// Keep listening for turn stream events after approval; otherwise busy
		// stays true forever and later prompts fall into offline-queue.
		if m.mode == ModeApprove {
			return m, nil
		}
		return m, m.continueTurnStream()
	case ModeInput:
		answer := strings.TrimSpace(m.input)
		if answer == "" && m.inputCard != nil && len(m.inputCard.Options) > 0 {
			idx := m.inputCard.Selected
			if idx >= 0 && idx < len(m.inputCard.Options) {
				answer = m.inputCard.Options[idx]
			}
		}
		if answer == "" {
			answer = "ok"
		}
		// Allow "1"/"2"… to pick numbered options when the composer has a digit.
		if m.inputCard != nil && len(m.inputCard.Options) > 0 {
			if n, err := strconv.Atoi(answer); err == nil && n >= 1 && n <= len(m.inputCard.Options) {
				answer = m.inputCard.Options[n-1]
			}
		}
		id := ""
		if m.inputCard != nil {
			id = m.inputCard.ID
			m.inputCard.Status = "answered"
			m.inputCard.Answer = answer
			m = m.noteStatus(m.inputCard.Render())
		}
		_ = m.runtime.ReplyInput(context.Background(), id, answer)
		m = m.noteStatus("input:" + answer)
		m.inputCard = nil
		m = m.clearComposer()
		m.mode = ModeChat
		m = m.refreshViewport(true)
		return m, m.continueTurnStream()
	case ModePicker:
		if len(m.pickerItems) == 0 {
			m.mode = ModeChat
			return m, nil
		}
		selected := m.pickerItems[m.pickerIndex]
		switch m.picker {
		case PickerModel:
			m.modelID = selected
			_ = m.writeSettings()
		case PickerProvider:
			m.provider = selected
			_ = m.writeSettings()
		case PickerSession:
			m.session = selected
			_ = m.activateSession(selected)
		}
		m.picker = PickerNone
		m.mode = ModeChat
		m = m.noteStatus("selected:" + selected)
		m = m.refreshViewport(true)
		return m, nil
	case ModePanel:
		prompt := strings.TrimSpace(m.input)
		if action, ok := commands.Parse(prompt); ok {
			m.lastSlash = action.Name
			m = m.clearComposer()
			m.mode = ModeChat
			m.panel = PanelNone
			m = m.dispatchSlash(action)
			m = m.refreshViewport(true)
			if m.quitting {
				return m, tea.Quit
			}
			if m.busy {
				return m, m.runtime.WaitMsg()
			}
			return m, nil
		}
		// A typed question must start a turn, not silently refresh and discard
		// the composer. Empty Enter still refreshes the observation panel.
		if prompt != "" {
			m.mode = ModeChat
			m.panel = PanelNone
			return m.submitChatPrompt(prompt)
		}
		m = m.panelAction()
		m = m.clearComposer()
		m = m.refreshViewport(true)
		return m, nil
	default:
		prompt := strings.TrimSpace(m.input)
		if prompt == "" {
			return m, nil
		}
		if action, ok := commands.Parse(prompt); ok {
			m.lastSlash = action.Name
			m = m.clearComposer()
			m = m.dispatchSlash(action)
			m = m.refreshViewport(true)
			if m.quitting {
				return m, tea.Quit
			}
			if m.busy {
				return m, m.runtime.WaitMsg()
			}
			return m, nil
		}
		return m.submitChatPrompt(prompt)
	}
}

// submitChatPrompt starts a turn (or queues it) for a non-slash composer line.
func (m Model) submitChatPrompt(prompt string) (tea.Model, tea.Cmd) {
	m.showWelcome = false
	m = m.appendCell(cellYou, prompt)
	if len(m.pendingImages) > 0 {
		paths := strings.Join(m.pendingImages, ", ")
		prompt = fmt.Sprintf(
			"[attached image paths=%s — use image_analyze once per workspace-relative path when vision is configured]\n\n%s",
			paths, prompt,
		)
		m = m.noteStatus("attach:queued " + paths)
		m.pendingImages = nil
	}
	if m.busy {
		if m.dataDir != "" {
			_ = ux.Enqueue(m.dataDir, ux.QueueItem{ThreadID: m.session, Prompt: prompt})
			m = m.noteStatus("offline-queue:enqueued")
		} else {
			m = m.noteStatus("error: runtime busy")
		}
		m = m.clearComposer()
		m = m.refreshViewport(true)
		return m, nil
	}
	if err := m.runtime.StartTurn(context.Background(), prompt); err != nil {
		if m.dataDir != "" && (errors.Is(err, app.ErrQueueFull) || strings.Contains(err.Error(), "active turn")) {
			_ = ux.Enqueue(m.dataDir, ux.QueueItem{ThreadID: m.session, Prompt: prompt})
			m = m.noteStatus("offline-queue:enqueued")
		} else {
			m = m.noteStatus("error: " + err.Error())
		}
		m = m.clearComposer()
		m = m.refreshViewport(true)
		return m, nil
	}
	m.busy = true
	m.streamOutIdx = -1
	m.streamReasonIdx = -1
	m.followTail = true
	m.phase = PhaseWorking
	pending := ToolCard{
		ID: "pending", Name: "turn", Status: "pending", Detail: "awaiting runtime",
	}
	m = m.upsertActiveTool(pending)
	m = m.clearComposer()
	m = m.refreshViewport(true)
	return m, m.runtime.WaitMsg()
}

func (m Model) dispatchSlash(action commands.Action) Model {
	switch action.Kind {
	case commands.KindHelp:
		m = m.noteStatus("slash: " + commands.HelpText())
	case commands.KindHotbar:
		return m.openPanel(PanelHotbar)
	case commands.KindModel:
		return m.openPicker(PickerModel)
	case commands.KindProvider:
		return m.openPicker(PickerProvider)
	case commands.KindSession, commands.KindThread:
		return m.openPicker(PickerSession)
	case commands.KindMCP:
		return m.openPanel(PanelMCP)
	case commands.KindFleet:
		return m.openPanel(PanelFleet)
	case commands.KindWorkflow:
		return m.openPanel(PanelWorkflow)
	case commands.KindSettings:
		return m.openPanel(PanelSettings)
	case commands.KindLane:
		return m.openPanel(PanelLane)
	case commands.KindPlugin:
		return m.openPanel(PanelPlugin)
	case commands.KindSkill:
		return m.openPanel(PanelSkill)
	case commands.KindNew:
		m = m.sessionNew()
	case commands.KindLoad, commands.KindResume, commands.KindContinue:
		id := m.session
		if len(action.Args) > 0 {
			id = action.Args[0]
		}
		m = m.sessionLoad(id)
	case commands.KindSave:
		m = m.sessionSave()
	case commands.KindFork:
		to := "thread-fork"
		if len(action.Args) > 0 {
			to = action.Args[0]
		}
		m = m.sessionFork(to)
	case commands.KindExport:
		m = m.sessionExport()
	case commands.KindClear:
		m.cells = nil
		m.settledTools = nil
		m.activeTool = nil
		m.exploring = nil
		m.approvalCard = nil
		m.approvalQueue = nil
		m.showWelcome = true
		m.streamOutIdx = -1
		m.streamReasonIdx = -1
		m.followTail = true
		m.phase = PhaseIdle
		m.expandedToolID = ""
		m = m.noteStatus("cleared")
		m = m.refreshViewport(true)
	case commands.KindCompact:
		if host, ok := m.runtime.(*SessionHost); ok {
			summary, err := host.CompactThread(context.Background())
			if err != nil {
				m = m.noteStatus("compact:error:" + err.Error())
			} else {
				m = m.noteStatus("compact:ok")
				if summary != "" {
					display := summary
					if len(display) > 160 {
						display = display[:160] + "…"
					}
					m = m.noteStatus("compact:" + display)
				}
				if m.dataDir != "" {
					_ = ux.SaveSnapshot(m.dataDir, m.sessionSnapshot([]string{"compacted"}))
				}
			}
		} else {
			// Offline / fake host: keep a light UI trim for local transcript only.
			kept := 4
			m = m.truncateStatusKeep(kept)
			if m.dataDir != "" {
				_ = ux.SaveSnapshot(m.dataDir, m.sessionSnapshot([]string{"compacted"}))
			}
			m = m.noteStatus("compact:ok")
		}
	case commands.KindDiff:
		diffOut := ""
		if host, ok := m.runtime.(*SessionHost); ok {
			diffOut = host.FormatTurnDiff()
		}
		if diffOut == "" {
			diffOut = "diff: no git"
			if out, err := exec.Command("git", "diff", "--stat").CombinedOutput(); err == nil {
				summary := strings.TrimSpace(string(out))
				if summary == "" {
					summary = "(clean)"
				}
				if len(summary) > 240 {
					summary = summary[:240] + "…"
				}
				diffOut = "diff:\n" + summary
			}
		}
		m.cells = append(m.cells, transcriptCell{Kind: cellDiff, Raw: diffOut})
		m = m.noteStatus(diffOut)
	case commands.KindReview:
		workspace := m.workspaceRoot
		if workspace == "" {
			workspace = "."
		}
		target := review.ParseArgs(action.Args)
		prompt, err := review.BuildPrompt(workspace, target)
		if err != nil {
			m = m.noteStatus("review:error:" + err.Error())
			return m
		}
		m = m.noteStatus("review:" + string(target.Kind))
		if err := m.runtime.StartTurn(context.Background(), prompt); err != nil {
			m = m.noteStatus("review:error:" + err.Error())
		} else {
			m.busy = true
			m.phase = PhaseWorking
		}
	case commands.KindBacktrack:
		prompt := ""
		if len(action.Args) > 0 {
			prompt = strings.Join(action.Args, " ")
		} else {
			for i := len(m.cells) - 1; i >= 0; i-- {
				if m.cells[i].Kind == cellYou {
					prompt = m.cells[i].Raw
					break
				}
			}
		}
		if prompt == "" {
			m = m.noteStatus("backtrack: no user message to restore")
			return m
		}
		if host, ok := m.runtime.(*SessionHost); ok {
			parent := host.ThreadID()
			newID, err := host.ForkThread(context.Background())
			if err != nil {
				m = m.noteStatus("backtrack:error:" + err.Error())
				return m
			}
			m.parentFork = parent
			m.session = string(newID)
			_ = m.activateSession(m.session)
			m = m.withComposerText(prompt)
			m = m.noteStatus("backtrack:forked " + string(newID) + " (composer restored; transcript preserved)")
		} else {
			m = m.withComposerText(prompt)
			m = m.noteStatus("backtrack:composer restored (offline)")
		}
	case commands.KindSearch:
		query := strings.Join(action.Args, " ")
		if strings.TrimSpace(query) == "" {
			m = m.noteStatus("search: usage /search <query>")
			return m
		}
		if host, ok := m.runtime.(*SessionHost); ok {
			hits, err := host.SearchHistory(context.Background(), query, 10)
			if err != nil {
				m = m.noteStatus("search:error:" + err.Error())
				return m
			}
			if len(hits) == 0 {
				m = m.noteStatus("search: no hits")
				return m
			}
			for _, hit := range hits {
				m = m.noteStatus(fmt.Sprintf(
					"search:%s %s@%d %s", hit.Kind, hit.TurnID, hit.Cursor, hit.Snippet,
				))
			}
		} else {
			m = m.noteStatus("search: requires live session store")
		}
	case commands.KindUndo, commands.KindRestore:
		label := "undo"
		if action.Kind == commands.KindRestore {
			label = "restore"
		}
		if host, ok := m.runtime.(*SessionHost); ok {
			if err := host.RevertLastTurn(context.Background()); err != nil {
				m = m.noteStatus(label + ":error:" + err.Error())
			} else {
				m = m.noteStatus(label + ":reverted")
			}
		} else if len(m.cells) > 0 {
			m = m.popLastStatus()
			m = m.noteStatus(label + ":ok")
		}
	case commands.KindRedo, commands.KindCopy:
		m = m.noteStatus(string(action.Kind) + ":noop")
	case commands.KindCost, commands.KindUsage:
		// The panel, not a status line: the answer is three scopes deep and a
		// one-line summary of it can only be misleading about which scope it means.
		return m.openPanel(PanelCost)
	case commands.KindStatus:
		usage := "usage:n/a"
		if m.dataDir != "" {
			if cp, err := ux.LoadCheckpoint(m.dataDir, m.session); err == nil {
				usage = fmt.Sprintf("checkpoint:%s@%s", cp.Status, cp.UpdatedAt.Format(time.RFC3339))
			}
		}
		if m.tokenPrompt+m.tokenCompletion > 0 {
			m.costGlance = fmt.Sprintf("%d tok", m.tokenPrompt+m.tokenCompletion)
			usage = fmt.Sprintf("tokens in=%d out=%d ctx=%d%%", m.tokenPrompt, m.tokenCompletion, m.contextPercent())
		}
		m = m.noteStatus(fmt.Sprintf(
			"status: provider=%s model=%s session=%s mode=%s posture=%s %s %s %s (/cost for detail)",
			m.provider, m.modelID, m.session, m.toolMode, m.posture, m.granularSummary(),
			usage, m.costFragment(),
		))
	case commands.KindCancel, commands.KindStop:
		_ = m.runtime.Cancel(context.Background())
		m.busy = false
		m = m.noteStatus("cancel:ok")
	case commands.KindQuit, commands.KindExit:
		m.quitting = true
		m.restored = true
		_ = m.runtime.Cancel(context.Background())
		m = m.noteStatus("quit")
	case commands.KindPlan:
		m.mode = ModePlan
		m.toolMode = policy.ModePlan
		m = m.syncSecurity()
		m = m.noteStatus("mode:plan")
	case commands.KindMode:
		m = m.applyToolModeArg(action.Args)
		m = m.noteStatus("mode:" + string(m.toolMode))
	case commands.KindGranular:
		m = m.applyGranularArg(action.Args)
	case commands.KindAgent:
		// This used to fork a thread on the parent runtime and draw an "[agent]"
		// card for it, which looked like spawning a child agent and was not one.
		// Spawning belongs to the model's agent tool; the slash command observes.
		if len(action.Args) > 0 {
			m = m.noteStatus(
				"agent: spawning is the model's agent tool, not a slash command; " +
					"ask for the work in the prompt — showing existing agents",
			)
		}
		return m.openPanel(PanelAgents)
	case commands.KindConstitution:
		m = m.noteStatus(m.constitutionStatusLine())
	case commands.KindRelay:
		m = m.writeRelay(action.Args)
	case commands.KindJobs:
		// Bare /jobs opens the panel; the verbs stay because they act on one job,
		// which a refreshing panel has no place doing.
		if len(action.Args) == 0 {
			return m.openPanel(PanelJobs)
		}
		m = m.handleJobs(action.Args)
	case commands.KindTask, commands.KindAutomation, commands.KindRLM:
		lines := m.backendSlashLines(action.Kind)
		for _, line := range lines {
			m = m.noteStatus(line)
		}
		if action.Kind == commands.KindTask {
			m.sidebarTasks = nil
			for _, line := range lines {
				if strings.HasPrefix(line, "task:") && !strings.Contains(line, "empty") && !strings.Contains(line, "error") {
					m.sidebarTasks = append(m.sidebarTasks, line)
				}
			}
			if len(m.sidebarTasks) > 8 {
				m.sidebarTasks = m.sidebarTasks[:8]
			}
			// The panel is where the list keeps living: it says whether anything in
			// this process is executing these tasks, which the lines alone do not.
			return m.openPanel(PanelTasks)
		}
	case commands.KindAttach:
		m = m.attachImage(action.Args)
	case commands.KindAuth:
		return m.openPanel(PanelSettings)
	case commands.KindPermissions:
		m = m.noteStatus(m.permissionsStatusLine())
	case commands.KindContext:
		m = m.noteStatus(m.contextStatusLine())
	case commands.KindSandbox, commands.KindDoctor, commands.KindMemory,
		commands.KindInit, commands.KindApply:
		m = m.noteStatus("slash:/" + action.Name + " — use CLI for full control; TUI entry acknowledged")
	default:
		m = m.noteStatus("unknown slash: /" + action.Name)
	}
	return m
}

func (m Model) sessionDir(id string) string {
	if m.dataDir == "" {
		return ""
	}
	if !strings.HasPrefix(id, "thread-") {
		id = "thread-" + id
	}
	return filepath.Join(m.dataDir, id)
}

func (m Model) sessionNew() Model {
	if m.dataDir == "" {
		m = m.noteStatus("session:/new requires data-dir")
		return m
	}
	id := fmt.Sprintf("thread-%d", time.Now().UnixNano())
	if err := os.MkdirAll(m.sessionDir(id), 0o700); err != nil {
		m = m.noteStatus("session:new error:" + err.Error())
		return m
	}
	m.session = id
	_ = m.activateSession(id)
	m = m.noteStatus("session:new " + id)
	m = m.noteRelayIfPresent()
	return m
}

func (m Model) sessionLoad(id string) Model {
	if m.dataDir == "" {
		m = m.noteStatus("session:/load requires data-dir")
		return m
	}
	dir := m.sessionDir(id)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		// Fall back to sessions/<id>.json snapshot metadata.
		if snap, snapErr := ux.LoadSnapshot(m.dataDir, id); snapErr == nil {
			if snap.ThreadID != "" {
				m.session = snap.ThreadID
			} else {
				m.session = id
			}
			if snap.Provider != "" {
				m.provider = snap.Provider
			}
			if snap.Model != "" {
				m.modelID = snap.Model
			}
			m.parentFork = snap.ParentFork
			if snap.Mode == "plan" {
				m.mode = ModePlan
				m.toolMode = policy.ModePlan
			} else if snap.Mode == "operate" {
				m.toolMode = policy.ModeOperate
			} else if snap.Mode == "act" {
				m.toolMode = policy.ModeAct
			}
			m = m.applySnapshotSecurity(snap)
			m = m.syncSecurity()
			_ = m.activateSession(m.session)
			if host, ok := m.runtime.(*SessionHost); ok {
				host.SetThreadID(m.session)
			}
			m = m.noteStatus("session:load-snapshot " + m.session)
			m = m.noteRelayIfPresent()
			return m
		}
		m = m.noteStatus("session:load missing " + id)
		return m
	}
	m.session = filepath.Base(dir)
	_ = m.activateSession(m.session)
	if snap, err := ux.LoadSnapshot(m.dataDir, "session-local"); err == nil {
		if snap.Provider != "" {
			m.provider = snap.Provider
		}
		if snap.Model != "" {
			m.modelID = snap.Model
		}
		m.parentFork = snap.ParentFork
		m = m.applySnapshotSecurity(snap)
		m = m.syncSecurity()
	}
	if host, ok := m.runtime.(*SessionHost); ok {
		host.SetThreadID(m.session)
	}
	m = m.noteStatus("session:load " + m.session)
	m = m.noteRelayIfPresent()
	return m
}

func (m Model) sessionSave() Model {
	if m.dataDir == "" {
		m = m.noteStatus("session:/save requires data-dir")
		return m
	}
	dir := m.sessionDir(m.session)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		m = m.noteStatus("session:save error:" + err.Error())
		return m
	}
	meta := map[string]any{
		"session": m.session, "provider": m.provider, "model": m.modelID,
		"saved_at": time.Now().UTC().Format(time.RFC3339),
	}
	data, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "session.json"), append(data, '\n'), 0o600); err != nil {
		m = m.noteStatus("session:save error:" + err.Error())
		return m
	}
	summary := m.statusSummary(16)
	_ = ux.SaveSnapshot(m.dataDir, m.sessionSnapshot(summary))
	_ = m.activateSession(m.session)
	m = m.noteStatus("session:save " + m.session)
	return m
}

func (m Model) sessionFork(to string) Model {
	if m.dataDir == "" {
		m = m.noteStatus("session:/fork requires data-dir")
		return m
	}
	src := m.sessionDir(m.session)
	dstID := to
	if !strings.HasPrefix(dstID, "thread-") {
		dstID = "thread-" + dstID
	}
	dst := filepath.Join(m.dataDir, dstID)
	if err := os.MkdirAll(src, 0o700); err != nil {
		m = m.noteStatus("session:fork error:" + err.Error())
		return m
	}
	_ = os.WriteFile(filepath.Join(src, "marker"), []byte(m.session), 0o600)
	if err := copyTree(src, dst); err != nil {
		m = m.noteStatus("session:fork error:" + err.Error())
		return m
	}
	m.session = dstID
	m.parentFork = filepath.Base(src)
	_ = m.activateSession(dstID)
	_ = ux.SaveSnapshot(m.dataDir, m.sessionSnapshot(nil))
	m = m.noteStatus("session:fork " + dstID)
	return m
}

func (m Model) sessionExport() Model {
	if m.dataDir == "" {
		m = m.noteStatus("session:/export requires data-dir")
		return m
	}
	path := filepath.Join(m.dataDir, m.session+".export.json")
	payload := map[string]any{
		"session": m.session, "provider": m.provider, "model": m.modelID,
		"status": m.statusSummary(64),
	}
	data, _ := json.MarshalIndent(payload, "", "  ")
	if err := os.WriteFile(path, append(data, '\n'), 0o600); err != nil {
		m = m.noteStatus("session:export error:" + err.Error())
		return m
	}
	m = m.noteStatus("session:export " + path)
	return m
}

func (m Model) activateSession(id string) error {
	if m.dataDir == "" {
		return nil
	}
	if err := os.MkdirAll(m.dataDir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(m.dataDir, "active-thread"), []byte(id+"\n"), 0o600)
}

func (m Model) writeSettings() error {
	if m.configPath == "" {
		return nil
	}
	doc := map[string]any{}
	if data, err := os.ReadFile(m.configPath); err == nil {
		_ = toml.Unmarshal(data, &doc)
	}
	execution, _ := doc["execution"].(map[string]any)
	if execution == nil {
		execution = map[string]any{}
	}
	execution["provider"] = m.provider
	execution["model"] = m.modelID
	doc["execution"] = execution
	out, err := toml.Marshal(doc)
	if err != nil {
		return err
	}
	return os.WriteFile(m.configPath, out, 0o600)
}

func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o600)
	})
}

func (m Model) openPicker(kind PickerKind) Model {
	m.mode = ModePicker
	m.picker = kind
	m.pickerIndex = 0
	switch kind {
	case PickerModel:
		m.pickerItems = modelIDs()
	case PickerProvider:
		m.pickerItems = providerIDs()
	case PickerSession:
		m.pickerItems = m.listSessions()
	}
	return m
}

func (m Model) listSessions() []string {
	items := []string{m.session}
	if m.dataDir == "" {
		items = append(items, "thread-alt")
		return uniqueStrings(items)
	}
	entries, err := os.ReadDir(m.dataDir)
	if err != nil {
		return uniqueStrings(items)
	}
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "thread-") {
			items = append(items, entry.Name())
		}
	}
	return uniqueStrings(items)
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, item := range in {
		if seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	sort.Strings(out)
	return out
}

func (m Model) openPanel(kind PanelKind) Model {
	m.mode = ModePanel
	m.panel = kind
	m.panelBody = m.renderPanel(kind)
	return m
}

func (m Model) renderPanel(kind PanelKind) string {
	switch kind {
	case PanelMCP:
		if m.mcpConfig == "" {
			return "mcp: config unset — set --mcp-config or press Enter to seed demo config (readonly: transport/command)"
		}
		config, err := mcp.LoadConfig(m.mcpConfig)
		if err != nil {
			return "mcp: error loading config: " + err.Error() + " (fix JSON or reseed)"
		}
		if len(config.Servers) == 0 {
			return "mcp: error: no servers configured — press Enter to seed demo"
		}
		names := make([]string, 0, len(config.Servers))
		toolCount := 0
		for name, server := range config.Servers {
			state := "on"
			if !server.IsEnabled() {
				state = "off"
			} else if health, ok := m.mcpHealth[name]; ok {
				state = health.State
				if health.ConsecutiveFailures > 0 {
					state += fmt.Sprintf("(%d)", health.ConsecutiveFailures)
				}
			}
			toolCount += len(server.Tools)
			names = append(names, name+":"+state)
		}
		sort.Strings(names)
		return fmt.Sprintf(
			"mcp: servers=%d tools=%d [%s] | Enter=reload+toggle first | note: reconnect=reload config from disk",
			len(config.Servers), toolCount, strings.Join(names, ","),
		)
	case PanelFleet:
		if m.fleetRoot == "" {
			return "fleet: ledger root unset — pass --fleet-root or --data-dir (readonly: runs/tasks/seq)"
		}
		ledger, err := fleet.Open(m.fleetRoot)
		if err != nil {
			return "fleet: error open: " + err.Error()
		}
		state, err := ledger.Replay()
		if err != nil {
			return "fleet: error replay: " + err.Error() + " (ledger may be corrupt)"
		}
		return fmt.Sprintf(
			"fleet: runs=%d tasks=%d seq=%d (readonly audit trail; empty Enter refreshes; "+
				"type a question + Enter to chat; Esc closes; background work: codehelper worker)",
			len(state.Runs), len(state.Tasks), state.LastSeq,
		)
	case PanelWorkflow:
		spec := workflow.Spec{Goal: "inspect", Nodes: []workflow.Node{{ID: "n1", Kind: workflow.NodePhase, Prompt: "ready"}}}
		if err := spec.Validate(); err != nil {
			return "workflow: validation error: " + err.Error()
		}
		return "workflow: IR valid; permissions default-deny (Enter revalidates; readonly: goal/nodes)"
	case PanelSettings:
		return fmt.Sprintf(
			"settings: provider=%s model=%s config=%s | Enter writes TOML | readonly: provider/model from config; secrets never shown",
			m.provider, m.modelID, m.configPath,
		)
	case PanelHotbar:
		return "hotbar: /help /new /mcp /fleet /lane /plugin /skill | " +
			"/agent /task /jobs observe background work | /cost reports tokens and spend | " +
			"keys m/p/s · Alt+1-7 panels · onboarding ready"
	case PanelLane:
		root := m.laneRoot()
		if root == "" {
			return "lane: data-dir unset"
		}
		registry, err := lane.Open(root)
		if err != nil {
			return "lane: error: " + err.Error()
		}
		records := registry.List()
		ids := make([]string, 0, len(records))
		for _, record := range records {
			ids = append(ids, record.ID+":"+string(record.Status))
		}
		sort.Strings(ids)
		hints := make([]string, 0)
		for _, record := range records {
			if record.AttachCmd != "" {
				hints = append(hints, record.ID+": "+record.AttachCmd)
			}
		}
		body := fmt.Sprintf("lane: count=%d [%s]", len(records), strings.Join(ids, ","))
		if len(hints) > 0 {
			body += " attach=[" + strings.Join(hints, "; ") + "]"
		}
		return body + " (Enter refreshes)"
	case PanelPlugin:
		return m.renderExtensionPanel("plugin")
	case PanelSkill:
		return m.renderExtensionPanel("skill")
	case PanelAgents:
		return m.renderAgentsPanel()
	case PanelTasks:
		return m.renderTasksPanel()
	case PanelJobs:
		return m.renderJobsPanel()
	case PanelCost:
		return m.renderCostPanel()
	default:
		return ""
	}
}

func (m Model) laneRoot() string {
	if m.dataDir == "" {
		return ""
	}
	return filepath.Join(m.dataDir, "lanes")
}

func (m Model) renderExtensionPanel(kind string) string {
	if m.dataDir == "" {
		return kind + ": data-dir unset"
	}
	paths, err := wire.ResolveExtensionPaths(wire.ExtensionOptions{DataDir: m.dataDir}, ".")
	if err != nil {
		return kind + ": paths error: " + err.Error()
	}
	if kind == "plugin" {
		candidates, err := pluginruntime.Discover(pluginruntime.DiscoveryOptions{
			WorkspaceRoot: paths.PluginWorkspaceRoot,
			UserRoot:      paths.PluginUserRoot,
			BuiltinRoot:   paths.PluginBuiltinRoot,
		})
		if err != nil {
			return "plugin: " + err.Error()
		}
		names := make([]string, 0, len(candidates))
		for _, candidate := range candidates {
			names = append(names, candidate.Name)
		}
		sort.Strings(names)
		return fmt.Sprintf("plugin: count=%d [%s] (Enter refreshes)", len(names), strings.Join(names, ","))
	}
	stateStore, err := skillruntime.NewStateStore(paths.SkillsStatePath)
	if err != nil {
		return "skill: " + err.Error()
	}
	catalog, err := skillruntime.Discover(skillruntime.DiscoveryOptions{
		Workspace: ".", ConfiguredDir: paths.SkillsConfiguredDir,
		UserHome: paths.UserHome, Locale: paths.SkillsLocale, State: stateStore,
	})
	if err != nil {
		return "skill: " + err.Error()
	}
	summaries, _ := catalog.List(context.Background())
	names := make([]string, 0, len(summaries))
	for _, summary := range summaries {
		names = append(names, summary.Name)
	}
	sort.Strings(names)
	return fmt.Sprintf("skill: count=%d [%s] (Enter refreshes)", len(names), strings.Join(names, ","))
}

func (m Model) panelAction() Model {
	switch m.panel {
	case PanelFleet:
		// Refresh only. The ledger records what ran; it no longer schedules, so
		// there is nothing here for a keypress to start. Do not
		// append a status cell on every refresh — that flooded the transcript
		// when users kept pressing Enter expecting the panel to "run" something.
		m.panelBody = m.renderPanel(PanelFleet)
	case PanelMCP:
		if m.mcpConfig == "" && m.dataDir != "" {
			m.mcpConfig = filepath.Join(m.dataDir, "mcp.json")
		}
		if m.mcpConfig == "" {
			m.panelBody = "mcp: no config path"
			return m
		}
		if _, err := os.Stat(m.mcpConfig); os.IsNotExist(err) {
			config := mcp.Config{
				Version: mcp.ConfigVersion,
				Servers: map[string]mcp.ServerConfig{
					"local": {
						Transport: "stdio",
						Command:   "echo",
						Tools: map[string]mcp.ToolBinding{
							"default": {
								Capability: "read", AccessMode: "read",
								ParallelPolicy: "serial", SandboxRequirement: "none",
							},
						},
					},
				},
			}
			data, _ := json.MarshalIndent(config, "", "  ")
			_ = os.MkdirAll(filepath.Dir(m.mcpConfig), 0o700)
			if err := os.WriteFile(m.mcpConfig, append(data, '\n'), 0o600); err != nil {
				m.panelBody = "mcp: seed error: " + err.Error()
				return m
			}
			m = m.noteStatus("mcp:seeded " + m.mcpConfig)
		} else if config, err := mcp.LoadConfig(m.mcpConfig); err != nil {
			m.panelBody = "mcp: reload error: " + err.Error()
			m = m.noteStatus("mcp:reconnect_failed")
			return m
		} else if len(config.Servers) > 0 {
			names := make([]string, 0, len(config.Servers))
			for name := range config.Servers {
				names = append(names, name)
			}
			sort.Strings(names)
			server := config.Servers[names[0]]
			enabled := !server.IsEnabled()
			server.Enabled = &enabled
			config.Servers[names[0]] = server
			if data, err := json.MarshalIndent(config, "", "  "); err == nil {
				_ = os.WriteFile(m.mcpConfig, append(data, '\n'), 0o600)
				m = m.noteStatus(fmt.Sprintf("mcp:reconnect ok toggle %s enabled=%v", names[0], enabled))
			}
		} else {
			m.panelBody = "mcp: error: empty server list after reload"
			m = m.noteStatus("mcp:reconnect_failed")
			return m
		}
		m.panelBody = m.renderPanel(PanelMCP)
	case PanelLane, PanelPlugin, PanelSkill, PanelAgents, PanelTasks, PanelJobs, PanelCost:
		// Observation panels: a keypress re-reads the source of truth. Acting on one
		// of these rows belongs to the tools and CLI that own the lifecycle.
		m.panelBody = m.renderPanel(m.panel)
		m = m.noteStatus(string(m.panel) + ":refreshed")
	case PanelWorkflow:
		m.panelBody = m.renderPanel(PanelWorkflow)
		m = m.noteStatus("workflow:revalidated")
	case PanelSettings:
		if err := m.writeSettings(); err != nil {
			m.panelBody = "settings: write error: " + err.Error()
		} else {
			m = m.noteStatus("settings:written")
			m.panelBody = m.renderPanel(PanelSettings)
		}
	case PanelHotbar:
		m = m.noteStatus("slash: " + commands.HelpText())
		m.panelBody = m.renderPanel(PanelHotbar)
	}
	return m
}

func modelIDs() []string {
	catalog := model.DefaultCatalog()
	var out []string
	for _, provider := range catalog.Providers() {
		for id := range provider.Models {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	if len(out) == 0 {
		return []string{"gpt-4.1"}
	}
	return out
}

func providerIDs() []string {
	catalog := model.DefaultCatalog()
	var out []string
	for _, provider := range catalog.Providers() {
		out = append(out, provider.ID)
	}
	sort.Strings(out)
	return out
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}
	m.ensureChrome()
	m = m.recomputePhase()
	if m.mode == ModeTranscript {
		return m.renderTranscriptOverlayView()
	}
	body := ""
	bodyHeight := 0
	if m.ready {
		// Content is owned by Update-path refresh/sync; View only reads the pager.
		m.layoutChrome()
		body = m.viewport.View()
		bodyHeight = m.viewport.Height
		body = padBlockHeight(body, bodyHeight)
	} else {
		body = m.buildTranscriptView()
	}
	header := m.renderHeader()
	scrollHint := ""
	if m.ready && !m.followTail {
		scrollHint = styleMuted.Render("  ↑↓/PgUp · End↓ · Ctrl+T")
	}
	sidebar := ""
	if len(m.sidebarTasks) > 0 {
		sidebar = "\n" + styleSidebar.Render(strings.Join(m.sidebarTasks, "\n"))
	}
	extra := ""
	pane := m.bottomPane()
	if m.height > 0 && m.height < 12 {
		compact := BottomPane{Width: pane.Width, Status: pane.Status, Composer: pane.Composer}
		return ensureTrailingNewline(header + scrollHint + "\n" + compact.View())
	}
	var b strings.Builder
	b.WriteString(header)
	if scrollHint != "" {
		b.WriteString(scrollHint)
	}
	b.WriteByte('\n')
	b.WriteString(body)
	if sidebar != "" {
		b.WriteString(sidebar)
	}
	if extra != "" {
		b.WriteString(extra)
	}
	b.WriteByte('\n')
	b.WriteString(pane.View())
	out := ensureTrailingNewline(b.String())
	// When AltScreen is unavailable, paint enough rows to cover the terminal
	// so the multi-line launch command cannot show through.
	if m.height > 0 {
		lines := strings.Count(out, "\n")
		if lines < m.height {
			out += strings.Repeat("\n", m.height-lines)
		}
	}
	return out
}

func padBlockHeight(block string, height int) string {
	if height <= 0 {
		return block
	}
	block = strings.TrimRight(block, "\n")
	n := 0
	if block != "" {
		n = strings.Count(block, "\n") + 1
	}
	if n >= height {
		return block
	}
	if block == "" {
		return strings.Repeat("\n", height-1)
	}
	return block + strings.Repeat("\n", height-n)
}

func ensureTrailingNewline(s string) string {
	if s == "" || strings.HasSuffix(s, "\n") {
		return s
	}
	return s + "\n"
}

func (m Model) cyclePosture() Model {
	switch m.posture {
	case "suggest":
		m.posture = "auto"
	case "auto":
		m.posture = "bypass"
	default:
		m.posture = "suggest"
	}
	label := map[string]string{"suggest": "Ask", "auto": "Auto", "bypass": "Full"}[m.posture]
	m = m.syncSecurity()
	m = m.appendSystem("posture:" + m.posture + " (" + label + ")")
	return m
}

func (m Model) applyToolModeArg(args []string) Model {
	next := m.toolMode
	if len(args) == 0 {
		switch m.toolMode {
		case policy.ModePlan:
			next = policy.ModeAct
		case policy.ModeAct:
			next = policy.ModeOperate
		default:
			next = policy.ModePlan
		}
	} else {
		switch strings.ToLower(strings.TrimSpace(args[0])) {
		case "plan":
			next = policy.ModePlan
		case "act":
			next = policy.ModeAct
		case "operate":
			next = policy.ModeOperate
		default:
			return m
		}
	}
	m.toolMode = next
	if next == policy.ModePlan {
		m.mode = ModePlan
	} else if m.mode == ModePlan {
		m.mode = ModeChat
	}
	return m.syncSecurity()
}

func (m Model) syncSecurity() Model {
	type policyHost interface {
		SetPolicyMode(mode policy.Mode)
		SetPermission(permission policy.Permission)
		SetGranular(granular policy.Granular)
	}
	host, ok := m.runtime.(policyHost)
	if !ok {
		return m
	}
	mode := m.toolMode
	if mode == "" {
		mode = policy.ModeAct
	}
	host.SetPolicyMode(mode)
	switch m.posture {
	case "suggest":
		host.SetPermission(policy.PermissionSuggest)
	case "bypass":
		host.SetPermission(policy.PermissionBypass)
	default:
		host.SetPermission(policy.PermissionAuto)
	}
	host.SetGranular(m.granular)
	return m
}

func (m Model) applyGranularArg(args []string) Model {
	if len(args) == 0 {
		m = m.noteStatus("granular:" + m.granularSummary())
		return m
	}
	if len(args) != 2 {
		m = m.noteStatus("usage: /granular [sandbox|rules|skills|mcp] [ask|allow|deny|inherit]")
		return m
	}
	surface := strings.ToLower(strings.TrimSpace(args[0]))
	posture, ok := parseSurfacePosture(args[1])
	if !ok {
		m = m.noteStatus("usage: /granular [sandbox|rules|skills|mcp] [ask|allow|deny|inherit]")
		return m
	}
	switch surface {
	case "sandbox":
		m.granular.Sandbox = posture
	case "rules":
		m.granular.Rules = posture
	case "skills":
		m.granular.Skills = posture
	case "mcp":
		m.granular.MCP = posture
	default:
		m = m.noteStatus("usage: /granular [sandbox|rules|skills|mcp] [ask|allow|deny|inherit]")
		return m
	}
	m = m.syncSecurity()
	if m.dataDir != "" {
		_ = ux.SaveSnapshot(m.dataDir, m.sessionSnapshot(nil))
	}
	m = m.noteStatus("granular:" + m.granularSummary())
	return m
}

func parseSurfacePosture(raw string) (policy.SurfacePosture, bool) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "ask":
		return policy.SurfaceAsk, true
	case "allow":
		return policy.SurfaceAllow, true
	case "deny":
		return policy.SurfaceDeny, true
	case "inherit", "":
		return policy.SurfaceInherit, true
	default:
		return "", false
	}
}

func (m Model) granularSummary() string {
	parts := make([]string, 0, 4)
	add := func(name string, value policy.SurfacePosture) {
		if value == policy.SurfaceInherit {
			return
		}
		parts = append(parts, name+":"+string(value))
	}
	add("sandbox", m.granular.Sandbox)
	add("rules", m.granular.Rules)
	add("skills", m.granular.Skills)
	add("mcp", m.granular.MCP)
	if len(parts) == 0 {
		return "inherit"
	}
	return strings.Join(parts, " ")
}

func (m Model) snapshotGranular() map[string]string {
	out := map[string]string{}
	if m.granular.Sandbox != policy.SurfaceInherit {
		out["sandbox"] = string(m.granular.Sandbox)
	}
	if m.granular.Rules != policy.SurfaceInherit {
		out["rules"] = string(m.granular.Rules)
	}
	if m.granular.Skills != policy.SurfaceInherit {
		out["skills"] = string(m.granular.Skills)
	}
	if m.granular.MCP != policy.SurfaceInherit {
		out["mcp"] = string(m.granular.MCP)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (m Model) applySnapshotSecurity(snap ux.Snapshot) Model {
	if snap.Posture == "suggest" || snap.Posture == "auto" || snap.Posture == "bypass" {
		m.posture = snap.Posture
	}
	if len(snap.Granular) == 0 {
		return m
	}
	if value, ok := parseSurfacePosture(snap.Granular["sandbox"]); ok {
		m.granular.Sandbox = value
	}
	if value, ok := parseSurfacePosture(snap.Granular["rules"]); ok {
		m.granular.Rules = value
	}
	if value, ok := parseSurfacePosture(snap.Granular["skills"]); ok {
		m.granular.Skills = value
	}
	if value, ok := parseSurfacePosture(snap.Granular["mcp"]); ok {
		m.granular.MCP = value
	}
	return m
}

func (m Model) sessionSnapshot(messages []string) ux.Snapshot {
	return ux.Snapshot{
		SessionID: "session-local", ThreadID: m.session,
		Provider: m.provider, Model: m.modelID, Mode: string(m.toolMode),
		Posture: m.posture, Granular: m.snapshotGranular(),
		Messages: messages, ParentFork: m.parentFork, UpdatedAt: time.Now().UTC(),
	}
}

func (m Model) constitutionStatusLine() string {
	workspace := "."
	if m.dataDir != "" {
		// data-dir is typically <workspace>/.codehelper
		workspace = filepath.Dir(m.dataDir)
	}
	bundle, err := constitution.Load(workspace, "")
	if err != nil {
		return "constitution:error:" + err.Error()
	}
	return fmt.Sprintf(
		"constitution:loaded=%v repo=%v user=%v rules=%d",
		bundle.Status.Loaded, bundle.Status.RepoPresent, bundle.Status.UserPresent, bundle.Status.RuleCount,
	)
}

func (m Model) permissionsStatusLine() string {
	workspace := "."
	if m.dataDir != "" {
		workspace = filepath.Dir(m.dataDir)
	}
	bundle, err := permissions.Load(workspace)
	if err != nil {
		return "permissions:error:" + err.Error()
	}
	deny, ask, allow := bundle.Summary()
	return fmt.Sprintf(
		"permissions:path=%s present=%v deny=%d ask=%d allow=%d",
		bundle.Path, bundle.Present, deny, ask, allow,
	)
}

// contextStatusLine reports what the last turn's prompt context carried. It says
// so explicitly when no turn has run yet, rather than looking like an empty
// context.
func (m Model) contextStatusLine() string {
	if m.lastContextLine == "" {
		return "context: no turn has reported its context yet"
	}
	return "context: " + m.lastContextLine
}

// SetLastPlan caches plan artifact text for /relay handoff rendering.
func (m *Model) SetLastPlan(text string) {
	if m == nil {
		return
	}
	m.lastPlanText = strings.TrimSpace(text)
}

func (m Model) handleJobs(args []string) Model {
	if m.jobs == nil {
		m = m.noteStatus("jobs:unavailable")
		return m
	}
	verb := "list"
	if len(args) > 0 {
		verb = strings.ToLower(args[0])
	}
	rest := []string{}
	if len(args) > 1 {
		rest = args[1:]
	}
	switch verb {
	case "list", "ls":
		jobs := m.jobs.List()
		if len(jobs) == 0 {
			m = m.noteStatus("jobs:empty")
			return m
		}
		for _, job := range jobs {
			m = m.noteStatus(formatJobLine(job))
		}
	case "show", "info":
		if len(rest) == 0 {
			m = m.noteStatus("jobs:usage show <id>")
			return m
		}
		job, ok := m.jobs.Info(rest[0])
		if !ok {
			m = m.noteStatus("jobs:not-found:" + rest[0])
			return m
		}
		m = m.noteStatusLines(formatJobDetail(job)...)
	case "poll":
		if len(rest) == 0 {
			m = m.noteStatus("jobs:usage poll <id>")
			return m
		}
		job, err := m.jobs.Poll(context.Background(), rest[0], false)
		if err != nil {
			m = m.noteStatus("jobs:error:" + err.Error())
			return m
		}
		m = m.noteStatus(formatJobLine(job))
		if job.OutputTail != "" {
			m = m.noteStatus("jobs:tail:" + truncateJobTail(job.OutputTail))
		}
	case "wait":
		if len(rest) == 0 {
			m = m.noteStatus("jobs:usage wait <id>")
			return m
		}
		job, err := m.jobs.Poll(context.Background(), rest[0], true)
		if err != nil {
			m = m.noteStatus("jobs:error:" + err.Error())
			return m
		}
		m = m.noteStatus(formatJobLine(job))
	case "stdin":
		if len(rest) < 2 {
			m = m.noteStatus("jobs:usage stdin <id> <data>")
			return m
		}
		data := strings.Join(rest[1:], " ") + "\n"
		if err := m.jobs.Stdin(rest[0], data); err != nil {
			m = m.noteStatus("jobs:error:" + err.Error())
			return m
		}
		m = m.noteStatus("jobs:stdin-ok:" + rest[0])
	case "cancel":
		if len(rest) == 0 {
			m = m.noteStatus("jobs:usage cancel <id>")
			return m
		}
		if err := m.jobs.Cancel(rest[0]); err != nil {
			m = m.noteStatus("jobs:error:" + err.Error())
			return m
		}
		m = m.noteStatus("jobs:canceled:" + rest[0])
	case "cancel-all", "cancelall":
		m.jobs.CancelAll()
		m = m.noteStatus("jobs:canceled-all")
	default:
		m = m.noteStatus("jobs:usage list|show|poll|wait|stdin|cancel|cancel-all")
	}
	return m
}

func formatJobLine(job process.JobInfo) string {
	return fmt.Sprintf(
		"jobs:%s status=%s running=%v exit=%d cmd=%q",
		job.ID, job.Status, job.Running, job.ExitCode, job.Command,
	)
}

func formatJobDetail(job process.JobInfo) []string {
	lines := []string{
		formatJobLine(job),
		fmt.Sprintf("jobs:cwd=%q created=%s task=%q", job.Cwd, job.CreatedAt.UTC().Format(time.RFC3339), job.LinkedTaskID),
	}
	if job.OutputTail != "" {
		lines = append(lines, "jobs:tail:"+truncateJobTail(job.OutputTail))
	}
	return lines
}

func truncateJobTail(tail string) string {
	const max = 512
	if len(tail) <= max {
		return strings.ReplaceAll(tail, "\n", "\\n")
	}
	return "…" + strings.ReplaceAll(tail[len(tail)-max:], "\n", "\\n")
}

func (m Model) handoffPath() string {
	if m.dataDir != "" {
		return filepath.Join(m.dataDir, "handoff.md")
	}
	return filepath.Join(".", ".codehelper", "handoff.md")
}

func (m Model) writeRelay(args []string) Model {
	path := m.handoffPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		m = m.noteStatus("relay:error:" + err.Error())
		return m
	}
	focus := strings.TrimSpace(strings.Join(args, " "))
	if focus == "" {
		focus = "continue current goal"
	}
	var b strings.Builder
	b.WriteString("# Session relay\n\n")
	b.WriteString("## Goal / focus\n\n")
	b.WriteString(focus)
	b.WriteString("\n\n## Session\n\n")
	b.WriteString("- session: ")
	b.WriteString(m.session)
	b.WriteString("\n- provider: ")
	b.WriteString(m.provider)
	b.WriteString("\n- model: ")
	b.WriteString(m.modelID)
	b.WriteString("\n- mode: ")
	b.WriteString(string(m.mode))
	b.WriteString("\n\n")
	if m.lastPlanText != "" {
		b.WriteString("## Plan\n\n")
		b.WriteString("```\n")
		b.WriteString(m.lastPlanText)
		b.WriteString("\n```\n\n")
	}
	b.WriteString("## Next action\n\n")
	b.WriteString("1. Resume from this relay in a fresh session.\n")
	b.WriteString("2. Verify outstanding checks before mutating further.\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		m = m.noteStatus("relay:error:" + err.Error())
		return m
	}
	m = m.noteStatus("relay:wrote " + path)
	return m
}

func (m Model) noteRelayIfPresent() Model {
	path := m.handoffPath()
	if _, err := os.Stat(path); err != nil {
		return m
	}
	m = m.noteStatus("relay:loaded path=" + path)
	return m
}

func (m Model) backendSlashLines(kind commands.Kind) []string {
	ctx := context.Background()
	switch kind {
	case commands.KindTask:
		return m.listTaskLines(ctx)
	case commands.KindAutomation:
		return m.listAutomationLines(ctx)
	case commands.KindRLM:
		return m.listRLMLines()
	default:
		return []string{"slash:" + string(kind)}
	}
}

func (m Model) listTaskLines(ctx context.Context) []string {
	if host, ok := m.runtime.(*SessionHost); ok && host.Session() != nil && host.Session().Tasks() != nil {
		tasks, err := host.Session().Tasks().List(ctx, taskstate.Filter{}, 20)
		if err != nil {
			return []string{"task:error:" + err.Error()}
		}
		if len(tasks) == 0 {
			return []string{"task:empty — no tasks yet; create via CLI/task APIs (list only in TUI)"}
		}
		lines := make([]string, 0, len(tasks))
		for _, item := range tasks {
			lines = append(lines, fmt.Sprintf("task:%s\t%s\t%s", item.ID, item.State, item.Kind))
		}
		return lines
	}
	if m.dataDir == "" {
		return []string{"task:no-data-dir"}
	}
	store, err := state.Open(ctx, state.Options{DataDir: m.dataDir})
	if err != nil {
		return []string{"task:error:" + err.Error()}
	}
	defer func() { _ = store.Close(ctx) }()
	repo := taskstate.NewSQLiteRepository(store.SQLite())
	tasks, err := repo.List(ctx, taskstate.Filter{}, 20)
	if err != nil {
		return []string{"task:error:" + err.Error()}
	}
	if len(tasks) == 0 {
		return []string{"task:empty — no tasks yet; create via CLI/task APIs (list only in TUI)"}
	}
	lines := make([]string, 0, len(tasks))
	for _, item := range tasks {
		lines = append(lines, fmt.Sprintf("task:%s\t%s\t%s", item.ID, item.State, item.Kind))
	}
	return lines
}

func (m Model) listAutomationLines(ctx context.Context) []string {
	if host, ok := m.runtime.(*SessionHost); ok && host.Session() != nil && host.Session().Automations() != nil {
		values, err := host.Session().Automations().List(ctx, automation.Filter{})
		if err != nil {
			return []string{"automation:error:" + err.Error()}
		}
		if len(values) == 0 {
			return []string{"automation:empty — no automations configured; list is read-only here"}
		}
		lines := make([]string, 0, len(values))
		for _, item := range values {
			lines = append(lines, fmt.Sprintf("automation:%s\t%s", item.ID, item.Status))
		}
		return lines
	}
	if m.dataDir == "" {
		return []string{"automation:no-data-dir"}
	}
	store, err := state.Open(ctx, state.Options{DataDir: m.dataDir})
	if err != nil {
		return []string{"automation:error:" + err.Error()}
	}
	defer func() { _ = store.Close(ctx) }()
	repo := automation.NewSQLiteRepository(store.SQLite())
	values, err := repo.List(ctx, automation.Filter{})
	if err != nil {
		return []string{"automation:error:" + err.Error()}
	}
	if len(values) == 0 {
		return []string{"automation:empty — no automations configured; list is read-only here"}
	}
	lines := make([]string, 0, len(values))
	for _, item := range values {
		lines = append(lines, fmt.Sprintf("automation:%s\t%s", item.ID, item.Status))
	}
	return lines
}

func (m Model) listRLMLines() []string {
	if host, ok := m.runtime.(*SessionHost); ok && host.Session() != nil && host.Session().RLM() != nil {
		store := host.Session().RLM()
		names := store.ListSessions()
		py := "missing"
		if store.PythonAvailable() {
			py = store.Python()
		}
		if len(names) == 0 {
			return []string{fmt.Sprintf("rlm:sessions=0 python=%s", py)}
		}
		lines := []string{fmt.Sprintf("rlm:sessions=%d python=%s", len(names), py)}
		for _, name := range names {
			lines = append(lines, "rlm:"+name)
		}
		return lines
	}
	py := "unknown"
	if _, err := exec.LookPath("python3"); err == nil {
		py = "python3"
	}
	return []string{fmt.Sprintf("rlm:sessions=0 python=%s", py)}
}

func (m Model) Restored() bool { return m.restored }

func (m Model) attachImage(args []string) Model {
	if len(args) == 0 {
		m = m.noteStatus("attach:error: usage /attach <workspace-relative-image-path> (max 3)")
		return m
	}
	root := m.workspaceRoot
	if root == "" {
		root = "."
	}
	for _, raw := range args {
		if len(m.pendingImages) >= maxPendingImages {
			m = m.noteStatus("attach:error: at most 3 images (no workgraph)")
			return m
		}
		path := strings.TrimSpace(raw)
		if path == "" || filepath.IsAbs(path) {
			m = m.noteStatus("attach:error: path must be workspace-relative")
			return m
		}
		cleaned := filepath.Clean(path)
		if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
			m = m.noteStatus("attach:error: path escapes workspace")
			return m
		}
		abs := filepath.Join(root, cleaned)
		info, err := os.Stat(abs)
		if err != nil {
			m = m.noteStatus("attach:error:" + err.Error())
			return m
		}
		if info.IsDir() {
			m = m.noteStatus("attach:error: path is a directory")
			return m
		}
		m.pendingImages = append(m.pendingImages, cleaned)
		m = m.noteStatus(fmt.Sprintf(
			"attach:ok path=%s count=%d/%d (next prompt injects image_analyze hints; requires [vision] config)",
			cleaned, len(m.pendingImages), maxPendingImages,
		))
	}
	return m
}

// LastSlash returns the last routed slash command name (tests).
func (m Model) LastSlash() string { return m.lastSlash }

// Run starts the interactive TUI. With FixturePath (or an injected Host) it
// binds a real bootstrap Runtime; otherwise it uses a hermetic fake host so
// PTY smoke stays offline-safe.
func Run(ctx context.Context, options Options) error {
	host, closer, err := openRuntimeHost(ctx, options)
	if err != nil {
		return err
	}
	if closer != nil {
		defer func() {
			closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = closer(closeCtx)
		}()
	}
	model := NewModel(options, host)
	if sessionHost, ok := host.(*SessionHost); ok && options.DataDir != "" {
		if active, err := os.ReadFile(filepath.Join(options.DataDir, "active-thread")); err == nil {
			if id := strings.TrimSpace(string(active)); id != "" {
				sessionHost.SetThreadID(id)
				model.session = id
			}
		}
	}
	opts := []tea.ProgramOption{tea.WithContext(ctx)}
	// CLI always injects os.Stdin/Stdout, so nil-checks must not gate alt-screen.
	// Without alt-screen the launch command stays visible and only one viewport
	// page appears to "stick" over the shell scrollback.
	if !options.DisableAltScreen {
		opts = append(opts,
			tea.WithAltScreen(),
			tea.WithMouseCellMotion(),
			tea.WithFPS(60),
		)
	}
	if options.Stdin != nil {
		opts = append(opts, tea.WithInput(options.Stdin))
	}
	if options.Stdout != nil {
		opts = append(opts, tea.WithOutput(options.Stdout))
	}
	program := tea.NewProgram(model, opts...)
	final, err := program.Run()
	if err != nil {
		return err
	}
	if ended, ok := final.(Model); ok && !ended.Restored() {
		if options.DisableAltScreen {
			return nil // hermetic / pipe exit
		}
		return fmt.Errorf("terminal restore flag not set")
	}
	return nil
}

func openRuntimeHost(ctx context.Context, options Options) (RuntimeHost, func(context.Context) error, error) {
	if options.Host != nil {
		return options.Host, options.Host.Close, nil
	}
	if !wantsLiveRuntime(options) {
		return &fakeRuntime{}, nil, nil
	}
	if options.FixturePath == "" {
		if options.Provider == "" || options.Model == "" {
			return nil, nil, fmt.Errorf("live tui requires --provider and --model (or --provider-fixture)")
		}
		if options.BaseURL != "" && options.APIKeyEnv == "" {
			return nil, nil, fmt.Errorf("custom --base-url requires --api-key-env")
		}
	}

	protocolName := options.Protocol
	if protocolName == "" {
		protocolName = "openai_chat"
	}
	mode := options.Mode
	if mode == "" {
		mode = "act"
	}
	permission := options.Permission
	if permission == "" {
		permission = "auto"
	}
	workspace := options.Workspace
	if workspace == "" {
		workspace = "."
	}

	overrides := config.Overrides{
		Provider:  &options.Provider,
		Model:     &options.Model,
		Protocol:  &protocolName,
		Mode:      &mode,
		Workspace: &workspace,
		Tools:     &options.EnableTools,
	}
	if options.MaxSteps > 0 {
		overrides.MaxSteps = &options.MaxSteps
	}
	if options.DataDir != "" {
		overrides.StateDataDir = &options.DataDir
	}

	execOpts := wire.ExecOptions{
		ConfigPath:      options.ConfigPath,
		ConfigOverrides: overrides,
		BaseURL:         options.BaseURL,
		APIKeyEnv:       options.APIKeyEnv,
		FixturePath:     options.FixturePath,
		Permission:      permission,
		MCPConfigPath:   options.MCPConfig,
		Extensions:      wire.ExtensionOptions{DataDir: options.DataDir},
	}
	if options.BaseURL != "" {
		caps := options.ModelCapabilities
		if caps == "" {
			caps = "streaming,reasoning,tool_calls"
		}
		contextTokens := options.ContextTokens
		if contextTokens == 0 {
			contextTokens = 262144
		}
		maxOut := options.ModelMaxOutputTokens
		if maxOut == 0 {
			maxOut = 131072
		}
		inPrice := options.InputPricePerMillion
		if inPrice == 0 {
			inPrice = 0.25
		}
		outPrice := options.OutputPricePerMillion
		if outPrice == 0 {
			outPrice = 2.0
		}
		currency := options.PricingCurrency
		if currency == "" {
			currency = "USD"
		}
		execOpts.ModelMetadata = wire.ModelMetadataOptions{
			ContextTokens: contextTokens, MaxOutputTokens: maxOut, Capabilities: caps,
			InputPerMillion: inPrice, OutputPerMillion: outPrice, Currency: currency,
			ContextSet: true, OutputSet: true, CapabilitiesSet: true,
			InputPriceSet: true, OutputPriceSet: true, CurrencySet: true,
		}
	}

	var store *state.Store
	if options.DataDir != "" {
		loaded, err := config.Load(config.LoadOptions{
			Path:      options.ConfigPath,
			Overrides: overrides,
		})
		if err != nil {
			return nil, nil, err
		}
		store, err = state.Open(ctx, state.Options{
			DataDir: options.DataDir, BusyTimeout: loaded.Config.State.BusyTimeout,
		})
		if err != nil {
			return nil, nil, err
		}
		execOpts.PersistentStore = store
		if loaded.Config.Execution.Workspace != "" {
			workspace = loaded.Config.Execution.Workspace
		}
	}
	session, err := wire.NewExec(ctx, execOpts)
	if err != nil {
		if store != nil {
			_ = store.CloseAll(context.Background())
		}
		return nil, nil, err
	}
	host, err := NewSessionHost(session)
	if err != nil {
		_ = session.Close(ctx)
		if store != nil {
			_ = store.CloseAll(context.Background())
		}
		return nil, nil, err
	}
	if store != nil {
		host.AttachStore(store, "session-local", workspace)
	}
	return host, host.Close, nil
}

func wantsLiveRuntime(options Options) bool {
	return options.FixturePath != "" ||
		options.BaseURL != "" ||
		options.Provider != "" ||
		options.Model != ""
}
