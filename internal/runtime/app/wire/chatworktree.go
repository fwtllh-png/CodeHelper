package wire

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	agentengine "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/engine"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	maxChatMergeFiles     = 512
	chatMergeBatchFiles   = 64
	maxChatMergeDiffBytes = 3 << 20
)

type chatWorkspace struct {
	sessionID string
	threadID  protocol.ThreadID
	worktree  subagent.Worktree
}

// chatWorkspaces turns ordinary host sessions into isolated writing threads.
// Worktrees remain on disk across Runtime restarts; Restore derives their path
// from the session id instead of trusting persisted filesystem input.
type chatWorkspaces struct {
	repository string
	trees      *childWorktrees
	tools      *childToolsets
	threads    *app.ThreadManager
	parent     *filetool.Tools
	journal    *workspacejournal.Manager
	gate       *agentengine.WorkspaceTurnGate
	allowApply bool

	mu       sync.Mutex
	sessions map[string]chatWorkspace
}

func newChatWorkspaces(
	repository string,
	trees *childWorktrees,
	tools *childToolsets,
	threads *app.ThreadManager,
	parent *filetool.Tools,
	journal *workspacejournal.Manager,
	gate *agentengine.WorkspaceTurnGate,
	allowApply bool,
) *chatWorkspaces {
	if trees == nil || tools == nil || threads == nil || parent == nil ||
		journal == nil || gate == nil {
		return nil
	}
	canonicalRepository, err := filepath.EvalSymlinks(repository)
	if err != nil {
		return nil
	}
	return &chatWorkspaces{
		repository: canonicalRepository, trees: trees, tools: tools, threads: threads,
		parent: parent, journal: journal, gate: gate,
		allowApply: allowApply,
		sessions:   make(map[string]chatWorkspace),
	}
}

func (c *chatWorkspaces) Provision(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
) (app.SessionWorkspace, error) {
	if err := c.validateIdentity(sessionID, threadID); err != nil {
		return app.SessionWorkspace{}, err
	}
	id := chatWorktreeID(sessionID)
	worktree, err := c.trees.Provision(id, subagent.StanceWrite)
	if err != nil {
		return app.SessionWorkspace{}, err
	}
	canonical, err := filepath.EvalSymlinks(worktree.Path)
	if err != nil {
		_ = c.trees.Discard(worktree)
		return app.SessionWorkspace{}, err
	}
	worktree.Path = canonical
	if err := c.snapshotParent(ctx, worktree.Path); err != nil {
		_ = c.trees.Discard(worktree)
		return app.SessionWorkspace{}, fmt.Errorf("snapshot parent workspace: %w", err)
	}
	value := chatWorkspace{sessionID: sessionID, threadID: threadID, worktree: worktree}
	if err := c.register(value); err != nil {
		_ = c.trees.Discard(worktree)
		return app.SessionWorkspace{}, err
	}
	return app.SessionWorkspace{Mode: app.SessionIsolationWorktree, Root: worktree.Path}, nil
}

