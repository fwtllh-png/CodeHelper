package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

var lspFixtureLog = flag.String("lsp-fixture-log", "", "internal LSP fixture log")

func TestCheckerRunsJSONRPCLifecycleAndReturnsEditedDiagnostics(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "lifecycle.log")
	checker := Checker{
		Binary: os.Args[0],
		Args: []string{
			"-test.run=^TestLSPFixtureProcess$",
			"-lsp-fixture-log=" + logPath,
		},
		Root: root, DiagnosticTimeout: 2 * time.Second, Sandbox: lspTestBackend{},
	}
	diagnostics, err := checker.Analyze(t.Context(), []string{"main.go"}, []Change{{
		Path: "main.go", Text: "package main\nvar broken =\n", Version: 2,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
	got := diagnostics[0]
	if got.File != "main.go" || got.Message != "after edit" || got.Severity != 1 || got.Code != "fixture" {
		t.Fatalf("diagnostic = %+v", got)
	}
	lifecycle, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []string{"initialize", "initialized", "didOpen", "didChange", "shutdown", "exit"} {
		if !strings.Contains(string(lifecycle), event+"\n") {
			t.Fatalf("lifecycle missing %q:\n%s", event, lifecycle)
		}
	}
}

type lspTestBackend struct{}

func (lspTestBackend) Capability() sandbox.Capability {
	return sandbox.Capability{Platform: "test", Backend: "passthrough", Strength: sandbox.StrengthStrong, Available: true}
}

func (lspTestBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	return command, nil
}

func TestLSPFixtureProcess(t *testing.T) {
	if *lspFixtureLog == "" {
		return
	}
	reader := bufio.NewReader(os.Stdin)
	writer := bufio.NewWriter(os.Stdout)
	logPath := *lspFixtureLog
	for {
		message, err := readMessage(reader)
		if err != nil {
			os.Exit(2)
		}
		event := strings.TrimPrefix(message.Method, "textDocument/")
		appendFixtureLog(logPath, event)
		switch message.Method {
		case "initialize":
			writeFixtureMessage(writer, map[string]any{
				"jsonrpc": "2.0", "id": rawID(message.ID),
				"result": map[string]any{"capabilities": map[string]any{
					"textDocumentSync": map[string]any{"openClose": true, "change": 1},
				}},
			})
		case "textDocument/didOpen":
			var params struct {
				TextDocument struct {
					URI string `json:"uri"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(message.Params, &params)
			writeFixtureDiagnostics(writer, params.TextDocument.URI, 1, "before edit")
		case "textDocument/didChange":
			var params struct {
				TextDocument struct {
					URI     string `json:"uri"`
					Version int    `json:"version"`
				} `json:"textDocument"`
			}
			_ = json.Unmarshal(message.Params, &params)
			writeFixtureDiagnostics(writer, params.TextDocument.URI, params.TextDocument.Version, "after edit")
		case "shutdown":
			writeFixtureMessage(writer, map[string]any{
				"jsonrpc": "2.0", "id": rawID(message.ID), "result": nil,
			})
		case "exit":
			os.Exit(0)
		}
	}
}

func writeFixtureDiagnostics(writer *bufio.Writer, uri string, version int, message string) {
	writeFixtureMessage(writer, map[string]any{
		"jsonrpc": "2.0", "method": "textDocument/publishDiagnostics",
		"params": map[string]any{
			"uri": uri, "version": version,
			"diagnostics": []map[string]any{{
				"range": map[string]any{
					"start": map[string]any{"line": 1, "character": 4},
					"end":   map[string]any{"line": 1, "character": 10},
				},
				"severity": 1, "code": "fixture", "source": "fixture-lsp", "message": message,
			}},
		},
	})
}

func writeFixtureMessage(writer *bufio.Writer, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		os.Exit(3)
	}
	_, _ = fmt.Fprintf(writer, "Content-Length: %d\r\n\r\n%s", len(body), body)
	_ = writer.Flush()
}

func appendFixtureLog(path, event string) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		os.Exit(4)
	}
	_, _ = file.WriteString(event + "\n")
	_ = file.Close()
}

func rawID(value json.RawMessage) any {
	var result any
	_ = json.Unmarshal(value, &result)
	return result
}
