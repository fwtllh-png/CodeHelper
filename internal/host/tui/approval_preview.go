package tui

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
)

type approvalKind string

const (
	approvalKindExec  approvalKind = "exec"
	approvalKindPatch approvalKind = "patch"
	approvalKindOther approvalKind = "other"
)

const (
	approvalPreviewMaxLines = 40
	approvalPreviewMaxBytes = 4096
	approvalOtherMaxLines   = 20
)

func classifyApproval(tool string) approvalKind {
	f := classifyTool(tool)
	switch f {
	case FamilyRun:
		return approvalKindExec
	case FamilyPatch:
		return approvalKindPatch
	default:
		n := strings.ToLower(tool)
		if strings.Contains(n, "shell") || strings.Contains(n, "exec") || n == "bash" {
			return approvalKindExec
		}
		if strings.Contains(n, "patch") || strings.Contains(n, "edit") || strings.Contains(n, "write") {
			return approvalKindPatch
		}
		return approvalKindOther
	}
}

func buildApprovalPreview(tool string, args json.RawMessage) (kind approvalKind, preview string) {
	kind = classifyApproval(tool)
	// A transaction carries whole file bodies in its arguments; summarise the
	// operations instead of dumping them at the reviewer.
	if summary, ok := previewChanges(args); ok {
		return approvalKindPatch, summary
	}
	switch kind {
	case approvalKindExec:
		return kind, previewExec(args)
	case approvalKindPatch:
		return kind, previewPatch(args)
	default:
		return kind, previewOtherJSON(args)
	}
}

func previewExec(args json.RawMessage) string {
	cmd := argString(args, "command", "cmd", "script")
	if cmd == "" {
		cmd = strings.TrimSpace(string(args))
	}
	if cmd == "" {
		return styleMuted.Render("(empty command)")
	}
	return styleDiff.Render("$") + " " + highlightBash(cmd)
}

func highlightBash(cmd string) string {
	// Light keyword tint; keep most of the command in tool color.
	keywords := []string{"sudo", "rm", "curl", "wget", "chmod", "chown", "kill", "dd"}
	lower := strings.ToLower(cmd)
	for _, kw := range keywords {
		if idx := indexWord(lower, kw); idx >= 0 {
			end := idx + len(kw)
			return styleTool.Render(cmd[:idx]) + styleErr.Render(cmd[idx:end]) + styleTool.Render(cmd[end:])
		}
	}
	return styleTool.Render(cmd)
}

func indexWord(s, word string) int {
	idx := strings.Index(s, word)
	for idx >= 0 {
		beforeOK := idx == 0 || !isBashWordChar(s[idx-1])
		after := idx + len(word)
		afterOK := after >= len(s) || !isBashWordChar(s[after])
		if beforeOK && afterOK {
			return idx
		}
		next := strings.Index(s[idx+1:], word)
		if next < 0 {
			return -1
		}
		idx = idx + 1 + next
	}
	return -1
}

func isBashWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= '0' && b <= '9') || b == '_' || b == '-'
}

