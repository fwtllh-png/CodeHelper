package app

import (
	"crypto/sha256"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestResolveEditorContextReadsExplicitFileAndSelection(t *testing.T) {
	root := t.TempDir()
	content := []byte("zero\nA😀BC\n")
	file := filepath.Join(root, "value.go")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	common := protocol.EditorContextReference{
		URI:  (&url.URL{Scheme: "file", Path: file}).String(),
		Path: "value.go", DocumentVersion: 3,
		Digest: hex.EncodeToString(digest[:]), Explicit: true,
	}
	fileReference := common
	fileReference.Kind = protocol.EditorContextFile
	selection := common
	selection.Kind = protocol.EditorContextSelection
	selection.Range = &protocol.EditorRange{
		Start: protocol.EditorPosition{Line: 1, Character: 1},
		End:   protocol.EditorPosition{Line: 1, Character: 3},
	}

	got, receipts, err := resolveEditorContext(
		root, "explain", []protocol.EditorContextReference{fileReference, selection},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"explain", "untrusted data", `"path":"value.go"`,
		`"content":"zero\nA😀BC\n"`, `"content":"😀"`,
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("resolved prompt does not contain %q:\n%s", fragment, got)
		}
	}
	if len(receipts) != 2 || receipts[0].Kind != protocol.EditorContextFile ||
		receipts[1].Kind != protocol.EditorContextSelection ||
		receipts[1].OriginalBytes != len("😀") ||
		receipts[1].RetainedBytes != len("😀") {
		t.Fatalf("editor context receipts = %+v", receipts)
	}
}

