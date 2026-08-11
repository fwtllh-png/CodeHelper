package httpclient

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func TestDumpProviderFailureWritesFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("CODEHELPER_DEBUG_DIR", dir)
	t.Setenv("CODEHELPER_PROVIDER_DUMP", "reasoning")

	request := testRequest(t, "https://provider.test", "openai_responses")
	request.Messages = []provider.Message{
		provider.TextMessage(provider.RoleUser, "hi"),
		{Role: provider.RoleAssistant, Blocks: []provider.ContentBlock{
			{Type: provider.ContentReasoning, Text: "think"},
			{Type: provider.ContentToolCall, ToolCall: &provider.ToolCall{ID: "c1", Name: "echo", Arguments: `{}`}},
		}},
	}
	body, path, err := encodeRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	dumpPath, err := dumpProviderFailure(
		request, body, path, 400,
		`The 'reasoning_text' in the thinking mode must be passed back to the API.`,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(dumpPath, dir) {
		t.Fatalf("dumpPath = %q", dumpPath)
	}
	data, err := os.ReadFile(dumpPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "messages_summary") ||
		!strings.Contains(string(data), "encoded_input_summary") {
		t.Fatalf("dump missing summaries: %s", data)
	}
	if filepath.Ext(dumpPath) != ".json" {
		t.Fatalf("ext = %q", dumpPath)
	}
}

func TestShouldDumpProviderModes(t *testing.T) {
	t.Setenv("CODEHELPER_PROVIDER_DUMP", "off")
	if shouldDumpProvider(400) {
		t.Fatal("off should not dump")
	}
	t.Setenv("CODEHELPER_PROVIDER_DUMP", "error")
	if !shouldDumpProvider(500) {
		t.Fatal("error mode should dump any 4xx/5xx")
	}
	t.Setenv("CODEHELPER_PROVIDER_DUMP", "reasoning")
	if shouldDumpProvider(400) {
		t.Fatal("legacy reasoning mode must not classify errors by text")
	}
}
