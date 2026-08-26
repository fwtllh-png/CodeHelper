package interact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/platform/repowalk"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	promptcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/prompt"
	rlmlib "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type Options struct {
	Host      *Host
	Workspace string
	Backend   sandbox.Backend
	RLM       *rlmlib.Store
	Governor  *rlmlib.Governor
	Python    string
	Vision    VisionClient
	OnPlan    func(Plan) error
}

const (
	StepPending    = agentcontext.StepPending
	StepInProgress = agentcontext.StepInProgress
	StepDone       = agentcontext.StepDone
)

type PlanStep = agentcontext.PlanStep
type Plan = agentcontext.Plan

type Tools struct {
	host      *Host
	workspace string
	backend   sandbox.Backend
	rlm       *rlmlib.Store
	governor  *rlmlib.Governor
	vision    VisionClient
	onPlan    func(Plan) error
	planMu    sync.Mutex
	plan      Plan
}

type executor struct {
	tools *Tools
	name  string
}

func Register(registry *tool.Registry, options Options) error {
	if registry == nil {
		return errors.New("interact tool registry is required")
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
	host := options.Host
	if host == nil {
		host = NewHost(0)
	}
	governor := options.Governor
	if governor == nil {
		governor = rlmlib.NewGovernor(rlmlib.Limits{})
	}
	tools := &Tools{
		host: host, workspace: root, backend: backend,
		rlm: options.RLM, governor: governor, vision: options.Vision, onPlan: options.OnPlan,
	}
	for _, name := range []string{
		"request_user_input", "update_plan", "submit_plan", "project_map",
		"code_execution", "image_analyze",
	} {
		if err := registry.Register(&executor{tools: tools, name: name}, nil); err != nil {
			return err
		}
	}
	return nil
}

func (e *executor) Descriptor() tool.Descriptor {
	switch e.name {
	case "request_user_input":
		return tool.Descriptor{
			Name: e.name,
			Description: "Request required user input and block the current Turn until the host replies. " +
				"Resolve discoverable facts first, include options for finite choices, and never ask " +
				"for required input in ordinary final text.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "user_input", ID: "pending", Access: tool.AccessRead,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"prompt": map[string]any{"type": "string", "minLength": float64(1)},
					"options": map[string]any{
						"type": "array", "maxItems": float64(12), "uniqueItems": true,
						"items": map[string]any{"type": "string", "minLength": float64(1)},
					},
				},
				"required":             []string{"prompt"},
				"additionalProperties": false,
			},
		}
	case "update_plan", "submit_plan":
		description := "Replace the structured working plan projected through ContextLedger."
		if e.name == "submit_plan" {
			description = "Submit a structured, user-reviewable implementation plan. " +
				"Use this in plan mode or when the active planning policy requires a plan."
		}
		return tool.Descriptor{
			Name: e.name, Description: description,
			Visibility: tool.VisibleModel, Capability: tool.CapabilityWrite,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "plan", ID: "session", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"version": map[string]any{"type": "integer", "enum": []any{float64(1)}},
					"title":   map[string]any{"type": "string"},
					"steps": map[string]any{
						"type": "array",
						"items": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"id":    map[string]any{"type": "string"},
								"title": map[string]any{"type": "string", "minLength": float64(1)},
								"status": map[string]any{
									"type": "string",
									"enum": []string{StepPending, StepInProgress, StepDone},
								},
								"dependencies": map[string]any{
									"type": "array", "items": map[string]any{"type": "string"},
								},
								"expected_evidence": map[string]any{"type": "string"},
								"affected_files": map[string]any{
									"type": "array", "items": map[string]any{"type": "string"},
								},
							},
							"required":             []string{"title"},
							"additionalProperties": false,
						},
						"minItems": float64(1),
					},
					"notes":                map[string]any{"type": "string"},
					"objective":            map[string]any{"type": "string"},
					"context_summary":      map[string]any{"type": "string"},
					"sources_used":         map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"critical_files":       map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"constraints":          map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
					"recommended_approach": map[string]any{"type": "string"},
					"verification_plan":    map[string]any{"type": "string"},
					"risks_and_unknowns":   map[string]any{"type": "string"},
					"handoff_packet":       map[string]any{"type": "string"},
				},
				"required":             []string{"steps"},
				"additionalProperties": false,
			},
		}
	case "project_map":
		return tool.Descriptor{
			Name: e.name, Description: "Summarize workspace structure as a bounded directory map.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessTree, ParallelPolicy: tool.ParallelConcurrent,
			SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "repo", ID: ".", Access: tool.AccessRead, Tree: true,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":      map[string]any{"type": "string"},
					"max_depth": map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(8)},
					"limit":     map[string]any{"type": "integer", "minimum": float64(1), "maximum": float64(500)},
				},
				"additionalProperties": false,
			},
		}
	case "code_execution":
		available := tool.AvailabilityAvailable
		reason := ""
		if e.tools.backend == nil || e.tools.rlm == nil || !e.tools.rlm.PythonAvailable() {
			available = tool.AvailabilityUnavailable
			reason = "strong sandboxed Python runner is unavailable"
		} else if err := sandbox.RequireStrong(e.tools.backend); err != nil {
			available = tool.AvailabilityUnavailable
			reason = "strong sandbox is unavailable"
		}
		return tool.Descriptor{
			Name: e.name, Description: "Execute isolated Python code in a strong sandbox.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityProcess,
			AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
			SandboxRequirement: tool.SandboxStrong, Availability: available, UnavailableReason: reason,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "code", ID: "ephemeral", Access: tool.AccessWrite,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"code":    map[string]any{"type": "string", "minLength": float64(1)},
					"context": map[string]any{"type": "string"},
				},
				"required":             []string{"code"},
				"additionalProperties": false,
			},
		}
	case "image_analyze":
		available := tool.AvailabilityUnavailable
		reason := VisionUnavailableReason
		if e.tools.vision != nil {
			available = tool.AvailabilityAvailable
			reason = ""
		}
		return tool.Descriptor{
			Name: e.name, Description: "Analyze an image with a vision-capable model when [vision] is configured.",
			Visibility: tool.VisibleModel, Capability: tool.CapabilityRead,
			AccessMode: tool.AccessRead, ParallelPolicy: tool.ParallelConcurrent,
			SandboxRequirement: tool.SandboxNone,
			Availability:       available,
			UnavailableReason:  reason,
			ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
				Kind: "image", Field: "path", Access: tool.AccessRead,
			}}},
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path":   map[string]any{"type": "string", "minLength": float64(1)},
					"prompt": map[string]any{"type": "string"},
				},
				"required":             []string{"path"},
				"additionalProperties": false,
			},
		}
	default:
		return tool.Descriptor{Name: e.name, Availability: tool.AvailabilityUnavailable}
	}
}

