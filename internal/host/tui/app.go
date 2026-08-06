// Package tui implements the Bubble Tea interactive host over app.Runtime.
package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

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
		posture = "suggest"
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

// SetLastPlan caches plan artifact text for /relay handoff rendering.
func (m *Model) SetLastPlan(text string) {
	if m == nil {
		return
	}
	m.lastPlanText = strings.TrimSpace(text)
}

func (m Model) Restored() bool { return m.restored }

// LastSlash returns the last routed slash command name (tests).
func (m Model) LastSlash() string { return m.lastSlash }
