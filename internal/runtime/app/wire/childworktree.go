package wire

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agenttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/agent"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/builtin"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	interacttool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	webtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/web"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/observability/diagnostics"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/joblog"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// gitCommandTimeout bounds a single provisioning command. A worktree checkout of
// a large repository is slow, but not minutes-slow, and hanging here would hang
// the parent turn that called the agent tool.
const gitCommandTimeout = 2 * time.Minute

// childWorktrees provisions where each child agent works. A read-only child gets
// a scratch directory it never writes to; a writing child gets a real git
// worktree, because "isolated" has to mean the parent workspace cannot be
// touched, not that the child was asked politely to stay put.
type childWorktrees struct {
	// repository is the host workspace, which must be inside a git work tree for
	// isolation to be possible at all.
	repository string
	root       string
	strategy   string
	backend    sandbox.Backend
	scratch    subagent.WorktreeProvider
}

func newChildWorktrees(
	workspace, root, strategy string, backend sandbox.Backend,
) (*childWorktrees, error) {
	trees := &childWorktrees{
		repository: workspace, root: root, strategy: strategy, backend: backend,
		scratch: subagent.NewScratchWorktrees(root),
	}
	if strategy != config.SubagentWorkspaceWorktree {
		return trees, nil
	}
	// An operator who asked for worktree isolation gets told at startup that this
	// workspace cannot provide it, rather than at the first spawn.
	if err := trees.checkRepository(context.Background()); err != nil {
		return nil, err
	}
	return trees, nil
}

func (c *childWorktrees) isolates(stance subagent.Stance) bool {
	switch c.strategy {
	case config.SubagentWorkspaceReadOnly:
		return false
	case config.SubagentWorkspaceWorktree:
		return stance != subagent.StanceReadOnly
	default:
		return stance != subagent.StanceReadOnly
	}
}

func (c *childWorktrees) Provision(
	agentID string, stance subagent.Stance,
) (subagent.Worktree, error) {
	if c.strategy == config.SubagentWorkspaceSerialized {
		return subagent.Worktree{
			ID: agentID, Path: c.repository, Serialized: true,
		}, nil
	}
	if !c.isolates(stance) {
		return c.scratch.Provision(agentID, stance)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	// Under the auto strategy this is the first moment a workspace's inability to
	// isolate matters, and refusing here means the agent is never created rather
	// than created and then unusable.
	if err := c.checkRepository(ctx); err != nil {
		return subagent.Worktree{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			fmt.Sprintf(
				"child agents with stance %q need an isolated git worktree: %s. "+
					"Spawn an explore or review agent, or set execution.subagent.workspace = %q "+
					"to run children read-only.",
				stance, err, config.SubagentWorkspaceReadOnly,
			),
			false, nil,
		)
	}
	path := filepath.Join(c.root, "worktrees", agentID)
	// git refuses to add a worktree at an existing non-empty path, and a stale
	// directory from a previous run is exactly that.
	if err := os.RemoveAll(path); err != nil {
		return subagent.Worktree{}, err
	}
	if err := c.addDetachedWorktree(ctx, path); err != nil {
		return subagent.Worktree{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			fmt.Sprintf("cannot create a git worktree for child agent %s: %s", agentID, err),
			false, nil,
		)
	}
	baseRev, err := c.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		_ = c.Discard(subagent.Worktree{ID: agentID, Path: path, Isolated: true})
		return subagent.Worktree{}, protocol.NewProblem(
			protocol.CodeUnavailable,
			fmt.Sprintf("cannot record base revision for child agent %s: %s", agentID, err),
			false, nil,
		)
	}
	return subagent.Worktree{
		ID: agentID, Path: path, Isolated: true,
		BaseRev: strings.TrimSpace(baseRev),
	}, nil
}

func (c *childWorktrees) addDetachedWorktree(
	ctx context.Context,
	path string,
) error {
	_, firstErr := c.git(ctx, "worktree", "add", "--detach", path, "HEAD")
	if firstErr == nil {
		return nil
	}
	// A killed host can leave an exact-path Git registration after its managed
	// directory disappears. Remove only that registration, then retry once.
	if _, cleanupErr := c.git(
		ctx, "worktree", "remove", "--force", path,
	); cleanupErr != nil {
		return firstErr
	}
	_, retryErr := c.git(ctx, "worktree", "add", "--detach", path, "HEAD")
	if retryErr != nil {
		return errors.Join(firstErr, fmt.Errorf("retry worktree add: %w", retryErr))
	}
	return nil
}