func (c *chatWorkspaces) Restore(
	_ context.Context,
	sessionID string,
	threadID protocol.ThreadID,
) (app.SessionWorkspace, error) {
	if err := c.validateIdentity(sessionID, threadID); err != nil {
		return app.SessionWorkspace{}, err
	}
	c.mu.Lock()
	if existing, ok := c.sessions[sessionID]; ok {
		c.mu.Unlock()
		if existing.threadID != threadID {
			return app.SessionWorkspace{},
				errors.New("Chat session thread identity mismatch")
		}
		return app.SessionWorkspace{
			Mode: app.SessionIsolationWorktree,
			Root: existing.worktree.Path,
		}, nil
	}
	c.mu.Unlock()
	path := filepath.Join(c.trees.root, "worktrees", chatWorktreeID(sessionID))
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return app.SessionWorkspace{}, fmt.Errorf("restore Chat worktree: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(c.trees.root)
	if err != nil {
		return app.SessionWorkspace{}, err
	}
	expected := filepath.Join(
		canonicalRoot, "worktrees", chatWorktreeID(sessionID),
	)
	if canonical != filepath.Clean(expected) {
		return app.SessionWorkspace{}, errors.New("Chat worktree path identity mismatch")
	}
	if _, err := c.git(context.Background(), canonical, "rev-parse", "--verify", "HEAD"); err != nil {
		return app.SessionWorkspace{}, fmt.Errorf("restore Chat worktree HEAD: %w", err)
	}
	value := chatWorkspace{
		sessionID: sessionID, threadID: threadID,
		worktree: subagent.Worktree{
			ID: chatWorktreeID(sessionID), Path: canonical, Isolated: true,
		},
	}
	if err := c.register(value); err != nil {
		return app.SessionWorkspace{}, err
	}
	return app.SessionWorkspace{Mode: app.SessionIsolationWorktree, Root: canonical}, nil
}

func (c *chatWorkspaces) Discard(
	_ context.Context,
	sessionID string,
	threadID protocol.ThreadID,
) error {
	c.mu.Lock()
	value, ok := c.sessions[sessionID]
	if ok {
		delete(c.sessions, sessionID)
	}
	c.mu.Unlock()
	if !ok {
		return nil
	}
	if value.threadID != threadID {
		return errors.New("Chat session thread identity mismatch")
	}
	c.threads.Release(threadID)
	c.tools.release(value.worktree.Path)
	return c.trees.Discard(value.worktree)
}

func (c *chatWorkspaces) PlanMerge(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
) (tool.EditPlan, error) {
	plan, err := c.mergePlan(ctx, sessionID, threadID)
	if err != nil {
		return tool.EditPlan{}, err
	}
	return plan.edit, nil
}

func (c *chatWorkspaces) ApplyMerge(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
	planID string,
) (_ tool.EditPlan, resultErr error) {
	if !c.allowApply {
		return tool.EditPlan{}, errors.New(
			"Chat merge apply is unavailable in a read-only workspace",
		)
	}
	if len(planID) != 64 {
		return tool.EditPlan{}, errors.New("Chat merge plan id is invalid")
	}
	release, err := c.gate.Acquire(ctx)
	if err != nil {
		return tool.EditPlan{}, err
	}
	defer release()

	plan, err := c.mergePlan(ctx, sessionID, threadID)
	if err != nil {
		return tool.EditPlan{}, err
	}
	if plan.edit.ID != planID {
		return tool.EditPlan{}, errors.New("Chat merge plan is stale")
	}
	transactionID := "chat-merge-" + sessionID
	if err := c.journal.Begin(transactionID); err != nil {
		return tool.EditPlan{}, err
	}
	committed := false
	defer func() {
		if committed {
			return
		}
		receipt, rollbackErr := c.journal.Rollback(context.WithoutCancel(ctx), transactionID)
		if len(receipt.Conflicts) != 0 {
			rollbackErr = errors.Join(
				rollbackErr,
				fmt.Errorf("Chat merge rollback left %d conflict(s)", len(receipt.Conflicts)),
			)
		}
		resultErr = errors.Join(resultErr, rollbackErr)
	}()
	for _, path := range plan.paths {
		if err := c.journal.Before(ctx, filepath.Join(c.repository, filepath.FromSlash(path))); err != nil {
			return tool.EditPlan{}, err
		}
	}
	applyContext := workspacejournal.WithExpectedWrites(ctx, plan.expected)
	for index, batch := range plan.batches {
		if _, _, err := c.parent.Apply(applyContext, batch, false); err != nil {
			return tool.EditPlan{}, fmt.Errorf(
				"apply Chat merge batch %d/%d: %w",
				index+1, len(plan.batches), err,
			)
		}
	}
	for _, path := range plan.paths {
		if err := c.journal.After(filepath.Join(c.repository, filepath.FromSlash(path))); err != nil {
			return tool.EditPlan{}, err
		}
	}
	if err := c.journal.Commit(transactionID); err != nil {
		committed = true
		return tool.EditPlan{}, err
	}
	committed = true
	if err := c.syncChatWorktreeFromParent(
		plan.workspace.worktree.Path, plan.paths,
	); err != nil {
		return tool.EditPlan{}, fmt.Errorf(
			"Chat changes reached the main workspace but worktree refresh failed: %w", err,
		)
	}
	if err := c.commitBaseline(ctx, plan.workspace.worktree.Path, plan.paths); err != nil {
		return tool.EditPlan{}, fmt.Errorf(
			"Chat changes reached the main workspace but baseline refresh failed: %w", err,
		)
	}
	return plan.edit, nil
}

type preparedChatMerge struct {
	workspace chatWorkspace
	edit      tool.EditPlan
	batches   [][]filetool.Change
	paths     []string
	expected  map[string]workspacejournal.Fingerprint
}

func (c *chatWorkspaces) mergePlan(
	ctx context.Context,
	sessionID string,
	threadID protocol.ThreadID,
) (preparedChatMerge, error) {
	workspace, err := c.workspace(sessionID, threadID)
	if err != nil {
		return preparedChatMerge{}, err
	}
	paths, err := c.changedPaths(ctx, workspace.worktree.Path)
	if err != nil {
		return preparedChatMerge{}, err
	}
	if len(paths) == 0 {
		return preparedChatMerge{}, app.ErrSessionWorkspaceClean
	}
	if len(paths) > maxChatMergeFiles {
		return preparedChatMerge{}, fmt.Errorf(
			"Chat merge has %d files; at most %d are allowed", len(paths), maxChatMergeFiles,
		)
	}
	changes := make([]filetool.Change, 0, len(paths))
	expected := make(map[string]workspacejournal.Fingerprint, len(paths))
	for _, path := range paths {
		parentPath := filepath.Join(c.repository, filepath.FromSlash(path))
		fingerprint, _, _, err := workspacejournal.Snapshot(parentPath)
		if err != nil {
			return preparedChatMerge{}, err
		}
		fingerprint.Path = parentPath
		expected[fingerprint.Path] = fingerprint
		change, required, err := c.mergeChatPath(
			ctx, workspace.worktree.Path, path,
		)
		if err != nil {
			return preparedChatMerge{}, err
		}
		if required {
			changes = append(changes, change)
		}
	}
	if len(changes) == 0 {
		return preparedChatMerge{}, app.ErrSessionWorkspaceClean
	}
	planContext := workspacejournal.WithExpectedWrites(ctx, expected)
	batches := chunkChatMergeChanges(changes)
	edit := tool.EditPlan{}
	digest := sha256.New()
	var diff strings.Builder
	for index, batch := range batches {
		batchPlan, err := c.parent.PlanApply(planContext, batch)
		if err != nil {
			return preparedChatMerge{}, fmt.Errorf(
				"plan Chat merge batch %d/%d: %w",
				index+1, len(batches), err,
			)
		}
		_, _ = digest.Write([]byte(batchPlan.ID))
		if diff.Len() != 0 && !strings.HasSuffix(diff.String(), "\n") {
			diff.WriteByte('\n')
		}
		diff.WriteString(batchPlan.Diff)
		if diff.Len() > maxChatMergeDiffBytes {
			return preparedChatMerge{}, fmt.Errorf(
				"Chat merge diff exceeds %d bytes", maxChatMergeDiffBytes,
			)
		}
		for _, file := range batchPlan.Files {
			edit.Files = append(edit.Files, compactChatMergePlanFile(file))
		}
	}
	edit.ID = hex.EncodeToString(digest.Sum(nil))
	edit.Diff = diff.String()
	return preparedChatMerge{
		workspace: workspace, edit: edit, batches: batches,
		paths: paths, expected: expected,
	}, nil
}

func chunkChatMergeChanges(changes []filetool.Change) [][]filetool.Change {
	batches := make([][]filetool.Change, 0, (len(changes)+chatMergeBatchFiles-1)/chatMergeBatchFiles)
	for start := 0; start < len(changes); start += chatMergeBatchFiles {
		end := min(start+chatMergeBatchFiles, len(changes))
		batches = append(batches, changes[start:end])
	}
	return batches
}

func compactChatMergePlanFile(file tool.EditPlanFile) tool.EditPlanFile {
	file.Before = ""
	file.After = ""
	return file
}

func (c *chatWorkspaces) register(value chatWorkspace) error {
	c.mu.Lock()
	if existing, ok := c.sessions[value.sessionID]; ok {
		c.mu.Unlock()
		if existing.threadID == value.threadID &&
			existing.worktree.Path == value.worktree.Path {
			return nil
		}
		return errors.New("Chat session is already bound to another worktree")
	}
	c.mu.Unlock()
	if err := c.threads.RegisterChild(value.threadID, app.ChildSpec{
		AgentID: value.sessionID, Role: "chat", Stance: string(subagent.StanceWrite),
		Workspace: value.worktree.Path,
	}); err != nil {
		return err
	}
	c.mu.Lock()
	c.sessions[value.sessionID] = value
	c.mu.Unlock()
	return nil
}

func (c *chatWorkspaces) workspace(
	sessionID string,
	threadID protocol.ThreadID,
) (chatWorkspace, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	value, ok := c.sessions[sessionID]
	if !ok {
		return chatWorkspace{}, errors.New("Chat session has no isolated worktree")
	}
	if value.threadID != threadID {
		return chatWorkspace{}, errors.New("Chat session thread identity mismatch")
	}
	return value, nil
}

func (c *chatWorkspaces) validateIdentity(
	sessionID string,
	threadID protocol.ThreadID,
) error {
	if c == nil {
		return errors.New("isolated Chat workspaces are unavailable")
	}
	if strings.TrimSpace(sessionID) == "" || threadID == "" {
		return errors.New("Chat session and thread ids are required")
	}
	return nil
}

func chatWorktreeID(sessionID string) string {
	sum := sha256.Sum256([]byte(sessionID))
	return "chat-" + hex.EncodeToString(sum[:16])
}

func (c *chatWorkspaces) snapshotParent(ctx context.Context, worktree string) error {
	diff, err := c.git(
		ctx, c.repository, "diff", "--binary", "--no-ext-diff", "HEAD",
		"--", ".", ":(exclude).codehelper",
	)
	if err != nil {
		return err
	}
	if diff != "" {
		file, err := os.CreateTemp(c.trees.root, "chat-parent-*.patch")
		if err != nil {
			return err
		}
		name := file.Name()
		if _, err := file.WriteString(diff); err != nil {
			_ = file.Close()
			_ = os.Remove(name)
			return err
		}
		if err := file.Close(); err != nil {
			_ = os.Remove(name)
			return err
		}
		defer os.Remove(name)
		if _, err := c.git(ctx, worktree, "apply", "--whitespace=nowarn", name); err != nil {
			return err
		}
	}
	untracked, err := c.git(
		ctx, c.repository, "ls-files", "--others", "--exclude-standard", "-z",
		"--", ".", ":(exclude).codehelper",
	)
	if err != nil {
		return err
	}
	for _, path := range splitNUL(untracked) {
		if err := copyRegularFile(c.repository, worktree, path); err != nil {
			return err
		}
	}
	return c.commitBaseline(ctx, worktree, nil)
}

func (c *chatWorkspaces) changedPaths(
	ctx context.Context,
	worktree string,
) ([]string, error) {
	tracked, err := c.git(
		ctx, worktree, "diff", "--name-only", "-z", "HEAD",
		"--", ".", ":(exclude).codehelper",
	)
	if err != nil {
		return nil, err
	}
	untracked, err := c.git(
		ctx, worktree, "ls-files", "--others", "--exclude-standard", "-z",
		"--", ".", ":(exclude).codehelper",
	)
	if err != nil {
		return nil, err
	}
	unique := make(map[string]struct{})
	for _, path := range append(splitNUL(tracked), splitNUL(untracked)...) {
		path = filepath.ToSlash(filepath.Clean(path))
		if path == "." || path == ".codehelper" || strings.HasPrefix(path, ".codehelper/") {
			continue
		}
		unique[path] = struct{}{}
	}
	result := make([]string, 0, len(unique))
	for path := range unique {
		result = append(result, path)
	}
	sort.Strings(result)
	return result, nil
}

type chatMergeFile struct {
	exists bool
	data   []byte
}

func (c *chatWorkspaces) mergeChatPath(
	ctx context.Context,
	worktree string,
	path string,
) (filetool.Change, bool, error) {
	base, err := c.chatBaselineFile(ctx, worktree, path)
	if err != nil {
		return filetool.Change{}, false, err
	}
	parent, err := readChatMergeFile(
		filepath.Join(c.repository, filepath.FromSlash(path)),
	)
	if err != nil {
		return filetool.Change{}, false, err
	}
	child, err := readChatMergeFile(
		filepath.Join(worktree, filepath.FromSlash(path)),
	)
	if err != nil {
		return filetool.Change{}, false, err
	}
	if equalChatMergeFile(parent, child) {
		return filetool.Change{}, false, nil
	}
	if equalChatMergeFile(parent, base) {
		return chatMergeChange(path, child), true, nil
	}
	if equalChatMergeFile(child, base) {
		return filetool.Change{}, false, nil
	}
	if !base.exists || !parent.exists || !child.exists {
		return filetool.Change{}, false, fmt.Errorf(
			"Chat merge conflict on %s: main workspace drifted with overlapping changes",
			path,
		)
	}
	merged, err := c.mergeChatText(ctx, path, parent.data, base.data, child.data)
	if err != nil {
		return filetool.Change{}, false, err
	}
	desired := chatMergeFile{exists: true, data: merged}
	if equalChatMergeFile(parent, desired) {
		return filetool.Change{}, false, nil
	}
	return chatMergeChange(path, desired), true, nil
}

func (c *chatWorkspaces) chatBaselineFile(
	ctx context.Context,
	worktree string,
	path string,
) (chatMergeFile, error) {
	result, err := process.Run(ctx, process.Options{
		Path: gitExecutable(),
		Args: managedGitArguments([]string{"show", "HEAD:" + path}),
		Dir:  worktree,
	})
	if err != nil {
		return chatMergeFile{}, err
	}
	if result.ExitCode != 0 {
		return chatMergeFile{}, nil
	}
	data := []byte(result.Stdout)
	if !utf8.Valid(data) {
		return chatMergeFile{}, fmt.Errorf(
			"Chat merge baseline %q is not UTF-8 text", path,
		)
	}
	return chatMergeFile{exists: true, data: data}, nil
}

func readChatMergeFile(path string) (chatMergeFile, error) {
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if !utf8.Valid(data) {
			return chatMergeFile{}, fmt.Errorf(
				"Chat merge path %q is not UTF-8 text", path,
			)
		}
		return chatMergeFile{exists: true, data: data}, nil
	case errors.Is(err, os.ErrNotExist):
		return chatMergeFile{}, nil
	default:
		return chatMergeFile{}, err
	}
}

