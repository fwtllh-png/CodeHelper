package tui

import (
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/glamour/styles"
	"github.com/muesli/termenv"
)

// prepareMarkdown makes model markdown TUI-friendly before glamour:
// flatten GFM tables (CJK + wrap breaks box tables) and soften blockquotes.
func prepareMarkdown(src string) string {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = flattenMarkdownTables(src)
	src = softenBlockquotes(src)
	src = collapseBlankLines(src)
	return src
}

func softenBlockquotes(src string) string {
	lines := strings.Split(src, "\n")
	for i, line := range lines {
		trim := strings.TrimLeft(line, " \t")
		if strings.HasPrefix(trim, "> ") {
			indent := line[:len(line)-len(trim)]
			lines[i] = indent + trim[2:]
		} else if trim == ">" {
			lines[i] = ""
		}
	}
	return strings.Join(lines, "\n")
}

// flattenMarkdownTables converts pipe tables into compact bullet rows so long
// CJK / path cells do not shatter across soft-wrapped │ columns.
func flattenMarkdownTables(src string) string {
	lines := strings.Split(src, "\n")
	var out []string
	i := 0
	for i < len(lines) {
		if isTableSeparator(lines, i) {
			i++
			continue
		}
		if !isTableRow(lines[i]) {
			out = append(out, lines[i])
			i++
			continue
		}
		if i+1 >= len(lines) || !isTableSeparator(lines, i+1) {
			out = append(out, lines[i])
			i++
			continue
		}
		headers := splitTableRow(lines[i])
		i += 2
		for i < len(lines) && isTableRow(lines[i]) && !isTableSeparator(lines, i) {
			cells := splitTableRow(lines[i])
			var parts []string
			for c, cell := range cells {
				cell = strings.TrimSpace(cell)
				if cell == "" {
					continue
				}
				label := ""
				if c < len(headers) {
					label = strings.TrimSpace(headers[c])
				}
				if label != "" {
					parts = append(parts, "**"+label+"**: "+cell)
				} else {
					parts = append(parts, cell)
				}
			}
			if len(parts) > 0 {
				out = append(out, "- "+strings.Join(parts, " · "))
			}
			i++
		}
	}
	return strings.Join(out, "\n")
}

func isTableRow(line string) bool {
	trim := strings.TrimSpace(line)
	if !strings.Contains(trim, "|") {
		return false
	}
	return strings.Count(trim, "|") >= 1 && (strings.HasPrefix(trim, "|") || strings.Contains(trim, " | "))
}

func isTableSeparator(lines []string, i int) bool {
	if i < 0 || i >= len(lines) {
		return false
	}
	trim := strings.TrimSpace(lines[i])
	if trim == "" || !strings.Contains(trim, "-") {
		return false
	}
	for _, r := range trim {
		switch r {
		case '|', '-', ':', ' ', '\t':
		default:
			return false
		}
	}
	return strings.Contains(trim, "-")
}

func splitTableRow(line string) []string {
	trim := strings.TrimSpace(line)
	trim = strings.TrimPrefix(trim, "|")
	trim = strings.TrimSuffix(trim, "|")
	parts := strings.Split(trim, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		out = append(out, strings.TrimSpace(p))
	}
	return out
}

func tuiGlamourStyle() ansi.StyleConfig {
	style := styles.DarkStyleConfig
	empty := ""
	zero := uint(0)
	two := uint(2)

	// Drop raw ### prefixes; color alone carries heading weight.
	style.H1.BlockPrefix = empty
	style.H2.BlockPrefix = empty
	style.H3.BlockPrefix = empty
	style.H4.BlockPrefix = empty
	style.H5.BlockPrefix = empty
	style.H6.BlockPrefix = empty
	style.H1.Prefix = empty
	style.H2.Prefix = empty
	style.H3.Prefix = empty
	style.H4.Prefix = empty
	style.H5.Prefix = empty
	style.H6.Prefix = empty
	// Compact H1: no full-bleed background chip (burns vertical space).
	style.H1.BackgroundColor = nil
	style.H1.Suffix = empty
	style.H1.Color = strPtr("228")
	style.Heading.BlockSuffix = empty
	style.Heading.BlockPrefix = empty

	style.Document.Margin = &zero
	style.Document.BlockPrefix = empty
	style.Document.BlockSuffix = empty

	style.Paragraph.BlockSuffix = empty
	style.Paragraph.BlockPrefix = empty

	style.BlockQuote.Indent = &zero
	style.BlockQuote.Prefix = empty
	style.BlockQuote.Suffix = empty
	style.BlockQuote.IndentToken = &empty
	style.BlockQuote.BlockSuffix = empty

	style.List.LevelIndent = two
	style.Item.BlockPrefix = "• "

	style.CodeBlock.Margin = &zero
	style.CodeBlock.BlockPrefix = empty
	style.CodeBlock.BlockSuffix = empty
	style.Code.Prefix = empty
	style.Code.Suffix = empty

	style.HorizontalRule.Format = "────────"
	style.ImageText.Format = "{{.text}}"
	style.Enumeration.BlockPrefix = ". "
	return style
}

func strPtr(s string) *string { return &s }

func newGlamourRenderer(width int) (*glamour.TermRenderer, error) {
	if width < 24 {
		width = 24
	}
	// Use nearly full width; stripTrailingPad avoids mid-row soft-wrap from pad.
	wrap := width - 1
	if wrap < 20 {
		wrap = 20
	}
	return glamour.NewTermRenderer(
		glamour.WithStyles(tuiGlamourStyle()),
		glamour.WithWordWrap(wrap),
		glamour.WithColorProfile(termenv.TrueColor),
	)
}

// stripTrailingPad removes glamour's per-line space padding so a slightly
// narrower terminal does not soft-wrap mid-row.
func stripTrailingPad(s string) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = strings.TrimRight(line, " ")
	}
	return strings.Join(lines, "\n")
}

// collapseBlankLines keeps at most one empty line between blocks.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blank := false
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			if blank {
				continue
			}
			blank = true
			out = append(out, "")
			continue
		}
		blank = false
		out = append(out, line)
	}
	return strings.TrimRight(strings.Join(out, "\n"), "\n")
}
