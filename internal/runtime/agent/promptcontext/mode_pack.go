package promptcontext

import "strings"

// ModeInstructionPack returns the developer-facing CollaborationMode pack
// injected into PartitionMode (W5.2). Switching mode changes this text for the
// next Assemble / turn.
func ModeInstructionPack(mode string) string {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan":
		return strings.TrimSpace(`Mode: plan
You are in Plan mode. Propose concrete steps inside <proposed_plan>…</proposed_plan>.
Do not edit files, run mutating shell commands, or call write/network tools.
Use shell_read for inspection pipelines; its workspace is mechanically read-only and network-isolated.
Ask clarifying questions when requirements are ambiguous.`)
	case "operate":
		return strings.TrimSpace(`Mode: operate
You are in Operate mode. Prefer careful execution: investigate, then act with clear receipts.
Use shell_read instead of shell_run whenever a command only inspects local data.
Process tools may run under auto posture; still request approval for network and plugins.
Keep the user informed of irreversible side effects before applying them.`)
	case "act":
		return strings.TrimSpace(`Mode: act
You are in Act mode. Implement the requested change with tools when appropriate.
Use shell_read instead of shell_run whenever a command only inspects local data.
High-risk capabilities (process/network/plugin) still follow the active permission posture.`)
	default:
		if mode == "" {
			return ""
		}
		return "Mode: " + mode
	}
}
