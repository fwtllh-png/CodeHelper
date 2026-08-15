package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/host/review"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/persist/session/ux"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
	"github.com/fwtllh-png/CodeHelper/internal/security/permissions"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

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

func (m Model) constitutionStatusLine() string {
	workspace := "."
	if m.dataDir != "" {

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
	workspace := m.workspaceRoot
	if workspace == "" {
		workspace = "."
	}
	path, err := permissions.Path(m.dataDir, workspace)
	if err != nil {
		return "permissions:error:" + err.Error()
	}
	bundle, err := permissions.Load(path)
	if err != nil {
		return "permissions:error:" + err.Error()
	}
	deny, ask, allow := bundle.Summary()
	return fmt.Sprintf(
		"permissions:path=%s present=%v deny=%d ask=%d allow=%d",
		bundle.Path, bundle.Present, deny, ask, allow,
	)
}

func (m Model) contextStatusLine() string {
	if m.lastContextLine == "" {
		return "context: no turn has reported its context yet"
	}
	return "context: " + m.lastContextLine
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
