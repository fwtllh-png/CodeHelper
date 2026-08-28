package vcsbroker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
)

func TestBrokerAddsAndRemovesDetachedWorktree(t *testing.T) {
	repository := gitRepository(t)
	manager := authority.NewLeaseAuthority(authority.LeaseAuthorityOptions{})
	broker, err := New(repository, manager, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	head, err := broker.Read(t.Context(), repository, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "worktree")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	if _, err := broker.Mutate(ctx, Mutation{
		Kind: WorktreeAdd, Dir: repository,
		Args: []string{
			"worktree", "add", "--detach", target, strings.TrimSpace(head),
		},
	}); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(target, ".git")); err != nil || info.IsDir() {
		t.Fatalf("detached worktree marker = %+v, err = %v", info, err)
	}
	if _, err := broker.Mutate(ctx, Mutation{
		Kind: WorktreeRemove, Dir: repository,
		Args: []string{"worktree", "remove", "--force", target},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("removed worktree still exists: %v", err)
	}
}

func TestBrokerRejectsUnallowlistedGitMutation(t *testing.T) {
	repository := gitRepository(t)
	broker, err := New(
		repository,
		authority.NewLeaseAuthority(authority.LeaseAuthorityOptions{}),
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := broker.Mutate(ctx, Mutation{
		Kind: IndexAdd, Dir: repository,
		Args: []string{"reset", "--hard"},
	}); err == nil || !strings.Contains(err.Error(), "invalid index") {
		t.Fatalf("unallowlisted mutation error = %v", err)
	}
}

func TestBrokerRequiresExplicitLeaseTTL(t *testing.T) {
	repository := gitRepository(t)
	if _, err := New(
		repository,
		authority.NewLeaseAuthority(authority.LeaseAuthorityOptions{}),
		0,
	); err == nil || !strings.Contains(err.Error(), "Lease TTL") {
		t.Fatalf("missing TTL error = %v", err)
	}
}

func TestBrokerRejectsIndexDriftBeforeMutation(t *testing.T) {
	repository := gitRepository(t)
	broker, err := New(
		repository,
		authority.NewLeaseAuthority(authority.LeaseAuthorityOptions{}),
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	broker.beforeRevalidate = func() error {
		if err := os.WriteFile(
			filepath.Join(repository, "drift.txt"), []byte("drift\n"), 0o600,
		); err != nil {
			return err
		}
		command := exec.Command("git", "add", "drift.txt")
		command.Dir = repository
		command.Env = append(
			os.Environ(), "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_SYSTEM=",
		)
		return command.Run()
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := broker.Mutate(ctx, Mutation{
		Kind: WorktreePrune, Dir: repository,
		Args: []string{"worktree", "prune"},
	}); err == nil || !strings.Contains(err.Error(), "repository changed") {
		t.Fatalf("index drift error = %v", err)
	}
}

func TestBrokerBindsMutationToTargetWorktreeIndex(t *testing.T) {
	repository := gitRepository(t)
	broker, err := New(
		repository,
		authority.NewLeaseAuthority(authority.LeaseAuthorityOptions{}),
		time.Minute,
	)
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "worktree")
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	head, err := broker.Read(ctx, repository, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := broker.Mutate(ctx, Mutation{
		Kind: WorktreeAdd, Dir: repository,
		Args: []string{
			"worktree", "add", "--detach", target, strings.TrimSpace(head),
		},
	}); err != nil {
		t.Fatal(err)
	}
	broker.beforeRevalidate = func() error {
		if err := os.WriteFile(
			filepath.Join(target, "target-drift.txt"), []byte("drift\n"), 0o600,
		); err != nil {
			return err
		}
		command := exec.Command("git", "add", "target-drift.txt")
		command.Dir = target
		command.Env = append(
			os.Environ(), "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_SYSTEM=",
		)
		return command.Run()
	}
	if _, err := broker.Mutate(ctx, Mutation{
		Kind: WorktreePrune, Dir: target,
		Args: []string{"worktree", "prune"},
	}); err == nil || !strings.Contains(err.Error(), "repository changed") {
		t.Fatalf("target worktree index drift error = %v", err)
	}
}

func gitRepository(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git unavailable")
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, arguments := range [][]string{
		{"init", "--quiet"},
		{"config", "user.email", "fixture@example.invalid"},
		{"config", "user.name", "Fixture"},
		{"config", "commit.gpgsign", "false"},
		{"add", "README.md"},
		{"commit", "--quiet", "-m", "seed"},
	} {
		command := exec.Command("git", arguments...)
		command.Dir = root
		command.Env = append(
			os.Environ(), "GIT_CONFIG_GLOBAL=", "GIT_CONFIG_SYSTEM=",
		)
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v: %s", strings.Join(arguments, " "), err, output)
		}
	}
	return root
}
