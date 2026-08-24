package tui

import tea "github.com/charmbracelet/bubbletea"

func StreamOutputMessage(text string) tea.Msg {
	return streamMsg{kind: streamKindOutput, text: text}
}

func StreamReasoningMessage(text string) tea.Msg {
	return streamMsg{kind: streamKindReasoning, text: text}
}

func StreamDoneMessage() tea.Msg {
	return streamDoneMsg{}
}

func StreamApprovalMessage(id, text string) tea.Msg {
	return streamMsg{text: text, approvalID: id}
}

func StreamInputMessage(id, text string, options ...string) tea.Msg {
	return streamMsg{
		text: text, inputID: id,
		inputOptions: append([]string(nil), options...),
	}
}

func StreamToolMessage(id, name, detail string) tea.Msg {
	return streamMsg{toolID: id, tool: name, text: detail}
}
