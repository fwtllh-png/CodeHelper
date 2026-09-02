package repohost

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/adapter/tool/typed"
	"github.com/fwtllh-png/QCode/internal/platform/process"
	"github.com/fwtllh-png/QCode/internal/security/sandbox"
)

var repositoryPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

type githubTool struct {
	kind       string
	root       string
	backend    sandbox.Backend
	binary     string
	runProcess func(context.Context, process.Options) (process.Result, error)
}

type githubInput struct {
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	Limit      int    `json:"limit"`
	Title      string `json:"title"`
	Body       string `json:"body"`
	Base       string `json:"base"`
	Head       string `json:"head"`
	Draft      bool   `json:"draft"`
}

type githubExecutor struct {
	tool.OutcomeExecutor
	binding tool.TrustedBinding
}

func (e *githubExecutor) TrustedBinding() tool.TrustedBinding {
	return e.binding
}

func (*githubExecutor) ExecutionDisposition() tool.ExecutionDisposition {
	return tool.DispositionWaitForTeardown
}

func Register(
	registry *tool.Registry,
	root string,
	backend sandbox.Backend,
) error {
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return err
	}
	root = workspace.Root()
	binary, _ := exec.LookPath("gh")
	for _, kind := range []string{
		"github_pr_list", "github_pr_view", "github_ci_status", "github_pr_create",
	} {
		instance := &githubTool{
			kind: kind, root: root, backend: backend, binary: binary,
		}
		runtime, err := typed.Define(typed.Spec[githubInput, tool.Result]{
			Descriptor: instance.Descriptor(), Disposition: tool.DispositionWaitForTeardown,
			Run: instance.run,
			Encode: func(result tool.Result) (tool.Result, error) {
				return result, nil
			},
		})
		if err != nil {
			return err
		}
		outcome, ok := runtime.(tool.OutcomeExecutor)
		if !ok {
			return errors.New("GitHub typed runtime is incomplete")
		}
		if err := registry.Register(&githubExecutor{
			OutcomeExecutor: outcome, binding: instance.binding(),
		}); err != nil {
			return err
		}
	}
	return nil
}

func (t *githubTool) Descriptor() tool.Descriptor {
	availability := tool.AvailabilityAvailable
	reason := ""
	if t.binary == "" {
		availability = tool.AvailabilityUnavailable
		reason = "GitHub CLI gh is not installed"
	}
	properties := map[string]any{
		"repository": map[string]any{
			"type": "string", "pattern": `^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`,
		},
	}
	required := []string{"repository"}
	switch t.kind {
	case "github_pr_list", "github_ci_status":
		properties["limit"] = map[string]any{
			"type": "integer", "minimum": 1, "maximum": 100,
		}
	case "github_pr_view":
		properties["number"] = map[string]any{"type": "integer", "minimum": 1}
		required = append(required, "number")
	case "github_pr_create":
		properties["title"] = map[string]any{"type": "string", "minLength": 1, "maxLength": 256}
		properties["body"] = map[string]any{"type": "string", "maxLength": 65536}
		properties["base"] = map[string]any{"type": "string", "minLength": 1}
		properties["head"] = map[string]any{"type": "string", "minLength": 1}
		properties["draft"] = map[string]any{"type": "boolean"}
		required = append(required, "title", "base", "head")
	}
	description := map[string]string{
		"github_pr_list":   "List GitHub pull requests for an explicit owner/repository",
		"github_pr_view":   "Read one GitHub pull request and its checks",
		"github_ci_status": "List recent GitHub Actions runs and conclusions",
		"github_pr_create": "Create a GitHub pull request from an explicit head to base branch",
	}[t.kind]
	capability := tool.CapabilityExternal
	access := tool.AccessRead
	if t.kind == "github_pr_create" {
		capability, access = tool.CapabilityExternal, tool.AccessWrite
	}
	resources := []tool.ResourceTemplate{
		{Kind: "url", ID: "https://api.github.com", Access: access},
		{Kind: "url", ID: "https://github.com", Access: access},
		{Kind: "process", ID: "github-cli", Access: tool.AccessWrite, Tree: true},
	}
	if t.binary != "" {
		resources = append(resources, tool.ResourceTemplate{
			Kind: "directory", ID: filepath.Dir(t.binary),
			Access: tool.AccessRead, Tree: true,
		})
	}
	return tool.Descriptor{
		Name: t.kind, Description: description,
		DiscoveryTerms: []string{
			"github", "pull request", "ci status", "pr", "代码托管", "拉取请求", "流水线",
		},
		Visibility: tool.VisibleModel, Capability: capability, AccessMode: access,
		ResourceResolver: tool.ResourceResolver{Templates: resources},
		ParallelPolicy:   tool.ParallelSerial, RepeatPolicy: tool.RepeatExecute,
		SandboxRequirement: tool.SandboxStrong,
		Availability:       availability, UnavailableReason: reason,
		InputSchema: map[string]any{
			"type": "object", "properties": properties, "required": required,
			"additionalProperties": false,
		},
	}
}

