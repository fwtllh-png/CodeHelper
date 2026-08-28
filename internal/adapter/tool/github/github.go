// Package github exposes GitHub/GitLab context and write tools plus local PR attempts.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const responseLimit = 2 << 20

type Options struct {
	Workspace string
	Backend   sandbox.Backend
	BaseURL   string
	Token     string
	Client    *http.Client
}

type client struct {
	baseURL   string
	token     string
	http      *http.Client
	workspace *sandbox.Workspace
	backend   sandbox.Backend
	attempts  *attemptStore
}

type executor struct {
	client *client
	name   string
}

func Register(registry *tool.Registry, options Options) error {
	if registry == nil {
		return errors.New("github tool registry is required")
	}
	root := strings.TrimSpace(options.Workspace)
	if root == "" {
		root = "."
	}
	backend := options.Backend
	if backend != nil {
		bound, err := sandbox.BindPolicy(backend, sandbox.Options{WorkspaceRoot: root})
		if err != nil {
			return err
		}
		backend = bound
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return err
	}
	baseURL := strings.TrimRight(options.BaseURL, "/")
	if baseURL == "" {
		baseURL = strings.TrimRight(os.Getenv("CODEHELPER_HOSTED_GIT_URL"), "/")
	}
	token := options.Token
	if token == "" {
		token = os.Getenv("CODEHELPER_HOSTED_GIT_TOKEN")
	}
	httpClient := options.Client
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	c := &client{
		baseURL: baseURL, token: token, http: httpClient,
		workspace: workspace, backend: backend,
		attempts: newAttemptStore(filepath.Join(workspace.Root(), ".codehelper", "pr-attempts.json")),
	}
	for _, name := range []string{
		"github_issue_context", "github_pr_context",
		"github_comment", "github_close_issue", "github_close_pr",
		"pr_attempt_record", "pr_attempt_list", "pr_attempt_read", "pr_attempt_preflight",
	} {
		if err := registry.Register(&executor{client: c, name: name}); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) Descriptor() tool.Descriptor {
	switch e.name {
	case "github_issue_context":
		return readDescriptor(e.name, "Fetch a GitHub/GitLab issue with optional comments as compact context.")
	case "github_pr_context":
		return readDescriptor(e.name, "Fetch a GitHub pull request / GitLab merge request with optional reviews.")
	case "github_comment":
		return writeDescriptor(e.name, "Post a comment on an issue or pull request. Approval-required.")
	case "github_close_issue":
		return writeDescriptor(e.name, "Close an issue after acceptance criteria and evidence. Rejects dirty worktrees by default.")
	case "github_close_pr":
		return writeDescriptor(e.name, "Close a pull/merge request after acceptance criteria and evidence. Rejects dirty worktrees by default.")
	case "pr_attempt_record":
		return tool.Descriptor{
			Name: e.name, Description: "Record a local PR attempt (patch + metadata) for later preflight.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "pr_attempt", Field: "attempt_id", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"attempt_id": map[string]any{"type": "string"},
					"repository": map[string]any{"type": "string", "minLength": float64(1)},
					"title":      map[string]any{"type": "string"},
					"base":       map[string]any{"type": "string"},
					"head":       map[string]any{"type": "string"},
					"patch":      map[string]any{"type": "string", "minLength": float64(1)},
					"task_id":    map[string]any{"type": "string"},
				},
				"required":             []string{"repository", "patch"},
				"additionalProperties": false,
			},
		}
	case "pr_attempt_list":
		return tool.Descriptor{
			Name: e.name, Description: "List recorded local PR attempts.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "pr_attempt", ID: "list", Access: tool.AccessRead,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"limit": map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(200)},
				},
				"additionalProperties": false,
			},
		}
	case "pr_attempt_read":
		return tool.Descriptor{
			Name: e.name, Description: "Read a recorded local PR attempt.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "pr_attempt", Field: "attempt_id", Access: tool.AccessRead,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"attempt_id": map[string]any{"type": "string", "minLength": float64(1)},
				},
				"required":             []string{"attempt_id"},
				"additionalProperties": false,
			},
		}
	case "pr_attempt_preflight":
		return tool.Descriptor{
			Name:        e.name,
			Description: "Run git apply --check on a PR attempt patch without mutating the worktree.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessTree, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxStrong, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{
				{Kind: "pr_attempt", Field: "attempt_id", Access: tool.AccessRead},
				{Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true},
			}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"attempt_id": map[string]any{"type": "string"},
					"patch":      map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
		}
	default:
		return tool.Descriptor{Name: e.name, Availability: tool.AvailabilityUnavailable}
	}
}

