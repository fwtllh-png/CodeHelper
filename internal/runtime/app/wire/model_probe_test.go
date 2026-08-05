package wire

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
)

func TestProbeModelCapabilitiesStoresTightenObservation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if strings.Contains(string(body), "image_url") || strings.Contains(string(body), "input_image") {
			http.Error(w, `{"error":{"message":"vision not supported"}}`, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: {\"id\":\"1\",\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(server.Close)

	dataDir := t.TempDir()
	store, err := state.Open(context.Background(), state.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	results, err := ProbeModelCapabilities(context.Background(), ProbeOptions{
		ProviderID: "openai", ModelID: "gpt-4.1",
		Capabilities: []model.Capability{model.CapVision},
		Store:        store,
		BaseURL:      server.URL,
	})
	if err != nil {
		t.Fatal(err)
	}
	foundVision := false
	for _, result := range results {
		if result.Capability == string(model.CapVision) {
			foundVision = true
			if result.Supported {
				t.Fatalf("vision probe = %+v, want unsupported", result)
			}
		}
	}
	if !foundVision {
		t.Fatalf("results = %+v, want vision", results)
	}

	repo := model.NewCapabilityRepository(store.SQLite().DB())
	observations, err := repo.List(context.Background(), "openai", "gpt-4.1")
	if err != nil {
		t.Fatal(err)
	}
	if len(observations) < 1 {
		t.Fatal("expected stored observations")
	}
}

func TestOverlayProbeRejectsVisionSlotAfterUnsupportedProbe(t *testing.T) {
	dataDir := t.TempDir()
	store, err := state.Open(context.Background(), state.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	repo := model.NewCapabilityRepository(store.SQLite().DB())
	if err := repo.Upsert(context.Background(), model.CapabilityObservation{
		ProviderID: "openai", ModelID: "gpt-4.1", Capability: model.CapVision,
		Supported: false, Source: "probe", Detail: "HTTP 400",
	}); err != nil {
		t.Fatal(err)
	}

	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	act, err := resolver.Resolve(model.RouteRequest{ProviderID: "openai", ModelID: "gpt-4.1"})
	if err != nil {
		t.Fatal(err)
	}
	vision, err := resolver.Resolve(model.RouteRequest{
		ProviderID: "openai", ModelID: "gpt-4.1",
		Require: []model.Capability{model.CapVision},
	})
	if err != nil {
		t.Fatal(err)
	}
	routes, err := model.NewRouteSet(act, map[model.Purpose]model.ReadyRoute{
		model.PurposeVision: vision,
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	_, err = overlayProbeCapabilities(context.Background(), routes, store, false)
	if err == nil {
		t.Fatal("expected overlay to refuse a vision slot after probe unsupported")
	}
	if !strings.Contains(err.Error(), "vision") {
		t.Fatalf("error = %v, want vision mention", err)
	}
}

func TestTrustProbeWidensReasoning(t *testing.T) {
	dataDir := t.TempDir()
	store, err := state.Open(context.Background(), state.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close(context.Background()) })

	repo := model.NewCapabilityRepository(store.SQLite().DB())
	if err := repo.Upsert(context.Background(), model.CapabilityObservation{
		ProviderID: "openai", ModelID: "gpt-4.1", Capability: model.CapReasoning,
		Supported: true, Source: "probe", Detail: "stream completed",
	}); err != nil {
		t.Fatal(err)
	}

	resolver, err := model.NewResolver(model.DefaultCatalog())
	if err != nil {
		t.Fatal(err)
	}
	act, err := resolver.Resolve(model.RouteRequest{ProviderID: "openai", ModelID: "gpt-4.1"})
	if err != nil {
		t.Fatal(err)
	}
	if act.Model().Capabilities.Reasoning {
		t.Fatal("gpt-4.1 catalog reasoning should be false")
	}
	routes, err := model.NewRouteSet(act, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	withoutTrust, err := overlayProbeCapabilities(context.Background(), routes, store, false)
	if err != nil {
		t.Fatal(err)
	}
	if withoutTrust.Act().Model().Capabilities.Reasoning {
		t.Fatal("without --trust-probe, supported must not widen")
	}
	withTrust, err := overlayProbeCapabilities(context.Background(), routes, store, true)
	if err != nil {
		t.Fatal(err)
	}
	if !withTrust.Act().Model().Capabilities.Reasoning {
		t.Fatal("with --trust-probe, supported must widen")
	}
}