func TestResolveEditorContextBindsRemoteEditorURIToRuntimePath(t *testing.T) {
	root := t.TempDir()
	content := []byte("remote\n")
	if err := os.WriteFile(filepath.Join(root, "value.go"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	rootURI := (&url.URL{
		Scheme: "vscode-remote", Host: "ssh-remote+dev", Path: root,
	}).String()
	identity, err := protocol.NewWorkspaceIdentity(rootURI, root, "ssh-remote")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	reference := protocol.EditorContextReference{
		Kind: protocol.EditorContextFile,
		URI: (&url.URL{
			Scheme: "vscode-remote", Host: "ssh-remote+dev",
			Path: filepath.ToSlash(filepath.Join(root, "value.go")),
		}).String(),
		Path: "value.go", DocumentVersion: 1,
		Digest: hex.EncodeToString(sum[:]), Explicit: true,
	}
	if _, _, err := resolveEditorContext(
		root, "inspect", []protocol.EditorContextReference{reference}, identity,
	); err != nil {
		t.Fatal(err)
	}
	reference.URI = strings.Replace(reference.URI, "ssh-remote+dev", "ssh-remote+other", 1)
	if _, _, err := resolveEditorContext(
		root, "inspect", []protocol.EditorContextReference{reference}, identity,
	); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("remote authority mismatch error = %v", err)
	}
}

func TestResolveEditorContextRejectsDriftAndIdentityMismatch(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "value.go")
	if err := os.WriteFile(file, []byte("current"), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := protocol.EditorContextReference{
		Kind: protocol.EditorContextFile,
		URI:  (&url.URL{Scheme: "file", Path: file}).String(),
		Path: "value.go", DocumentVersion: 1,
		Digest: strings.Repeat("a", 64), Explicit: true,
	}
	if _, _, err := resolveEditorContext(
		root, "inspect", []protocol.EditorContextReference{valid},
	); err == nil || !strings.Contains(err.Error(), "changed after capture") {
		t.Fatalf("digest drift error = %v", err)
	}
	sum := sha256.Sum256([]byte("current"))
	valid.Digest = hex.EncodeToString(sum[:])
	other := filepath.Join(root, "other.go")
	if err := os.WriteFile(other, []byte("other"), 0o600); err != nil {
		t.Fatal(err)
	}
	valid.URI = (&url.URL{
		Scheme: "file", Path: other,
	}).String()
	if _, _, err := resolveEditorContext(
		root, "inspect", []protocol.EditorContextReference{valid},
	); err == nil || !strings.Contains(err.Error(), "does not belong") {
		t.Fatalf("URI mismatch error = %v", err)
	}
	valid.URI = (&url.URL{Scheme: "file", Path: file}).String()
	valid.Path = "../value.go"
	if _, _, err := resolveEditorContext(
		root, "inspect", []protocol.EditorContextReference{valid},
	); err == nil || !strings.Contains(err.Error(), "not canonical") {
		t.Fatalf("path escape error = %v", err)
	}
}

func TestResolveEditorContextCropsLargeText(t *testing.T) {
	root := t.TempDir()
	content := []byte(strings.Repeat("x", maxEditorContextItemBytes+100))
	file := filepath.Join(root, "large.txt")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	got, receipts, err := resolveEditorContext(root, "inspect", []protocol.EditorContextReference{{
		Kind: protocol.EditorContextFile,
		URI:  (&url.URL{Scheme: "file", Path: file}).String(),
		Path: "large.txt", DocumentVersion: 1,
		Digest: hex.EncodeToString(sum[:]), Explicit: true,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "editor context truncated") ||
		!strings.Contains(got, `"content_truncated":true`) {
		t.Fatalf("large context was not cropped: %s", got[:256])
	}
	if len(receipts) != 1 || !receipts[0].Truncated ||
		receipts[0].OriginalBytes != len(content) ||
		receipts[0].RetainedBytes != maxEditorContextItemBytes {
		t.Fatalf("large context receipt = %+v", receipts)
	}
}

func TestResolveEditorContextValidatesSymbolAndDiagnostics(t *testing.T) {
	root := t.TempDir()
	content := []byte("package demo\n\nfunc Serve() {}\n")
	file := filepath.Join(root, "main.go")
	if err := os.WriteFile(file, content, 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	common := protocol.EditorContextReference{
		Source: protocol.EditorContextSourceSelectionCommand,
		URI:    (&url.URL{Scheme: "file", Path: file}).String(),
		Path:   "main.go", DocumentVersion: 2,
		Digest: hex.EncodeToString(sum[:]), Explicit: true,
	}
	symbol := common
	symbol.Kind = protocol.EditorContextSymbol
	symbol.Range = &protocol.EditorRange{
		Start: protocol.EditorPosition{Line: 2},
		End:   protocol.EditorPosition{Line: 2, Character: 15},
	}
	symbol.Symbol = &protocol.EditorSymbol{
		Name: "Serve", Kind: "function",
		SelectionRange: &protocol.EditorRange{
			Start: protocol.EditorPosition{Line: 2, Character: 5},
			End:   protocol.EditorPosition{Line: 2, Character: 10},
		},
	}
	diagnostics := common
	diagnostics.Kind = protocol.EditorContextDiagnostics
	diagnostics.Source = protocol.EditorContextSourceCodeAction
	diagnostics.Diagnostics = []protocol.EditorDiagnostic{{
		Range: protocol.EditorRange{
			Start: protocol.EditorPosition{Line: 2, Character: 5},
			End:   protocol.EditorPosition{Line: 2, Character: 10},
		},
		Severity: "error", Code: "E001", Message: "fixture failure", Source: "fixture",
	}}
	diagnostics.OmittedDiagnostics = 2

	got, receipts, err := resolveEditorContext(
		root, "fix", []protocol.EditorContextReference{symbol, diagnostics},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		`"kind":"symbol"`, `"name":"Serve"`, `"kind":"diagnostics"`,
		`"message":"fixture failure"`, `"omitted_diagnostics":2`,
	} {
		if !strings.Contains(got, fragment) {
			t.Fatalf("resolved prompt does not contain %q:\n%s", fragment, got)
		}
	}
	if len(receipts) != 2 || receipts[0].Symbol == nil ||
		receipts[1].DiagnosticCount != 1 ||
		receipts[1].OmittedDiagnostics != 2 {
		t.Fatalf("native context receipts = %+v", receipts)
	}

	symbol.Range.End.Character = 100
	if _, _, err := resolveEditorContext(
		root, "fix", []protocol.EditorContextReference{symbol},
	); err == nil || !strings.Contains(err.Error(), "outside line") {
		t.Fatalf("symbol range error = %v", err)
	}
	diagnostics.Diagnostics[0].Range.End.Character = 100
	if _, _, err := resolveEditorContext(
		root, "fix", []protocol.EditorContextReference{diagnostics},
	); err == nil || !strings.Contains(err.Error(), "outside line") {
		t.Fatalf("diagnostic range error = %v", err)
	}
}
