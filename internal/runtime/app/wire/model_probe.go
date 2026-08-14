package wire

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider/httpclient"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
)

// 1x1 PNG — enough for an image_input / vision probe without a fixture file.
var probeTinyPNG, _ = base64.StdEncoding.DecodeString(
	"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==",
)

// ProbeResult is one capability verdict written by ProbeModelCapabilities.
type ProbeResult struct {
	Capability string `json:"capability"`
	Supported  bool   `json:"supported"`
	Detail     string `json:"detail"`
	Source     string `json:"source"`
}

// ProbeOptions configures a manual capability probe.
type ProbeOptions struct {
	ProviderID   string
	ModelID      string
	Capabilities []model.Capability
	Store        *state.Store
	Credential   model.CredentialRef
	// BaseURL overrides the catalog endpoint (hermetic fixture servers).
	BaseURL string
}

// ProbeModelCapabilities sends minimal requests through the egress Gate, stores
// observations in provider_capabilities, and returns the verdicts. It never runs
// on session start — callers (CLI) must invoke it explicitly.
func ProbeModelCapabilities(ctx context.Context, options ProbeOptions) ([]ProbeResult, error) {
	if options.Store == nil {
		return nil, fmt.Errorf("probe requires a persistent store")
	}
	if options.ProviderID == "" || options.ModelID == "" {
		return nil, fmt.Errorf("provider and model are required")
	}
	if len(options.Capabilities) == 0 {
		return nil, fmt.Errorf("at least one capability is required")
	}
	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		return nil, err
	}
	route, err := resolver.Resolve(model.RouteRequest{
		ProviderID: options.ProviderID, ModelID: options.ModelID,
	})
	if err != nil {
		return nil, err
	}
	if options.BaseURL != "" {
		route = route.WithEndpoint(strings.TrimRight(options.BaseURL, "/"))
	} else if override := strings.TrimSpace(os.Getenv("CODEHELPER_MODEL_PROBE_BASE_URL")); override != "" {
		route = route.WithEndpoint(strings.TrimRight(override, "/"))
	}
	if options.Credential.Kind != "" || options.Credential.Name != "" {
		route = route.WithCredential(options.Credential)
	}

	gate := &egress.Gate{Enforce: true}
	if !gate.AllowURL(route.Endpoint()) {
		return nil, fmt.Errorf("probe endpoint host cannot be granted")
	}
	client := httpclient.New()
	client.Egress = gate
	client.HTTP = &http.Client{Timeout: 30 * time.Second}
	if options.BaseURL != "" || strings.TrimSpace(os.Getenv("CODEHELPER_MODEL_PROBE_BASE_URL")) != "" {
		// Hermetic / overridden endpoints still require a credential resolver; the
		// fixture server never sees the value.
		client.Credentials = probeCredentials("codehelper-probe-key")
	}
	routes, err := model.NewRouteSet(route, nil, false)
	if err != nil {
		return nil, err
	}
	runtimeProvider, err := newProviderRouter(client, routes)
	if err != nil {
		return nil, err
	}

	repo := model.NewCapabilityRepository(options.Store.SQLite().DB())
	results := make([]ProbeResult, 0, len(options.Capabilities)+1)
	for _, capability := range options.Capabilities {
		supported, detail, err := probeOne(ctx, runtimeProvider, route, capability)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", capability, err)
		}
		observation := model.CapabilityObservation{
			ProviderID: options.ProviderID, ModelID: options.ModelID,
			Capability: capability, Supported: supported, Source: "probe", Detail: detail,
			ObservedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
		if err := repo.Upsert(ctx, observation); err != nil {
			return nil, err
		}
		if capability == model.CapVision {
			imageObs := observation
			imageObs.Capability = model.CapImageInput
			if err := repo.Upsert(ctx, imageObs); err != nil {
				return nil, err
			}
			results = append(results, ProbeResult{
				Capability: string(model.CapImageInput), Supported: supported,
				Detail: detail, Source: "probe",
			})
		}
		results = append(results, ProbeResult{
			Capability: string(capability), Supported: supported,
			Detail: detail, Source: "probe",
		})
	}
	return results, nil
}

