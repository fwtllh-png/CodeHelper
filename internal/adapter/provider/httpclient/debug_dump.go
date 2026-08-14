package httpclient

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

func providerDumpMode() string {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("CODEHELPER_PROVIDER_DUMP")))
	switch mode {
	case "off", "0", "false", "never", "", "reasoning":
		return "off"
	case "always", "all", "1", "true":
		return "always"
	case "error", "errors":
		return "error"
	default:
		return "off"
	}
}
func shouldDumpProvider(status int) bool {
	switch providerDumpMode() {
	case "off":
		return false
	case "always":
		return true
	default:
		return status >= 400
	}
}
func providerDebugDir() string {
	if dir := strings.TrimSpace(os.Getenv("CODEHELPER_DEBUG_DIR")); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(os.TempDir(), "codehelper-debug")
	}
	return filepath.Join(home, ".codehelper", "debug")
}

type providerDump struct {
	Time         string           `json:"time"`
	Status       int              `json:"status"`
	Error        string           `json:"error"`
	Protocol     string           `json:"protocol"`
	Model        string           `json:"model"`
	Provider     string           `json:"provider"`
	Path         string           `json:"request_path"`
	Messages     []messageSummary `json:"messages_summary"`
	EncodedInput []inputSummary   `json:"encoded_input_summary"`
	HowToShare   string           `json:"how_to_share"`
	DumpHint     string           `json:"dump_hint"`
}
type messageSummary struct {
	Index  int            `json:"index"`
	Role   string         `json:"role"`
	Turn   uint64         `json:"turn,omitempty"`
	Blocks []blockSummary `json:"blocks"`
}
type blockSummary struct {
	Type            string `json:"type"`
	TextLen         int    `json:"text_len,omitempty"`
	ID              string `json:"id,omitempty"`
	ProviderType    string `json:"provider_type,omitempty"`
	ProviderDataLen int    `json:"provider_data_len,omitempty"`
	ToolName        string `json:"tool_name,omitempty"`
	ToolCallID      string `json:"tool_call_id,omitempty"`
	HasSignature    bool   `json:"has_signature,omitempty"`
}
type inputSummary struct {
	Index    int    `json:"index"`
	Type     string `json:"type"`
	Role     string `json:"role,omitempty"`
	ID       string `json:"id,omitempty"`
	CallID   string `json:"call_id,omitempty"`
	Name     string `json:"name,omitempty"`
	TextLen  int    `json:"text_len,omitempty"`
	ContentN int    `json:"content_parts,omitempty"`
}

// dumpProviderFailure writes a redacted diagnostic JSON for the failed request.
func dumpProviderFailure(
	request provider.ModelRequest,
	body []byte,
	path string,
	status int,
	errorText string,
) (string, error) {
	dir := providerDebugDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	stamp := time.Now().UTC().Format("20060102-150405")
	name := fmt.Sprintf("provider-%d-%s.json", status, stamp)
	filePath := filepath.Join(dir, name)

	dump := providerDump{
		Time:         time.Now().UTC().Format(time.RFC3339),
		Status:       status,
		Error:        truncateDump(errorText, 2<<10),
		Protocol:     string(request.Route.Protocol()),
		Model:        request.Route.Model().ID,
		Provider:     request.Route.ProviderID(),
		Path:         path,
		Messages:     summarizeMessages(request.Messages),
		EncodedInput: summarizeEncodedBody(body, request.Route.Protocol()),
		HowToShare:   "Review this diagnostic before attaching it to a provider error report.",
		DumpHint:     "CODEHELPER_PROVIDER_DUMP=error|always  CODEHELPER_DEBUG_DIR=/path",
	}
	data, err := json.MarshalIndent(dump, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return "", err
	}
	return filePath, nil
}
func summarizeMessages(messages []provider.Message) []messageSummary {
	out := make([]messageSummary, 0, len(messages))
	for index, message := range messages {
		summary := messageSummary{
			Index: index, Role: string(message.Role), Turn: message.Turn,
			Blocks: make([]blockSummary, 0, len(message.Blocks)),
		}
		for _, block := range message.Blocks {
			item := blockSummary{
				Type: string(block.Type), TextLen: len(block.Text),
				ID: block.ID, ProviderType: block.ProviderType,
				ProviderDataLen: len(block.ProviderData),
				HasSignature:    block.Signature != "",
			}
			if block.ToolCall != nil {
				item.ToolName = block.ToolCall.Name
				item.ToolCallID = block.ToolCall.ID
			}
			if block.ToolResult != nil {
				item.ToolCallID = block.ToolResult.CallID
			}
			summary.Blocks = append(summary.Blocks, item)
		}
		out = append(out, summary)
	}
	return out
}
func summarizeEncodedBody(body []byte, wire model.WireProtocol) []inputSummary {
	if len(body) == 0 {
		return nil
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil
	}
	switch wire {
	case model.ProtocolOpenAIResponses:
		items, _ := payload["input"].([]any)
		out := make([]inputSummary, 0, len(items))
		for index, raw := range items {
			item, _ := raw.(map[string]any)
			summary := inputSummary{
				Index:  index,
				Type:   stringValue(item["type"]),
				Role:   stringValue(item["role"]),
				ID:     stringValue(item["id"]),
				CallID: stringValue(item["call_id"]),
				Name:   stringValue(item["name"]),
			}
			if summary.Type == "" && summary.Role != "" {
				summary.Type = "message"
			}
			switch content := item["content"].(type) {
			case string:
				summary.TextLen = len(content)
			case []any:
				summary.ContentN = len(content)
				for _, part := range content {
					m, _ := part.(map[string]any)
					if text := stringValue(m["text"]); text != "" {
						summary.TextLen += len(text)
					}
				}
			}
			out = append(out, summary)
		}
		return out
	default:
		return nil
	}
}
func stringValue(value any) string {
	result, _ := value.(string)
	return result
}
func truncateDump(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}