func equalChatMergeFile(left, right chatMergeFile) bool {
	return left.exists == right.exists &&
		(!left.exists || bytes.Equal(left.data, right.data))
}

func chatMergeChange(path string, file chatMergeFile) filetool.Change {
	if !file.exists {
		return filetool.Change{Op: "delete", Path: path}
	}
	return filetool.Change{Op: "write", Path: path, Content: string(file.data)}
}

func (c *chatWorkspaces) mergeChatText(
	ctx context.Context,
	path string,
	parent []byte,
	base []byte,
	child []byte,
) ([]byte, error) {
	directory, err := os.MkdirTemp(c.trees.root, "chat-merge-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(directory)
	files := []struct {
		name string
		data []byte
	}{
		{name: "parent", data: parent},
		{name: "base", data: base},
		{name: "child", data: child},
	}
	for _, file := range files {
		if err := os.WriteFile(
			filepath.Join(directory, file.name), file.data, 0o600,
		); err != nil {
			return nil, err
		}
	}
	result, err := process.Run(ctx, process.Options{
		Path: gitExecutable(),
		Args: managedGitArguments([]string{
			"merge-file", "-p",
			filepath.Join(directory, "parent"),
			filepath.Join(directory, "base"),
			filepath.Join(directory, "child"),
		}),
		Dir: c.repository,
	})
	if err != nil {
		return nil, err
	}
	switch result.ExitCode {
	case 0:
		return []byte(result.Stdout), nil
	case 1:
		return nil, fmt.Errorf(
			"Chat merge conflict on %s: main workspace drifted with overlapping changes",
			path,
		)
	default:
		return nil, fmt.Errorf(
			"Chat merge failed on %s: %s",
			path, strings.TrimSpace(result.Stderr),
		)
	}
}

func (c *chatWorkspaces) syncChatWorktreeFromParent(
	worktree string,
	paths []string,
) error {
	for _, path := range paths {
		source := filepath.Join(c.repository, filepath.FromSlash(path))
		target := filepath.Join(worktree, filepath.FromSlash(path))
		_, err := os.Lstat(source)
		switch {
		case err == nil:
			if err := copyRegularFile(c.repository, worktree, path); err != nil {
				return err
			}
		case errors.Is(err, os.ErrNotExist):
			if err := os.Remove(target); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
		default:
			return err
		}
	}
	return nil
}

func (c *chatWorkspaces) commitBaseline(
	ctx context.Context,
	worktree string,
	paths []string,
) error {
	arguments := []string{"add", "-A", "--"}
	if len(paths) == 0 {
		arguments = append(arguments, ".", ":(exclude).codehelper")
	} else {
		arguments = append(arguments, paths...)
	}
	if _, err := c.git(ctx, worktree, arguments...); err != nil {
		return err
	}
	_, err := c.git(
		ctx, worktree,
		"-c", "user.name=CodeHelper", "-c", "user.email=codehelper@localhost",
		"commit", "--allow-empty", "--no-gpg-sign", "-m", "codehelper chat baseline",
	)
	return err
}

func (c *chatWorkspaces) git(
	ctx context.Context,
	directory string,
	arguments ...string,
) (string, error) {
	result, err := process.Run(ctx, process.Options{
		Path: gitExecutable(), Args: managedGitArguments(arguments), Dir: directory,
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

func splitNUL(value string) []string {
	parts := strings.Split(value, "\x00")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}

func copyRegularFile(sourceRoot, targetRoot, relative string) error {
	source := filepath.Join(sourceRoot, filepath.FromSlash(relative))
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("untracked Chat baseline path %q is not a regular file", relative)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	target := filepath.Join(targetRoot, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	return os.WriteFile(target, data, info.Mode().Perm())
}