func probeOne(
	ctx context.Context,
	client provider.Provider,
	route model.ReadyRoute,
	capability model.Capability,
) (bool, string, error) {
	request, err := buildProbeRequest(route, capability)
	if err != nil {
		return false, "", err
	}
	stream, err := client.Stream(ctx, request)
	if err != nil {
		return classifyProbeError(err)
	}
	defer stream.Close()
	if _, err := provider.Drain(stream); err != nil {
		return classifyProbeError(err)
	}
	return true, "stream completed", nil
}

func buildProbeRequest(route model.ReadyRoute, capability model.Capability) (provider.ModelRequest, error) {
	caps := route.Model().Capabilities
	switch capability {
	case model.CapVision, model.CapImageInput:
		caps = model.ForceCapabilities(caps, model.CapVision, model.CapImageInput)
		return provider.ModelRequest{
			Route: route.WithCapabilities(caps),
			Messages: []provider.Message{{
				Role: provider.RoleUser,
				Blocks: []provider.ContentBlock{
					{Type: provider.ContentText, Text: "Describe this image in one word."},
					{Type: provider.ContentImage, Attachment: &provider.Attachment{
						MediaType: "image/png", Data: probeTinyPNG, Name: "probe.png",
					}},
				},
			}},
			MaxOutputTokens: 16,
			Idempotent:      true,
			Purpose:         model.PurposeVision,
		}, nil
	case model.CapReasoning:
		caps = model.ForceCapabilities(caps, model.CapReasoning)
		return provider.ModelRequest{
			Route: route.WithCapabilities(caps),
			Messages: []provider.Message{
				provider.TextMessage(provider.RoleUser, "Reply with the single word ok."),
			},
			MaxOutputTokens: 1025,
			ReasoningEffort: "low",
			Idempotent:      true,
		}, nil
	case model.CapPromptCache:
		if route.Protocol() != model.ProtocolOpenAIResponses {
			return provider.ModelRequest{}, fmt.Errorf(
				"prompt_cache probe requires openai_responses (got %s)", route.Protocol(),
			)
		}
		caps = model.ForceCapabilities(caps, model.CapPromptCache)
		return provider.ModelRequest{
			Route: route.WithCapabilities(caps),
			Messages: []provider.Message{
				provider.TextMessage(provider.RoleUser, "Reply with the single word ok."),
			},
			MaxOutputTokens: 16,
			PromptCacheKey:  "codehelper-probe",
			Idempotent:      true,
		}, nil
	default:
		return provider.ModelRequest{}, fmt.Errorf("capability %q is not probeable yet", capability)
	}
}

func classifyProbeError(err error) (bool, string, error) {
	var problem *protocol.Problem
	if errors.As(err, &problem) {
		detail := problem.Message
		if detail == "" {
			detail = problem.Error()
		}
		detail = truncateProbeDetail(detail)
		switch problem.Code {
		case protocol.CodeInvalidArgument:
			return false, detail, nil
		case protocol.CodeUnavailable, protocol.CodeResourceExhausted, protocol.CodeDeadlineExceeded:
			return false, "", fmt.Errorf("probe transport failed: %s", detail)
		default:
			return false, detail, nil
		}
	}
	if errors.Is(err, egress.ErrDenied) {
		return false, "", err
	}
	return false, truncateProbeDetail(err.Error()), nil
}

func truncateProbeDetail(value string) string {
	value = strings.TrimSpace(value)
	if len(value) > 240 {
		return value[:240] + "…"
	}
	return value
}

type probeCredentials string

func (c probeCredentials) Resolve(_ context.Context, reference model.CredentialRef) (string, error) {
	if reference.Kind == "" {
		return "", nil
	}
	if string(c) == "" {
		return "", fmt.Errorf("probe credential is empty")
	}
	return string(c), nil
}
