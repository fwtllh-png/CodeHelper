package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	filetool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/file"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

// mergeInput is the model-visible agent_merge payload. ExpandArguments fills
// changes from the child's worktree so Guard can journal write paths.
type mergeInput struct {
	Op      string            `json:"op,omitempty"`
	AgentID string            `json:"agent_id"`
	DryRun  *bool             `json:"dry_run"`
	Paths   []string          `json:"paths"`
	Changes []filetool.Change `json:"changes"`
}

func (o *operation) ExpandArguments(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if o == nil || o.tools == nil {
		return raw, nil
	}
	if o.kind == "agent" {
		var input struct {
			Op string `json:"op"`
		}
		if err := json.Unmarshal(raw, &input); err != nil {
			return nil, err
		}
		if strings.TrimSpace(input.Op) != "merge" {
			return raw, nil
		}
	} else if o.kind != "agent_merge" {
		return raw, nil
	}
	return o.tools.expandMerge(ctx, raw)
}

func (t *Tool) expandMerge(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	var input mergeInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		return nil, errors.New("agent_id is required")
	}
	plan, err := t.planMerge(ctx, agentID, input.Paths)
	if err != nil {
		return nil, err
	}
	dryRun := true
	if input.DryRun != nil {
		dryRun = *input.DryRun
	}
	out := mergeInput{
		Op: strings.TrimSpace(input.Op), AgentID: agentID,
		DryRun: &dryRun, Paths: plan.paths, Changes: plan.changes,
	}
	return json.Marshal(out)
}

type mergePlan struct {
	paths   []string
	changes []filetool.Change
}

func (t *Tool) planMerge(ctx context.Context, agentID string, filter []string) (mergePlan, error) {
	if t.files == nil {
		return mergePlan{}, errors.New("agent_merge requires parent file tools")
	}
	workspace := strings.TrimSpace(t.workspace)
	if workspace == "" {
		return mergePlan{}, errors.New("agent_merge requires parent workspace")
	}
	agent, ok := t.manager.Agent(agentID)
	if !ok {
		if _, hasResult := t.manager.Result(agentID); hasResult {
			return mergePlan{}, fmt.Errorf("agent %s is closed; merge before agent_close", agentID)
		}
		return mergePlan{}, fmt.Errorf("agent %s not found", agentID)
	}
	if agent.Closed || agent.Status == subagent.StatusShutdown {
		return mergePlan{}, fmt.Errorf("agent %s is closed; merge before agent_close", agentID)
	}
	if !agent.Isolated || strings.TrimSpace(agent.Worktree) == "" {
		return mergePlan{}, fmt.Errorf("agent %s has no isolated worktree to merge", agentID)
	}
	if strings.TrimSpace(agent.BaseRev) == "" {
		return mergePlan{}, fmt.Errorf("agent %s has no base revision for conflict detection", agentID)
	}
	result, ok := t.manager.Result(agentID)
	if !ok {
		return mergePlan{}, fmt.Errorf("agent %s has no settled result yet; wait for the child turn", agentID)
	}
	if !subagent.IsTerminal(result.Status) {
		return mergePlan{}, fmt.Errorf("agent %s is still %s", agentID, result.Status)
	}
	paths := result.WritePaths()
	if len(filter) != 0 {
		allowed := make(map[string]struct{}, len(filter))
		for _, path := range filter {
			path = strings.TrimSpace(path)
			if path == "" {
				continue
			}
			allowed[path] = struct{}{}
		}
		filtered := make([]string, 0, len(paths))
		for _, path := range paths {
			if _, ok := allowed[path]; ok {
				filtered = append(filtered, path)
			}
		}
		paths = filtered
	}
	if len(paths) == 0 {
		return mergePlan{}, errors.New("nothing to merge: child wrote no matching paths")
	}
	kinds := changeKinds(result)
	changes := make([]filetool.Change, 0, len(paths))
	for _, path := range paths {
		if err := validateMergePath(path); err != nil {
			return mergePlan{}, err
		}
		if owner, claimed := t.manager.WriteOwner(path); claimed && owner != agentID {
			return mergePlan{}, fmt.Errorf(
				"merge conflict on %s: also claimed by %s", path, owner,
			)
		}
		if err := checkBaseline(ctx, workspace, agent.Worktree, agent.BaseRev, path); err != nil {
			return mergePlan{}, err
		}
		kind := kinds[path]
		switch kind {
		case "deleted":
			changes = append(changes, filetool.Change{Op: "delete", Path: path})
		default:
			body, err := os.ReadFile(filepath.Join(agent.Worktree, filepath.FromSlash(path)))
			if err != nil {
				if os.IsNotExist(err) && kind == "" {
					changes = append(changes, filetool.Change{Op: "delete", Path: path})
					continue
				}
				return mergePlan{}, fmt.Errorf("read child %s: %w", path, err)
			}
			changes = append(changes, filetool.Change{
				Op: "write", Path: path, Content: string(body),
			})
		}
	}
	return mergePlan{paths: paths, changes: changes}, nil
}

