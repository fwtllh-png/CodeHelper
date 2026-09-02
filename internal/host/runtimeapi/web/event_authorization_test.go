package web

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/persist/repoindex"
	sqlitestate "github.com/fwtllh-png/QCode/internal/persist/state/sqlite"
	"github.com/fwtllh-png/QCode/internal/platform/repowalk"
	"github.com/fwtllh-png/QCode/internal/platform/workspacequery"
	"github.com/fwtllh-png/QCode/internal/runtime/app"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
	"github.com/fwtllh-png/QCode/internal/security/controlmatrix"
	"github.com/fwtllh-png/QCode/internal/security/sandbox"
)

func TestAuthorizedEventSessionRejectsForeignWorkspace(t *testing.T) {
	store := &eventAuthorizationLifecycle{summary: protocol.SessionSummary{
		Version:       protocol.SessionLifecycleVersion,
		Revision:      1,
		SessionID:     "session-foreign",
		ThreadID:      "thread-foreign",
		Status:        protocol.SessionStatusIdle,
		WorkspaceRoot: "/workspace/foreign",
		CreatedAt:     time.Now().UTC(),
		UpdatedAt:     time.Now().UTC(),
	}}
	runtime := app.NewRuntime(app.Options{
		WorkspaceRoot:    "/workspace/current",
		SessionLifecycle: store,
	})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })

	sessionID, authorized := authorizedEventSession(t.Context(), runtime, protocol.Event{
		ThreadID: "thread-foreign",
		Sequence: 7,
	})
	if authorized || sessionID != "" {
		t.Fatalf("foreign event authorization = %q, %t", sessionID, authorized)
	}

	store.summary.WorkspaceRoot = "/workspace/current"
	sessionID, authorized = authorizedEventSession(t.Context(), runtime, protocol.Event{
		ThreadID: "thread-foreign",
		Sequence: 8,
	})
	if !authorized || sessionID != "session-foreign" {
		t.Fatalf("local event authorization = %q, %t", sessionID, authorized)
	}
}