func (c *childWorktrees) Discard(worktree subagent.Worktree) error {
	if worktree.Serialized {
		return nil
	}
	if !worktree.Isolated {
		return c.scratch.Discard(worktree)
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitCommandTimeout)
	defer cancel()
	// --force because the child almost certainly left uncommitted changes: this
	// is a discard, and the caller already decided the work is done with.
	if _, err := c.git(ctx, "worktree", "remove", "--force", worktree.Path); err != nil {
		// Removing the registration without the directory, or the other way
		// round, would leave git complaining forever. Prune reconciles both.
		if _, pruneErr := c.git(ctx, "worktree", "prune"); pruneErr != nil {
			return errors.Join(err, pruneErr)
		}
		return os.RemoveAll(worktree.Path)
	}
	return nil
}

func (c *childWorktrees) checkRepository(ctx context.Context) error {
	out, err := c.git(ctx, "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return fmt.Errorf(
			"execution.subagent.workspace = %q needs a git work tree at %s: %w",
			config.SubagentWorkspaceWorktree, c.repository, err,
		)
	}
	if strings.TrimSpace(out) != "true" {
		return fmt.Errorf(
			"execution.subagent.workspace = %q needs a git work tree at %s",
			config.SubagentWorkspaceWorktree, c.repository,
		)
	}
	if _, err := c.git(ctx, "rev-parse", "--verify", "HEAD"); err != nil {
		return fmt.Errorf(
			"execution.subagent.workspace = %q needs at least one commit at %s: %w",
			config.SubagentWorkspaceWorktree, c.repository, err,
		)
	}
	return nil
}