func (t *Tool) merge(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	if t.files == nil {
		return tool.Result{}, errors.New("agent_merge requires parent file tools")
	}
	var input mergeInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	agentID := strings.TrimSpace(input.AgentID)
	if agentID == "" {
		return tool.Result{}, errors.New("agent_id is required")
	}
	dryRun := true
	if input.DryRun != nil {
		dryRun = *input.DryRun
	}
	changes := input.Changes
	if len(changes) == 0 {
		// ExpandArguments normally fills changes; allow direct Execute in tests.
		plan, err := t.planMerge(ctx, agentID, input.Paths)
		if err != nil {
			return tool.Result{}, err
		}
		changes = plan.changes
	}
	applied, diff, err := t.files.Apply(ctx, changes, dryRun)
	if err != nil {
		return tool.Result{}, err
	}
	result := filetool.ResultFromApply(applied, diff, dryRun)
	if result.Metadata == nil {
		result.Metadata = map[string]any{}
	}
	result.Metadata["agent_id"] = agentID
	result.Metadata["merged"] = !dryRun
	result.Metadata["dry_run"] = dryRun
	return result, nil
}

func changeKinds(result subagent.Result) map[string]string {
	kinds := make(map[string]string, len(result.Diff))
	for _, change := range result.Diff {
		path := strings.TrimSpace(change.Path)
		if path == "" {
			continue
		}
		kinds[path] = change.Kind
	}
	return kinds
}

func validateMergePath(path string) error {
	clean := filepath.Clean(path)
	if clean == "." || clean == "" || filepath.IsAbs(clean) ||
		strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return fmt.Errorf("refusing to merge unsafe path %q", path)
	}
	return nil
}

// checkBaseline compares the parent workspace file to the child's spawn-time
// revision. Content equality (Exists + SHA256) is the D8 level-2 gate.
func checkBaseline(ctx context.Context, workspace, worktree, baseRev, relPath string) error {
	parentPath := filepath.Join(workspace, filepath.FromSlash(relPath))
	parent, _, _, err := workspacejournal.Snapshot(parentPath)
	if err != nil {
		return fmt.Errorf("fingerprint parent %s: %w", relPath, err)
	}
	baselineExists, baselineHash, err := gitBlobHash(ctx, worktree, baseRev, relPath)
	if err != nil {
		return err
	}
	if parent.Exists != baselineExists {
		return fmt.Errorf(
			"merge conflict on %s: parent exists=%v but child base exists=%v",
			relPath, parent.Exists, baselineExists,
		)
	}
	if parent.Exists && parent.SHA256 != baselineHash {
		return fmt.Errorf(
			"merge conflict on %s: parent drifted from child base revision",
			relPath,
		)
	}
	return nil
}

func gitBlobHash(ctx context.Context, worktree, baseRev, relPath string) (bool, string, error) {
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > 30*time.Second {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	revision := strings.TrimSpace(baseRev)
	baseSpec := revision + "^{commit}"
	base, err := process.Run(ctx, process.Options{
		Path: gitMergeExecutable(),
		Args: []string{"cat-file", "-e", baseSpec},
		Dir:  worktree,
	})
	if err != nil {
		return false, "", fmt.Errorf("git cat-file %s: %w", baseSpec, err)
	}
	if base.ExitCode != 0 {
		return false, "", gitCommandError("cat-file", baseSpec, base)
	}
	spec := revision + ":" + filepath.ToSlash(relPath)
	exists, err := process.Run(ctx, process.Options{
		Path: gitMergeExecutable(),
		Args: []string{"cat-file", "-e", spec},
		Dir:  worktree,
	})
	if err != nil {
		return false, "", fmt.Errorf("git cat-file %s: %w", spec, err)
	}
	if exists.ExitCode != 0 {
		return false, "", nil
	}
	result, err := process.Run(ctx, process.Options{
		Path: gitMergeExecutable(),
		Args: []string{"show", spec},
		Dir:  worktree,
	})
	if err != nil {
		return false, "", fmt.Errorf("git show %s: %w", spec, err)
	}
	if result.ExitCode != 0 {
		return false, "", gitCommandError("show", spec, result)
	}
	sum := sha256.Sum256([]byte(result.Stdout))
	return true, hex.EncodeToString(sum[:]), nil
}

func gitCommandError(command, spec string, result process.Result) error {
	message := strings.TrimSpace(result.Stderr)
	if message == "" {
		message = strings.TrimSpace(result.Stdout)
	}
	return fmt.Errorf("git %s %s exited with code %d: %s", command, spec, result.ExitCode, message)
}

func gitMergeExecutable() string {
	for _, candidate := range []string{
		"/Library/Developer/CommandLineTools/usr/bin/git",
		"/Applications/Xcode.app/Contents/Developer/usr/bin/git",
	} {
		info, err := os.Stat(candidate)
		if err == nil && info.Mode().IsRegular() && info.Mode().Perm()&0o111 != 0 {
			return candidate
		}
	}
	return "git"
}
