// Package workspacebroker composes the trusted workspace mutation brokers.
package workspacebroker

import (
	"context"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
	"github.com/fwtllh-png/CodeHelper/internal/security/filebroker"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
	"github.com/fwtllh-png/CodeHelper/internal/security/vcsbroker"
)

type MutationKind = vcsbroker.MutationKind

const (
	WorktreeAdd    = vcsbroker.WorktreeAdd
	WorktreeRemove = vcsbroker.WorktreeRemove
	WorktreePrune  = vcsbroker.WorktreePrune
	IndexAdd       = vcsbroker.IndexAdd
	Commit         = vcsbroker.Commit
	ApplyPatch     = vcsbroker.ApplyPatch
)

type Runtime struct {
	Files     *filebroker.Runtime
	VCS       *vcsbroker.Broker
	authority *authority.LeaseAuthority
	leaseTTL  time.Duration
}

func (r *Runtime) ReadVCS(
	ctx context.Context,
	dir string,
	arguments ...string,
) (string, error) {
	return r.VCS.Read(ctx, dir, arguments...)
}

func (r *Runtime) MutateVCS(
	ctx context.Context,
	kind MutationKind,
	dir string,
	arguments []string,
) error {
	_, err := r.VCS.Mutate(ctx, vcsbroker.Mutation{
		Kind: kind, Dir: dir, Args: arguments,
	})
	return err
}

func (r *Runtime) AddWorktree(
	ctx context.Context, dir, path, revision string,
) error {
	return r.MutateVCS(ctx, WorktreeAdd, dir, []string{
		"worktree", "add", "--detach", path, revision,
	})
}

func (r *Runtime) RemoveWorktree(
	ctx context.Context, dir, path string,
) error {
	return r.MutateVCS(ctx, WorktreeRemove, dir, []string{
		"worktree", "remove", "--force", path,
	})
}

func (r *Runtime) PruneWorktrees(ctx context.Context, dir string) error {
	return r.MutateVCS(
		ctx, WorktreePrune, dir, []string{"worktree", "prune"},
	)
}

func (r *Runtime) ApplyPatch(
	ctx context.Context, dir, patchPath string,
) error {
	return r.MutateVCS(ctx, ApplyPatch, dir, []string{
		"apply", "--whitespace=nowarn", patchPath,
	})
}

func (r *Runtime) AddIndex(
	ctx context.Context, dir string, paths []string,
) error {
	arguments := append([]string{"add", "-A", "--"}, paths...)
	return r.MutateVCS(ctx, IndexAdd, dir, arguments)
}

func (r *Runtime) CommitBaseline(
	ctx context.Context, dir string,
) error {
	return r.MutateVCS(ctx, Commit, dir, []string{
		"-c", "user.name=CodeHelper",
		"-c", "user.email=codehelper@localhost",
		"commit", "--allow-empty", "--no-gpg-sign",
		"-m", "codehelper chat baseline",
	})
}

func (r *Runtime) CommitFiles(
	ctx context.Context,
	toolName string,
	plan filebroker.Plan,
	journal *workspacejournal.Manager,
) (filebroker.Result, error) {
	var transactionJournal filebroker.Journal
	if journal != nil {
		transactionJournal = journal
	}
	return r.Files.Commit(ctx, toolName, plan, transactionJournal)
}

func New(
	workspace string,
	manager *authority.LeaseAuthority,
	leaseTTL time.Duration,
) (*Runtime, error) {
	root, err := sandbox.NewWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	files, err := filebroker.NewRuntime(root, manager, leaseTTL)
	if err != nil {
		return nil, err
	}
	vcs, err := vcsbroker.New(workspace, manager, leaseTTL)
	if err != nil {
		return nil, err
	}
	return &Runtime{
		Files: files, VCS: vcs, authority: manager, leaseTTL: leaseTTL,
	}, nil
}

func (r *Runtime) CommitFilesAt(
	ctx context.Context,
	workspace string,
	toolName string,
	plan filebroker.Plan,
) (filebroker.Result, error) {
	root, err := sandbox.NewWorkspace(workspace)
	if err != nil {
		return filebroker.Result{}, err
	}
	runtime, err := filebroker.NewRuntime(root, r.authority, r.leaseTTL)
	if err != nil {
		return filebroker.Result{}, err
	}
	return runtime.Commit(ctx, toolName, plan, nil)
}
