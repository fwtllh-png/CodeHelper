package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestProbeWebReadinessRequiresTrustedReadyEndpoint(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/healthz" {
			t.Errorf("probe path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"version":1,"status":"ready"}`))
	}))
	defer ready.Close()
	if err := probeWebReadiness(t.Context(), ready.URL+"/untrusted"); err != nil {
		t.Fatalf("ready owner rejected: %v", err)
	}

	notReady := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		http.Error(writer, `{"version":1,"status":"initializing"}`, http.StatusServiceUnavailable)
	}))
	defer notReady.Close()
	if err := probeWebReadiness(t.Context(), notReady.URL); err == nil {
		t.Fatal("unready owner accepted")
	}

	redirect := httptest.NewServer(http.RedirectHandler(ready.URL, http.StatusFound))
	defer redirect.Close()
	if err := probeWebReadiness(t.Context(), redirect.URL); err == nil ||
		!strings.Contains(err.Error(), "redirects are forbidden") {
		t.Fatalf("redirect probe error = %v", err)
	}

	if err := probeWebReadiness(context.Background(), "http://localhost:1234/"); err == nil {
		t.Fatal("non-canonical loopback owner URL accepted")
	}
}