// previewChanges renders one line per operation of a file_apply transaction. It
// reports false for arguments that carry no changes array, so other tools fall
// through to their own preview.
func previewChanges(args json.RawMessage) (string, bool) {
	if len(args) == 0 {
		return "", false
	}
	var payload struct {
		Changes []struct {
			Op      string `json:"op"`
			Path    string `json:"path"`
			Content string `json:"content"`
			Old     string `json:"old"`
			New     string `json:"new"`
			To      string `json:"to"`
		} `json:"changes"`
		DryRun bool `json:"dry_run"`
	}
	if err := json.Unmarshal(args, &payload); err != nil || len(payload.Changes) == 0 {
		return "", false
	}
	var b strings.Builder
	if payload.DryRun {
		b.WriteString(styleMuted.Render("dry run: nothing is written"))
		b.WriteByte('\n')
	}
	for index, change := range payload.Changes {
		if index >= approvalOtherMaxLines {
			b.WriteString(styleMuted.Render("…"))
			break
		}
		if index > 0 {
			b.WriteByte('\n')
		}
		switch change.Op {
		case "write":
			b.WriteString(styleOK.Render("write ") + styleTool.Render(change.Path))
			b.WriteString(styleMuted.Render(" " + lineCount(change.Content)))
		case "edit":
			b.WriteString(styleDiff.Render("edit ") + styleTool.Render(change.Path))
			b.WriteString(styleMuted.Render(
				" " + lineCount(change.Old) + " → " + lineCount(change.New),
			))
		case "move":
			b.WriteString(styleDiff.Render("move ") + styleTool.Render(change.Path))
			b.WriteString(styleMuted.Render(" → ") + styleTool.Render(change.To))
		case "delete":
			b.WriteString(styleErr.Render("delete ") + styleTool.Render(change.Path))
		default:
			b.WriteString(styleMuted.Render(change.Op+" ") + styleTool.Render(change.Path))
		}
	}
	return b.String(), true
}

func lineCount(text string) string {
	if text == "" {
		return "0 lines"
	}
	count := strings.Count(text, "\n")
	if !strings.HasSuffix(text, "\n") {
		count++
	}
	if count == 1 {
		return "1 line"
	}
	return strconv.Itoa(count) + " lines"
}

func previewPatch(args json.RawMessage) string {
	diff := argString(args, "diff", "patch", "hunks", "content", "input")
	if diff == "" {
		// Common nested shapes: {"path":"...","content":"..."} already tried; fallback raw.
		diff = strings.TrimSpace(string(args))
	}
	if diff == "" {
		return styleMuted.Render("(empty patch)")
	}
	return colorDiffLines(diff)
}

func colorDiffLines(diff string) string {
	if len(diff) > approvalPreviewMaxBytes {
		diff = diff[:approvalPreviewMaxBytes] + "\n…"
	}
	lines := strings.Split(diff, "\n")
	if len(lines) > approvalPreviewMaxLines {
		lines = append(lines[:approvalPreviewMaxLines], "…")
	}
	var b strings.Builder
	for i, line := range lines {
		if i > 0 {
			b.WriteByte('\n')
		}
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			b.WriteString(styleMuted.Render(line))
		case strings.HasPrefix(line, "@@"):
			b.WriteString(styleInfoOrTool(line))
		case strings.HasPrefix(line, "+"):
			b.WriteString(styleOK.Render(line))
		case strings.HasPrefix(line, "-"):
			b.WriteString(styleErr.Render(line))
		default:
			b.WriteString(styleDiff.Render(line))
		}
	}
	return b.String()
}

func styleInfoOrTool(s string) string {
	return styleTool.Render(s)
}

func previewOtherJSON(args json.RawMessage) string {
	if len(args) == 0 {
		return styleMuted.Render("(no arguments)")
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, args, "", "  "); err != nil {
		raw := string(args)
		if len(raw) > approvalPreviewMaxBytes {
			raw = raw[:approvalPreviewMaxBytes] + "…"
		}
		return styleMuted.Render(raw)
	}
	text := buf.String()
	lines := strings.Split(text, "\n")
	if len(lines) > approvalOtherMaxLines {
		lines = append(lines[:approvalOtherMaxLines], "…")
	}
	joined := strings.Join(lines, "\n")
	if len(joined) > approvalPreviewMaxBytes {
		joined = joined[:approvalPreviewMaxBytes] + "…"
	}
	return styleMuted.Render(joined)
}

func argString(args json.RawMessage, keys ...string) string {
	if len(args) == 0 {
		return ""
	}
	var m map[string]any
	if err := json.Unmarshal(args, &m); err != nil {
		return ""
	}
	for _, key := range keys {
		if v, ok := m[key]; ok {
			switch t := v.(type) {
			case string:
				if strings.TrimSpace(t) != "" {
					return t
				}
			default:
				b, err := json.Marshal(t)
				if err == nil {
					return string(b)
				}
			}
		}
	}
	return ""
}
