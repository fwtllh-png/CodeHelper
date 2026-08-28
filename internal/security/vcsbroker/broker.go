// Package vcsbroker owns allowlisted Git metadata mutations.
package vcsbroker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/processbroker"
)

type MutationKind string

const (
	WorktreeAdd    MutationKind = "worktree_add"
	WorktreeRemove MutationKind = "worktree_remove"
	WorktreePrune  MutationKind = "worktree_prune"
	IndexAdd       MutationKind = "index_add"
	Commit         MutationKind = "commit"
	ApplyPatch     MutationKind = "apply_patch"
)

type RepositoryState struct {
	CommonDirIdentity string `json:"common_dir_identity"`
	HEAD              string `json:"head"`
	Ref               string `json:"ref"`
	IndexDigest       string `json:"index_digest"`
	WorktreesDigest   string `json:"worktrees_digest"`
}

type Mutation struct {
	Kind MutationKind
	Dir  string
	Args []string
}

type Broker struct {
	repository       string
	workspaceID      string
	commonDir        string
	authority        *authority.LeaseAuthority
	processes        *processbroker.Broker
	sequence         atomic.Uint64
	leaseTTL         time.Duration
	mu               sync.Mutex
	beforeRevalidate func() error
}

func New(
	repository string,
	manager *authority.LeaseAuthority,
	leaseTTL time.Duration,
) (*Broker, error) {
	if manager == nil {
		return nil, errors.New("VCS Broker requires a Lease Authority")
	}
	if leaseTTL <= 0 {
		return nil, errors.New("VCS Broker requires an explicit Lease TTL")
	}
	canonical, err := filepath.Abs(repository)
	if err != nil {
		return nil, err
	}
	canonical, err = filepath.EvalSymlinks(canonical)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return nil, errors.New("VCS Broker repository must be a directory")
	}
	processes, err := processbroker.New(manager)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(filepath.Clean(canonical)))
	return &Broker{
		repository: canonical, workspaceID: hex.EncodeToString(sum[:]),
		authority: manager, processes: processes, leaseTTL: leaseTTL,
	}, nil
}

func (b *Broker) Read(
	ctx context.Context,
	dir string,
	arguments ...string,
) (string, error) {
	if b == nil {
		return "", errors.New("VCS Broker is required")
	}
	return runGit(ctx, dir, arguments...)
}

