// Package rlm exposes Recursive LM REPL tools backed by a sandboxed Python runner.
package rlm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/handle"
	rlmlib "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type Options struct {
	Store     *rlmlib.Store
	Handles   *handle.Store
	Governor  *rlmlib.Governor
	SubQuery  rlmlib.SubQueryClient
	SessionID string
	Root      string
	Backend   sandbox.Backend
	Workspace string
	Objects   []rlmlib.SessionObject
	Payloads  map[string]string
	Python    string
}

type Tools struct {
	store     *rlmlib.Store
	handles   *handle.Store
	governor  *rlmlib.Governor
	sessionID string
	available bool
}

type executor struct {
	tools *Tools
	name  string
}

func Register(registry *tool.Registry, options Options) error {
	if registry == nil {
		return errors.New("rlm tool registry is required")
	}
	if options.Handles == nil {
		return errors.New("rlm handle store is required")
	}
	sessionID := strings.TrimSpace(options.SessionID)
	if sessionID == "" {
		sessionID = "session-local"
	}
	store := options.Store
	if store == nil {
		root := strings.TrimSpace(options.Root)
		if root == "" {
			return errors.New("rlm root is required when store is nil")
		}
		workspaceRoot := strings.TrimSpace(options.Workspace)
		if workspaceRoot == "" {
			workspaceRoot = "."
		}
		backend := options.Backend
		if backend != nil {
			bound, err := sandbox.BindPolicy(backend, sandbox.Options{WorkspaceRoot: workspaceRoot})
			if err != nil {
				return err
			}
			backend = bound
		}
		workspace, err := sandbox.NewWorkspace(workspaceRoot)
		if err != nil {
			return err
		}
		store, err = rlmlib.NewStore(rlmlib.StoreOptions{
			Root: root, Python: options.Python, Backend: backend,
			Workspace: workspace, Objects: options.Objects, Payloads: options.Payloads,
			SubQuery: options.SubQuery, Governor: options.Governor,
		})
		if err != nil {
			return err
		}
	}
	governor := options.Governor
	if governor == nil {
		governor = rlmlib.NewGovernor(rlmlib.Limits{})
	}
	tools := &Tools{
		store: store, handles: options.Handles, governor: governor,
		sessionID: sessionID, available: store.PythonAvailable(),
	}
	for _, name := range []string{
		"rlm_session_objects", "rlm_open", "rlm_eval", "rlm_configure", "rlm_close",
	} {
		if err := registry.Register(&executor{tools: tools, name: name}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) Descriptor() tool.Descriptor {
	availability := tool.AvailabilityAvailable
	reason := ""
	if !e.tools.available && (e.name == "rlm_open" || e.name == "rlm_eval") {
		availability = tool.AvailabilityUnavailable
		reason = "python interpreter is unavailable"
	}
	switch e.name {
	case "rlm_session_objects":
		return tool.Descriptor{
			Name: "rlm_session_objects",
			Description: "List compact cards for the active prompt, transcript, and session refs " +
				"usable as session_object inputs to rlm_open.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "session", ID: "rlm_objects", Access: tool.AccessRead,
			}}},
			InputSchema: map[string]any{
				"type": "object", "properties": map[string]any{},
				"additionalProperties": false,
			},
		}
	case "rlm_open":
		return tool.Descriptor{
			Name: "rlm_open",
			Description: "Open a named sandboxed Python RLM session over exactly one of " +
				"file_path, content, url, or session_object. Binds _context/_ctx/content.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityProcess,
			AccessMode: tool.AccessTree, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxStrong,
			Availability:       availability, UnavailableReason: reason,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{
				{Kind: "rlm", Field: "name", Access: tool.AccessWrite},
				{Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true},
			}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name":           map[string]any{"type": "string", "minLength": float64(1)},
					"file_path":      map[string]any{"type": "string"},
					"content":        map[string]any{"type": "string"},
					"url":            map[string]any{"type": "string"},
					"session_object": map[string]any{"type": "string"},
				},
				"additionalProperties": false,
			},
		}
	case "rlm_eval":
		return tool.Descriptor{
			Name: "rlm_eval",
			Description: "Evaluate bounded Python in an open RLM session. Hard-timeout and truncate " +
				"output; large transcript is returned as a var_handle for handle_read. " +
				"Python helpers sub_query(prompt, slice=None), sub_query_batch(prompt, slices, " +
				"dependency_mode=\"independent\"), and sub_query_map(prompt, items) fan out through " +
				"the configured SubQueryClient (max 16 concurrent).",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityProcess,
			AccessMode: tool.AccessTree, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxStrong,
			Availability:       availability, UnavailableReason: reason,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{
				{Kind: "rlm", Field: "name", Access: tool.AccessWrite},
				{Kind: "process", ID: "workspace", Access: tool.AccessWrite, Tree: true},
			}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "minLength": float64(1)},
					"code": map[string]any{"type": "string", "minLength": float64(1)},
				},
				"required":             []string{"name", "code"},
				"additionalProperties": false,
			},
		}
	case "rlm_configure":
		return tool.Descriptor{
			Name:        "rlm_configure",
			Description: "Adjust RLM session timeout, output feedback, child depth, and sharing.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "rlm", Field: "name", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "minLength": float64(1)},
					"output_feedback": map[string]any{
						"type": "string", "enum": []any{"full", "metadata"},
					},
					"eval_timeout_secs": map[string]any{
						"type": "integer", "minimum": float64(1), "maximum": float64(600),
					},
					"sub_query_timeout_secs": map[string]any{
						"type": "integer", "minimum": float64(1), "maximum": float64(600),
					},
					"sub_rlm_max_depth": map[string]any{
						"type": "integer", "minimum": float64(0), "maximum": float64(3),
					},
					"share_session": map[string]any{"type": "boolean"},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		}
	case "rlm_close":
		return tool.Descriptor{
			Name:        "rlm_close",
			Description: "Close an RLM session, release the runtime, and return final stats.",
			Visibility:  tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "rlm", Field: "name", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{"type": "string", "minLength": float64(1)},
				},
				"required":             []string{"name"},
				"additionalProperties": false,
			},
		}
	default:
		return tool.Descriptor{Name: e.name, Availability: tool.AvailabilityUnavailable}
	}
}

