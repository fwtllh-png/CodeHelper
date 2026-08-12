package tui_test

import (
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/host/tui"
	"github.com/fwtllh-png/CodeHelper/internal/host/tui/commands"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/testsupport/processfixture"
)

func TestJobsAliasesAndVerbs(t *testing.T) {
	action, ok := commands.Parse("/jobs list")
	if !ok || action.Kind != commands.KindJobs {
		t.Fatalf("jobs => %+v", action)
	}

	fake := processfixture.NewJobCenter(
		process.JobInfo{
			ID: "job_live", Command: "sleep 1", Status: process.JobStatusRunning,
			Running: true, ExitCode: -1, CreatedAt: time.Now().UTC(),
			OutputTail: "hello",
		},
		process.JobInfo{
			ID: "job_stale", Command: "old", Status: process.JobStatusStale,
			Running: false, ExitCode: -1, CreatedAt: time.Now().UTC().Add(-time.Hour),
		},
	)
	model := tui.NewModel(tui.Options{Jobs: fake}, &recordingHost{})

	// Bare /jobs opens the panel, which shows every job and whether it is running.
	model = enterSlash(model, "/jobs")
	view := model.View()
	if !strings.Contains(view, "panel:jobs") || !strings.Contains(view, "count=2 running=1") {
		t.Fatalf("panel view=%s", view)
	}
	if !strings.Contains(view, "job_live") || !strings.Contains(view, "job_stale") {
		t.Fatalf("panel view=%s", view)
	}

	// The verb form still lists as status lines, because a script or a person
	// following a transcript wants the lines, not an overlay.
	model = enterSlash(model, "/jobs list")
	if !strings.Contains(model.View(), "jobs:job_live") {
		t.Fatalf("list view=%s", model.View())
	}

	model = enterSlash(model, "/jobs show job_live")
	if !strings.Contains(model.View(), "jobs:cwd=") {
		t.Fatalf("show view=%s", model.View())
	}

	model = enterSlash(model, "/jobs poll job_live")
	if !strings.Contains(model.View(), "jobs:job_live") {
		t.Fatalf("poll view=%s", model.View())
	}

	model = enterSlash(model, "/jobs cancel job_live")
	if !strings.Contains(model.View(), "jobs:canceled:job_live") {
		t.Fatalf("cancel view=%s", model.View())
	}
	if _, ok := fake.Info("job_live"); ok {
		t.Fatal("live job should be removed")
	}

	model = enterSlash(model, "/jobs cancel job_stale")
	if !strings.Contains(model.View(), "jobs:canceled:job_stale") {
		t.Fatalf("stale cancel view=%s", model.View())
	}

	model = tui.NewModel(tui.Options{}, &recordingHost{})
	model = enterSlash(model, "/jobs list")
	if !strings.Contains(model.View(), "jobs:unavailable") {
		t.Fatalf("unavailable view=%s", model.View())
	}
}