func readDescriptor(name, description string) tool.Descriptor {
	return tool.Descriptor{
		Name: name, Description: description,
		Visibility: tool.VisibleModel, Capability: tool.CapabilityNetwork,
		AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "repository", Field: "repository", Access: tool.AccessRead,
		}}},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"provider":   map[string]any{"type": "string", "enum": []any{"github", "gitlab"}},
				"repository": map[string]any{"type": "string", "minLength": float64(1)},
				"number":     map[string]any{"type": "integer", "minimum": float64(1)},
				"include": map[string]any{
					"type": "array",
					"items": map[string]any{
						"type": "string", "enum": []any{"body", "comments", "files", "reviews"},
					},
				},
			},
			"required":             []string{"provider", "repository", "number"},
			"additionalProperties": false,
		},
	}
}

func writeDescriptor(name, description string) tool.Descriptor {
	properties := map[string]any{
		"provider":   map[string]any{"type": "string", "enum": []any{"github", "gitlab"}},
		"repository": map[string]any{"type": "string", "minLength": float64(1)},
		"number":     map[string]any{"type": "integer", "minimum": float64(1)},
	}
	required := []string{"provider", "repository", "number"}
	switch name {
	case "github_comment":
		properties["target"] = map[string]any{"type": "string", "enum": []any{"issue", "pull_request"}}
		properties["body"] = map[string]any{"type": "string", "minLength": float64(1)}
		required = append(required, "body")
	case "github_close_issue", "github_close_pr":
		properties["acceptance_criteria"] = map[string]any{
			"type": "array", "items": map[string]any{"type": "string", "minLength": float64(1)},
			"minItems": float64(1),
		}
		properties["evidence"] = map[string]any{
			"type": "array", "items": map[string]any{"type": "string", "minLength": float64(1)},
			"minItems": float64(1),
		}
		properties["comment"] = map[string]any{"type": "string"}
		properties["allow_dirty"] = map[string]any{"type": "boolean"}
		required = append(required, "acceptance_criteria", "evidence")
	}
	return tool.Descriptor{
		Name: name, Description: description,
		Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "repository", Field: "repository", Access: tool.AccessWrite,
		}}},
		InputSchema: map[string]any{
			"type": "object", "properties": properties, "required": required,
			"additionalProperties": false,
		},
	}
}

func (e *executor) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	switch e.name {
	case "github_issue_context":
		return e.client.issueContext(ctx, raw)
	case "github_pr_context":
		return e.client.prContext(ctx, raw)
	case "github_comment":
		return e.client.comment(ctx, raw)
	case "github_close_issue":
		return e.client.closeIssue(ctx, raw)
	case "github_close_pr":
		return e.client.closePR(ctx, raw)
	case "pr_attempt_record":
		return e.client.recordAttempt(raw)
	case "pr_attempt_list":
		return e.client.listAttempts(raw)
	case "pr_attempt_read":
		return e.client.readAttempt(raw)
	case "pr_attempt_preflight":
		return e.client.preflight(ctx, raw)
	default:
		return tool.Result{}, fmt.Errorf("unknown github tool %q", e.name)
	}
}

func (*executor) ExecutionDisposition() tool.ExecutionDisposition {
	return tool.DispositionWaitForTeardown
}

func (e *executor) ExecuteOutcome(
	ctx context.Context,
	raw json.RawMessage,
) (tool.Result, tool.Outcome, error) {
	return typed.ExecuteOutcome(ctx, e, raw)
}

type contextInput struct {
	Provider   string   `json:"provider"`
	Repository string   `json:"repository"`
	Number     int      `json:"number"`
	Include    []string `json:"include"`
}

func (c *client) issueContext(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	return c.context(ctx, raw, false)
}

func (c *client) prContext(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	return c.context(ctx, raw, true)
}

