package workspacequery

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

type GitState struct {
	Repository bool     `json:"repository"`
	Branch     string   `json:"branch,omitempty"`
	Branches   []string `json:"branches,omitempty"`
	Detached   bool     `json:"detached,omitempty"`
	Dirty      bool     `json:"dirty,omitempty"`
}

func (s *Service) GitState(ctx context.Context) (GitState, error) {
	inside, err := s.git(ctx, true, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		if strings.Contains(err.Error(), "not a git repository") {
			return GitState{}, nil
		}
		return GitState{}, err
	}
	if strings.TrimSpace(inside) != "true" {
		return GitState{}, nil
	}
	branches, err := s.git(
		ctx, true, "for-each-ref", "--format=%(refname:short)", "refs/heads",
	)
	if err != nil {
		return GitState{}, err
	}
	current, currentErr := s.git(ctx, true, "symbolic-ref", "--quiet", "--short", "HEAD")
	state := GitState{
		Repository: true,
		Branches:   nonemptyLines(branches),
		Branch:     strings.TrimSpace(current),
	}
	if currentErr != nil {
		state.Detached = true
		state.Branch, err = s.git(ctx, true, "rev-parse", "--short", "HEAD")
		if err != nil {
			return GitState{}, err
		}
		state.Branch = strings.TrimSpace(state.Branch)
	} else {
		found := false
		for _, branch := range state.Branches {
			found = found || branch == state.Branch
		}
		if !found && state.Branch != "" {
			state.Branches = append(state.Branches, state.Branch)
		}
	}
	status, err := s.git(ctx, true, "status", "--porcelain=v1")
	if err != nil {
		return GitState{}, err
	}
	state.Dirty = strings.TrimSpace(status) != ""
	return state, nil
}

func (s *Service) SwitchBranch(ctx context.Context, branch string) (GitState, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" || strings.HasPrefix(branch, "-") ||
		strings.ContainsAny(branch, "\x00\r\n") {
		return GitState{}, errors.New("Git branch is invalid")
	}
	state, err := s.GitState(ctx)
	if err != nil {
		return GitState{}, err
	}
	if !state.Repository {
		return GitState{}, errors.New("workspace is not a Git repository")
	}
	found := false
	for _, candidate := range state.Branches {
		found = found || candidate == branch
	}
	if !found {
		return GitState{}, fmt.Errorf("Git branch %q does not exist locally", branch)
	}
	if state.Branch != branch || state.Detached {
		if _, err := s.git(ctx, false, "switch", "--no-guess", "--", branch); err != nil {
			return GitState{}, err
		}
	}
	return s.GitState(ctx)
}

func (s *Service) git(
	ctx context.Context,
	readOnly bool,
	arguments ...string,
) (string, error) {
	root := s.workspace.Root()
	directory, err := os.Open(root)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	result, err := process.Run(ctx, process.Options{
		Path: process.GitExecutable(), Args: process.ManagedGitArguments(arguments),
		Dir: root, DirFile: directory, Sandbox: s.backend,
		WorkspaceReadOnly: readOnly, DenyNetwork: true,
	})
	if err != nil && readOnly {
		// These fixed metadata queries do not execute repository content. Keep
		// branch discovery available when unrelated dependency symlinks make the
		// full Workspace sandbox preflight reject the repository.
		result, err = process.Run(ctx, process.Options{
			Path: process.GitExecutable(), Args: process.ManagedGitArguments(arguments),
			Dir: root, DirFile: directory,
		})
	}
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		return "", fmt.Errorf("git %s: %s", arguments[0], message)
	}
	return result.Stdout, nil
}

func nonemptyLines(value string) []string {
	var result []string
	for line := range strings.SplitSeq(value, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			result = append(result, line)
		}
	}
	return result
}
