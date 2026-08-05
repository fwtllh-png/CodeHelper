package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

const BrowserUnavailableReason = "browser runtime is unavailable"

// Browser driver fidelity values reported by BrowserDriverStatus.
const (
	BrowserDriverUnavailable = "unavailable"
	BrowserDriverFakeFixture = "fake:fixture"
	BrowserDriverFakeBinary  = "fake:binary-gated"
)

// BrowserDriverStatus reports which driver web_run would use and its fidelity.
// No real engine is implemented yet, so every available path is a hermetic fake;
// hosts must not present it as real browser automation.
func BrowserDriverStatus() string {
	if os.Getenv("CODEHELPER_BROWSER_FIXTURE") == "1" {
		return BrowserDriverFakeFixture
	}
	binary := strings.TrimSpace(os.Getenv("CODEHELPER_BROWSER_BINARY"))
	if binary == "" {
		binary = "codehelper-browser"
	}
	if path, err := exec.LookPath(binary); err == nil && path != "" {
		return BrowserDriverFakeBinary
	}
	return BrowserDriverUnavailable
}

// BrowserRuntime drives minimal navigate/snapshot/click/fill interactions.
type BrowserRuntime interface {
	Navigate(ctx context.Context, url string) (snapshot string, err error)
	Snapshot(ctx context.Context) (string, error)
	Click(ctx context.Context, selector string) error
	Fill(ctx context.Context, selector, value string) error
	Close() error
}

type browserTool struct {
	runtime BrowserRuntime
}

func registerBrowser(registry *tool.Registry, runtime BrowserRuntime) error {
	return registry.Register(&browserTool{runtime: runtime}, nil)
}

func browserRuntimeFromEnv() BrowserRuntime {
	if os.Getenv("CODEHELPER_BROWSER_FIXTURE") == "1" {
		return NewFakeBrowser()
	}
	binary := strings.TrimSpace(os.Getenv("CODEHELPER_BROWSER_BINARY"))
	if binary == "" {
		binary = "codehelper-browser"
	}
	if path, err := exec.LookPath(binary); err == nil && path != "" {
		return NewFakeBrowser() // binary presence gates availability; hermetic driver is Fake
	}
	return nil
}

func (b *browserTool) Descriptor() tool.Descriptor {
	available := tool.AvailabilityUnavailable
	reason := BrowserUnavailableReason
	if b.runtime != nil {
		available = tool.AvailabilityAvailable
		reason = ""
	}
	return tool.Descriptor{
		Name: "web_run",
		Description: "Drive a minimal browser session: navigate, snapshot, click, or fill. " +
			"Unavailable without CODEHELPER_BROWSER_FIXTURE=1 or CODEHELPER_BROWSER_BINARY.",
		Visibility: tool.VisibleModel, Capability: tool.CapabilityNetwork,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone,
		Availability:       available, UnavailableReason: reason,
		ResourceResolver: tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
			Kind: "url", Field: "url", Access: tool.AccessWrite,
		}}},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string", "enum": []any{"navigate", "snapshot", "click", "fill"},
				},
				"url":      map[string]any{"type": "string"},
				"selector": map[string]any{"type": "string"},
				"value":    map[string]any{"type": "string"},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (b *browserTool) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	if b.runtime == nil {
		return tool.Result{
			Content: BrowserUnavailableReason, IsError: true,
			Metadata: map[string]any{"error_category": "unavailable"},
		}, nil
	}
	var input struct {
		Action   string `json:"action"`
		URL      string `json:"url"`
		Selector string `json:"selector"`
		Value    string `json:"value"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	switch strings.ToLower(strings.TrimSpace(input.Action)) {
	case "navigate":
		if strings.TrimSpace(input.URL) == "" {
			return tool.Result{}, errors.New("url is required for navigate")
		}
		snapshot, err := b.runtime.Navigate(ctx, input.URL)
		if err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{
			Content:  snapshot,
			Metadata: map[string]any{"action": "navigate", "url": input.URL},
		}, nil
	case "snapshot":
		snapshot, err := b.runtime.Snapshot(ctx)
		if err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{
			Content:  snapshot,
			Metadata: map[string]any{"action": "snapshot"},
		}, nil
	case "click":
		if strings.TrimSpace(input.Selector) == "" {
			return tool.Result{}, errors.New("selector is required for click")
		}
		if err := b.runtime.Click(ctx, input.Selector); err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{
			Content:  fmt.Sprintf("clicked %s", input.Selector),
			Metadata: map[string]any{"action": "click", "selector": input.Selector},
		}, nil
	case "fill":
		if strings.TrimSpace(input.Selector) == "" {
			return tool.Result{}, errors.New("selector is required for fill")
		}
		if err := b.runtime.Fill(ctx, input.Selector, input.Value); err != nil {
			return tool.Result{Content: err.Error(), IsError: true}, nil
		}
		return tool.Result{
			Content: fmt.Sprintf("filled %s", input.Selector),
			Metadata: map[string]any{
				"action": "fill", "selector": input.Selector, "value_len": len(input.Value),
			},
		}, nil
	default:
		return tool.Result{}, fmt.Errorf("unsupported web_run action %q", input.Action)
	}
}

// FakeBrowser is an in-process hermetic browser for tests and CODEHELPER_BROWSER_FIXTURE.
type FakeBrowser struct {
	mu       sync.Mutex
	url      string
	snapshot string
	clicks   []string
	fills    map[string]string
}

func NewFakeBrowser() *FakeBrowser {
	return &FakeBrowser{
		snapshot: "<html><body>empty</body></html>",
		fills:    map[string]string{},
	}
}

func (f *FakeBrowser) Navigate(_ context.Context, rawURL string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.url = rawURL
	f.snapshot = fmt.Sprintf(`<html><body data-url=%q>navigated</body></html>`, rawURL)
	return f.snapshot, nil
}

func (f *FakeBrowser) Snapshot(context.Context) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.snapshot, nil
}

func (f *FakeBrowser) Click(_ context.Context, selector string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.clicks = append(f.clicks, selector)
	f.snapshot = fmt.Sprintf(`<html><body data-clicked=%q>ok</body></html>`, selector)
	return nil
}

func (f *FakeBrowser) Fill(_ context.Context, selector, value string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fills[selector] = value
	f.snapshot = fmt.Sprintf(`<html><body data-filled=%q>ok</body></html>`, selector)
	return nil
}

func (f *FakeBrowser) Close() error { return nil }
