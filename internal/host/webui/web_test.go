package webui_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/webui"
)

func TestEmbeddedUIServedWithCSP(t *testing.T) {
	api := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("content-type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{"status": "ready"})
	})
	handler, err := webui.Mount(api)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	response, err := http.Get(server.URL + "/ui/")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.StatusCode, body)
	}
	if !strings.Contains(string(body), "CodeHelper") {
		t.Fatalf("missing brand: %s", body)
	}
	if csp := response.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("csp=%q", csp)
	}

	health, err := http.Get(server.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer health.Body.Close()
	if health.StatusCode != http.StatusOK {
		t.Fatalf("api mount broken: %d", health.StatusCode)
	}
}

func TestControlPageCanCreateThreadAgainstMountedAPI(t *testing.T) {
	var created bool
	api := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/v1/threads" && request.Method == http.MethodPost {
			created = true
			_ = json.NewEncoder(writer).Encode(map[string]any{"thread_id": "thread-web-1"})
			return
		}
		if request.URL.Path == "/v1/events" {
			writer.Header().Set("content-type", "text/event-stream")
			_, _ = writer.Write([]byte("data: {\"type\":\"ready\"}\n\n"))
			return
		}
		http.NotFound(writer, request)
	})
	handler, err := webui.Mount(api)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()

	js, err := http.Get(server.URL + "/ui/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer js.Body.Close()
	script, _ := io.ReadAll(js.Body)
	if !strings.Contains(string(script), "/v1/threads") ||
		!strings.Contains(string(script), "/v1/events") ||
		!strings.Contains(string(script), "approvals") {
		t.Fatalf("control script missing Runtime API calls: %s", script)
	}

	response, err := http.Post(server.URL+"/v1/threads", "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if !created || response.StatusCode != http.StatusOK {
		t.Fatalf("created=%v status=%d", created, response.StatusCode)
	}
}