func (e *executor) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	switch e.name {
	case "request_user_input":
		return e.tools.requestInput(ctx, raw)
	case "update_plan":
		return e.tools.updatePlan(raw, false)
	case "submit_plan":
		return e.tools.updatePlan(raw, true)
	case "project_map":
		return e.tools.projectMap(ctx, raw)
	case "code_execution":
		return e.tools.codeExecution(ctx, raw)
	case "image_analyze":
		return e.tools.imageAnalyze(ctx, raw)
	default:
		return tool.Result{}, fmt.Errorf("unknown interact tool %q", e.name)
	}
}

func (t *Tools) requestInput(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Prompt  string   `json:"prompt"`
		Options []string `json:"options"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	input.Prompt = strings.TrimSpace(input.Prompt)
	if input.Prompt == "" {
		return tool.Result{}, errors.New("prompt is required")
	}
	options, err := normalizeInputOptions(input.Options)
	if err != nil {
		return tool.Result{}, err
	}
	reply, err := t.host.Wait(ctx, "tool", input.Prompt, options)
	if err != nil {
		return tool.Result{}, err
	}
	body := map[string]any{"answer": reply.Answer, "request_id": reply.RequestID}
	if len(reply.Values) > 0 {
		body["values"] = reply.Values
	}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content:  string(content),
		Metadata: map[string]any{"request_id": reply.RequestID},
	}, nil
}

func normalizeInputOptions(values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			return nil, errors.New("input options must be non-empty")
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			return nil, fmt.Errorf("duplicate input option %q", trimmed)
		}
		seen[key] = struct{}{}
		out = append(out, trimmed)
	}
	return out, nil
}

func (t *Tools) updatePlan(raw json.RawMessage, submitted bool) (tool.Result, error) {
	if submitted {
		plan, err := ParseSubmittedPlan(raw)
		if err != nil {
			return tool.Result{}, err
		}
		plan.FileBaseline, err = t.capturePlanBaseline(plan)
		if err != nil {
			return tool.Result{}, err
		}
		contextPlan := plan.ContextPlan()
		if err := t.applyPlan(contextPlan); err != nil {
			return tool.Result{}, err
		}
		content, err := json.Marshal(plan)
		return tool.Result{
			Content: string(content), Metadata: map[string]any{
				"steps": len(plan.Steps), "submitted_plan": true,
			},
		}, err
	}
	var plan Plan
	if err := json.Unmarshal(raw, &plan); err != nil {
		return tool.Result{}, err
	}
	if len(plan.Steps) == 0 {
		return tool.Result{}, errors.New("steps are required")
	}
	for index := range plan.Steps {
		plan.Steps[index].Title = strings.TrimSpace(plan.Steps[index].Title)
		if plan.Steps[index].Title == "" {
			return tool.Result{}, errors.New("plan steps must have a title")
		}
		if plan.Steps[index].Status == "" {
			plan.Steps[index].Status = StepPending
		}
	}
	if err := t.applyPlan(plan); err != nil {
		return tool.Result{}, err
	}
	content, err := json.Marshal(plan)
	return tool.Result{
		Content: string(content), Metadata: map[string]any{"steps": len(plan.Steps)},
	}, err
}

func (t *Tools) applyPlan(plan Plan) error {
	t.planMu.Lock()
	t.plan = plan
	t.planMu.Unlock()
	if t.onPlan != nil {
		if err := t.onPlan(plan); err != nil {
			return err
		}
	}
	return nil
}

func (t *Tools) capturePlanBaseline(plan SubmittedPlan) ([]PlanFileBaseline, error) {
	workspace, err := sandbox.NewWorkspace(t.workspace)
	if err != nil {
		return nil, err
	}
	paths := append([]string(nil), plan.CriticalFiles...)
	for _, step := range plan.Steps {
		paths = append(paths, step.AffectedFiles...)
	}
	seen := make(map[string]struct{}, len(paths))
	baseline := make([]PlanFileBaseline, 0, len(paths))
	for _, name := range paths {
		name = filepath.ToSlash(strings.TrimPrefix(strings.TrimSpace(name), "./"))
		if name == "" {
			continue
		}
		if _, exists := seen[name]; exists {
			continue
		}
		if len(seen) >= 128 {
			return nil, errors.New("plan file baseline accepts at most 128 paths")
		}
		seen[name] = struct{}{}
		file, err := workspace.OpenFile(name)
		if errors.Is(err, os.ErrNotExist) {
			baseline = append(baseline, PlanFileBaseline{
				Path: name, Missing: true,
			})
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("capture plan baseline %q: %w", name, err)
		}
		digest := sha256.New()
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return nil, fmt.Errorf(
				"capture plan baseline %q: %w",
				name,
				errors.Join(copyErr, closeErr),
			)
		}
		baseline = append(baseline, PlanFileBaseline{
			Path: name, Digest: hex.EncodeToString(digest.Sum(nil)),
		})
	}
	sort.Slice(baseline, func(i, j int) bool {
		return baseline[i].Path < baseline[j].Path
	})
	return baseline, nil
}

func (t *Tools) projectMap(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Path     string `json:"path"`
		MaxDepth int    `json:"max_depth"`
		Limit    int    `json:"limit"`
	}
	if len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &input); err != nil {
			return tool.Result{}, err
		}
	}
	if input.MaxDepth <= 0 {
		input.MaxDepth = 3
	}
	if input.Limit <= 0 {
		input.Limit = 120
	}
	rel := strings.TrimSpace(input.Path)
	if rel == "" {
		rel = "."
	}
	prefix, err := t.mapPrefix(rel)
	if err != nil {
		return tool.Result{}, err
	}
	// The map is drawn from the same enumeration the search tools use, so it shows
	// the files a reader would find and not the build output an ignore rule hides.
	walker, err := repowalk.New(t.workspace, t.backend)
	if err != nil {
		return tool.Result{}, err
	}
	listing, err := walker.List(ctx)
	if err != nil {
		return tool.Result{}, err
	}
	names := make(map[string]struct{})
	for _, entry := range listing.Files {
		if prefix != "" && !strings.HasPrefix(entry.Path, prefix) {
			continue
		}
		segments := strings.Split(entry.Path, "/")
		for depth := 1; depth <= len(segments) && depth <= input.MaxDepth; depth++ {
			name := strings.Join(segments[:depth], "/")
			if depth < len(segments) {
				name += "/"
			}
			names[name] = struct{}{}
		}
	}
	entries := make([]string, 0, len(names))
	for name := range names {
		entries = append(entries, name)
	}
	sort.Strings(entries)
	truncated := len(entries) > input.Limit
	if truncated {
		entries = entries[:input.Limit]
	}
	content, err := json.Marshal(map[string]any{
		"root": rel, "entries": entries, "count": len(entries), "truncated": truncated,
	})
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"count": len(entries), "truncated": truncated, "max_depth": input.MaxDepth,
		},
	}, nil
}

// mapPrefix turns a requested subdirectory into the workspace-relative prefix
// its entries share, refusing anything that leaves the workspace.
func (t *Tools) mapPrefix(rel string) (string, error) {
	rootAbs, err := filepath.Abs(t.workspace)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(filepath.Join(rootAbs, filepath.FromSlash(rel)))
	if err != nil {
		return "", err
	}
	if abs != rootAbs &&
		!strings.HasPrefix(abs+string(os.PathSeparator), rootAbs+string(os.PathSeparator)) {
		return "", errors.New("path escapes workspace")
	}
	if abs == rootAbs {
		return "", nil
	}
	relative, err := filepath.Rel(rootAbs, abs)
	if err != nil {
		return "", err
	}
	return filepath.ToSlash(relative) + "/", nil
}

func (t *Tools) codeExecution(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	if t.backend == nil {
		return tool.Result{
			Content: "strong sandbox is unavailable", IsError: true,
			Metadata: map[string]any{"error_category": "sandbox_unavailable"},
		}, nil
	}
	if err := sandbox.RequireStrong(t.backend); err != nil {
		return tool.Result{
			Content: err.Error(), IsError: true,
			Metadata: map[string]any{"error_category": "sandbox_unavailable"},
		}, nil
	}
	if t.rlm == nil || !t.rlm.PythonAvailable() {
		return tool.Result{
			Content: "python interpreter is unavailable", IsError: true,
			Metadata: map[string]any{"error_category": "unavailable"},
		}, nil
	}
	var input struct {
		Code    string `json:"code"`
		Context string `json:"context"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	lease, err := t.governor.Admit(0, 0, 0)
	if err != nil {
		return tool.Result{}, err
	}
	defer t.governor.Release(lease)
	name := fmt.Sprintf("codeexec-%d", time.Now().UTC().UnixNano())
	contextBody := input.Context
	if contextBody == "" {
		contextBody = "# code_execution context\n"
	}
	session, err := t.rlm.Open(name, "inline", "", contextBody, 0)
	if err != nil {
		return tool.Result{}, err
	}
	defer func() { _, _ = t.rlm.Close(name) }()
	eval, _, err := t.rlm.Eval(ctx, session.Name, input.Code)
	if err != nil {
		return tool.Result{}, err
	}
	_ = t.governor.Charge(1, 0)
	body := map[string]any{
		"classification": eval.Classification, "exit_code": eval.ExitCode,
		"timed_out": eval.TimedOut, "stdout": eval.Stdout, "stderr": eval.Stderr,
		"duration_ms": eval.DurationMS,
	}
	content, err := json.Marshal(body)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		IsError: eval.Classification != "passed",
		Metadata: map[string]any{
			"classification": eval.Classification, "exit_code": eval.ExitCode,
		},
	}, nil
}

