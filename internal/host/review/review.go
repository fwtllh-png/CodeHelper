// Package review builds reproducible review prompts from scoped targets (N15).
package review

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

// TargetKind selects the review scope.
type TargetKind string

const (
	KindUncommitted TargetKind = "uncommitted"
	KindBaseBranch  TargetKind = "base_branch"
	KindCommit      TargetKind = "commit"
	KindCustom      TargetKind = "custom"
)

// Target is a reproducible review scope.
type Target struct {
	Kind  TargetKind
	Ref   string // base branch, commit sha, or custom instruction
	Extra string // optional free-text focus
}

// ParseArgs maps slash/CLI args onto a Target.
//
//	/review                  → uncommitted
//	/review base main        → base_branch main
//	/review commit abc123    → commit abc123
//	/review custom "focus X" → custom
func ParseArgs(args []string) Target {
	if len(args) == 0 {
		return Target{Kind: KindUncommitted}
	}
	switch strings.ToLower(args[0]) {
	case "uncommitted", "dirty", "working":
		return Target{Kind: KindUncommitted, Extra: strings.Join(args[1:], " ")}
	case "base", "base_branch", "branch":
		ref := "main"
		if len(args) > 1 {
			ref = args[1]
		}
		return Target{Kind: KindBaseBranch, Ref: ref, Extra: strings.Join(args[2:], " ")}
	case "commit", "sha":
		ref := ""
		if len(args) > 1 {
			ref = args[1]
		}
		return Target{Kind: KindCommit, Ref: ref, Extra: strings.Join(args[2:], " ")}
	case "custom":
		return Target{Kind: KindCustom, Ref: strings.Join(args[1:], " ")}
	default:
		return Target{Kind: KindCustom, Ref: strings.Join(args, " ")}
	}
}

// BuildPrompt gathers git context for target and returns a review prompt.
func BuildPrompt(workspace string, target Target) (string, error) {
	if workspace == "" {
		workspace = "."
	}
	var scope, diff string
	var err error
	switch target.Kind {
	case KindUncommitted:
		scope = "uncommitted working tree changes"
		diff, err = gitOutput(workspace, "diff", "HEAD")
		if err != nil {
			diff, _ = gitOutput(workspace, "diff")
		}
		status, _ := gitOutput(workspace, "status", "--short")
		if status != "" {
			diff = "STATUS:\n" + status + "\n\nDIFF:\n" + diff
		}
	case KindBaseBranch:
		ref := target.Ref
		if ref == "" {
			ref = "main"
		}
		scope = "changes versus base branch " + ref
		diff, err = gitOutput(workspace, "diff", ref+"...HEAD")
	case KindCommit:
		if target.Ref == "" {
			return "", fmt.Errorf("review commit requires a sha")
		}
		scope = "commit " + target.Ref
		diff, err = gitOutput(workspace, "show", "--stat", "--patch", target.Ref)
	case KindCustom:
		scope = "custom review request"
		diff = target.Ref
		if target.Extra != "" {
			diff = strings.TrimSpace(diff + "\n" + target.Extra)
		}
	default:
		return "", fmt.Errorf("unknown review target %q", target.Kind)
	}
	if err != nil && target.Kind != KindCustom {
		diff = "(git unavailable: " + err.Error() + ")"
	}
	if strings.TrimSpace(diff) == "" {
		diff = "(no changes detected)"
	}
	var b strings.Builder
	b.WriteString("You are performing a code review.\n")
	b.WriteString("Scope: ")
	b.WriteString(scope)
	b.WriteString("\n")
	if extra := strings.TrimSpace(target.Extra); extra != "" && target.Kind != KindCustom {
		b.WriteString("Focus: ")
		b.WriteString(extra)
		b.WriteString("\n")
	}
	b.WriteString("\nProduce structured findings: severity (blocker/major/minor/nit), file path,")
	b.WriteString(" brief title, and concrete remediation. Prefer actionable issues over praise.\n\n")
	b.WriteString("```\n")
	b.WriteString(truncate(diff, 48<<10))
	b.WriteString("\n```\n")
	return b.String(), nil
}

func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("%s", msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "\n…(truncated)"
}