func (c *client) context(
	ctx context.Context,
	raw json.RawMessage,
	pullRequest bool,
) (tool.Result, error) {
	var input contextInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if err := validateRepo(input.Provider, input.Repository, input.Number); err != nil {
		return tool.Result{}, err
	}
	if c.baseURL == "" {
		return failure("unavailable", 0, "hosted Git endpoint is not configured"), nil
	}
	include := setOf(input.Include)
	if len(include) == 0 {
		include = map[string]bool{"body": true}
	}
	primaryKey, relatedKey := "issue", "comments"
	primaryPath := issuePath(input.Provider, input.Repository, input.Number)
	relatedPath := issueCommentsPath(input.Provider, input.Repository, input.Number)
	if pullRequest {
		primaryKey, relatedKey = "pull_request", "reviews"
		primaryPath = prPath(input.Provider, input.Repository, input.Number)
		relatedPath = reviewsPath(input.Provider, input.Repository, input.Number)
	}
	if len(input.Include) == 0 {
		include[relatedKey] = true
	}
	body, status, category, err := c.request(
		ctx,
		http.MethodGet,
		input.Provider,
		primaryPath,
		nil,
	)
	if err != nil {
		return tool.Result{}, err
	}
	if category != "" {
		return failure(category, status, string(body)), nil
	}
	payload := map[string]any{primaryKey: json.RawMessage(body)}
	if include[relatedKey] {
		related, relatedStatus, relatedCategory, err := c.request(
			ctx,
			http.MethodGet,
			input.Provider,
			relatedPath,
			nil,
		)
		if err != nil {
			return tool.Result{}, err
		}
		if relatedCategory == "" {
			payload[relatedKey] = json.RawMessage(related)
		} else {
			payload[relatedKey+"_error"] = map[string]any{
				"category": relatedCategory,
				"status":   relatedStatus,
			}
		}
	}
	return compactResult(payload, map[string]any{
		"provider": input.Provider, "repository": input.Repository, "number": input.Number,
	})
}

func (c *client) comment(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Provider   string `json:"provider"`
		Repository string `json:"repository"`
		Number     int    `json:"number"`
		Target     string `json:"target"`
		Body       string `json:"body"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if err := validateRepo(input.Provider, input.Repository, input.Number); err != nil {
		return tool.Result{}, err
	}
	if strings.TrimSpace(input.Body) == "" {
		return tool.Result{}, errors.New("body is required")
	}
	if c.baseURL == "" {
		return failure("unavailable", 0, "hosted Git endpoint is not configured"), nil
	}
	target := input.Target
	if target == "" {
		target = "issue"
	}
	path := commentPath(input.Provider, input.Repository, input.Number, target)
	payload, _ := json.Marshal(map[string]string{"body": input.Body})
	body, status, category, err := c.request(ctx, http.MethodPost, input.Provider, path, payload)
	if err != nil {
		return tool.Result{}, err
	}
	if category != "" {
		return failure(category, status, string(body)), nil
	}
	return tool.Result{
		Content: string(body),
		Metadata: map[string]any{
			"provider": input.Provider, "repository": input.Repository,
			"number": input.Number, "target": target, "status_code": status,
		},
	}, nil
}

func (c *client) closeIssue(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	return c.closeResource(ctx, raw, "issue")
}

func (c *client) closePR(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	return c.closeResource(ctx, raw, "pull_request")
}

func (c *client) closeResource(ctx context.Context, raw json.RawMessage, kind string) (tool.Result, error) {
	var input struct {
		Provider           string   `json:"provider"`
		Repository         string   `json:"repository"`
		Number             int      `json:"number"`
		AcceptanceCriteria []string `json:"acceptance_criteria"`
		Evidence           []string `json:"evidence"`
		Comment            string   `json:"comment"`
		AllowDirty         bool     `json:"allow_dirty"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if err := validateRepo(input.Provider, input.Repository, input.Number); err != nil {
		return tool.Result{}, err
	}
	if len(input.AcceptanceCriteria) == 0 {
		return tool.Result{}, errors.New("acceptance_criteria is required")
	}
	if len(input.Evidence) == 0 {
		return tool.Result{}, errors.New("evidence is required")
	}
	if !input.AllowDirty {
		dirty, err := c.worktreeDirty(ctx)
		if err != nil {
			return tool.Result{}, err
		}
		if dirty {
			return failure("dirty_worktree", 0, "refusing to close while the worktree is dirty"), nil
		}
	}
	if c.baseURL == "" {
		return failure("unavailable", 0, "hosted Git endpoint is not configured"), nil
	}
	if strings.TrimSpace(input.Comment) != "" {
		path := commentPath(input.Provider, input.Repository, input.Number, "issue")
		if kind == "pull_request" && input.Provider == "github" {
			path = commentPath(input.Provider, input.Repository, input.Number, "pull_request")
		}
		payload, _ := json.Marshal(map[string]string{"body": input.Comment})
		if _, status, category, err := c.request(ctx, http.MethodPost, input.Provider, path, payload); err != nil {
			return tool.Result{}, err
		} else if category != "" {
			return failure(category, status, "failed to post close comment"), nil
		}
	}
	path := closePath(input.Provider, input.Repository, input.Number, kind)
	payload, _ := json.Marshal(closePayload(input.Provider, kind))
	body, status, category, err := c.request(ctx, http.MethodPatch, input.Provider, path, payload)
	if err != nil {
		return tool.Result{}, err
	}
	if category != "" {
		return failure(category, status, string(body)), nil
	}
	return tool.Result{
		Content: string(body),
		Metadata: map[string]any{
			"provider": input.Provider, "repository": input.Repository, "number": input.Number,
			"kind": kind, "acceptance_criteria": input.AcceptanceCriteria, "evidence": input.Evidence,
			"status_code": status,
		},
	}, nil
}

