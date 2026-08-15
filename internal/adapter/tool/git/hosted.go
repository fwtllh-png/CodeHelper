package git

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
)

const hostedResponseLimit = 2 << 20

type HostedTool struct {
	baseURL string
	token   string
	client  *http.Client
}

type hostedInput struct {
	Provider   string `json:"provider"`
	Operation  string `json:"operation"`
	Repository string `json:"repository"`
	Number     int    `json:"number"`
	State      string `json:"state"`
	MaxPages   int    `json:"max_pages"`
}

func registerHosted(registry *tool.Registry) error {
	return registry.Register(&HostedTool{
		baseURL: strings.TrimRight(os.Getenv("CODEHELPER_HOSTED_GIT_URL"), "/"),
		token:   os.Getenv("CODEHELPER_HOSTED_GIT_TOKEN"),
		client:  &http.Client{Timeout: 15 * time.Second},
	}, nil)
}

func (t *HostedTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "hosted_git", Description: "Read pull requests, merge requests, issues, and reviews from a configured Git host",
		Visibility: tool.VisibleModel,
		Capability: tool.CapabilityNetwork, AccessMode: tool.AccessRead,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "repository", Field: "repository", Access: tool.AccessRead,
		}}},
		ParallelPolicy:     tool.ParallelConcurrent,
		SandboxRequirement: tool.SandboxNone, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"provider":   map[string]any{"type": "string", "enum": []any{"github", "gitlab"}},
				"operation":  map[string]any{"type": "string", "enum": []any{"pull_request", "merge_request", "issues", "reviews"}},
				"repository": map[string]any{"type": "string", "minLength": 1},
				"number":     map[string]any{"type": "integer"},
				"state":      map[string]any{"type": "string", "enum": []any{"open", "closed", "all"}},
				"max_pages":  map[string]any{"type": "integer"},
			},
			"required":             []string{"provider", "operation", "repository"},
			"additionalProperties": false,
		},
	}
}

func (t *HostedTool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input hostedInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if t.baseURL == "" {
		return hostedFailure("unavailable", 0, "hosted Git endpoint is not configured"), nil
	}
	if err := validateHostedInput(input); err != nil {
		return tool.Result{}, err
	}
	if t.client == nil {
		t.client = &http.Client{Timeout: 15 * time.Second}
	}
	path, paginated := hostedPath(input)
	maxPages := input.MaxPages
	if maxPages <= 0 {
		maxPages = 5
	}
	if maxPages > 20 {
		maxPages = 20
	}

	next := t.baseURL + path
	var pages []json.RawMessage
	for page := 1; next != "" && page <= maxPages; page++ {
		body, link, status, category, err := t.get(ctx, input.Provider, next)
		if err != nil {
			return tool.Result{}, err
		}
		if category != "" {
			return hostedFailure(category, status, string(body)), nil
		}
		if !json.Valid(body) {
			return hostedFailure("invalid_response", status, "hosted Git returned invalid JSON"), nil
		}
		pages = append(pages, json.RawMessage(body))
		if !paginated {
			break
		}
		next = nextLink(link)
	}

	content, err := combineHostedPages(pages, paginated)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content),
		Metadata: map[string]any{
			"provider": input.Provider, "operation": input.Operation, "pages": len(pages),
		},
	}, nil
}

func (*HostedTool) ExecutionDisposition() tool.ExecutionDisposition {
	return tool.DispositionWaitForTeardown
}

func (t *HostedTool) ExecuteOutcome(
	ctx context.Context,
	raw json.RawMessage,
) (tool.Result, tool.Outcome, error) {
	return typed.ExecuteOutcome(ctx, t, raw)
}