func (e *executor) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	switch e.name {
	case "rlm_session_objects":
		return e.tools.sessionObjects(ctx, raw)
	case "rlm_open":
		return e.tools.open(ctx, raw)
	case "rlm_eval":
		return e.tools.eval(ctx, raw)
	case "rlm_configure":
		return e.tools.configure(ctx, raw)
	case "rlm_close":
		return e.tools.close(ctx, raw)
	default:
		return tool.Result{}, fmt.Errorf("unknown rlm tool %q", e.name)
	}
}

func (t *Tools) sessionObjects(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	objects := t.store.ListObjects()
	content, err := json.Marshal(objects)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(content),
		Metadata: map[string]any{"count": len(objects)},
	}, nil
}

func (t *Tools) open(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	if !t.available {
		return tool.Result{}, errors.New("python interpreter is unavailable")
	}
	var input struct {
		Name          string `json:"name"`
		FilePath      string `json:"file_path"`
		Content       string `json:"content"`
		URL           string `json:"url"`
		SessionObject string `json:"session_object"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	name := strings.TrimSpace(input.Name)
	if name == "" {
		name = "default"
	}
	kind, ref, body, err := t.loadContext(ctx, input.FilePath, input.Content, input.URL, input.SessionObject)
	if err != nil {
		return tool.Result{}, err
	}
	lease, err := t.governor.Admit(0, 0, 0)
	if err != nil {
		return tool.Result{}, err
	}
	defer t.governor.Release(lease)

	session, err := t.store.Open(name, kind, ref, body, 0)
	if err != nil {
		return tool.Result{}, err
	}
	if err := t.governor.Charge(1, 0); err != nil {
		_, _ = t.store.Close(name)
		return tool.Result{}, err
	}
	return summarizeSession(session, nil)
}

func (t *Tools) eval(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	if !t.available {
		return tool.Result{}, errors.New("python interpreter is unavailable")
	}
	var input struct {
		Name string `json:"name"`
		Code string `json:"code"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	current, err := t.store.Get(input.Name)
	if err != nil {
		return tool.Result{}, err
	}
	lease, err := t.governor.Admit(current.Depth, 0, 0)
	if err != nil {
		return tool.Result{}, err
	}
	defer t.governor.Release(lease)

	eval, session, err := t.store.Eval(ctx, input.Name, input.Code)
	if err != nil {
		return tool.Result{}, err
	}
	if err := t.governor.Charge(1, 0); err != nil {
		return tool.Result{}, err
	}
	handleName := filepath.ToSlash(filepath.Join("rlm-"+session.Name, "transcript"))
	varHandle, err := t.handles.PutText(t.sessionID, handleName, session.Transcript)
	if err != nil {
		return tool.Result{}, err
	}
	body := map[string]any{
		"name": session.Name, "eval_count": session.EvalCount,
		"classification": eval.Classification, "exit_code": eval.ExitCode,
		"timed_out": eval.TimedOut, "truncated": eval.Truncated,
		"duration_ms": eval.DurationMS, "transcript_handle": varHandle,
	}
	if session.Config.OutputFeedback != "metadata" {
		body["stdout"] = eval.Stdout
		body["stderr"] = eval.Stderr
	}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		IsError: eval.Classification != "passed",
		Metadata: map[string]any{
			"name": session.Name, "classification": eval.Classification,
			"transcript_handle": varHandle,
		},
	}, nil
}

