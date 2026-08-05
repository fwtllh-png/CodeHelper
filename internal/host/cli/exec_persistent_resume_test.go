package cli_test

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestExecPersistentResumeListTurns(t *testing.T) {
	root := t.TempDir()
	fixturePath := filepath.Join("..", "..", "..", "testdata", "providers", "openai")
	threadID := "thread-p11-resume"
	var stdout, stderr bytes.Buffer

	code := cli.Run([]string{
		"exec", "--provider-fixture", fixturePath, "--data-dir", root,
		"--thread-id", threadID, "--session-id", "session-p11",
		"say hello",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("first exec code=%d stderr=%q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.Run([]string{
		"exec", "--provider-fixture", fixturePath, "--data-dir", root, "--resume", "say hello",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("resume exec code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), threadID) {
		t.Fatalf("resume missing thread id: %q", stdout.String())
	}

	store, err := state.Open(context.Background(), state.Options{DataDir: root})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.CloseAll(context.Background()) }()
	repos, err := wire.NewPersistentRepositories(store)
	if err != nil {
		t.Fatal(err)
	}
	turns, err := repos.Threads.ListTurns(context.Background(), protocol.ThreadID(threadID))
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("ListTurns = %d, want 2 after two-process resume", len(turns))
	}
}
