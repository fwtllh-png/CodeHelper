package promptcontext

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/memory"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
)

func TestAssembleStableOrderAndWorkspaceBoundaries(t *testing.T) {
	workspace := t.TempDir()
	if err := os.Mkdir(filepath.Join(workspace, ".codehelper"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("root rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(workspace, ".codehelper", "instructions.md"), []byte("local rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(workspace), "AGENTS.md")
	if err := os.WriteFile(outside, []byte("must not load"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(outside) })

	context, err := Assemble(Options{
		BaseSystem: "base", Mode: "act", Workspace: workspace, ToolPrefix: "tools",
	})
	if err != nil {
		t.Fatal(err)
	}
	modePack := ModeInstructionPack("act")
	want := []string{"base", modePack, "root rules", "local rules", "tools"}
	if len(context.Messages) != len(want) {
		t.Fatalf("messages = %+v", context.Messages)
	}
	for index, text := range want {
		if context.Messages[index].Text() != text {
			t.Fatalf("message %d = %q, want %q", index, context.Messages[index].Text(), text)
		}
	}
	if !strings.Contains(modePack, "Act mode") {
		t.Fatalf("mode pack missing Act guidance: %q", modePack)
	}
}

func TestAssembleBudgetsAreDeterministicUTF8SafeAndReceipted(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "AGENTS.md"), []byte("repository-rules"), 0o600); err != nil {
		t.Fatal(err)
	}
	options := Options{
		BaseSystem: "你好世界-base",
		Mode:       "operate",
		Workspace:  workspace,
		ToolPrefix: "tool-prefix",
		Budgets: map[string]Budget{
			PartitionBase:       {MaxBytes: 7},
			PartitionMode:       {MaxTokens: 2},
			PartitionRepository: {MaxBytes: 8},
			PartitionToolPrefix: {MaxTokens: 1},
		},
	}
	first, err := Assemble(options)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Assemble(options)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Receipts) != 4 {
		t.Fatalf("receipts = %+v", first.Receipts)
	}
	for index, receipt := range first.Receipts {
		if receipt.Digest == "" || receipt.OriginalBytes < receipt.RetainedBytes ||
			receipt.OriginalTokens < receipt.RetainedTokens {
			t.Fatalf("receipt %d = %+v", index, receipt)
		}
		if receipt != second.Receipts[index] {
			t.Fatalf("receipt is not deterministic: %+v != %+v", receipt, second.Receipts[index])
		}
	}
	if !first.Receipts[0].Truncated ||
		first.Receipts[0].TruncationReason != "byte_budget" ||
		!first.Receipts[1].Truncated ||
		first.Receipts[1].TruncationReason != "token_budget" {
		t.Fatalf("truncation receipts = %+v", first.Receipts)
	}
	for _, message := range first.Messages {
		if !utf8.ValidString(message.Text()) {
			t.Fatalf("invalid UTF-8 retained message %q", message.Text())
		}
	}
}

