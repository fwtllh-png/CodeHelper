// Package commands routes TUI slash commands to typed actions.
package commands

import (
	"strings"
)

type Kind string

const (
	KindHelp         Kind = "help"
	KindModel        Kind = "model"
	KindProvider     Kind = "provider"
	KindSession      Kind = "session"
	KindNew          Kind = "new"
	KindLoad         Kind = "load"
	KindSave         Kind = "save"
	KindFork         Kind = "fork"
	KindExport       Kind = "export"
	KindMCP          Kind = "mcp"
	KindFleet        Kind = "fleet"
	KindWorkflow     Kind = "workflow"
	KindSettings     Kind = "settings"
	KindHotbar       Kind = "hotbar"
	KindClear        Kind = "clear"
	KindCompact      Kind = "compact"
	KindDiff         Kind = "diff"
	KindUndo         Kind = "undo"
	KindRestore      Kind = "restore"
	KindRedo         Kind = "redo"
	KindCopy         Kind = "copy"
	KindCost         Kind = "cost"
	KindStatus       Kind = "status"
	KindCancel       Kind = "cancel"
	KindStop         Kind = "stop"
	KindQuit         Kind = "quit"
	KindExit         Kind = "exit"
	KindPlugin       Kind = "plugin"
	KindSkill        Kind = "skill"
	KindLane         Kind = "lane"
	KindAuth         Kind = "auth"
	KindSandbox      Kind = "sandbox"
	KindReview       Kind = "review"
	KindPlan         Kind = "plan"
	KindMode         Kind = "mode"
	KindGranular     Kind = "granular"
	KindUsage        Kind = "usage"
	KindDoctor       Kind = "doctor"
	KindMemory       Kind = "memory"
	KindContext      Kind = "context"
	KindPermissions  Kind = "permissions"
	KindInit         Kind = "init"
	KindApply        Kind = "apply"
	KindThread       Kind = "thread"
	KindResume       Kind = "resume"
	KindContinue     Kind = "continue"
	KindAgent        Kind = "agent"
	KindConstitution Kind = "constitution"
	KindTask         Kind = "task"
	KindAutomation   Kind = "automation"
	KindRLM          Kind = "rlm"
	KindRelay        Kind = "relay"
	KindJobs         Kind = "jobs"
	KindAttach       Kind = "attach"
	KindBacktrack    Kind = "backtrack"
	KindSearch       Kind = "search"
	KindUnknown      Kind = "unknown"
)

// Action is a parsed slash command. It is intentionally free of View strings.
type Action struct {
	Raw  string
	Name string
	Args []string
	Kind Kind
}

// AllKinds returns the catalog of concrete slash kinds (excludes unknown).
func AllKinds() []Kind {
	return []Kind{
		KindHelp, KindModel, KindProvider, KindSession,
		KindNew, KindLoad, KindSave, KindFork, KindExport,
		KindMCP, KindFleet, KindWorkflow, KindSettings, KindHotbar,
		KindClear, KindCompact, KindDiff, KindUndo, KindRestore,
		KindCost, KindStatus, KindCancel, KindStop, KindQuit, KindExit,
		KindPlugin, KindSkill, KindLane, KindAuth,
		KindReview, KindPlan, KindMode, KindGranular, KindUsage,
		KindContext, KindPermissions,
		KindThread, KindResume, KindContinue, KindAgent,
		KindConstitution, KindTask, KindAutomation, KindRLM, KindRelay, KindJobs, KindAttach,
		KindBacktrack, KindSearch,
	}
}

// Parse returns true when line is a slash command (leading '/').
func Parse(line string) (Action, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "/") {
		return Action{}, false
	}
	fields := strings.Fields(line)
	name := strings.TrimPrefix(strings.ToLower(fields[0]), "/")
	args := fields[1:]
	action := Action{Raw: line, Name: name, Args: args, Kind: classify(name)}
	return action, true
}

func classify(name string) Kind {
	switch name {
	case "help":
		return KindHelp
	case "model", "models":
		return KindModel
	case "provider", "providers":
		return KindProvider
	case "session", "sessions":
		return KindSession
	case "new":
		return KindNew
	case "load":
		return KindLoad
	case "save":
		return KindSave
	case "fork":
		return KindFork
	case "export":
		return KindExport
	case "mcp":
		return KindMCP
	case "fleet":
		return KindFleet
	case "workflow":
		return KindWorkflow
	case "settings":
		return KindSettings
	case "hotbar":
		return KindHotbar
	case "clear":
		return KindClear
	case "compact":
		return KindCompact
	case "diff":
		return KindDiff
	case "undo":
		return KindUndo
	case "restore":
		return KindRestore
	case "cost":
		return KindCost
	case "status":
		return KindStatus
	case "cancel":
		return KindCancel
	case "stop":
		return KindStop
	case "quit":
		return KindQuit
	case "exit":
		return KindExit
	case "plugin", "plugins":
		return KindPlugin
	case "skill", "skills":
		return KindSkill
	case "lane", "lanes":
		return KindLane
	case "auth", "login", "logout":
		return KindAuth
	case "review":
		return KindReview
	case "backtrack":
		return KindBacktrack
	case "search", "history":
		return KindSearch
	case "plan":
		return KindPlan
	case "mode":
		return KindMode
	case "granular":
		return KindGranular
	case "usage":
		return KindUsage
	case "context":
		return KindContext
	case "permissions":
		return KindPermissions
	case "thread", "threads":
		return KindThread
	case "resume":
		return KindResume
	case "continue":
		return KindContinue
	case "agent":
		return KindAgent
	case "constitution":
		return KindConstitution
	case "task", "tasks":
		return KindTask
	case "automation", "automations":
		return KindAutomation
	case "rlm":
		return KindRLM
	case "relay":
		return KindRelay
	case "jobs":
		return KindJobs
	case "attach":
		return KindAttach
	default:
		return KindUnknown
	}
}

// StubKinds names CLI capabilities that TUI help may mention but does not register.
func StubKinds() []Kind {
	return []Kind{
		KindSandbox, KindDoctor, KindMemory, KindInit, KindApply,
	}
}

// NoopKinds names legacy placeholders that the TUI no longer registers.
func NoopKinds() []Kind {
	return []Kind{KindRedo, KindCopy}
}

// HelpText returns operable TUI slash commands plus a CLI-only footnote.
func HelpText() string {
	main := strings.Join([]string{
		"/help /status /clear /compact /diff /undo /restore",
		"/model /provider /session /thread /resume",
		"/new /load /save /fork /export",
		"/mcp /fleet /workflow /lane /plugin /skill",
		"/agent /task /jobs (panels: child agents, durable tasks, background jobs)",
		"/settings /hotbar /auth /mode /granular /automation /rlm /relay /attach /permissions /constitution /quit",
		"/context",
	}, " | ")
	cliOnly := "CLI-only:"
	for _, kind := range StubKinds() {
		cliOnly += " /" + string(kind)
	}
	return main + " | " + cliOnly
}
