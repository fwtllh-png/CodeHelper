package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Package-level styles are filled by applyTheme (see theme.go).
var (
	styleBrand        lipgloss.Style
	styleMuted        lipgloss.Style
	styleUser         lipgloss.Style
	styleThink        lipgloss.Style
	styleAsst         lipgloss.Style
	styleOK           lipgloss.Style
	styleWarn         lipgloss.Style
	styleErr          lipgloss.Style
	styleTool         lipgloss.Style
	styleToolLive     lipgloss.Style
	styleToolDone     lipgloss.Style
	styleHeader       lipgloss.Style
	styleChip         lipgloss.Style
	stylePhaseWorking lipgloss.Style
	stylePhaseWait    lipgloss.Style
	stylePhaseFail    lipgloss.Style
	stylePhaseDone    lipgloss.Style
	styleOverlay      lipgloss.Style
	styleSystem       lipgloss.Style
	styleDiff         lipgloss.Style
	styleSidebar      lipgloss.Style
)

func welcomeLines(provider, modelID, workspace string) []string {
	if workspace == "" {
		workspace = "."
	}
	return []string{
		styleBrand.Render("codehelper"),
		styleMuted.Render("终端优先的 AI 编程助手 · 对话 · 工具 · 审批 · 沙箱"),
		"",
		styleMuted.Render(fmt.Sprintf("model  %s / %s", provider, modelID)),
		styleMuted.Render(fmt.Sprintf("workspace  %s", workspace)),
		"",
		styleMuted.Render("输入提问开始 · /help · PgUp 滚动 · Ctrl+T 全文 · v 展开工具 · Esc 退出面板"),
		strings.Repeat("─", 48),
	}
}

func styleTranscriptLine(line string) string {
	switch {
	case strings.HasPrefix(line, "you: "):
		return styleUser.Render("you") + styleAsst.Render(line[len("you"):])
	case strings.HasPrefix(line, "thinking: "):
		return styleThink.Render(line)
	case strings.HasPrefix(line, "assistant: "):
		return styleAsst.Render(line)
	case strings.HasPrefix(line, "— turn.completed"):
		return styleOK.Render(line)
	case strings.HasPrefix(line, "turn.failed"), strings.HasPrefix(line, "error:"), strings.HasPrefix(line, "rejected:"):
		return styleErr.Render(line)
	case strings.HasPrefix(line, "[tool:"), strings.HasPrefix(line, "✓ "), strings.HasPrefix(line, "▷ "),
		strings.HasPrefix(line, "◉ "), strings.HasPrefix(line, "⌕ "), strings.HasPrefix(line, "• "):
		return styleTool.Render(line)
	case strings.HasPrefix(line, "codehelper"):
		return line
	default:
		if strings.Contains(line, "error") || strings.Contains(line, "failed") {
			return styleWarn.Render(line)
		}
		return styleSystem.Render(line)
	}
}