func (t *githubTool) binding() tool.TrustedBinding {
	binding := tool.TrustedBindingFromDescriptor(t.Descriptor())
	if t.kind == "github_pr_create" {
		binding.Effect = tool.EffectContract{
			Mode: tool.EffectFixed, Kind: tool.EffectExternalMutation,
			Risk: tool.RiskHigh, Reversibility: tool.Irreversible,
			WorkspaceTransaction: tool.TransactionNone,
			Approval:             tool.ApprovalPolicyOnce,
		}
	} else {
		binding.Effect = tool.EffectContract{
			Mode: tool.EffectFixed, Kind: tool.EffectNetworkRead,
			Risk: tool.RiskMedium, Reversibility: tool.Reversible,
			WorkspaceTransaction: tool.TransactionNone,
			Approval:             tool.ApprovalPolicyDefault,
		}
	}
	return binding
}

func (t *githubTool) run(ctx context.Context, input githubInput) (tool.Result, error) {
	if t.binary == "" {
		return tool.Result{}, errors.New("GitHub CLI gh is not installed")
	}
	if !repositoryPattern.MatchString(input.Repository) {
		return tool.Result{}, errors.New("repository must be owner/name")
	}
	limit := input.Limit
	if limit == 0 {
		limit = 20
	}
	var arguments []string
	switch t.kind {
	case "github_pr_list":
		arguments = []string{
			"pr", "list", "--repo", input.Repository, "--limit", strconv.Itoa(limit),
			"--json", "number,title,state,url,headRefName,baseRefName,isDraft",
		}
	case "github_pr_view":
		arguments = []string{
			"pr", "view", strconv.Itoa(input.Number), "--repo", input.Repository,
			"--json", "number,title,state,url,body,author,headRefName,baseRefName,mergeable,statusCheckRollup",
		}
	case "github_ci_status":
		arguments = []string{
			"run", "list", "--repo", input.Repository, "--limit", strconv.Itoa(limit),
			"--json", "databaseId,name,event,status,conclusion,headBranch,headSha,url,createdAt,updatedAt",
		}
	case "github_pr_create":
		if strings.TrimSpace(input.Title) == "" ||
			strings.TrimSpace(input.Base) == "" ||
			strings.TrimSpace(input.Head) == "" {
			return tool.Result{}, errors.New("title, base, and head are required")
		}
		arguments = []string{
			"pr", "create", "--repo", input.Repository,
			"--title", input.Title, "--body", input.Body,
			"--base", input.Base, "--head", input.Head,
		}
		if input.Draft {
			arguments = append(arguments, "--draft")
		}
	default:
		return tool.Result{}, fmt.Errorf("unsupported GitHub tool %q", t.kind)
	}
	directory, err := process.OpenPinnedDirectory(t.backend, t.root)
	if err != nil {
		return tool.Result{}, err
	}
	defer directory.Close()
	options := process.Options{
		Path: t.binary, Args: arguments, Dir: t.root, DirFile: directory,
		Sandbox: t.backend, RequireSandbox: true,
		WorkspaceReadOnly: true, DenyNetwork: false,
		AdditionalReadPaths: []string{filepath.Dir(t.binary)},
		OutputLimitBytes:    process.ModelOutputLimitBytes,
	}
	runner := t.runProcess
	if runner == nil {
		runner = process.Run
	}
	result, err := runner(ctx, options)
	if err != nil {
		return tool.Result{}, err
	}
	content := strings.TrimSpace(result.Stdout)
	if content == "" {
		content = strings.TrimSpace(result.Stderr)
	}
	metadata := map[string]any{
		"repository": input.Repository, "exit_code": result.ExitCode,
	}
	if t.kind == "github_pr_create" && result.ExitCode == 0 {
		metadata["url"] = content
	}
	return tool.Result{
		Content: content, IsError: result.ExitCode != 0, Metadata: metadata,
	}, nil
}