func (t *Tools) configure(_ context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Name                string `json:"name"`
		OutputFeedback      string `json:"output_feedback"`
		EvalTimeoutSecs     int    `json:"eval_timeout_secs"`
		SubQueryTimeoutSecs int    `json:"sub_query_timeout_secs"`
		SubRLMMaxDepth      *int   `json:"sub_rlm_max_depth"`
		ShareSession        *bool  `json:"share_session"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	current, err := t.store.Get(input.Name)
	if err != nil {
		return tool.Result{}, err
	}
	cfg := current.Config
	if input.OutputFeedback != "" {
		cfg.OutputFeedback = input.OutputFeedback
	}
	if input.EvalTimeoutSecs > 0 {
		cfg.EvalTimeoutSecs = input.EvalTimeoutSecs
	}
	if input.SubQueryTimeoutSecs > 0 {
		cfg.SubQueryTimeoutSecs = input.SubQueryTimeoutSecs
	}
	if input.SubRLMMaxDepth != nil {
		cfg.SubRLMMaxDepth = *input.SubRLMMaxDepth
	}
	if input.ShareSession != nil {
		cfg.ShareSession = *input.ShareSession
	}
	session, err := t.store.ApplyConfig(input.Name, cfg)
	if err != nil {
		return tool.Result{}, err
	}
	return summarizeSession(session, nil)
}

func (t *Tools) close(_ context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	session, err := t.store.Close(input.Name)
	if err != nil {
		return tool.Result{}, err
	}
	stats := map[string]any{
		"name": session.Name, "closed": true, "eval_count": session.EvalCount,
		"source_kind": session.SourceKind, "source_ref": session.SourceRef,
	}
	content, err := json.Marshal(stats)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: string(content), Metadata: stats}, nil
}

func (t *Tools) loadContext(
	ctx context.Context, filePath, content, url, sessionObject string,
) (kind, ref, body string, err error) {
	set := 0
	if strings.TrimSpace(filePath) != "" {
		set++
	}
	if content != "" {
		set++
	}
	if strings.TrimSpace(url) != "" {
		set++
	}
	if strings.TrimSpace(sessionObject) != "" {
		set++
	}
	if set != 1 {
		return "", "", "", errors.New("exactly one of file_path, content, url, session_object is required")
	}
	switch {
	case strings.TrimSpace(filePath) != "":
		path := strings.TrimSpace(filePath)
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", "", "", readErr
		}
		return "file", path, string(data), nil
	case content != "":
		return "content", "inline", content, nil
	case strings.TrimSpace(url) != "":
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(url), nil)
		if reqErr != nil {
			return "", "", "", reqErr
		}
		client := &http.Client{Timeout: 15 * time.Second}
		resp, doErr := client.Do(req)
		if doErr != nil {
			return "", "", "", doErr
		}
		defer resp.Body.Close()
		if resp.StatusCode >= 300 {
			return "", "", "", fmt.Errorf("url fetch status %d", resp.StatusCode)
		}
		limited := make([]byte, rlmlib.MaxInlineContent+1)
		n, _ := resp.Body.Read(limited)
		if n > rlmlib.MaxInlineContent {
			return "", "", "", fmt.Errorf("url body exceeds %d bytes", rlmlib.MaxInlineContent)
		}
		return "url", strings.TrimSpace(url), string(limited[:n]), nil
	default:
		ref = strings.TrimSpace(sessionObject)
		body, err = t.store.ResolveObject(ref)
		if err != nil {
			return "", "", "", err
		}
		return "session_object", ref, body, nil
	}
}

func summarizeSession(session *rlmlib.Session, extra map[string]any) (tool.Result, error) {
	body := map[string]any{
		"name": session.Name, "source_kind": session.SourceKind, "source_ref": session.SourceRef,
		"eval_count": session.EvalCount, "closed": session.Closed,
		"config": session.Config,
	}
	for key, value := range extra {
		body[key] = value
	}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"name": session.Name, "source_kind": session.SourceKind,
		},
	}, nil
}
