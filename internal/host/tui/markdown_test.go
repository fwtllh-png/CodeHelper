package tui

import (
	"strings"
	"testing"
)

func TestFlattenMarkdownTables(t *testing.T) {
	src := `前言

| 方向 | 目录 |
| --- | --- |
| 安全 | ` + "`internal/security/`" + ` |
| TUI | ` + "`internal/host/tui/`" + ` |

结尾`
	got := flattenMarkdownTables(src)
	if !strings.Contains(got, "**方向**: 安全") {
		t.Fatalf("flattened missing list row: %q", got)
	}
	if !strings.Contains(got, "·") || !strings.Contains(got, "internal/security/") {
		t.Fatalf("compact row missing: %q", got)
	}
	if strings.Contains(got, "| ---") {
		t.Fatalf("separator left behind: %q", got)
	}
}

func TestSoftenBlockquotes(t *testing.T) {
	src := "> 所有 Host 只靠协议\n\n正文"
	got := softenBlockquotes(src)
	if strings.HasPrefix(strings.TrimSpace(strings.Split(got, "\n")[0]), ">") {
		t.Fatalf("blockquote not softened: %q", got)
	}
}

func TestPrepareMarkdownNoHashesRequired(t *testing.T) {
	src := "### 标题\n\n| a | b |\n| --- | --- |\n| 1 | 2 |\n"
	out, err := renderMarkdown(src, 80)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "### ") {
		t.Fatalf("heading prefix should be stripped: %q", out)
	}
	if strings.Contains(out, "┼") || (strings.Contains(out, "│") && strings.Contains(out, "──")) {
		t.Fatalf("table box should be flattened away: %q", out)
	}
	if !strings.Contains(out, "1") || !strings.Contains(out, "2") {
		t.Fatalf("cell content missing: %q", out)
	}
}

func TestCollapseBlankLinesAndDenseStyle(t *testing.T) {
	src := "## 标题\n\n\n\n段落一\n\n\n段落二\n"
	out, err := renderMarkdown(src, 72)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\n\n\n") {
		t.Fatalf("too many blank lines: %q", out)
	}
	if strings.Contains(out, "## ") {
		t.Fatalf("heading prefix leaked: %q", out)
	}
}
