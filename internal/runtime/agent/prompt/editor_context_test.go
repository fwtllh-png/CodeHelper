package prompt

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
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

func TestResolveNativeImageAndInlineContext(t *testing.T) {
	root := t.TempDir()
	image := []byte("\x89PNG\r\n\x1a\nfixture")
	if err := os.WriteFile(filepath.Join(root, "screen.png"), image, 0o600); err != nil {
		t.Fatal(err)
	}
	imageDigest := sha256.Sum256(image)
	inline := "go test ./...\nPASS"
	inlineDigest := sha256.Sum256([]byte(inline))
	attachment := "Review the parser boundary."
	attachmentDigest := sha256.Sum256([]byte(attachment))
	pastedImage := []byte("\x89PNG\r\n\x1a\npasted")
	pastedImageDigest := sha256.Sum256(pastedImage)
	prompt, receipts, attachments, err := ResolveEditorContextWithAttachments(
		root,
		"inspect",
		[]protocol.EditorContextReference{
			{
				Kind:   protocol.EditorContextImage,
				Source: protocol.EditorContextSourceNativePicker,
				URI: (&url.URL{
					Scheme: "file", Path: filepath.Join(root, "screen.png"),
				}).String(),
				Path: "screen.png", DocumentVersion: 1,
				Digest: hex.EncodeToString(imageDigest[:]),
				Label:  "screen.png", MediaType: "image/png", Explicit: true,
			},
			{
				Kind:   protocol.EditorContextTerminal,
				Source: protocol.EditorContextSourceNativePicker,
				Digest: hex.EncodeToString(inlineDigest[:]),
				Label:  "Terminal output", MediaType: "text/plain",
				Content: inline, Explicit: true,
			},
			{
				Kind:   protocol.EditorContextAttachment,
				Source: protocol.EditorContextSourceNativePicker,
				Digest: hex.EncodeToString(attachmentDigest[:]),
				Label:  "notes.txt", MediaType: "text/plain",
				Content: attachment, Explicit: true,
			},
			{
				Kind:   protocol.EditorContextImage,
				Source: protocol.EditorContextSourceNativePicker,
				Digest: hex.EncodeToString(pastedImageDigest[:]),
				Label:  "pasted.png", MediaType: "image/png",
				Content:  base64.StdEncoding.EncodeToString(pastedImage),
				Explicit: true,
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(attachments) != 2 || attachments[0].MediaType != "image/png" ||
		attachments[1].Name != "pasted.png" ||
		len(receipts) != 4 || receipts[0].Kind != protocol.EditorContextImage ||
		receipts[1].Kind != protocol.EditorContextTerminal ||
		receipts[2].Kind != protocol.EditorContextAttachment ||
		receipts[3].Kind != protocol.EditorContextImage {
		t.Fatalf("attachments=%+v receipts=%+v", attachments, receipts)
	}
	if !strings.Contains(prompt, "native model content block") ||
		!strings.Contains(prompt, "go test ./...") ||
		!strings.Contains(prompt, "Review the parser boundary.") {
		t.Fatalf("resolved prompt = %q", prompt)
	}
}