func TestValidateWebEditorContextRequiresEnumeratedResource(t *testing.T) {
	root := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	if err := os.WriteFile(
		filepath.Join(root, "visible.txt"),
		[]byte("visible\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	query, err := workspacequery.New(root, eventAuthorizationBackend{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resource, err := query.Resource(t.Context(), "visible.txt")
	if err != nil {
		t.Fatal(err)
	}
	identity, err := protocol.NewWorkspaceIdentity(
		"file://"+filepath.ToSlash(root),
		root,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	resourceURI, err := workspaceResourceURI(identity, resource.Path)
	if err != nil {
		t.Fatal(err)
	}
	payload := &protocol.StartTurnPayload{
		Context: []protocol.EditorContextReference{{
			Kind: protocol.EditorContextFile, Source: protocol.EditorContextSourceComposer,
			URI: resourceURI, Path: resource.Path, DocumentVersion: 1,
			Digest: resource.Digest, Explicit: true,
		}},
	}
	dependencies := Dependencies{Workspace: query, WorkspaceIdentity: identity}
	if err := validateWebEditorContext(t.Context(), dependencies, payload); err != nil {
		t.Fatalf("valid context rejected: %v", err)
	}
	payload.Context[0].Path = ".git/config"
	if err := validateWebEditorContext(t.Context(), dependencies, payload); err == nil {
		t.Fatal("non-enumerated context was accepted")
	}
}

func TestValidateWebImageContextRequiresCurrentSignedMetadata(t *testing.T) {
	root, query, identity := webContextWorkspace(t, map[string][]byte{
		"diagram.png": append([]byte("\x89PNG\r\n\x1a\n"), []byte("fixture")...),
	})
	image, err := query.Image(t.Context(), "diagram.png")
	if err != nil {
		t.Fatal(err)
	}
	resourceURI, err := workspaceResourceURI(identity, image.Path)
	if err != nil {
		t.Fatal(err)
	}
	reference := protocol.EditorContextReference{
		Kind: protocol.EditorContextImage, Source: protocol.EditorContextSourceNativePicker,
		URI: resourceURI, Path: image.Path, DocumentVersion: 1,
		Digest: image.Digest, Label: "diagram.png", MediaType: image.MediaType,
		Explicit: true,
	}
	payload := &protocol.StartTurnPayload{Context: []protocol.EditorContextReference{reference}}
	dependencies := Dependencies{Workspace: query, WorkspaceIdentity: identity}
	if err := validateWebEditorContext(t.Context(), dependencies, payload); err != nil {
		t.Fatalf("valid image context rejected: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "diagram.png"),
		append([]byte("\x89PNG\r\n\x1a\n"), []byte("changed")...),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if err := validateWebEditorContext(t.Context(), dependencies, payload); err == nil {
		t.Fatal("stale image context was accepted")
	}
}

func TestValidateWebInlineAttachmentsRechecksContent(t *testing.T) {
	text := []byte("review the parser\n")
	textDigest := sha256.Sum256(text)
	textReference := protocol.EditorContextReference{
		Kind: protocol.EditorContextAttachment, Source: protocol.EditorContextSourceNativePicker,
		Digest: hex.EncodeToString(textDigest[:]), Label: "notes.txt",
		MediaType: "text/plain", Content: string(text), Explicit: true,
	}
	if err := validateWebEditorContext(
		t.Context(),
		Dependencies{},
		&protocol.StartTurnPayload{
			ThreadID: "thread",
			Context:  []protocol.EditorContextReference{textReference},
		},
	); err != nil {
		t.Fatalf("valid text attachment rejected: %v", err)
	}
	forgedText := textReference
	forgedText.Content = "forged"
	if err := validateWebEditorContext(
		t.Context(),
		Dependencies{},
		&protocol.StartTurnPayload{
			ThreadID: "thread",
			Context:  []protocol.EditorContextReference{forgedText},
		},
	); err == nil {
		t.Fatal("text attachment with a stale digest was accepted")
	}

	image := []byte("\x89PNG\r\n\x1a\nfixture")
	imageDigest := sha256.Sum256(image)
	imageReference := protocol.EditorContextReference{
		Kind: protocol.EditorContextImage, Source: protocol.EditorContextSourceNativePicker,
		Digest: hex.EncodeToString(imageDigest[:]), Label: "pasted.png",
		MediaType: "image/png",
		Content:   base64.StdEncoding.EncodeToString(image),
		Explicit:  true,
	}
	if err := validateWebEditorContext(
		t.Context(),
		Dependencies{},
		&protocol.EnqueueTurnPayload{
			ThreadID: "thread",
			Context:  []protocol.EditorContextReference{imageReference},
		},
	); err != nil {
		t.Fatalf("valid queued image attachment rejected: %v", err)
	}
	forgedImage := imageReference
	forgedImage.MediaType = "image/jpeg"
	if err := validateWebEditorContext(
		t.Context(),
		Dependencies{},
		&protocol.EnqueueTurnPayload{
			ThreadID: "thread",
			Context:  []protocol.EditorContextReference{forgedImage},
		},
	); err == nil {
		t.Fatal("image attachment with forged media type was accepted")
	}
}

func TestValidateWebSymbolContextReplaysRepositoryIndex(t *testing.T) {
	root, query, identity := webContextWorkspace(t, map[string][]byte{
		"main.go": []byte("package main\n\nfunc Serve() {}\n"),
	})
	database, err := sqlitestate.Open(
		t.Context(),
		filepath.Join(t.TempDir(), "state.db"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	rows, err := repoindex.NewStore(database.DB(), root)
	if err != nil {
		t.Fatal(err)
	}
	walker, err := repowalk.New(root, eventAuthorizationBackend{})
	if err != nil {
		t.Fatal(err)
	}
	index, err := repoindex.NewIndex(rows, walker, repoindex.Options{})
	if err != nil {
		t.Fatal(err)
	}
	found, snapshot, err := index.Symbols(
		t.Context(),
		repoindex.Query{Name: "Serve", Exact: true},
	)
	if err != nil || !snapshot.Ready() || len(found) != 1 {
		t.Fatalf("symbols = %+v, snapshot = %+v, err = %v", found, snapshot, err)
	}
	symbol, err := resolveWorkspaceSymbol(t.Context(), query, identity, found[0])
	if err != nil {
		t.Fatal(err)
	}
	reference := protocol.EditorContextReference{
		Kind: protocol.EditorContextSymbol, Source: protocol.EditorContextSourceNativePicker,
		URI: symbol.URI, Path: symbol.Path, DocumentVersion: symbol.DocumentVersion,
		Digest: symbol.Digest, Range: &symbol.Range,
		Symbol: &protocol.EditorSymbol{
			Name: symbol.Name, Kind: symbol.Kind,
			SelectionRange: &symbol.SelectionRange,
		},
		Explicit: true,
	}
	dependencies := Dependencies{
		Workspace: query, WorkspaceIdentity: identity, RepositoryIndex: index,
	}
	payload := &protocol.StartTurnPayload{Context: []protocol.EditorContextReference{reference}}
	if err := validateWebEditorContext(t.Context(), dependencies, payload); err != nil {
		t.Fatalf("valid symbol context rejected: %v", err)
	}
	payload.Context[0].Symbol.Name = "Forged"
	if err := validateWebEditorContext(t.Context(), dependencies, payload); err == nil {
		t.Fatal("forged symbol context was accepted")
	}
}

func TestValidateWebDiagnosticsContextRequiresPersistedThreadReceipt(t *testing.T) {
	_, query, identity := webContextWorkspace(t, map[string][]byte{
		"main.go": []byte("package main\n\nbroken\n"),
	})
	events := app.NewMemoryEventStore(8)
	diagnostics, err := protocol.NewEvent(protocol.EventMeta{
		Sequence: 1, OperationID: "operation",
		ThreadID: "thread", TurnID: "turn", ItemID: "diagnostics",
	}, &protocol.DiagnosticsData{
		Tool: "exec_command", CallID: "call-1",
		Receipts: []protocol.DiagnosticReceipt{{
			Path: "main.go", Status: "failed",
			Diagnostics: []protocol.Diagnostic{{
				Path: "main.go",
				Range: protocol.DiagnosticRange{
					Start: protocol.DiagnosticPosition{Line: 2, Character: 0},
					End:   protocol.DiagnosticPosition{Line: 2, Character: 6},
				},
				Severity: "error", Message: "broken", Source: "fixture",
			}},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := events.Append(t.Context(), diagnostics); err != nil {
		t.Fatal(err)
	}
	runtime := app.NewRuntime(app.Options{EventStore: events})
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	reference, ok, err := workspaceDiagnosticReference(
		t.Context(),
		query,
		identity,
		diagnostics.Data.(*protocol.DiagnosticsData).Receipts[0],
	)
	if err != nil || !ok {
		t.Fatalf("diagnostic reference ok=%t err=%v", ok, err)
	}
	dependencies := Dependencies{
		Runtime: runtime, Workspace: query, WorkspaceIdentity: identity,
	}
	payload := &protocol.StartTurnPayload{
		ThreadID: "thread", Context: []protocol.EditorContextReference{reference},
	}
	if err := validateWebEditorContext(t.Context(), dependencies, payload); err != nil {
		t.Fatalf("valid diagnostics context rejected: %v", err)
	}
	payload.Context[0].Diagnostics[0].Message = "forged"
	if err := validateWebEditorContext(t.Context(), dependencies, payload); err == nil {
		t.Fatal("forged diagnostics context was accepted")
	}
	payload.Context[0] = reference
	payload.ThreadID = "foreign-thread"
	if err := validateWebEditorContext(t.Context(), dependencies, payload); err == nil {
		t.Fatal("foreign diagnostics context was accepted")
	}
}

func webContextWorkspace(
	t *testing.T,
	files map[string][]byte,
) (string, *workspacequery.Service, protocol.WorkspaceIdentity) {
	t.Helper()
	root := t.TempDir()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(root, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	query, err := workspacequery.New(root, eventAuthorizationBackend{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := protocol.NewWorkspaceIdentity(
		"file://"+filepath.ToSlash(root),
		root,
		"",
	)
	if err != nil {
		t.Fatal(err)
	}
	return root, query, identity
}

type eventAuthorizationBackend struct{}

func (eventAuthorizationBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "test", Backend: "passthrough",
		Available: true,
		Effective: controlmatrix.
			Matrix{
			FilesystemRead: controlmatrix.
				FilesystemReadDeclaredRoots,
			FilesystemWrite: controlmatrix.
				FilesystemWriteExactPaths,
			Network: controlmatrix.
				NetworkDenied, ProcessTree: controlmatrix.ProcessTreeGroupKill,
			CrossProcess: controlmatrix.CrossProcessUnrestricted, Syscall: controlmatrix.
					SyscallDenyDangerous, IPC: controlmatrix.IPCUnrestricted,
			PathIdentity: controlmatrix.PathIdentityDescriptorRelative, ArtifactOrigin: controlmatrix.ArtifactOriginUnverifiedPath,
			DurableRecovery: controlmatrix.DurableRecoveryMemoryOnly},
	}
}

func (eventAuthorizationBackend) Prepare(
	_ context.Context,
	command sandbox.Command,
) (sandbox.Command, error) {
	return command, nil
}

type eventAuthorizationLifecycle struct {
	summary protocol.SessionSummary
}

func (s *eventAuthorizationLifecycle) ListLifecycle(
	context.Context,
	protocol.SessionListQuery,
) (protocol.SessionList, error) {
	return protocol.SessionList{
		Version:  protocol.SessionLifecycleVersion,
		Sessions: []protocol.SessionSummary{s.summary},
	}, nil
}

func (s *eventAuthorizationLifecycle) GetLifecycle(
	_ context.Context,
	sessionID string,
) (protocol.SessionSummary, error) {
	if sessionID != s.summary.SessionID {
		return protocol.SessionSummary{}, errors.New("session not found")
	}
	return s.summary, nil
}

func (s *eventAuthorizationLifecycle) ThreadIDs(
	_ context.Context,
	sessionID string,
) ([]protocol.ThreadID, error) {
	if sessionID != s.summary.SessionID {
		return nil, errors.New("session not found")
	}
	return []protocol.ThreadID{s.summary.ThreadID}, nil
}

func (s *eventAuthorizationLifecycle) SessionForThread(
	_ context.Context,
	threadID protocol.ThreadID,
) (string, error) {
	if threadID != s.summary.ThreadID {
		return "", errors.New("thread not found")
	}
	return s.summary.SessionID, nil
}

func (s *eventAuthorizationLifecycle) ActivateThread(
	context.Context,
	string,
	protocol.ThreadID,
) (protocol.SessionSummary, error) {
	return s.summary, nil
}

func (s *eventAuthorizationLifecycle) UpdateLifecycle(
	context.Context,
	string,
	uint64,
	protocol.SessionLifecyclePatch,
) (protocol.SessionSummary, error) {
	return s.summary, nil
}

func (s *eventAuthorizationLifecycle) DeleteLifecycle(
	context.Context,
	string,
	uint64,
) (protocol.SessionDeleteResult, error) {
	return protocol.SessionDeleteResult{}, nil
}