type Attempt struct {
	ID         string    `json:"attempt_id"`
	Repository string    `json:"repository"`
	Title      string    `json:"title,omitempty"`
	Base       string    `json:"base,omitempty"`
	Head       string    `json:"head,omitempty"`
	Patch      string    `json:"patch"`
	TaskID     string    `json:"task_id,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

type attemptStore struct {
	mu   sync.Mutex
	path string
	byID map[string]Attempt
}

func newAttemptStore(path string) *attemptStore {
	store := &attemptStore{path: path, byID: make(map[string]Attempt)}
	_ = store.load()
	return store
}

func (s *attemptStore) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var items []Attempt
	if err := json.Unmarshal(data, &items); err != nil {
		return err
	}
	for _, item := range items {
		s.byID[item.ID] = item
	}
	return nil
}

func (s *attemptStore) persist() error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	items := make([]Attempt, 0, len(s.byID))
	for _, item := range s.byID {
		items = append(items, item)
	}
	data, err := json.MarshalIndent(items, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(s.path, append(data, '\n'), 0o600)
}

func (c *client) recordAttempt(raw json.RawMessage) (tool.Result, error) {
	var input struct {
		AttemptID  string `json:"attempt_id"`
		Repository string `json:"repository"`
		Title      string `json:"title"`
		Base       string `json:"base"`
		Head       string `json:"head"`
		Patch      string `json:"patch"`
		TaskID     string `json:"task_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if strings.TrimSpace(input.Repository) == "" || strings.TrimSpace(input.Patch) == "" {
		return tool.Result{}, errors.New("repository and patch are required")
	}
	id := strings.TrimSpace(input.AttemptID)
	if id == "" {
		id = fmt.Sprintf("pra_%d", time.Now().UTC().UnixNano())
	}
	attempt := Attempt{
		ID: id, Repository: input.Repository, Title: input.Title,
		Base: input.Base, Head: input.Head, Patch: input.Patch,
		TaskID: input.TaskID, CreatedAt: time.Now().UTC(),
	}
	c.attempts.mu.Lock()
	c.attempts.byID[id] = attempt
	err := c.attempts.persist()
	c.attempts.mu.Unlock()
	if err != nil {
		return tool.Result{}, err
	}
	content, err := json.Marshal(map[string]any{
		"attempt_id": id, "repository": attempt.Repository, "task_id": attempt.TaskID,
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(content),
		Metadata: map[string]any{"attempt_id": id, "repository": attempt.Repository},
	}, nil
}