func TestAssembleWorkingSetInjectionCanonicalizationAndSymlinkEscape(t *testing.T) {
	workspace := t.TempDir()
	firstPath := filepath.Join(workspace, "a.go")
	secondPath := filepath.Join(workspace, "b.go")
	if err := os.WriteFile(firstPath, []byte("disk-a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secondPath, []byte("disk-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	firstPath, err := filepath.EvalSymlinks(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err = filepath.EvalSymlinks(secondPath)
	if err != nil {
		t.Fatal(err)
	}
	injected := "unsaved-b"
	newFile := "package newfile"
	context, err := Assemble(Options{
		Workspace: workspace,
		WorkingSet: []FileContext{
			{Path: "b.go", Content: &injected, Critical: true},
			{Path: "a.go"},
			{Path: "c.go", Content: &newFile},
		},
		Budgets: map[string]Budget{PartitionWorkingSet: {MaxBytes: 1 << 10}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(context.WorkingSet) != 3 ||
		context.WorkingSet[0] != firstPath ||
		context.WorkingSet[1] != secondPath ||
		!strings.HasSuffix(context.WorkingSet[2], string(filepath.Separator)+"c.go") ||
		len(context.CriticalPaths) != 1 ||
		context.CriticalPaths[0] != secondPath {
		t.Fatalf("working set = %+v critical=%+v", context.WorkingSet, context.CriticalPaths)
	}
	if !strings.Contains(context.Messages[1].Text(), "unsaved-b") ||
		strings.Contains(context.Messages[1].Text(), "disk-b") {
		t.Fatalf("host-injected context was not used: %q", context.Messages[1].Text())
	}
	if !strings.Contains(context.Messages[2].Text(), "package newfile") {
		t.Fatalf("new host-injected file was not used: %q", context.Messages[2].Text())
	}
	for _, receipt := range context.Receipts {
		if receipt.Kind != PartitionWorkingSet {
			continue
		}
		if !filepath.IsAbs(receipt.SourcePath) {
			t.Fatalf("non-canonical receipt path = %q", receipt.SourcePath)
		}
	}

	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(workspace, "escape.go")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := Assemble(Options{
		Workspace: workspace, WorkingSet: []FileContext{{Path: "escape.go"}},
	}); err == nil || !strings.Contains(err.Error(), "escapes workspace") {
		t.Fatalf("symlink escape error = %v", err)
	}
}

func TestAssembleSkillsPartitionHasHardBudgetAndStableOrder(t *testing.T) {
	workspace := t.TempDir()
	skills := make([]skill.Summary, 80)
	for index := range skills {
		skills[index] = skill.Summary{
			Name:        "skill-" + string(rune('a'+index%26)) + strings.Repeat("x", index/26),
			Description: strings.Repeat("description-", 40),
			Source:      skill.SourceWorkspace,
			Path:        filepath.Join(workspace, "skills", string(rune('a'+index%26)), "SKILL.md"),
		}
	}
	context, err := Assemble(Options{
		Workspace: workspace,
		Skills:    skills,
		Budgets: map[string]Budget{
			PartitionSkills: {MaxBytes: MaxSkillsPromptBytes * 2},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var receipt *Receipt
	for index := range context.Receipts {
		if context.Receipts[index].Kind == PartitionSkills {
			receipt = &context.Receipts[index]
			break
		}
	}
	if receipt == nil {
		t.Fatal("skills receipt is missing")
	}
	if receipt.RetainedBytes > MaxSkillsPromptBytes || !receipt.Truncated ||
		receipt.TruncationReason != "byte_budget" {
		t.Fatalf("skills receipt = %+v", *receipt)
	}
	if len(context.Messages) != 1 ||
		!strings.Contains(context.Messages[0].Text(), "Available skills") ||
		!IsContextualFragment(context.Messages[0].Text()) ||
		!utf8.ValidString(context.Messages[0].Text()) {
		t.Fatalf("skills prompt messages = %+v", context.Messages)
	}
}

func TestAssembleUserMemoryInjectsOnlyWhenEnabled(t *testing.T) {
	workspace := t.TempDir()
	store, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append("prefer short diffs"); err != nil {
		t.Fatal(err)
	}
	absent, err := Assemble(Options{Workspace: workspace, Memory: store})
	if err != nil {
		t.Fatal(err)
	}
	for _, receipt := range absent.Receipts {
		if receipt.Kind == PartitionUserMemory {
			t.Fatalf("disabled memory injected: %+v", receipt)
		}
	}
	context, err := Assemble(Options{
		Workspace: workspace, MemoryEnabled: true, Memory: store,
	})
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, message := range context.Messages {
		if strings.Contains(message.Text(), "<user_memory") &&
			strings.Contains(message.Text(), "prefer short diffs") {
			found = true
		}
	}
	if !found {
		t.Fatalf("messages = %+v", context.Messages)
	}
}
