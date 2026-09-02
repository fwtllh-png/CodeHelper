package diagnostics

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/fwtllh-png/QCode/internal/platform/process"
	"github.com/fwtllh-png/QCode/internal/security/sandbox"
)

type Position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type Range struct {
	Start Position `json:"start"`
	End   Position `json:"end"`
}

type Diagnostic struct {
	Path     string `json:"path"`
	Range    Range  `json:"range"`
	Severity string `json:"severity"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message"`
	Source   string `json:"source"`
}

type Receipt struct {
	Path          string       `json:"path"`
	Status        string       `json:"status"`
	Runner        string       `json:"runner,omitempty"`
	Diagnostics   []Diagnostic `json:"diagnostics"`
	Message       string       `json:"message,omitempty"`
	ErrorCategory string       `json:"error_category,omitempty"`
	ExitCode      int          `json:"exit_code,omitempty"`
}

type Runner interface {
	Run(context.Context, string) (Receipt, error)
}

type Command struct {
	Name string
	Args []string
}

type CommandRunner struct {
	Root     string
	Sandbox  sandbox.Backend
	Commands map[string]Command
}

var locationPattern = regexp.MustCompile(
	`^(.+?):(\d+):(\d+)(?::(\d+))?(?::|\s)\s*(.*)$`,
)

func NewCommandRunner(root string, backend sandbox.Backend, configured map[string]Command) *CommandRunner {
	commands := make(map[string]Command, len(configured)+1)
	for extension, command := range configured {
		commands[strings.ToLower(extension)] = command
	}
	if _, exists := commands[".go"]; !exists {
		commands[".go"] = Command{Name: "gopls", Args: []string{"check", "{path}"}}
	}
	return &CommandRunner{Root: root, Sandbox: backend, Commands: commands}
}

func (r *CommandRunner) Run(ctx context.Context, path string) (Receipt, error) {
	extension := strings.ToLower(filepath.Ext(path))
	command, exists := r.Commands[extension]
	if !exists || command.Name == "" {
		return Receipt{
			Path: path, Status: "unavailable", Diagnostics: []Diagnostic{},
			Message: "no post-edit diagnostics command is configured for " + extension,
		}, nil
	}
	binary, err := exec.LookPath(command.Name)
	if err != nil {
		return Receipt{
			Path: path, Status: "unavailable", Runner: command.Name, Diagnostics: []Diagnostic{},
			Message: command.Name + " is not available",
		}, nil
	}
	args := make([]string, len(command.Args))
	for index, value := range command.Args {
		args[index] = strings.ReplaceAll(value, "{path}", path)
	}
	backend, err := sandbox.BindPolicy(r.Sandbox, sandbox.Options{WorkspaceRoot: r.Root})
	if err != nil {
		return Receipt{}, err
	}
	policy, _ := sandbox.BackendPolicy(backend)
	directory, err := process.OpenPinnedDirectory(backend, policy.WorkspaceRoot)
	if err != nil {
		return Receipt{}, err
	}
	defer directory.Close()
	result, runErr := process.Run(ctx, process.Options{
		Path: binary, Args: args, Dir: policy.WorkspaceRoot, DirFile: directory, Sandbox: backend,
		RequireSandbox: true, Env: []string{"OPENSSL_CONF=/dev/null"},
	})
	if runErr != nil {
		return Receipt{}, runErr
	}
	if ctx.Err() != nil {
		return Receipt{}, ctx.Err()
	}
	exitCode := result.ExitCode
	values := parse(result.Stdout+"\n"+result.Stderr, command.Name, path)
	status := "completed"
	message := ""
	if exitCode != 0 && len(values) == 0 {
		status = "unavailable"
		message = strings.TrimSpace(result.Stderr)
		if message == "" {
			message = fmt.Sprintf("%s exited with code %d", command.Name, exitCode)
		}
	}
	return Receipt{
		Path: path, Status: status, Runner: command.Name,
		Diagnostics: values, Message: message,
		ErrorCategory: func() string {
			if status == "unavailable" {
				return "runner_failure"
			}
			return ""
		}(),
		ExitCode: exitCode,
	}, nil
}

func parse(output, source, fallbackPath string) []Diagnostic {
	var result []Diagnostic
	scanner := bufio.NewScanner(strings.NewReader(output))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		match := locationPattern.FindStringSubmatch(line)
		if len(match) == 0 {
			continue
		}
		lineNumber, _ := strconv.Atoi(match[2])
		column, _ := strconv.Atoi(match[3])
		endColumn := column
		if match[4] != "" {
			endColumn, _ = strconv.Atoi(match[4])
		}
		message := strings.TrimSpace(match[5])
		severity := "error"
		lower := strings.ToLower(message)
		if strings.HasPrefix(lower, "warning:") {
			severity = "warning"
			message = strings.TrimSpace(message[len("warning:"):])
		} else if strings.HasPrefix(lower, "info:") {
			severity = "information"
			message = strings.TrimSpace(message[len("info:"):])
		}
		path := match[1]
		if path == "" {
			path = fallbackPath
		}
		result = append(result, Diagnostic{
			Path: path,
			Range: Range{
				Start: Position{Line: max(0, lineNumber-1), Character: max(0, column-1)},
				End:   Position{Line: max(0, lineNumber-1), Character: max(0, endColumn)},
			},
			Severity: severity, Message: message, Source: source,
		})
	}
	return result
}

type UnavailableRunner struct {
	Message string
}

func (r UnavailableRunner) Run(_ context.Context, path string) (Receipt, error) {
	message := r.Message
	if message == "" {
		message = "post-edit diagnostics runner is not configured"
	}
	return Receipt{Path: path, Status: "unavailable", Diagnostics: []Diagnostic{}, Message: message}, nil
}

var ErrUnavailable = errors.New("diagnostics unavailable")