func (c *client) listAttempts(raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Limit int `json:"limit"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return tool.Result{}, err
		}
	}
	limit := input.Limit
	if limit <= 0 {
		limit = 50
	}
	c.attempts.mu.Lock()
	items := make([]Attempt, 0, len(c.attempts.byID))
	for _, item := range c.attempts.byID {
		items = append(items, item)
	}
	c.attempts.mu.Unlock()
	if len(items) > limit {
		items = items[:limit]
	}
	summaries := make([]map[string]any, 0, len(items))
	for _, item := range items {
		summaries = append(summaries, map[string]any{
			"attempt_id": item.ID, "repository": item.Repository, "title": item.Title,
			"task_id": item.TaskID, "created_at": item.CreatedAt.Format(time.RFC3339Nano),
		})
	}
	content, err := json.Marshal(summaries)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: string(content), Metadata: map[string]any{"count": len(summaries)}}, nil
}

func (c *client) readAttempt(raw json.RawMessage) (tool.Result, error) {
	var input struct {
		AttemptID string `json:"attempt_id"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	c.attempts.mu.Lock()
	attempt, ok := c.attempts.byID[strings.TrimSpace(input.AttemptID)]
	c.attempts.mu.Unlock()
	if !ok {
		return tool.Result{}, fmt.Errorf("pr attempt %q not found", input.AttemptID)
	}
	content, err := json.Marshal(attempt)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(content),
		Metadata: map[string]any{"attempt_id": attempt.ID},
	}, nil
}

