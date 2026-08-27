package web_test

import (
	"context"
	"io/fs"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"

	webhost "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/web"
	tracestate "github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestTraceExportDownloadsAuthenticatedWorkspaceScopedNDJSON(t *testing.T) {
	const host = "127.0.0.1:43218"
	server := newTestServerWithOptions(t, webhost.Options{
		Assets: fstest.MapFS{
			"index.html": &fstest.MapFile{
				Data: []byte("<main>CodeHelper</main>"),
				Mode: fs.FileMode(0o444),
			},
		},
		ExpectedHost: host,
		Origin:       "http://" + host,
		Build:        "test-build",
	})
	runtime := app.NewRuntime(app.Options{})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	exporter := &traceExportStub{
		result: tracestate.ExportResult{
			Filename:  "trace.ndjson",
			MediaType: tracestate.ExportMediaType,
			Content:   []byte("{\"record_type\":\"manifest\"}\n"),
		},
	}
	runtime.TraceExport = exporter
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
	token := bootstrapToken(t, server, host)

	response := postWebWorkspace(
		t,
		server,
		host,
		token,
		identity.RootID,
		"trace/export",
		`{"session_id":"session-1"}`,
	)
	if response.Code != http.StatusOK ||
		response.Body.String() != "{\"record_type\":\"manifest\"}\n" ||
		response.Header().Get("Content-Type") !=
			"application/x-ndjson; charset=utf-8" ||
		!strings.Contains(
			response.Header().Get("Content-Disposition"),
			"trace.ndjson",
		) ||
		response.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf(
			"status=%d headers=%v body=%q",
			response.Code,
			response.Header(),
			response.Body.String(),
		)
	}
	if exporter.request.SessionID != "session-1" ||
		exporter.request.ProducerVersion != "test-build" {
		t.Fatalf("export request = %+v", exporter.request)
	}

	unauthorized := postWebWorkspace(
		t,
		server,
		host,
		"",
		identity.RootID,
		"trace/export",
		`{"session_id":"session-1"}`,
	)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}
}

type traceExportStub struct {
	request tracestate.ExportRequest
	result  tracestate.ExportResult
}

func (s *traceExportStub) Export(
	_ context.Context,
	request tracestate.ExportRequest,
) (tracestate.ExportResult, error) {
	s.request = request
	return s.result, nil
}