func (c *childWorktrees) git(ctx context.Context, arguments ...string) (string, error) {
	result, err := process.Run(ctx, process.Options{
		Path: process.GitExecutable(), Args: process.ManagedGitArguments(arguments), Dir: c.repository,
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

func (c *childWorktrees) commonGitDir(ctx context.Context) (string, error) {
	value, err := c.git(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		if _, markerErr := os.Lstat(filepath.Join(c.repository, ".git")); errors.Is(markerErr, os.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	path := strings.TrimSpace(value)
	if !filepath.IsAbs(path) {
		path = filepath.Join(c.repository, path)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("Git common directory is not a directory")
	}
	return filepath.Clean(canonical), nil
}

func worktreeGitReadRoots(root, expectedCommonDir string) ([]string, error) {
	if expectedCommonDir == "" {
		return nil, nil
	}
	common, err := filepath.EvalSymlinks(expectedCommonDir)
	if err != nil {
		return nil, err
	}
	gitFile, err := os.ReadFile(filepath.Join(root, ".git"))
	if err != nil {
		return nil, err
	}
	const prefix = "gitdir: "
	value := strings.TrimSpace(string(gitFile))
	if !strings.HasPrefix(value, prefix) {
		return nil, errors.New("worktree .git file has no gitdir")
	}
	gitDirPath := strings.TrimSpace(strings.TrimPrefix(value, prefix))
	if !filepath.IsAbs(gitDirPath) {
		gitDirPath = filepath.Join(root, gitDirPath)
	}
	gitDir, err := filepath.EvalSymlinks(gitDirPath)
	if err != nil {
		return nil, err
	}
	relative, err := filepath.Rel(common, gitDir)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return nil, errors.New("worktree gitdir escapes the repository Git directory")
	}
	commonRef, err := os.ReadFile(filepath.Join(gitDir, "commondir"))
	if err != nil {
		return nil, err
	}
	resolvedCommon := strings.TrimSpace(string(commonRef))
	if !filepath.IsAbs(resolvedCommon) {
		resolvedCommon = filepath.Join(gitDir, resolvedCommon)
	}
	resolvedCommon, err = filepath.EvalSymlinks(resolvedCommon)
	if err != nil {
		return nil, err
	}
	if filepath.Clean(resolvedCommon) != filepath.Clean(common) {
		return nil, errors.New("worktree commondir does not match the repository")
	}
	candidates := []string{
		gitDir,
		filepath.Join(common, "objects"),
		filepath.Join(common, "refs"), filepath.Join(common, "info"),
		filepath.Join(common, "packed-refs"),
		filepath.Join(common, "HEAD"),
		filepath.Join(common, "shallow"),
		filepath.Join(common, "config"),
		filepath.Join(common, "config.worktree"),
	}
	roots := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, err := os.Lstat(candidate); err == nil {
			roots = append(roots, candidate)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	return roots, nil
}

// childToolset is the tool plane of one isolated child: a registry, sandbox and
// journal rooted at the child's worktree. It exists because a tool registry
// resolves paths against the root it was built with — handing a child the
// parent's registry would send its writes to the parent workspace no matter what
// the child's Engine believed its workspace was.
type childToolset struct {
	registry    *tool.Registry
	backend     sandbox.Backend
	processes   *process.SessionManager
	journal     *workspacejournal.Manager
	jobLogs     *joblog.Store
	inputHost   *interacttool.Host
	diagnostics diagnostics.Runner
	verify      verify.Runner
	files       *filetool.Tools
}

func (t *childToolset) close() {
	if t == nil {
		return
	}
	if t.processes != nil {
		t.processes.CloseAll()
	}
	if t.jobLogs != nil {
		_ = t.jobLogs.Close()
	}
	if t.journal != nil {
		_ = t.journal.Close(context.Background())
	}
	_ = sandbox.CloseBackend(t.backend)
}

// childToolsets builds and owns one toolset per isolated child root.
type childToolsets struct {
	helperPath          string
	content             contentstore.Store
	web                 webtool.Options
	verify              config.Verify
	journals            config.Journal
	diagnosticCommands  map[string]diagnostics.Command
	diagnosticReadRoots []string
	diagnosticReadFiles []string
	gitCommonDir        string
	managedProxyPort    uint16
	agents              *subagent.AgentControl
	agentSession        string
	agentRelease        func(string)
	interactionsBound   bool
	interactionRLM      *rlm.Store
	interactionGovernor *rlm.Governor
	interactionVision   interacttool.VisionClient
	interactionPlan     func(interacttool.Plan) error

	mu    sync.Mutex
	built map[string]*childToolset
}

func (c *childToolsets) bindAgents(
	control *subagent.AgentControl,
	sessionID string,
	onRelease func(string),
) {
	c.mu.Lock()
	c.agents, c.agentSession, c.agentRelease = control, sessionID, onRelease
	c.mu.Unlock()
}

func (c *childToolsets) bindInteractions(
	store *rlm.Store,
	governor *rlm.Governor,
	vision interacttool.VisionClient,
	onPlan func(interacttool.Plan) error,
) {
	c.mu.Lock()
	c.interactionsBound, c.interactionRLM = true, store
	c.interactionGovernor, c.interactionVision = governor, vision
	c.interactionPlan = onPlan
	c.mu.Unlock()
}

func newChildToolsets(
	helperPath string, content contentstore.Store, web webtool.Options,
	verifyConfig config.Verify, journals config.Journal,
	diagnosticCommands map[string]diagnostics.Command,
	diagnosticReadRoots []string,
	diagnosticReadFiles []string,
	gitCommonDir string, managedProxyPort uint16,
) *childToolsets {
	return &childToolsets{
		helperPath: helperPath, content: content, web: web, verify: verifyConfig,
		journals: journals, diagnosticCommands: diagnosticCommands,
		diagnosticReadRoots: append([]string(nil), diagnosticReadRoots...),
		diagnosticReadFiles: append([]string(nil), diagnosticReadFiles...),
		gitCommonDir:        gitCommonDir,
		managedProxyPort:    managedProxyPort,
		built:               make(map[string]*childToolset),
	}
}

// open returns the toolset for a child root, building it on first use. Reuse
// matters: a follow-up turn on the same child must see the same journal, or its
// second turn could not roll back what its first turn wrote.
func (c *childToolsets) open(
	root string,
	interactive bool,
) (*childToolset, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if existing, ok := c.built[root]; ok {
		if (existing.inputHost != nil) != interactive {
			return nil, errors.New("child toolset interaction mode changed")
		}
		return existing, nil
	}
	if interactive && !c.interactionsBound {
		return nil, errors.New("child interaction tools are not configured")
	}
	hostReadRoots := append([]string(nil), c.diagnosticReadRoots...)
	gitRoots, err := worktreeGitReadRoots(root, c.gitCommonDir)
	if err != nil {
		return nil, fmt.Errorf("child Git metadata: %w", err)
	}
	hostReadRoots = append(hostReadRoots, gitRoots...)
	backend, err := newPlatformBackend(sandbox.Options{
		WorkspaceRoot: root, HelperPath: c.helperPath,
		ManagedProxyPort: c.managedProxyPort, HostReadRoots: hostReadRoots,
		HostReadFiles: c.diagnosticReadFiles,
	})
	if err != nil {
		return nil, fmt.Errorf("child sandbox: %w", err)
	}
	// The child gets its own process manager: background jobs are journaled per
	// root, and sharing the parent's would mix the two sessions' job lists.
	processes := process.NewSessionManager(0)
	processes.SetJournalPath(filepath.Join(root, ".codehelper", "jobs-journal.jsonl"))
	var jobs *joblog.Store
	if archive, err := joblog.New(filepath.Join(root, ".codehelper", "jobs")); err == nil {
		jobs = archive
		processes.SetArchive(archive)
	}
	registry, handles, err := builtin.NewWithDependencies(
		root, backend, c.content, processes, c.web,
	)
	if err != nil {
		processes.CloseAll()
		_ = sandbox.CloseBackend(backend)
		return nil, fmt.Errorf("child tools: %w", err)
	}
	var inputHost *interacttool.Host
	if interactive {
		inputHost = interacttool.NewHost(0)
		registerErr := interacttool.Register(registry, interacttool.Options{
			Host: inputHost, Workspace: root, Backend: backend,
			RLM: c.interactionRLM, Governor: c.interactionGovernor,
			Vision: c.interactionVision, OnPlan: c.interactionPlan,
		})
		if registerErr != nil {
			if jobs != nil {
				_ = jobs.Close()
			}
			processes.CloseAll()
			_ = sandbox.CloseBackend(backend)
			return nil, fmt.Errorf("child interact tools: %w", registerErr)
		}
	}
	journal, err := c.openJournal(root)
	if err != nil {
		processes.CloseAll()
		_ = sandbox.CloseBackend(backend)
		return nil, err
	}
	runner := &verify.CommandRunner{Root: root, Sandbox: backend}
	if c.verify.Command != "" {
		runner.Commands = []verify.Command{{Name: "custom", Command: c.verify.Command}}
	}
	files, err := filetool.NewWithBackend(root, backend)
	if err != nil {
		_ = journal.Close(context.Background())
		processes.CloseAll()
		_ = sandbox.CloseBackend(backend)
		return nil, fmt.Errorf("child integration files: %w", err)
	}
	if c.agents != nil {
		if err := agenttool.Register(registry, agenttool.Options{
			Control: c.agents, Handles: handles, SessionID: c.agentSession,
			Files: files, Workspace: root, OnRelease: c.agentRelease,
			Verify: runner, Sandbox: backend,
		}); err != nil {
			_ = journal.Close(context.Background())
			processes.CloseAll()
			_ = sandbox.CloseBackend(backend)
			return nil, fmt.Errorf("child agent tools: %w", err)
		}
	}
	toolset := &childToolset{
		registry: registry, backend: backend, processes: processes, journal: journal,
		jobLogs: jobs, inputHost: inputHost,
		diagnostics: diagnostics.NewCommandRunner(root, backend, c.diagnosticCommands),
		verify:      runner, files: files,
	}
	c.built[root] = toolset
	return toolset, nil
}

// openJournal gives the child the same durability the parent has. A worktree
// outlives the turn that created it, so a child killed mid-turn leaves the same
// half-applied writes — with the added twist that the person who has to clean up
// never looked at that directory in the first place.
func (c *childToolsets) openJournal(root string) (*workspacejournal.Manager, error) {
	if !c.journals.Durable {
		journal, err := workspacejournal.New(root, c.content)
		if err != nil {
			return nil, fmt.Errorf("child journal: %w", err)
		}
		return journal, nil
	}
	journal, err := workspacejournal.Open(root, filepath.Join(root, ".codehelper", "journal"))
	if err != nil {
		return nil, fmt.Errorf("child journal: %w", err)
	}
	if c.journals.RecoverOnStart {
		if _, err := journal.Recover(context.Background()); err != nil {
			_ = journal.Close(context.Background())
			return nil, fmt.Errorf("recover interrupted child turns: %w", err)
		}
	}
	return journal, nil
}

// release drops the toolset for a root once its child is closed.
func (c *childToolsets) release(root string) {
	c.mu.Lock()
	toolset := c.built[root]
	delete(c.built, root)
	c.mu.Unlock()
	toolset.close()
}

func (c *childToolsets) closeAll() {
	c.mu.Lock()
	toolsets := make([]*childToolset, 0, len(c.built))
	for _, toolset := range c.built {
		toolsets = append(toolsets, toolset)
	}
	c.built = make(map[string]*childToolset)
	c.mu.Unlock()
	for _, toolset := range toolsets {
		toolset.close()
	}
}