func (b *Broker) Mutate(
	ctx context.Context,
	mutation Mutation,
) (processbroker.Result, error) {
	if b == nil || b.authority == nil || b.processes == nil {
		return processbroker.Result{}, errors.New("VCS Broker is required")
	}
	if err := validateMutation(b.repository, mutation); err != nil {
		return processbroker.Result{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	before, err := b.snapshot(ctx, mutation.Dir)
	if err != nil {
		return processbroker.Result{}, err
	}
	sequence := b.sequence.Add(1)
	subject, err := authority.NewManagedProcessSubject(
		authority.SubjectHost,
		"vcs-broker",
		authority.TrustHost,
		sequence,
		struct {
			Mutation Mutation        `json:"mutation"`
			Before   RepositoryState `json:"before"`
		}{Mutation: mutation, Before: before},
	)
	if err != nil {
		return processbroker.Result{}, err
	}
	executable := process.GitExecutable()
	arguments := process.ManagedGitArguments(mutation.Args)
	operation, err := authority.BuildManagedProcessOperation(
		authority.ManagedProcessInput{
			ID: fmt.Sprintf("vcs-%d", sequence), Tool: "vcs_broker",
			WorkspaceID: b.workspaceID, WorkspaceGeneration: 1,
			Subject: subject, Executable: executable,
			Args: arguments, WorkingDirectory: mutation.Dir,
			Effect: authority.EffectContract{
				Kind:                 policy.EffectProcessMutating,
				Reversibility:        authority.ReversibilityBounded,
				Risk:                 policy.RiskHigh,
				WorkspaceTransaction: authority.WorkspaceTransactionNone,
			},
			Required: authority.RequiredControls{},
		},
	)
	if err != nil {
		return processbroker.Result{}, err
	}
	profile, err := authority.BuildManagedProcessProfile(
		authority.ManagedProfileInput{
			Operation: operation, Revision: sequence,
			WorkspaceRoot: b.repository, WorkspaceBaseWrite: true,
			AllowNetwork: true,
			Enforcement:  "none", Backend: "none",
		},
	)
	if err != nil {
		return processbroker.Result{}, err
	}
	expiresAt := time.Now().Add(b.leaseTTL)
	if deadline, ok := ctx.Deadline(); ok && deadline.Before(expiresAt) {
		expiresAt = deadline
	}
	lease, err := b.authority.Issue(authority.LeaseIssueRequest{
		Operation: operation, Profile: profile,
		PolicyRevision: 1, Attempt: sequence, ExpiresAt: expiresAt,
	})
	if err != nil {
		return processbroker.Result{}, err
	}
	terminal := false
	defer func() {
		if terminal {
			_ = b.authority.Release(lease)
			return
		}
		if snapshot, snapshotErr := b.authority.Snapshot(lease); snapshotErr == nil &&
			snapshot.State == authority.LeaseIssued {
			_ = b.authority.Revoke(lease)
			_ = b.authority.Release(lease)
		}
	}()
	if b.beforeRevalidate != nil {
		if err := b.beforeRevalidate(); err != nil {
			return processbroker.Result{}, err
		}
	}
	current, err := b.snapshot(ctx, mutation.Dir)
	if err != nil {
		return processbroker.Result{}, err
	}
	if current != before {
		return processbroker.Result{}, errors.New("repository changed before VCS mutation")
	}
	validation := authority.LeaseValidation{
		Operation: operation, PolicyRevision: 1,
		WorkspaceID: b.workspaceID, WorkspaceGeneration: 1,
		SubjectDigest: subject.Digest, SubjectGeneration: subject.Generation,
		Attempt: sequence,
	}
	result, err := b.processes.RunCommand(ctx, processbroker.CommandRequest{
		Lease: lease, Validation: validation,
		Options: process.Options{
			Path: executable, Args: arguments, Dir: mutation.Dir,
		},
		Identity: processbroker.Identity{
			SessionID: "vcs-broker", ThreadID: operation.ID, TurnID: operation.ID,
		},
	})
	terminal = true
	if err == nil && result.Process.ExitCode != 0 {
		message := strings.TrimSpace(result.Process.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Process.Stdout)
		}
		err = fmt.Errorf(
			"git %s: %s", strings.Join(mutation.Args, " "), message,
		)
	}
	return result, err
}

func (b *Broker) snapshot(
	ctx context.Context,
	dir string,
) (RepositoryState, error) {
	common, err := runGit(ctx, dir, "rev-parse", "--git-common-dir")
	if err != nil {
		return RepositoryState{}, err
	}
	common = strings.TrimSpace(common)
	if !filepath.IsAbs(common) {
		common = filepath.Join(dir, common)
	}
	common, err = filepath.EvalSymlinks(common)
	if err != nil {
		return RepositoryState{}, err
	}
	info, err := os.Stat(common)
	if err != nil || !info.IsDir() {
		return RepositoryState{}, errors.New("Git common directory is invalid")
	}
	if b.commonDir == "" {
		b.commonDir = common
	} else if b.commonDir != common {
		return RepositoryState{}, errors.New("VCS mutation changed Repository identity")
	}
	head, err := runGit(ctx, dir, "rev-parse", "--verify", "HEAD")
	if err != nil {
		return RepositoryState{}, err
	}
	ref, _ := runGit(ctx, dir, "symbolic-ref", "-q", "HEAD")
	indexPath, err := runGit(ctx, dir, "rev-parse", "--git-path", "index")
	if err != nil {
		return RepositoryState{}, err
	}
	indexPath = strings.TrimSpace(indexPath)
	if !filepath.IsAbs(indexPath) {
		indexPath = filepath.Join(dir, indexPath)
	}
	indexDigest, err := digestFile(indexPath)
	if err != nil {
		return RepositoryState{}, err
	}
	worktrees, err := runGit(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return RepositoryState{}, err
	}
	return RepositoryState{
		CommonDirIdentity: fmt.Sprintf(
			"%s:%d:%d", common, info.ModTime().UnixNano(), info.Size(),
		),
		HEAD: strings.TrimSpace(head), Ref: strings.TrimSpace(ref),
		IndexDigest: indexDigest, WorktreesDigest: digestBytes([]byte(worktrees)),
	}, nil
}

func validateMutation(repository string, mutation Mutation) error {
	if mutation.Dir == "" {
		mutation.Dir = repository
	}
	directory, err := filepath.Abs(mutation.Dir)
	if err != nil {
		return err
	}
	if mutation.Kind == "" || len(mutation.Args) == 0 {
		return errors.New("VCS mutation is incomplete")
	}
	switch mutation.Kind {
	case WorktreeAdd:
		if len(mutation.Args) != 5 ||
			strings.Join(mutation.Args[:3], " ") != "worktree add --detach" {
			return errors.New("invalid worktree add mutation")
		}
	case WorktreeRemove:
		if len(mutation.Args) != 4 ||
			strings.Join(mutation.Args[:3], " ") != "worktree remove --force" {
			return errors.New("invalid worktree remove mutation")
		}
	case WorktreePrune:
		if len(mutation.Args) != 2 ||
			mutation.Args[0] != "worktree" || mutation.Args[1] != "prune" {
			return errors.New("invalid worktree prune mutation")
		}
	case IndexAdd:
		if len(mutation.Args) < 3 ||
			mutation.Args[0] != "add" || mutation.Args[1] != "-A" ||
			mutation.Args[2] != "--" {
			return errors.New("invalid index mutation")
		}
	case Commit:
		if !validCommitArguments(mutation.Args) {
			return errors.New("invalid commit mutation")
		}
	case ApplyPatch:
		if len(mutation.Args) != 3 ||
			mutation.Args[0] != "apply" ||
			mutation.Args[1] != "--whitespace=nowarn" {
			return errors.New("invalid patch mutation")
		}
	default:
		return errors.New("VCS mutation kind is not allowlisted")
	}
	if directory == "" {
		return errors.New("VCS mutation directory is invalid")
	}
	return nil
}

func validCommitArguments(arguments []string) bool {
	expected := []string{
		"-c", "user.name=CodeHelper",
		"-c", "user.email=codehelper@localhost",
		"commit", "--allow-empty", "--no-gpg-sign", "-m",
		"codehelper chat baseline",
	}
	return strings.Join(arguments, "\x00") == strings.Join(expected, "\x00")
}

func runGit(ctx context.Context, dir string, arguments ...string) (string, error) {
	result, err := process.Run(ctx, process.Options{
		Path: process.GitExecutable(),
		Args: process.ManagedGitArguments(arguments),
		Dir:  dir,
	})
	if err != nil {
		return "", err
	}
	if result.ExitCode != 0 {
		message := strings.TrimSpace(result.Stderr)
		if message == "" {
			message = strings.TrimSpace(result.Stdout)
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(arguments, " "), message)
	}
	return result.Stdout, nil
}

func digestFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	return digestBytes(data), nil
}

func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
