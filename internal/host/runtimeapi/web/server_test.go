package web_test

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	webhost "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/web"
	"github.com/fwtllh-png/CodeHelper/internal/platform/workspacequery"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestBootstrapIsLoopbackFencedAndDoesNotCacheToken(t *testing.T) {
	server := newTestServer(t, "127.0.0.1:43210")
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:43210/api/v1/bootstrap", nil)
	request.Host = "127.0.0.1:43210"
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var value struct {
		Token string `json:"token"`
		Ready bool   `json:"ready"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	if len(value.Token) < 40 || value.Ready {
		t.Fatalf("bootstrap = %+v", value)
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache control = %q", response.Header().Get("Cache-Control"))
	}

	rebound := httptest.NewRequest(http.MethodGet, "http://evil.test/api/v1/bootstrap", nil)
	rebound.Host = "evil.test"
	rejected := httptest.NewRecorder()
	server.Handler().ServeHTTP(rejected, rebound)
	if rejected.Code != http.StatusForbidden {
		t.Fatalf("rebound status = %d", rejected.Code)
	}
}

func TestUnaryRequiresTokenAndRuntimeReadiness(t *testing.T) {
	server := newTestServer(t, "127.0.0.1:43210")
	request := httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1:43210/api/v1/system/describe",
		strings.NewReader(`{}`),
	)
	request.Host = "127.0.0.1:43210"
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", response.Code)
	}

	token := bootstrapToken(t, server, "127.0.0.1:43210")
	request = httptest.NewRequest(
		http.MethodPost,
		"http://127.0.0.1:43210/api/v1/system/describe",
		strings.NewReader(`{}`),
	)
	request.Host = "127.0.0.1:43210"
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response = httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("not-ready status = %d body=%s", response.Code, response.Body.String())
	}
}

func TestBootFailureAndBootstrapScriptAreNotCached(t *testing.T) {
	server := newTestServerWithAssets(t, "127.0.0.1:43210", fstest.MapFS{
		"index.html":         &fstest.MapFile{Data: []byte("<main>CodeHelper</main>"), Mode: fs.FileMode(0o444)},
		"theme-bootstrap.js": &fstest.MapFile{Data: []byte("void 0"), Mode: fs.FileMode(0o444)},
	})
	server.FailBoot(errors.New("configuration is invalid"))

	health := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:43210/healthz", nil)
	health.Host = "127.0.0.1:43210"
	healthResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(healthResponse, health)
	if healthResponse.Code != http.StatusServiceUnavailable ||
		!strings.Contains(healthResponse.Body.String(), `"boot_failed"`) {
		t.Fatalf("health status=%d body=%s", healthResponse.Code, healthResponse.Body.String())
	}

	asset := httptest.NewRequest(
		http.MethodGet,
		"http://127.0.0.1:43210/theme-bootstrap.js",
		nil,
	)
	asset.Host = "127.0.0.1:43210"
	assetResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(assetResponse, asset)
	if got := assetResponse.Header().Get("Cache-Control"); got != "no-cache" {
		t.Fatalf("theme bootstrap cache control = %q", got)
	}
}

func TestStaticAssetsRejectTraversalAndSupportETag(t *testing.T) {
	const host = "127.0.0.1:43210"
	server := newTestServerWithAssets(t, host, fstest.MapFS{
		"index.html": &fstest.MapFile{
			Data: []byte("<main>CodeHelper</main>"), Mode: fs.FileMode(0o444),
		},
		"assets/app.js": &fstest.MapFile{
			Data: []byte("void 0"), Mode: fs.FileMode(0o444),
		},
	})
	request := httptest.NewRequest(
		http.MethodGet,
		"http://"+host+"/assets/app.js",
		nil,
	)
	request.Host = host
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") == "" {
		t.Fatalf("asset status=%d headers=%v", response.Code, response.Header())
	}

	cached := httptest.NewRequest(
		http.MethodGet,
		"http://"+host+"/assets/app.js",
		nil,
	)
	cached.Host = host
	cached.Header.Set("If-None-Match", response.Header().Get("ETag"))
	cachedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(cachedResponse, cached)
	if cachedResponse.Code != http.StatusNotModified {
		t.Fatalf("cached status = %d", cachedResponse.Code)
	}

	traversal := httptest.NewRequest(
		http.MethodGet,
		"http://"+host+"/%2e%2e/secret",
		nil,
	)
	traversal.Host = host
	traversalResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(traversalResponse, traversal)
	if traversalResponse.Code != http.StatusNotFound {
		t.Fatalf("traversal status = %d", traversalResponse.Code)
	}
}

func TestCapacityAppliesExactBodyIdentityAndConnectionLimits(t *testing.T) {
	const host = "127.0.0.1:43210"
	server := newTestServerWithOptions(t, webhost.Options{
		Assets: fstest.MapFS{
			"index.html": &fstest.MapFile{
				Data: []byte("<main>CodeHelper</main>"), Mode: fs.FileMode(0o444),
			},
		},
		ExpectedHost: host,
		Origin:       "http://" + host,
		Capacity: webhost.Capacity{
			MaxJSONBodyBytes:       2,
			MaxWebSocketFrameBytes: 256,
			MaxReplayEvents:        10,
			MaxConnections:         1,
			MaxIdentityBytes:       4,
		},
	})
	runtime := app.NewRuntime(app.Options{})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	identity, err := protocol.NewWorkspaceIdentity("file:///workspace", "/workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Activate(webhost.Dependencies{
		Runtime: runtime, WorkspaceRoot: "/workspace",
		WorkspaceIdentity: identity,
	}); err != nil {
		t.Fatal(err)
	}
	token := bootstrapToken(t, server, host)

	exact := postWeb(t, server, host, token, "system/describe", `{}`)
	if exact.Code != http.StatusOK {
		t.Fatalf("exact body status=%d body=%s", exact.Code, exact.Body.String())
	}
	over := postWeb(t, server, host, token, "system/describe", "{} ")
	if over.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("over body status=%d body=%s", over.Code, over.Body.String())
	}

	longID := httptest.NewRequest(
		http.MethodPost,
		"http://"+host+"/api/v1/system/describe",
		strings.NewReader(`{}`),
	)
	longID.Host = host
	longID.Header.Set("Content-Type", "application/json")
	longID.Header.Set("Authorization", "Bearer "+token)
	longID.Header.Set("X-CodeHelper-Request-ID", "12345")
	longIDResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(longIDResponse, longID)
	if longIDResponse.Code != http.StatusBadRequest {
		t.Fatalf("long identity status = %d", longIDResponse.Code)
	}
}

func TestRequestPanicIsContainedAndRedacted(t *testing.T) {
	const host = "127.0.0.1:43210"
	server := newTestServer(t, host)
	runtime := app.NewRuntime(app.Options{})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	identity, err := protocol.NewWorkspaceIdentity("file:///workspace", "/workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics strings.Builder
	if err := server.Activate(webhost.Dependencies{
		Runtime: runtime, WorkspaceRoot: "/workspace",
		WorkspaceIdentity: identity,
		Diagnostics:       &diagnostics,
		MCPHealth: func() []mcp.HealthSnapshot {
			panic("secret panic detail")
		},
	}); err != nil {
		t.Fatal(err)
	}
	response := postWeb(
		t,
		server,
		host,
		bootstrapToken(t, server, host),
		"system/diagnostics",
		`{}`,
	)
	if response.Code != http.StatusInternalServerError ||
		strings.Contains(response.Body.String(), "secret panic detail") ||
		strings.Contains(diagnostics.String(), "secret panic detail") {
		t.Fatalf(
			"panic status=%d body=%q diagnostics=%q",
			response.Code,
			response.Body.String(),
			diagnostics.String(),
		)
	}
}

func TestWebSocketDownlinkConcurrencyAndShutdown(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host := listener.Addr().String()
	server := newTestServer(t, host)
	runtime := app.NewRuntime(app.Options{})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	identity, err := protocol.NewWorkspaceIdentity(
		"file:///workspace",
		"/workspace",
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Activate(webhost.Dependencies{
		Runtime: runtime, WorkspaceRoot: "/workspace",
		WorkspaceIdentity: identity,
	}); err != nil {
		t.Fatal(err)
	}
	httpServer := &http.Server{Handler: server.Handler()}
	go func() { _ = httpServer.Serve(listener) }()
	t.Cleanup(func() { _ = httpServer.Shutdown(context.Background()) })

	token := fetchBootstrapToken(t, "http://"+host)
	connection, _, err := websocket.Dial(
		t.Context(),
		"ws://"+host+"/api/v1/events",
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = connection.CloseNow() })
	if err := wsjson.Write(
		t.Context(),
		connection,
		map[string]any{
			"type": "authenticate", "token": token, "cursor": 0,
		},
	); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var hello struct {
		Type string `json:"type"`
	}
	if err := wsjson.Read(ctx, connection, &hello); err != nil {
		t.Fatal(err)
	}
	if hello.Type != "hello" {
		t.Fatalf("frame = %+v", hello)
	}
	if err := wsjson.Write(
		t.Context(),
		connection,
		map[string]any{"type": "unexpected"},
	); err != nil {
		t.Fatal(err)
	}
	if _, _, err := connection.Read(ctx); websocket.CloseStatus(err) !=
		websocket.StatusPolicyViolation {
		t.Fatalf("post-auth client frame close = %v", err)
	}
}

func TestArtifactRouteUsesTypedValidation(t *testing.T) {
	const host = "127.0.0.1:43210"
	server := newTestServer(t, host)
	runtime := app.NewRuntime(app.Options{})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	identity, err := protocol.NewWorkspaceIdentity("file:///workspace", "/workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Activate(webhost.Dependencies{
		Runtime: runtime, WorkspaceRoot: "/workspace",
		WorkspaceIdentity: identity,
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://"+host+"/api/v1/checkpoint/get",
		strings.NewReader(`{"session_id":"session"}`),
	)
	request.Host = host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+bootstrapToken(t, server, host))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest ||
		!strings.Contains(response.Body.String(), "checkpoint_id is required") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWorkspaceRoutesUseBoundedWorkspaceQuery(t *testing.T) {
	const host = "127.0.0.1:43210"
	root := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(
		filepath.Join(root, "main.go"),
		[]byte("package main\n\nfunc hello() {}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("fixture")...)
	if err := os.WriteFile(filepath.Join(root, "diagram.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	query, err := workspacequery.New(root, webTestBackend{})
	if err != nil {
		t.Fatal(err)
	}
	runtime := app.NewRuntime(app.Options{})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	identity, err := protocol.NewWorkspaceIdentity("file://"+root, root, "")
	if err != nil {
		t.Fatal(err)
	}
	server := newTestServer(t, host)
	if err := server.Activate(webhost.Dependencies{
		Runtime: runtime, WorkspaceRoot: root,
		WorkspaceIdentity: identity, Workspace: query,
	}); err != nil {
		t.Fatal(err)
	}
	token := bootstrapToken(t, server, host)
	response := postWeb(t, server, host, token, "workspace/search", `{"query":"hello"}`)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"path":"main.go"`) {
		t.Fatalf("search status=%d body=%s", response.Code, response.Body.String())
	}
	response = postWeb(
		t,
		server,
		host,
		token,
		"workspace/resource",
		`{"path":"main.go"}`,
	)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"uri":"file://`) ||
		!strings.Contains(response.Body.String(), `"document_version":1`) ||
		!strings.Contains(response.Body.String(), `"content_handle":"`) ||
		strings.Contains(response.Body.String(), `"digest":"sha256:`) {
		t.Fatalf("resource status=%d body=%s", response.Code, response.Body.String())
	}
	var resourceEnvelope struct {
		Result struct {
			ContentHandle string `json:"content_handle"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &resourceEnvelope); err != nil {
		t.Fatal(err)
	}
	contentRequest := httptest.NewRequest(
		http.MethodGet,
		"http://"+host+"/api/v1/content/"+resourceEnvelope.Result.ContentHandle,
		nil,
	)
	contentRequest.Host = host
	contentRequest.Header.Set("Authorization", "Bearer "+token)
	contentResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(contentResponse, contentRequest)
	if contentResponse.Code != http.StatusOK ||
		contentResponse.Body.String() != "package main\n\nfunc hello() {}\n" ||
		!strings.Contains(
			contentResponse.Header().Get("Content-Disposition"),
			"main.go",
		) ||
		contentResponse.Header().Get("ETag") == "" {
		t.Fatalf(
			"content status=%d headers=%v body=%q",
			contentResponse.Code,
			contentResponse.Header(),
			contentResponse.Body.String(),
		)
	}
	unauthorized := httptest.NewRequest(
		http.MethodGet,
		"http://"+host+"/api/v1/content/"+resourceEnvelope.Result.ContentHandle,
		nil,
	)
	unauthorized.Host = host
	unauthorizedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized content status=%d", unauthorizedResponse.Code)
	}
	imageResponse := postWeb(
		t,
		server,
		host,
		token,
		"workspace/image",
		`{"path":"diagram.png"}`,
	)
	if imageResponse.Code != http.StatusOK ||
		!strings.Contains(imageResponse.Body.String(), `"media_type":"image/png"`) ||
		!strings.Contains(imageResponse.Body.String(), `"content_handle":"`) {
		t.Fatalf("image status=%d body=%s", imageResponse.Code, imageResponse.Body.String())
	}
	var imageEnvelope struct {
		Result struct {
			ContentHandle string `json:"content_handle"`
		} `json:"result"`
	}
	if err := json.Unmarshal(imageResponse.Body.Bytes(), &imageEnvelope); err != nil {
		t.Fatal(err)
	}
	imageRequest := httptest.NewRequest(
		http.MethodGet,
		"http://"+host+"/api/v1/content/"+imageEnvelope.Result.ContentHandle,
		nil,
	)
	imageRequest.Host = host
	imageRequest.Header.Set("Authorization", "Bearer "+token)
	imageContent := httptest.NewRecorder()
	server.Handler().ServeHTTP(imageContent, imageRequest)
	if imageContent.Code != http.StatusOK ||
		imageContent.Header().Get("Content-Type") != "image/png" ||
		imageContent.Body.String() != string(png) {
		t.Fatalf(
			"image content status=%d headers=%v body=%q",
			imageContent.Code,
			imageContent.Header(),
			imageContent.Body.Bytes(),
		)
	}
	tampered := httptest.NewRequest(
		http.MethodGet,
		"http://"+host+"/api/v1/content/"+resourceEnvelope.Result.ContentHandle+"0",
		nil,
	)
	tampered.Host = host
	tampered.Header.Set("Authorization", "Bearer "+token)
	tamperedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(tamperedResponse, tampered)
	if tamperedResponse.Code != http.StatusBadRequest {
		t.Fatalf("tampered handle status=%d", tamperedResponse.Code)
	}
	if err := os.WriteFile(
		filepath.Join(root, "main.go"),
		[]byte("package changed\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	staleResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(staleResponse, contentRequest)
	if staleResponse.Code != http.StatusConflict {
		t.Fatalf("stale handle status=%d body=%s", staleResponse.Code, staleResponse.Body.String())
	}
	response = postWeb(
		t,
		server,
		host,
		token,
		"workspace/resource",
		`{"path":"../secret"}`,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("escape status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestUnaryRejectsDeclaredOversizedBody(t *testing.T) {
	const host = "127.0.0.1:43210"
	server := newTestServer(t, host)
	runtime := app.NewRuntime(app.Options{})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	identity, err := protocol.NewWorkspaceIdentity("file:///workspace", "/workspace", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Activate(webhost.Dependencies{
		Runtime: runtime, WorkspaceRoot: "/workspace",
		WorkspaceIdentity: identity,
	}); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://"+host+"/api/v1/system/describe",
		strings.NewReader(`{}`),
	)
	request.Host = host
	request.ContentLength = (1 << 20) + 1
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+bootstrapToken(t, server, host))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func newTestServer(t *testing.T, host string) *webhost.Server {
	t.Helper()
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<main>CodeHelper</main>"), Mode: fs.FileMode(0o444)},
	}
	return newTestServerWithAssets(t, host, assets)
}

func newTestServerWithAssets(
	t *testing.T,
	host string,
	assets fs.FS,
) *webhost.Server {
	t.Helper()
	return newTestServerWithOptions(t, webhost.Options{
		Assets: assets, ExpectedHost: host, Origin: "http://" + host,
	})
}

func newTestServerWithOptions(
	t *testing.T,
	options webhost.Options,
) *webhost.Server {
	t.Helper()
	server, err := webhost.New(options)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func bootstrapToken(t *testing.T, server *webhost.Server, host string) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "http://"+host+"/api/v1/bootstrap", nil)
	request.Host = host
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var value struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &value); err != nil {
		t.Fatal(err)
	}
	return value.Token
}

func fetchBootstrapToken(t *testing.T, origin string) string {
	t.Helper()
	response, err := http.Get(origin + "/api/v1/bootstrap")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var value struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(response.Body).Decode(&value); err != nil {
		t.Fatal(err)
	}
	return value.Token
}

func postWeb(
	t *testing.T,
	server *webhost.Server,
	host, token, route, body string,
) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(
		http.MethodPost,
		"http://"+host+"/api/v1/"+route,
		strings.NewReader(body),
	)
	request.Host = host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}

type webTestBackend struct{}

func (webTestBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "test", Backend: "passthrough",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (webTestBackend) Prepare(
	_ context.Context,
	command sandbox.Command,
) (sandbox.Command, error) {
	return command, nil
}
