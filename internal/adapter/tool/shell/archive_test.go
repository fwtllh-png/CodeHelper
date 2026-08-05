package shell

import (
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/contentstore"
	"github.com/fwtllh-png/CodeHelper/internal/persist/joblog"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

// A model polling a chatty background job used to hit "cursor expired" and lose
// the output for good. Now the wait says the bytes came from the archive, hands
// them over, and — when they are too big to inline — leaves a handle to page.
func TestWaitingOnAChattyJobRecoversOutputThroughAHandle(t *testing.T) {
	archive, err := joblog.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archive.Close() })
	// A buffer far smaller than the output makes the poller fall behind for sure.
	manager := process.NewSessionManager(64)
	manager.SetArchive(archive)
	t.Cleanup(manager.CloseAll)

	content := contentstore.NewMemory(contentstore.Options{})
	results := tool.NewResultStoreWithStore(1024, content)
	registry := tool.NewRegistry(nil, results)
	if err := RegisterWithManagerAndBackend(
		registry, t.TempDir(), manager, passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}
	started := executeSessionTool(t, registry, "background_shell_start", map[string]any{
		"command": `for index in $(seq 1 400); do printf "line-$index\n"; done`,
	})
	id, _ := started.Metadata["session_id"].(string)
	if id == "" {
		t.Fatalf("background start = %+v", started)
	}

	var archived tool.Result
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		archived = executeSessionTool(t, registry, "background_shell_wait", map[string]any{
			"session_id": id, "cursor": 0, "timeout_ms": 200,
		})
		if archived.Metadata["archived"] == true {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if archived.Metadata["archived"] != true {
		t.Fatalf("the buffer never lapped, so the case under test never happened: %+v", archived)
	}
	if _, reported := archived.Metadata["pending_bytes"]; !reported {
		t.Fatalf("wait = %+v, want it to say how much is still unread", archived.Metadata)
	}
	// The recovered span is larger than the inline budget, so it is addressable
	// rather than dropped: the handle is how the model reads the rest.
	if archived.Handle == "" {
		t.Fatalf("wait = %+v, want a content handle for the recovered output", archived)
	}
	whole, exists := results.Get(archived.Handle)
	if !exists {
		t.Fatalf("handle %q is not readable", archived.Handle)
	}
	if !strings.Contains(whole, "line-1\r") {
		t.Fatalf("handled output lost the beginning: %d bytes", len(whole))
	}
}

// A poller that keeps up must see none of this: no archive marker, no handle.
func TestAPollerThatKeepsUpSeesNoArchiveMarker(t *testing.T) {
	archive, err := joblog.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = archive.Close() })
	manager := process.NewSessionManager(4096)
	manager.SetArchive(archive)
	t.Cleanup(manager.CloseAll)

	registry := tool.NewRegistry(nil, nil)
	if err := RegisterWithManagerAndBackend(
		registry, t.TempDir(), manager, passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}
	started := executeSessionTool(t, registry, "background_shell_start", map[string]any{
		"command": `printf "ready\n"`,
	})
	id, _ := started.Metadata["session_id"].(string)
	waited := executeSessionTool(t, registry, "background_shell_wait", map[string]any{
		"session_id": id, "cursor": 0, "timeout_ms": 2000,
	})
	if !strings.Contains(waited.Content, "ready") {
		t.Fatalf("wait = %+v", waited)
	}
	if _, marked := waited.Metadata["archived"]; marked {
		t.Fatalf("wait = %+v, want no archive marker for a caller that kept up", waited.Metadata)
	}
}
