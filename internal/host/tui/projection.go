package tui

import (
	"encoding/json"
	"fmt"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"strings"
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
