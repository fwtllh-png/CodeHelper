package web

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestSetupApplyReconfiguresReadyRuntime(t *testing.T) {
	identity, err := protocol.NewWorkspaceIdentity(
		"file:///workspace",
		"/workspace",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	var applied SetupRequest
	server, err := New(Options{
		Assets:       fstest.MapFS{"index.html": {Data: []byte("ok")}},
		ExpectedHost: "127.0.0.1:43210",
		Setup: &SetupOptions{
			WorkspaceRoot:     "/workspace",
			WorkspaceIdentity: identity,
			Catalog: SetupCatalog{
				Version: SetupCatalogVersion,
				Providers: []SetupProvider{{
					ID: "deepseek", DisplayName: "DeepSeek",
					Protocol: "openai_chat", RequiresAPIKey: true,
				}},
			},
			Apply: func(_ context.Context, request SetupRequest) error {
				applied = request
				return nil
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.ready.Store(true)
	request := httptest.NewRequest(
		"POST",
		"http://127.0.0.1:43210/api/v1/setup/apply",
		strings.NewReader(
			`{"provider":"deepseek","model":"deepseek-chat","api_key":"secret"}`,
		),
	)
	request.Header.Set("Idempotency-Key", "reconfigure")
	result, err := server.setupApply(request, Dependencies{})
	if err != nil {
		t.Fatal(err)
	}
	if applied.Provider != "deepseek" || applied.APIKey != "secret" {
		t.Fatalf("applied request = %+v", applied)
	}
	if ready := result.(SetupResult).Ready; !ready {
		t.Fatal("ready Runtime was not reported ready after reconfiguration")
	}
	response := httptest.NewRecorder()
	server.bootstrap(response, httptest.NewRequest(
		"GET",
		"http://127.0.0.1:43210/api/v1/bootstrap",
		nil,
	))
	var bootstrap bootstrapResponse
	if err := json.Unmarshal(response.Body.Bytes(), &bootstrap); err != nil {
		t.Fatal(err)
	}
	if bootstrap.SetupRequired || bootstrap.SetupCatalog == nil ||
		bootstrap.SetupCatalog.Providers[0].ID != "deepseek" {
		t.Fatalf("ready bootstrap setup catalog = %+v", bootstrap)
	}
}
