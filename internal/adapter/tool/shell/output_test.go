//go:build !windows

package shell

import (
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

func TestShellRunPublishesBoundedOutputReceipt(t *testing.T) {
	manager := process.NewSessionManager(4096)
	defer manager.CloseAll()
	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(
		registry, t.TempDir(), manager, passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}
	result := executeSessionTool(t, registry, "shell_run", map[string]any{
		"command": `dd if=/dev/zero bs=2097152 count=1 2>/dev/null | tr '\000' x`,
	})
	receipt, ok := result.Metadata["output_receipt"].(process.OutputReceipt)
	if !ok {
		t.Fatalf("output receipt type = %T", result.Metadata["output_receipt"])
	}
	if receipt.Stdout.TotalBytes != 2<<20 ||
		receipt.Stdout.RetainedBytes != process.ModelOutputLimitBytes ||
		receipt.Stdout.OmittedBytes != 1<<20 {
		t.Fatalf("output receipt = %+v", receipt)
	}
	captured, _ := result.Metadata["stdout"].(string)
	if !strings.Contains(captured, "[output truncated: 1048576 bytes omitted]") ||
		len(captured) > process.ModelOutputLimitBytes+128 {
		t.Fatalf("bounded captured output bytes = %d", len(captured))
	}
	if len(result.Content) > process.ModelOutputLimitBytes+128 {
		t.Fatalf("bounded model content bytes = %d", len(result.Content))
	}
	execution, ok := result.Metadata["command_execution"].(map[string]any)
	if !ok ||
		execution["stdout_bytes"] != uint64(2<<20) ||
		execution["omitted_bytes"] != uint64(1<<20) {
		t.Fatalf("command execution receipt = %#v", execution)
	}
}
