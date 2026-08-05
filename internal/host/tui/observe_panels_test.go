package tui_test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

func openPanelKey(t *testing.T, model tui.Model, key string) tui.Model {
	t.Helper()
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key), Alt: true})
	return updated.(tui.Model)
}

// The three observation panels have to be reachable and say something true even
// with nothing to show. A panel that renders empty is indistinguishable from a
// panel that is broken.
func TestObservationPanelsAreReachableAndHonestWhenEmpty(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".codehelper")
	model := tui.NewModel(tui.Options{DataDir: dataDir}, &recordingHost{})

	agents := openPanelKey(t, model, "5").View()
	if !strings.Contains(agents, "panel:agents") {
		t.Fatalf("agents panel unreachable: %q", agents)
	}
	// Without a live session there is no manager, and the panel says so rather
	// than claiming there are no agents.
	if !strings.Contains(agents, "agents: unavailable") {
		t.Fatalf("agents panel = %q, want it to explain why it is empty", agents)
	}

	tasks := openPanelKey(t, model, "6").View()
	if !strings.Contains(tasks, "panel:tasks") || !strings.Contains(tasks, "task:") {
		t.Fatalf("tasks panel = %q", tasks)
	}

	jobs := openPanelKey(t, model, "7").View()
	if !strings.Contains(jobs, "panel:jobs") || !strings.Contains(jobs, "jobs: unavailable") {
		t.Fatalf("jobs panel = %q", jobs)
	}
}

// A tasks panel that lists queued work without saying whether anything executes
// it is the trap this panel exists to close: an offline view and a running worker
// produce the same list.
func TestTasksPanelSaysWhetherAnythingIsExecutingTasks(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), ".codehelper")
	model := tui.NewModel(tui.Options{DataDir: dataDir}, &recordingHost{})
	view := openPanelKey(t, model, "6").View()
	if !strings.Contains(view, "offline view") {
		t.Fatalf("tasks panel = %q, want it to admit there is no live session", view)
	}
}

func TestJobsPanelShowsRunningStateAndTheNewestOutput(t *testing.T) {
	fake := process.NewFakeJobCenter(
		process.JobInfo{
			ID: "job_build", Command: "make build", Status: process.JobStatusRunning,
			Running: true, ExitCode: -1, CreatedAt: time.Now().UTC(),
			OutputTail: "compiling pkg/a\ncompiling pkg/b\n",
		},
		process.JobInfo{
			ID: "job_done", Command: "go test ./...", Status: process.JobStatusExited,
			Running: false, ExitCode: 1, CreatedAt: time.Now().UTC().Add(-time.Minute),
		},
	)
	model := tui.NewModel(tui.Options{Jobs: fake}, &recordingHost{})
	view := openPanelKey(t, model, "7").View()
	if !strings.Contains(view, "count=2 running=1") {
		t.Fatalf("jobs panel = %q", view)
	}
	// The newest line of a running job is the progress a person is looking for.
	if !strings.Contains(view, "compiling pkg/b") {
		t.Fatalf("jobs panel dropped live output: %q", view)
	}
	// A finished job reports how it finished; a running one has no exit yet.
	if !strings.Contains(view, "exit=1") {
		t.Fatalf("jobs panel = %q, want the failed job's exit code", view)
	}
	if strings.Contains(view, "job_build running exit=") {
		t.Fatalf("a running job must not report an exit code: %q", view)
	}
}

// Enter re-reads the source of truth rather than mutating anything: these panels
// observe work whose lifecycle belongs to the tools and the CLI.
func TestEnterRefreshesAnObservationPanel(t *testing.T) {
	fake := process.NewFakeJobCenter(process.JobInfo{
		ID: "job_one", Command: "sleep 1", Status: process.JobStatusRunning,
		Running: true, ExitCode: -1, CreatedAt: time.Now().UTC(),
	})
	model := tui.NewModel(tui.Options{Jobs: fake}, &recordingHost{})
	model = openPanelKey(t, model, "7")
	if !strings.Contains(model.View(), "count=1") {
		t.Fatalf("initial jobs panel = %q", model.View())
	}
	fake.Add(process.JobInfo{
		ID: "job_two", Command: "sleep 2", Status: process.JobStatusRunning,
		Running: true, ExitCode: -1, CreatedAt: time.Now().UTC(),
	})
	updated, _ := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	model = updated.(tui.Model)
	view := model.View()
	if !strings.Contains(view, "count=2") || !strings.Contains(view, "jobs:refreshed") {
		t.Fatalf("refreshed jobs panel = %q", view)
	}
}
