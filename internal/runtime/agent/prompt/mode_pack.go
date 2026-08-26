package prompt

import "strings"

const interactionInstructions = `
Resolve facts available through tools before asking the user.
When progress truly depends on a user answer, call request_user_input and wait
for the reply in the same Turn. Include options for a finite choice. Never ask
for required input in ordinary assistant text. Ordinary assistant text is
provisional in a tool-enabled Turn and cannot replace the
structured request_user_input or turn_complete terminal state.`

// ModeInstructionPack returns the developer-facing CollaborationMode pack
// injected into PartitionMode (W5.2). Switching mode changes this text for the
// next Assemble / turn.
func ModeInstructionPack(mode string, imageInput ...bool) string {
	var instructions string
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "plan":
		instructions = `Mode: plan
You are in Plan mode. Investigate first, then call submit_plan with a structured implementation plan.
Do not edit files, run mutating shell commands, or call write/network tools.
Use shell_read for inspection pipelines; its workspace is mechanically read-only and network-isolated.
Ask clarifying questions when requirements are ambiguous.`
	case "operate":
		instructions = `Mode: operate
You are in Operate mode. Prefer careful execution: investigate, then act with clear receipts.
Use shell_read instead of exec_command whenever a command only inspects local data.
Process tools may run under auto posture; still request approval for network and plugins.
Keep the user informed of irreversible side effects before applying them.`
	case "act":
		instructions = `Mode: act
You are in Act mode. Implement the requested change with tools when appropriate.
Use shell_read instead of exec_command whenever a command only inspects local data.
High-risk capabilities (process/network/plugin) still follow the active permission posture.`
	default:
		if mode == "" {
			return ""
		}
		instructions = "Mode: " + mode
	}
	if len(imageInput) != 0 && imageInput[0] {
		instructions += `
This Session accepts image attachments. You can inspect and reason about images supplied by the user.`
	}
	return strings.TrimSpace(instructions + interactionInstructions)
}
