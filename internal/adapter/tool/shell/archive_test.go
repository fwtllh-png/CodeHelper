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
	results := tool.NewResultStoreWithStore(256, content)
	registry := tool.NewRegistry(nil, results)
	if err := RegisterWithManagerAndBackend(
		registry, t.TempDir(), manager, passthroughBackend{},
	); err != nil {
		t.Fatal(err)
	}
	started := executeProcessTool(
		t,
		registry,
		processTestThread,
		"exec_command",
		map[string]any{
			"command": `for index in $(seq 1 400); do printf "line-$index\n"; done; sleep 1`,
			"tty":     true, "yield_time_ms": 10,
		},
	)
	id, _ := started.Metadata["session_id"].(string)
	if id == "" {
		t.Fatalf("background start = %+v", started)
	}

	delivered := processResultContent(results, started)
	var archived tool.Result
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		archived = executeProcessTool(
			t,
			registry,
			processTestThread,
			"write_stdin",
			map[string]any{
				"session_id": id, "yield_time_ms": 200,
			},
		)
		delivered += processResultContent(results, archived)
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
	// The recovered continuation is larger than the inline budget, so it is
	// addressable rather than dropped. Bytes delivered by the initial result or
	// an earlier poll are not replayed by a later write_stdin call.
	if archived.Handle == "" {
		t.Fatalf("wait = %+v, want a content handle for the recovered output", archived)
	}
	whole, exists := results.Get(archived.Handle)
	if !exists {
		t.Fatalf("handle %q is not readable", archived.Handle)
	}
	if !strings.Contains(delivered, "line-1\r") {
		t.Fatalf(
			"poll sequence lost output: delivered=%d continuation=%d "+
				"metadata=%+v head=%q",
			len(delivered),
			len(whole),
			started.Metadata,
			whole[:min(len(whole), 80)],
		)
	}
}

func processResultContent(results *tool.ResultStore, result tool.Result) string {
	if result.Handle != "" {
		if handled, ok := results.Get(result.Handle); ok {
			return handled
		}
	}
	return result.Content
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
	started := executeProcessTool(
		t,
		registry,
		processTestThread,
		"exec_command",
		map[string]any{
			"command": `printf "ready\n"; sleep 1`,
			"tty":     true, "yield_time_ms": 10,
		},
	)
	id, _ := started.Metadata["session_id"].(string)
	waited := executeProcessTool(
		t,
		registry,
		processTestThread,
		"write_stdin",
		map[string]any{
			"session_id": id, "yield_time_ms": 2000,
		},
	)
	if !strings.Contains(waited.Content, "ready") {
		t.Fatalf("wait = %+v", waited)
	}
	if _, marked := waited.Metadata["archived"]; marked {
		t.Fatalf("wait = %+v, want no archive marker for a caller that kept up", waited.Metadata)
	}
}
