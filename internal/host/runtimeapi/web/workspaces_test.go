package web

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

type workspaceControllerFixture struct {
	catalog   WorkspaceCatalog
	removedID *string
}

func (f workspaceControllerFixture) List(context.Context) (WorkspaceCatalog, error) {
	return f.catalog, nil
}

func (workspaceControllerFixture) Add(
	context.Context,
	string,
) (WorkspaceDescriptor, error) {
	return WorkspaceDescriptor{}, nil
}

func (f workspaceControllerFixture) Remove(
	_ context.Context,
	workspaceID string,
) (WorkspaceCatalog, error) {
	if f.removedID != nil {
		*f.removedID = workspaceID
	}
	return f.catalog, nil
}

func TestWorkspaceRemoveRequiresIdentityAndDelegates(t *testing.T) {
	const host = "127.0.0.1:43210"
	var removedID string
	server, err := New(Options{
		Assets: fstest.MapFS{
			"index.html": &fstest.MapFile{
				Data: []byte("<main>CodeHelper</main>"), Mode: fs.FileMode(0o444),
			},
		},
		ExpectedHost: host,
		Token:        "token",
		Workspaces: workspaceControllerFixture{
			catalog: WorkspaceCatalog{
				Version: 1, DefaultWorkspaceID: "default",
				Workspaces: []WorkspaceDescriptor{{ID: "default", Ready: true}},
			},
			removedID: &removedID,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	selectedRequest := httptest.NewRequest(
		http.MethodPost,
		"http://"+host+"/api/v1/workspace/remove",
		strings.NewReader(`{"workspace_id":"secondary"}`),
	)
	selectedRequest.Host = host
	selectedRequest.Header.Set("Content-Type", "application/json")
	selectedRequest.Header.Set("Authorization", "Bearer token")
	selectedRequest.Header.Set("Idempotency-Key", "remove-selected")
	selectedRequest.Header.Set(workspaceHeader, "secondary")
	selectedResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(selectedResponse, selectedRequest)
	if selectedResponse.Code != http.StatusConflict || removedID != "" {
		t.Fatalf(
			"selected removal status=%d removed=%q body=%s",
			selectedResponse.Code,
			removedID,
			selectedResponse.Body.String(),
		)
	}

	request := httptest.NewRequest(
		http.MethodPost,
		"http://"+host+"/api/v1/workspace/remove",
		strings.NewReader(`{"workspace_id":"secondary"}`),
	)
	request.Host = host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("Idempotency-Key", "remove-secondary")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		removedID != "secondary" ||
		!strings.Contains(response.Body.String(), `"default_workspace_id":"default"`) {
		t.Fatalf(
			"status=%d removed=%q body=%s",
			response.Code,
			removedID,
			response.Body.String(),
		)
	}

	missingIdentity := httptest.NewRequest(
		http.MethodPost,
		"http://"+host+"/api/v1/workspace/remove",
		strings.NewReader(`{"workspace_id":"secondary"}`),
	)
	missingIdentity.Host = host
	missingIdentity.Header.Set("Content-Type", "application/json")
	missingIdentity.Header.Set("Authorization", "Bearer token")
	rejected := httptest.NewRecorder()
	server.Handler().ServeHTTP(rejected, missingIdentity)
	if rejected.Code != http.StatusBadRequest {
		t.Fatalf("missing idempotency status=%d body=%s", rejected.Code, rejected.Body)
	}
}

func TestWorkspaceSelectDirectoryUsesNativePicker(t *testing.T) {
	const host = "127.0.0.1:43210"
	var initialPath string
	server, err := New(Options{
		Assets: fstest.MapFS{
			"index.html": &fstest.MapFile{
				Data: []byte("<main>CodeHelper</main>"), Mode: fs.FileMode(0o444),
			},
		},
		ExpectedHost: host,
		Token:        "token",
		Workspaces: workspaceControllerFixture{catalog: WorkspaceCatalog{
			Version: 1, DefaultWorkspaceID: "current",
			Workspaces: []WorkspaceDescriptor{{
				ID: "current", Root: "/workspace/current", Ready: true,
			}},
		}},
		PickDirectory: func(
			_ context.Context,
			initial string,
		) (string, bool, error) {
			initialPath = initial
			return "/workspace/selected", false, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://"+host+"/api/v1/workspace/select-directory",
		strings.NewReader(`{}`),
	)
	request.Host = host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"path":"/workspace/selected"`) ||
		initialPath != "/workspace/current" {
		t.Fatalf(
			"status=%d initial=%q body=%s",
			response.Code,
			initialPath,
			response.Body.String(),
		)
	}
}

func TestWorkspaceSelectDirectoryReportsCancellation(t *testing.T) {
	const host = "127.0.0.1:43210"
	server, err := New(Options{
		Assets: fstest.MapFS{
			"index.html": &fstest.MapFile{
				Data: []byte("<main>CodeHelper</main>"), Mode: fs.FileMode(0o444),
			},
		},
		ExpectedHost: host,
		Token:        "token",
		Workspaces:   workspaceControllerFixture{},
		PickDirectory: func(
			context.Context,
			string,
		) (string, bool, error) {
			return "", true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"http://"+host+"/api/v1/workspace/select-directory",
		strings.NewReader(`{}`),
	)
	request.Host = host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"cancelled":true`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestWorkspaceSelectDirectoryRejectsConcurrentPicker(t *testing.T) {
	const host = "127.0.0.1:43210"
	server, err := New(Options{
		Assets: fstest.MapFS{
			"index.html": &fstest.MapFile{
				Data: []byte("<main>CodeHelper</main>"), Mode: fs.FileMode(0o444),
			},
		},
		ExpectedHost: host,
		Token:        "token",
		Workspaces:   workspaceControllerFixture{},
		PickDirectory: func(
			context.Context,
			string,
		) (string, bool, error) {
			return "", true, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server.directoryPickerMu.Lock()
	defer server.directoryPickerMu.Unlock()
	request := httptest.NewRequest(
		http.MethodPost,
		"http://"+host+"/api/v1/workspace/select-directory",
		strings.NewReader(`{}`),
	)
	request.Host = host
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