func validateHostedInput(input hostedInput) error {
	if input.Provider != "github" && input.Provider != "gitlab" {
		return errors.New("hosted Git provider must be github or gitlab")
	}
	switch input.Operation {
	case "pull_request":
		if input.Provider != "github" {
			return errors.New("pull_request is only valid for github")
		}
		if input.Number <= 0 {
			return errors.New("pull request number must be positive")
		}
	case "merge_request":
		if input.Provider != "gitlab" {
			return errors.New("merge_request is only valid for gitlab")
		}
		if input.Number <= 0 {
			return errors.New("merge request number must be positive")
		}
	case "reviews":
		if input.Number <= 0 {
			return errors.New("review parent number must be positive")
		}
	case "issues":
	default:
		return errors.New("unsupported hosted Git operation")
	}
	if input.Repository == "" || strings.ContainsAny(input.Repository, "\x00\n\r?#") {
		return errors.New("invalid hosted Git repository")
	}
	return nil
}

func hostedPath(input hostedInput) (string, bool) {
	repository := input.Repository
	if input.Provider == "gitlab" {
		repository = url.PathEscape(repository)
	}
	state := input.State
	if state == "" {
		state = "open"
	}
	if input.Provider == "github" {
		switch input.Operation {
		case "pull_request":
			return "/repos/" + repository + "/pulls/" + strconv.Itoa(input.Number), false
		case "reviews":
			return "/repos/" + repository + "/pulls/" + strconv.Itoa(input.Number) + "/reviews?per_page=100", true
		default:
			return "/repos/" + repository + "/issues?state=" + url.QueryEscape(state) + "&per_page=100", true
		}
	}
	switch input.Operation {
	case "merge_request":
		return "/projects/" + repository + "/merge_requests/" + strconv.Itoa(input.Number), false
	case "reviews":
		return "/projects/" + repository + "/merge_requests/" + strconv.Itoa(input.Number) + "/approvals", false
	default:
		return "/projects/" + repository + "/issues?state=" + url.QueryEscape(state) + "&per_page=100", true
	}
}

func (t *HostedTool) get(ctx context.Context, provider, endpoint string) ([]byte, string, int, string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, "", 0, "", err
	}
	request.Header.Set("Accept", "application/json")
	if t.token != "" {
		if provider == "gitlab" {
			request.Header.Set("PRIVATE-TOKEN", t.token)
		} else {
			request.Header.Set("Authorization", "Bearer "+t.token)
		}
	}
	response, err := t.client.Do(request)
	if err != nil {
		return nil, "", 0, "", fmt.Errorf("hosted Git request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, hostedResponseLimit+1))
	if err != nil {
		return nil, "", 0, "", fmt.Errorf("read hosted Git response: %w", err)
	}
	if len(body) > hostedResponseLimit {
		return []byte("hosted Git response exceeds 2 MiB"), "", response.StatusCode, "response_too_large", nil
	}
	category := hostedErrorCategory(response)
	return body, response.Header.Get("Link"), response.StatusCode, category, nil
}

func hostedErrorCategory(response *http.Response) string {
	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		return ""
	case response.StatusCode == http.StatusUnauthorized:
		return "authentication"
	case response.StatusCode == http.StatusTooManyRequests:
		return "rate_limited"
	case response.StatusCode == http.StatusForbidden &&
		(response.Header.Get("X-RateLimit-Remaining") == "0" || response.Header.Get("RateLimit-Remaining") == "0"):
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

func hostedFailure(category string, status int, message string) tool.Result {
	return tool.Result{
		Content: message, IsError: true,
		Metadata: map[string]any{"error_category": category, "status_code": status},
	}
}

func nextLink(header string) string {
	for _, item := range strings.Split(header, ",") {
		parts := strings.Split(strings.TrimSpace(item), ";")
		if len(parts) < 2 || !strings.Contains(strings.Join(parts[1:], ";"), `rel="next"`) {
			continue
		}
		return strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(parts[0]), "<"), ">")
	}
	return ""
}

func combineHostedPages(pages []json.RawMessage, paginated bool) ([]byte, error) {
	if len(pages) == 0 {
		return []byte("[]"), nil
	}
	if !paginated {
		return pages[0], nil
	}
	var combined []json.RawMessage
	for _, page := range pages {
		var items []json.RawMessage
		if err := json.Unmarshal(page, &items); err != nil {
			return nil, errors.New("paginated hosted Git response must be a JSON array")
		}
		combined = append(combined, items...)
	}
	return json.Marshal(combined)
}
