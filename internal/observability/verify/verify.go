// Package verify runs post-change verification for a workspace and reports a
// receipt the turn gate and the quality tools can both consume.
package verify

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// Scope selects what a verification pass looks at.
type Scope string

const (
	// ScopeDiagnostics reuses the post-edit diagnostics already collected for
	// the files a turn touched, so it costs no extra process.
	ScopeDiagnostics Scope = "diagnostics"
	// ScopeRepository runs the repository's own verification commands.
	ScopeRepository Scope = "repository"
	// ScopeAffected runs only the tests that cover the files a turn changed. It
	// needs a mapping from source paths to tests, and reports itself unavailable
	// for the languages that mapping does not know rather than passing silently.
	ScopeAffected Scope = "affected"
	// ScopeQuality is a model-invoked quality command whose exact covered paths
	// the engine bound to the current workspace mutation revision.
	ScopeQuality Scope = "quality"
)

const (
	StatusPassed       = "passed"
	StatusFailed       = "failed"
	StatusUnavailable  = "unavailable"
	StatusNotEvaluated = "not_evaluated"

	ErrorCategoryDependencyUnavailable = "dependency_unavailable"
)

// Check is one verification command and, once run, its outcome.
type Check struct {
	Name     string `json:"name"`
	Command  string `json:"command"`
	Reason   string `json:"reason"`
	Category string `json:"category,omitempty"`
	Status   string `json:"status,omitempty"`
	ExitCode int    `json:"exit_code,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
}

// Receipt is the verdict of one verification pass.
type Receipt struct {
	Scope    Scope   `json:"scope"`
	Status   string  `json:"status"`
	Checks   []Check `json:"checks,omitempty"`
	Errors   int     `json:"errors,omitempty"`
	Warnings int     `json:"warnings,omitempty"`
	Message  string  `json:"message,omitempty"`
}

// Failed reports whether the pass produced a verdict the gate must act on.
// Unavailable is not a failure: a workspace without verification commands must
// not block every turn.
func (r Receipt) Failed() bool { return r.Status == StatusFailed }

// Feedback renders the receipt for the model, bounded so a noisy test suite
// cannot flood the context.
func (r Receipt) Feedback(limit int) string {
	var builder strings.Builder
	fmt.Fprintf(&builder, "verification (%s) failed", r.Scope)
	if r.Message != "" {
		fmt.Fprintf(&builder, ": %s", r.Message)
	}
	builder.WriteByte('\n')
	for _, check := range r.Checks {
		if check.Status == StatusPassed || check.Status == StatusUnavailable {
			continue
		}
		fmt.Fprintf(&builder, "$ %s (exit %d)\n", check.Command, check.ExitCode)
		if output := strings.TrimSpace(check.Stdout + "\n" + check.Stderr); output != "" {
			builder.WriteString(output)
			builder.WriteByte('\n')
		}
	}
	text := builder.String()
	if limit > 0 && len(text) > limit {
		return text[:limit] + "\n[verification output truncated]"
	}
	return text
}

// Request describes one verification pass. Diagnostics carries the post-edit
// receipts the turn already collected, so ScopeDiagnostics needs no new process.
type Request struct {
	Scope       Scope
	Paths       []string
	Diagnostics []diagnostics.Receipt
}

// Runner performs one verification pass.
type Runner interface {
	Verify(context.Context, Request) (Receipt, error)
}

// Command is a verification command discovered for a workspace.
type Command struct {
	Name    string
	Command string
	Reason  string
}

// Detect returns the verification commands for root, inferred from the build
// files present. The fallback keeps a single well-known entry point so an
// unrecognised workspace still reports something actionable.
func Detect(root string) []Command {
	var commands []Command
	if exists(filepath.Join(root, "go.mod")) {
		commands = append(commands, Command{
			Name: "go", Command: "go test ./...",
			Reason: "go.mod declares a Go module; run its repository test graph",
		})
	}
	if exists(filepath.Join(root, "Cargo.toml")) {
		commands = append(commands, Command{
			Name: "rust", Command: "cargo test --workspace",
			Reason: "Cargo.toml declares a Rust workspace or crate",
		})
	}
	if exists(filepath.Join(root, "package.json")) {
		commands = append(commands, Command{
			Name: "node", Command: nodeCommand(root),
			Reason: "package.json declares a JavaScript/TypeScript test script",
		})
	}
	if exists(filepath.Join(root, "pyproject.toml")) ||
		exists(filepath.Join(root, "setup.cfg")) ||
		exists(filepath.Join(root, "pytest.ini")) ||
		exists(filepath.Join(root, "requirements.txt")) {
		commands = append(commands, Command{
			Name: "python", Command: "python3 -m pytest",
			Reason: "Python project metadata declares a pytest-compatible repository",
		})
	}
	if len(commands) == 0 {
		commands = append(commands, Command{
			Name: "workspace", Command: "make verify",
			Reason: "no supported language manifest was found; use the repository verification entry point",
		})
	}
	return commands
}

func nodeCommand(root string) string {
	switch {
	case exists(filepath.Join(root, "pnpm-lock.yaml")):
		return "pnpm test"
	case exists(filepath.Join(root, "yarn.lock")):
		return "yarn test"
	default:
		return "npm test"
	}
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestMapper reports which test files cover the given workspace-relative source
// paths. A source path absent from the result has no convention the mapper
// knows, which is different from a path that maps to no tests.
type TestMapper interface {
	RelatedTests(context.Context, []string) (map[string][]string, error)
}

// CommandRunner verifies a workspace by running shell commands under the
// sandbox, or by summarising post-edit diagnostics when the scope asks for it.
type CommandRunner struct {
	Root     string
	Sandbox  sandbox.Backend
	Commands []Command
	// Tests maps changed paths to the tests that cover them. ScopeAffected needs
	// it; the other scopes ignore it.
	Tests TestMapper
	// Run is a seam for tests; nil means run the real process.
	Run func(context.Context, process.Options) (process.Result, error)
}

func (r *CommandRunner) Verify(ctx context.Context, request Request) (Receipt, error) {
	if r == nil {
		return Receipt{
			Scope: request.Scope, Status: StatusUnavailable, Message: "no verify runner",
		}, nil
	}
	switch request.Scope {
	case ScopeDiagnostics:
		return FromDiagnostics(request.Diagnostics, request.Paths), nil
	case ScopeRepository:
		commands := r.Commands
		if len(commands) == 0 {
			commands = Detect(r.Root)
		}
		return r.runCommands(ctx, ScopeRepository, commands)
	case ScopeAffected:
		return r.runAffected(ctx, request)
	default:
		return Receipt{}, fmt.Errorf("unknown verify scope %q", request.Scope)
	}
}

// runAffected verifies only what the change can reach. It refuses to answer for
// paths it cannot map instead of reporting a pass no test backed: an empty run
// that reads as green is worse than an honest "unavailable".
func (r *CommandRunner) runAffected(ctx context.Context, request Request) (Receipt, error) {
	paths := relativePaths(r.Root, request.Paths)
	if len(paths) == 0 {
		return Receipt{
			Scope: ScopeAffected, Status: StatusUnavailable,
			Message: "no changed paths to map to tests",
		}, nil
	}
	// A configured command overrides the mapping: the operator knows their suite,
	// and the placeholders let them narrow it to the change.
	if len(r.Commands) != 0 {
		return r.runCommands(ctx, ScopeAffected, expandCommands(r.Commands, paths))
	}
	related := map[string][]string{}
	if r.Tests != nil {
		found, err := r.Tests.RelatedTests(ctx, paths)
		if err != nil {
			return Receipt{
				Scope: ScopeAffected, Status: StatusUnavailable,
				Message: "the test mapping is unavailable: " + err.Error(),
			}, nil
		}
		related = found
	}
	commands, unmapped := AffectedCommands(r.Root, paths, related)
	if len(commands) == 0 {
		return Receipt{
			Scope: ScopeAffected, Status: StatusUnavailable,
			Message: "no affected tests could be derived for " + strings.Join(unmapped, ", ") +
				"; set execution.verify.command with {paths} or {packages}, or use the repository scope",
		}, nil
	}
	receipt, err := r.runCommands(ctx, ScopeAffected, commands)
	if err != nil {
		return Receipt{}, err
	}
	if len(unmapped) != 0 {
		receipt.Message = "no affected tests known for " + strings.Join(unmapped, ", ")
	}
	return receipt, nil
}

func (r *CommandRunner) runCommands(
	ctx context.Context, scope Scope, commands []Command,
) (Receipt, error) {
	receipt := Receipt{Scope: scope, Status: StatusPassed}
	for _, command := range commands {
		result, err := r.runProcess(ctx, command.Command)
		if err != nil {
			return Receipt{}, fmt.Errorf("verify %s: %w", command.Name, err)
		}
		status, reason := CommandResultStatus(command.Command, result)
		derivation := command.Reason
		if derivation == "" {
			derivation = "the verification command was explicitly configured by the repository or operator"
		}
		check := Check{
			Name: command.Name, Command: command.Command, Reason: derivation, Status: status,
			ExitCode: result.ExitCode, Stdout: result.Stdout, Stderr: result.Stderr,
		}
		switch status {
		case StatusFailed:
			check.Category = "test_failure"
			receipt.Status = StatusFailed
			receipt.Errors++
		case StatusUnavailable:
			check.Category = ErrorCategoryDependencyUnavailable
			if receipt.Status == StatusPassed {
				receipt.Status = StatusUnavailable
			}
			if receipt.Message == "" {
				receipt.Message = reason
			}
		}
		receipt.Checks = append(receipt.Checks, check)
	}
	return receipt, nil
}

func CommandResultStatus(_ string, result process.Result) (status, reason string) {
	if result.ExitCode == 0 {
		return StatusPassed, ""
	}
	return StatusFailed, ""
}

func (r *CommandRunner) runProcess(ctx context.Context, command string) (process.Result, error) {
	// The sandbox policy stores the workspace root with symlinks resolved, so
	// running from the caller's spelling of the path (a macOS /var temp
	// directory, say) is rejected as outside the workspace.
	workspace, err := sandbox.NewWorkspace(r.Root)
	if err != nil {
		return process.Result{}, err
	}
	root := workspace.Root()
	options := process.Options{
		Command: command, Dir: root, Sandbox: r.Sandbox,
		RequireSandbox: true, WorkspaceReadOnly: true,
	}
	if r.Run != nil {
		return r.Run(ctx, options)
	}
	directory, err := process.OpenPinnedDirectory(r.Sandbox, root)
	if err != nil {
		return process.Result{}, err
	}
	defer directory.Close()
	options.DirFile = directory
	return process.Run(ctx, options)
}

// FromDiagnostics turns the post-edit diagnostics of a turn into a verdict.
// Only error-severity diagnostics fail the pass; warnings are reported so the
// receipt still shows why a pass was noisy. When no receipt covers the changed
// paths the pass is unavailable rather than passed, so a missing diagnostics
// runner never reads as a green light.
func FromDiagnostics(receipts []diagnostics.Receipt, paths []string) Receipt {
	receipt := Receipt{Scope: ScopeDiagnostics, Status: StatusPassed}
	evaluated := 0
	for _, item := range receipts {
		if len(paths) > 0 && !containsPath(paths, item.Path) {
			continue
		}
		switch item.Status {
		case "unavailable":
			continue
		case "failed":
			receipt.Status = StatusFailed
			receipt.Errors++
			evaluated++
			receipt.Checks = append(receipt.Checks, Check{
				Name: diagnosticsCheckName(item), Command: "diagnostics " + item.Path,
				Reason:   "post-edit diagnostics cover the changed file",
				Category: "diagnostic_failure", Status: StatusFailed, Stderr: item.Message,
			})
			continue
		}
		evaluated++
		var messages []string
		errors := 0
		for _, diagnostic := range item.Diagnostics {
			switch diagnostic.Severity {
			case "error":
				errors++
				receipt.Errors++
				messages = append(messages, formatDiagnostic(item.Path, diagnostic))
			case "warning":
				receipt.Warnings++
			}
		}
		if errors == 0 {
			continue
		}
		receipt.Status = StatusFailed
		receipt.Checks = append(receipt.Checks, Check{
			Name: diagnosticsCheckName(item), Command: "diagnostics " + item.Path,
			Reason:   "post-edit diagnostics cover the changed file",
			Category: "diagnostic_failure",
			Status:   StatusFailed, Stderr: strings.Join(messages, "\n"),
		})
	}
	if evaluated == 0 {
		receipt.Status = StatusUnavailable
		receipt.Message = "no post-edit diagnostics covered the changed files"
	}
	return receipt
}

func diagnosticsCheckName(receipt diagnostics.Receipt) string {
	if receipt.Runner != "" {
		return receipt.Runner
	}
	return "diagnostics"
}

func formatDiagnostic(path string, diagnostic diagnostics.Diagnostic) string {
	location := path
	if diagnostic.Path != "" {
		location = diagnostic.Path
	}
	return fmt.Sprintf(
		"%s:%d:%d: %s", location,
		diagnostic.Range.Start.Line+1, diagnostic.Range.Start.Character+1, diagnostic.Message,
	)
}

func containsPath(paths []string, candidate string) bool {
	for _, path := range paths {
		if samePath(path, candidate) {
			return true
		}
	}
	return false
}

// samePath tolerates one side being absolute: the guard canonicalises the paths
// it hands to diagnostics, while the turn diff records workspace-relative ones.
func samePath(left, right string) bool {
	left, right = filepath.Clean(left), filepath.Clean(right)
	if left == right {
		return true
	}
	separator := string(filepath.Separator)
	return strings.HasSuffix(left, separator+right) ||
		strings.HasSuffix(right, separator+left)
}

// UnavailableRunner stands in when verification is not configured.
type UnavailableRunner struct{ Message string }

func (r UnavailableRunner) Verify(_ context.Context, request Request) (Receipt, error) {
	message := r.Message
	if message == "" {
		message = "verification runner is not configured"
	}
	return Receipt{Scope: request.Scope, Status: StatusUnavailable, Message: message}, nil
}