func (t *Tools) imageAnalyze(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	if t.vision == nil {
		return tool.Result{
			Content: VisionUnavailableReason, IsError: true,
			Metadata: map[string]any{"error_category": "unavailable"},
		}, nil
	}
	var input struct {
		Path   string `json:"path"`
		Prompt string `json:"prompt"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	path := strings.TrimSpace(input.Path)
	if path == "" {
		return tool.Result{}, errors.New("path is required")
	}
	if filepath.IsAbs(path) {
		return tool.Result{
			Content: "image path must be workspace-relative", IsError: true,
			Metadata: map[string]any{"error_category": "invalid_path"},
		}, nil
	}
	cleaned := filepath.Clean(path)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return tool.Result{
			Content: "image path escapes workspace", IsError: true,
			Metadata: map[string]any{"error_category": "invalid_path"},
		}, nil
	}
	abs := filepath.Join(t.workspace, cleaned)
	info, err := os.Stat(abs)
	if err != nil {
		return tool.Result{
			Content: err.Error(), IsError: true,
			Metadata: map[string]any{"error_category": "not_found"},
		}, nil
	}
	if info.IsDir() {
		return tool.Result{
			Content: "image path is a directory", IsError: true,
			Metadata: map[string]any{"error_category": "invalid_path"},
		}, nil
	}
	text, err := t.vision.Analyze(ctx, abs, input.Prompt)
	if err != nil {
		return tool.Result{
			Content: err.Error(), IsError: true,
			Metadata: map[string]any{"error_category": "vision_error"},
		}, nil
	}
	return tool.Result{
		Content:  text,
		Metadata: map[string]any{"path": cleaned, "bytes": info.Size()},
	}, nil
}

// Plan returns a copy of the last applied plan, if any.
func (t *Tools) Plan() (Plan, bool) {
	if t == nil {
		return Plan{}, false
	}
	t.planMu.Lock()
	defer t.planMu.Unlock()
	if len(t.plan.Steps) == 0 {
		return Plan{}, false
	}
	return t.plan, true
}

// FormatPlan renders a plan partition for WorldState projection.
func FormatPlan(plan Plan) string {
	var b strings.Builder
	b.WriteString("<plan")
	if plan.Title != "" {
		b.WriteString(` title="`)
		b.WriteString(plan.Title)
		b.WriteString(`"`)
	}
	b.WriteString(">\n")
	writePlanField(&b, "objective", plan.Objective)
	writePlanField(&b, "context_summary", plan.ContextSummary)
	writePlanList(&b, "sources_used", plan.SourcesUsed)
	writePlanList(&b, "critical_files", plan.CriticalFiles)
	writePlanList(&b, "constraints", plan.Constraints)
	writePlanField(&b, "recommended_approach", plan.RecommendedApproach)
	writePlanField(&b, "verification_plan", plan.VerificationPlan)
	writePlanField(&b, "risks_and_unknowns", plan.RisksAndUnknowns)
	writePlanField(&b, "handoff_packet", plan.HandoffPacket)
	for index, step := range plan.Steps {
		fmt.Fprintf(&b, "%d. %s", index+1, step.Title)
		// A pending step needs no marker: it is the default, and marking every
		// line would cost bytes to say nothing.
		if step.Status != "" && step.Status != StepPending {
			fmt.Fprintf(&b, " [%s]", step.Status)
		}
		b.WriteByte('\n')
	}
	if plan.Notes != "" {
		b.WriteString("Notes: ")
		b.WriteString(plan.Notes)
		b.WriteByte('\n')
	}
	b.WriteString("</plan>")
	return b.String()
}

func writePlanField(b *strings.Builder, name, value string) {
	if strings.TrimSpace(value) == "" {
		return
	}
	b.WriteString(name)
	b.WriteString(": ")
	b.WriteString(value)
	b.WriteByte('\n')
}

func writePlanList(b *strings.Builder, name string, values []string) {
	if len(values) == 0 {
		return
	}
	b.WriteString(name)
	b.WriteString(":\n")
	for _, value := range values {
		b.WriteString("- ")
		b.WriteString(value)
		b.WriteByte('\n')
	}
}

// PlanReceipt builds the audit receipt for an applied plan.
func PlanReceipt(plan Plan) promptcontext.Receipt {
	text := FormatPlan(plan)
	tokens := promptcontext.HeuristicTokenCounter{}.Count(text)
	digest := sha256.Sum256([]byte(text))
	return promptcontext.Receipt{
		Kind: promptcontext.PartitionPlan, SourcePath: "session://plan",
		OriginalBytes: len(text), RetainedBytes: len(text),
		OriginalTokens: tokens, RetainedTokens: tokens,
		Digest: fmt.Sprintf("sha256:%x", digest[:]),
	}
}
