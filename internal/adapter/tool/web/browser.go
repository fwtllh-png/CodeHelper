package web

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/adapter/tool/typed"
)

const BrowserUnavailableReason = "browser runtime is unavailable"

// Browser driver fidelity values reported by BrowserDriverStatus.
const (
	BrowserDriverUnavailable = "unavailable"
	BrowserDriverFakeFixture = "fake:fixture"
	BrowserDriverRealChrome  = "real:chromium-cdp"
)

// BrowserDriverStatus reports which driver web_run would use and its fidelity.
func BrowserDriverStatus() string {
	if os.Getenv("QCODE_BROWSER_FIXTURE") == "1" {
		return BrowserDriverFakeFixture
	}
	if findChromeBinary() != "" {
		return BrowserDriverRealChrome
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
	typed.Contract[browserInput, tool.Result]
	runtime BrowserRuntime
}

func (b *browserTool) Close() error {
	if b == nil || b.runtime == nil {
		return nil
	}
	return b.runtime.Close()
}

type browserInput struct {
	Action        string `json:"action"`
	URL           string `json:"url"`
	Selector      string `json:"selector"`
	Value         string `json:"value"`
	AllowLoopback bool   `json:"allow_loopback"`
}

func registerBrowser(registry *tool.Registry, runtime BrowserRuntime) error {
	executor := &browserTool{runtime: runtime}
	contract, err := typed.NewResultContract(typed.ResultSpec[browserInput]{
		Name: "web_run", Disposition: tool.DispositionWaitForTeardown,
		Decode: func(raw json.RawMessage) (browserInput, error) {
			if runtime == nil {
				return browserInput{}, nil
			}
			return typed.DecodeStrict[browserInput](raw)
		},
		Run: executor.run,
	})
	if err != nil {
		return err
	}
	executor.Contract = contract
	return registry.Register(executor)
}

func browserRuntimeFromEnv() BrowserRuntime {
	if os.Getenv("QCODE_BROWSER_FIXTURE") == "1" {
		return NewFakeBrowser()
	}
	if binary := findChromeBinary(); binary != "" {
		return newChromeBrowser(binary)
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
		Description: "Drive an isolated headless Chromium session through CDP: navigate, " +
			"snapshot the live DOM, click, or fill. Set QCODE_BROWSER_BINARY to override detection.",
		DiscoveryTerms: []string{
			"browser", "navigate", "click", "fill", "浏览器", "打开网页", "点击", "填写",
		},
		Visibility: tool.VisibleModel, Capability: tool.CapabilityNetwork,
		AccessMode: tool.AccessWrite, ParallelPolicy: tool.ParallelSerial,
		SandboxRequirement: tool.SandboxNone,
		Availability:       available, UnavailableReason: reason,
		ResourceResolver: tool.ResourceResolver{
			Templates: []tool.ResourceTemplate{{
				Kind: "url", Field: "url", Access: tool.AccessWrite,
			}},
			LoopbackField: "allow_loopback",
		},
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string", "enum": []any{"navigate", "snapshot", "click", "fill"},
				},
				"url":      map[string]any{"type": "string"},
				"selector": map[string]any{"type": "string"},
				"value":    map[string]any{"type": "string"},
				"allow_loopback": map[string]any{
					"type":        "boolean",
					"description": "Permit navigation to an explicitly supplied localhost development URL",
				},
			},
			"required":             []string{"action"},
			"additionalProperties": false,
		},
	}
}

func (b *browserTool) TrustedBinding() tool.TrustedBinding {
	binding := tool.TrustedBindingFromDescriptor(b.Descriptor())
	binding.Effect = tool.EffectContract{
		Mode: tool.EffectFixed, Kind: tool.EffectExternalMutation,
		Risk: tool.RiskHigh, Reversibility: tool.Irreversible,
		WorkspaceTransaction: tool.TransactionNone,
		Approval:             tool.ApprovalPolicyOnce,
	}
	return binding
}

func (b *browserTool) run(ctx context.Context, input browserInput) (tool.Result, error) {
	if b.runtime == nil {
		return tool.Result{
			Content: BrowserUnavailableReason, IsError: true,
			Metadata: map[string]any{"error_category": "unavailable"},
		}, nil
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

// FakeBrowser is an in-process hermetic browser for tests and QCODE_BROWSER_FIXTURE.
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
