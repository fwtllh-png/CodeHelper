package wire

import (
	"fmt"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/internal/persist/joblog"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

func configureProcessState(
	state *buildState,
	processes *process.SessionManager,
) error {
	if state.config.workspaceStateRoot == "" {
		return nil
	}
	processes.SetJournalPath(filepath.Join(
		state.config.workspaceStateRoot,
		"control",
		"jobs",
		"journal.jsonl",
	))
	if err := processes.LoadStaleJournal(); err != nil {
		return fmt.Errorf("load process session journal: %w", err)
	}
	return nil
}

func openJobLog(state *buildState) {
	if state.config.workspaceStateRoot == "" {
		return
	}
	jobs, err := joblog.New(filepath.Join(
		state.config.workspaceStateRoot,
		"control",
		"jobs",
		"logs",
	))
	if err != nil {
		return
	}
	state.session.jobLogs = jobs
	state.persistence.jobLogs = jobs
}