func (c *client) preflight(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		AttemptID string `json:"attempt_id"`
		Patch     string `json:"patch"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	patch := strings.TrimSpace(input.Patch)
	if patch == "" {
		if strings.TrimSpace(input.AttemptID) == "" {
			return tool.Result{}, errors.New("attempt_id or patch is required")
		}
		c.attempts.mu.Lock()
		attempt, ok := c.attempts.byID[strings.TrimSpace(input.AttemptID)]
		c.attempts.mu.Unlock()
		if !ok {
			return tool.Result{}, fmt.Errorf("pr attempt %q not found", input.AttemptID)
		}
		patch = attempt.Patch
	}
	if c.backend == nil {
		return tool.Result{}, errors.New("sandbox backend is required for preflight")
	}
	directory, err := process.OpenPinnedDirectory(c.backend, c.workspace.Root())
	if err != nil {
		return tool.Result{}, err
	}
	defer directory.Close()
	check, err := process.NewCommand(ctx, process.Options{
		Path: "git", Args: []string{"apply", "--check", "--whitespace=nowarn", "-"},
		Dir: c.workspace.Root(), DirFile: directory, Sandbox: c.backend, RequireSandbox: true,
	})
	if err != nil {
		return tool.Result{}, err
	}
	check.Stdin = strings.NewReader(patch)
	output, err := check.CombinedOutput()
	if err != nil {
		return tool.Result{
			Content: strings.TrimSpace(string(output)),
			IsError: true,
			Metadata: map[string]any{
				"ok": false, "attempt_id": input.AttemptID, "mutated": false,
			},
		}, nil
	}
	return tool.Result{
		Content: "preflight ok",
		Metadata: map[string]any{
			"ok": true, "attempt_id": input.AttemptID, "mutated": false,
		},
	}, nil
}

func (c *client) worktreeDirty(ctx context.Context) (bool, error) {
	if c.backend == nil {
		return false, errors.New("sandbox backend is required for dirty-tree checks")
	}
	directory, err := process.OpenPinnedDirectory(c.backend, c.workspace.Root())
	if err != nil {
		return false, err
	}
	defer directory.Close()
	result, err := process.Run(ctx, process.Options{
		Path: "git", Args: []string{"status", "--porcelain"},
		Dir: c.workspace.Root(), DirFile: directory, Sandbox: c.backend, RequireSandbox: true,
	})
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(result.Stdout) != "", nil
}

func (c *client) request(
	ctx context.Context, method, provider, path string, payload []byte,
) ([]byte, int, string, error) {
	endpoint := c.baseURL + path
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, reader)
	if err != nil {
		return nil, 0, "", err
	}
	request.Header.Set("Accept", "application/json")
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		if provider == "gitlab" {
			request.Header.Set("PRIVATE-TOKEN", c.token)
		} else {
			request.Header.Set("Authorization", "Bearer "+c.token)
		}
	}
	response, err := c.http.Do(request)
	if err != nil {
		return nil, 0, "", fmt.Errorf("hosted Git request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, 0, "", fmt.Errorf("read hosted Git response: %w", err)
	}
	if len(body) > responseLimit {
		return []byte("hosted Git response exceeds 2 MiB"), response.StatusCode, "response_too_large", nil
	}
	return body, response.StatusCode, errorCategory(response), nil
}

func errorCategory(response *http.Response) string {
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return ""
	case response.StatusCode == http.StatusUnauthorized:
		return "authentication"
	case response.StatusCode == http.StatusTooManyRequests:
		return "rate_limited"
	case response.StatusCode == http.StatusForbidden:
		return "authorization"
	case response.StatusCode == http.StatusNotFound:
		return "not_found"
	case response.StatusCode >= 500:
		return "remote_failure"
	default:
		return "request_rejected"
	}
}

func failure(category string, status int, message string) tool.Result {
	return tool.Result{
		Content: message, IsError: true,
		Metadata: map[string]any{"error_category": category, "status_code": status},
	}
}

func compactResult(payload map[string]any, meta map[string]any) (tool.Result, error) {
	content, err := json.Marshal(payload)
	if err != nil {
		return tool.Result{}, err
	}
	meta["bytes"] = len(content)
	return tool.Result{Content: string(content), Metadata: meta}, nil
}

func validateRepo(provider, repository string, number int) error {
	if provider != "github" && provider != "gitlab" {
		return errors.New("provider must be github or gitlab")
	}
	if repository == "" || strings.ContainsAny(repository, "\x00\n\r?#") {
		return errors.New("invalid repository")
	}
	if number <= 0 {
		return errors.New("number must be positive")
	}
	return nil
}

func setOf(values []string) map[string]bool {
	out := map[string]bool{}
	for _, value := range values {
		out[value] = true
	}
	return out
}

func repoPath(provider, repository string) string {
	if provider == "gitlab" {
		return url.PathEscape(repository)
	}
	return repository
}

func issuePath(provider, repository string, number int) string {
	repo := repoPath(provider, repository)
	if provider == "gitlab" {
		return "/projects/" + repo + "/issues/" + strconv.Itoa(number)
	}
	return "/repos/" + repo + "/issues/" + strconv.Itoa(number)
}

func issueCommentsPath(provider, repository string, number int) string {
	repo := repoPath(provider, repository)
	if provider == "gitlab" {
		return "/projects/" + repo + "/issues/" + strconv.Itoa(number) + "/notes"
	}
	return "/repos/" + repo + "/issues/" + strconv.Itoa(number) + "/comments"
}

func prPath(provider, repository string, number int) string {
	repo := repoPath(provider, repository)
	if provider == "gitlab" {
		return "/projects/" + repo + "/merge_requests/" + strconv.Itoa(number)
	}
	return "/repos/" + repo + "/pulls/" + strconv.Itoa(number)
}

func reviewsPath(provider, repository string, number int) string {
	repo := repoPath(provider, repository)
	if provider == "gitlab" {
		return "/projects/" + repo + "/merge_requests/" + strconv.Itoa(number) + "/approvals"
	}
	return "/repos/" + repo + "/pulls/" + strconv.Itoa(number) + "/reviews"
}

func commentPath(provider, repository string, number int, target string) string {
	repo := repoPath(provider, repository)
	if provider == "gitlab" {
		if target == "pull_request" {
			return "/projects/" + repo + "/merge_requests/" + strconv.Itoa(number) + "/notes"
		}
		return "/projects/" + repo + "/issues/" + strconv.Itoa(number) + "/notes"
	}
	if target == "pull_request" {
		return "/repos/" + repo + "/issues/" + strconv.Itoa(number) + "/comments"
	}
	return "/repos/" + repo + "/issues/" + strconv.Itoa(number) + "/comments"
}

func closePath(provider, repository string, number int, kind string) string {
	repo := repoPath(provider, repository)
	if provider == "gitlab" {
		if kind == "pull_request" {
			return "/projects/" + repo + "/merge_requests/" + strconv.Itoa(number)
		}
		return "/projects/" + repo + "/issues/" + strconv.Itoa(number)
	}
	if kind == "pull_request" {
		return "/repos/" + repo + "/pulls/" + strconv.Itoa(number)
	}
	return "/repos/" + repo + "/issues/" + strconv.Itoa(number)
}

func closePayload(provider, kind string) map[string]any {
	if provider == "gitlab" {
		if kind == "pull_request" {
			return map[string]any{"state_event": "close"}
		}
		return map[string]any{"state_event": "close"}
	}
	if kind == "pull_request" {
		return map[string]any{"state": "closed"}
	}
	return map[string]any{"state": "closed"}
}
