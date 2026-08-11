package wire

import (
	"context"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	dynamictool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/dynamic"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/worker"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func (s *Session) Close(ctx context.Context) error {
	s.closeOnce.Do(func() {
		s.closeErr = s.closeResources(ctx, true)
	})
	return s.closeErr
}

// ProviderID and ModelID report the immutable route selected while the
// long-lived runtime was bootstrapped. Transports may expose this route for
// discovery, but changing it requires constructing a new runtime.
func (s *Session) ProviderID() string { return s.providerID }

func (s *Session) ModelID() string { return s.modelID }

func (s *Session) ModelCapabilities() protocol.ModelCapabilities {
	return s.modelCapabilities
}

func (s *Session) ProviderCatalog() protocol.ProviderCatalog {
	return s.providerCatalog
}

func (s *Session) ModelCatalog() protocol.ModelCatalog {
	return s.modelCatalog
}

func (s *Session) SessionWorkspaces() app.SessionWorkspaceManager {
	if s == nil {
		return nil
	}
	return s.chatWorkspaces
}

func (s *Session) Tasks() *taskstate.Repository { return s.tasks }

func (s *Session) Automations() *automation.Repository { return s.automations }

// Scheduler reports the durable task scheduler, or nil when this host runs none.
// A host uses it to say whether background work is being executed at all, which
// is otherwise invisible: a queued task in a process without a scheduler simply
// waits.
func (s *Session) Scheduler() *worker.Scheduler { return s.scheduler }

// JournalRecovery reports what interrupted turns this process found at startup
// and what it did with them. It is empty in the normal case and for hosts running
// without a durable journal.
func (s *Session) JournalRecovery() workspacejournal.Recovery {
	if s == nil {
		return workspacejournal.Recovery{}
	}
	return s.journalRecovery
}

// Subagents reports the child-agent manager, or nil when this host runs without
// tools. A host needs it to show what child agents exist: the parent's thread
// history says a child was spawned, not what became of it.
func (s *Session) Subagents() *subagent.Manager {
	if s == nil {
		return nil
	}
	return s.subagents
}

func (s *Session) Security() *policy.Runtime { return s.security }

func (s *Session) RLM() *rlm.Store { return s.rlmStore }

func (s *Session) Processes() *process.SessionManager { return s.processes }

func (s *Session) MCPHealth() []mcp.HealthSnapshot {
	if s == nil || s.mcpPool == nil {
		return nil
	}
	return s.mcpPool.HealthSnapshots()
}

// DynamicTools reports the trusted-host management surface. Nil means the host
// did not explicitly enable dynamic registration for this session.
func (s *Session) DynamicTools() *dynamictool.Manager {
	if s == nil {
		return nil
	}
	return s.dynamicTools
}

func (s *Session) Jobs() process.JobCenter {
	if s.processes == nil {
		return nil
	}
	return s.processes
}

func (s *Session) SetPolicyMode(mode policy.Mode) {
	// Applies to the next turn's SnapshotTurnContext; in-flight turns use a
	// CloneSampling policy installed on Guard for the turn duration.
	if s != nil && s.security != nil {
		s.security.Mode = mode
		if s.threads != nil {
			s.threads.SetPolicyMode(mode)
		}
	}
}

func (s *Session) SetPermission(permission policy.Permission) {
	// Applies to the next turn only; see SetPolicyMode.
	if s != nil && s.security != nil {
		s.security.Permission = permission
		if s.threads != nil {
			s.threads.SetPermission(permission)
		}
	}
}

func (s *Session) SetGranular(granular policy.Granular) {
	// Applies to the next turn only; see SetPolicyMode.
	if s != nil && s.security != nil {
		s.security.Granular = granular
		if s.threads != nil {
			s.threads.SetGranular(granular)
		}
	}
}

func (s *Session) closeResources(ctx context.Context, closeRuntime bool) error {
	var closeErrors []error
	// The scheduler stops first and drains: its tasks run as child turns, so
	// stopping it after the child runtime would leave turns with nothing to settle
	// them. Draining returns in-flight work to the queue without spending an
	// attempt, so the next process picks it up.
	if s.scheduler != nil {
		closeErrors = append(closeErrors, s.scheduler.Close())
	}
	// The child pump subscribes to the runtime, so it stops before the runtime
	// does; otherwise it would resubscribe against a closing runtime.
	if s.children != nil {
		s.children.close()
	}
	if closeRuntime && s.Runtime != nil {
		closeErrors = append(closeErrors, s.Runtime.Close(ctx))
	}
	if s.turnCoordinators != nil {
		closeErrors = append(closeErrors, s.turnCoordinators.Close(ctx))
		s.turnCoordinators = nil
	}
	if s.processes != nil {
		s.processes.CloseAll()
	}
	// The job logs close after the jobs that write to them; the files stay on disk
	// so a later process can still read what a job printed.
	if s.jobLogs != nil {
		closeErrors = append(closeErrors, s.jobLogs.Close())
	}
	// Isolated children own a sandbox and a process manager each, so they close
	// after the runtime has stopped handing them turns.
	if s.childTools != nil {
		s.childTools.closeAll()
	}
	if s.mcpPrewarm != nil {
		s.mcpPrewarm.Stop()
	}
	if s.mcpPool != nil {
		closeErrors = append(closeErrors, s.mcpPool.ShutdownAll(ctx))
	}
	// The journal closes after the child toolsets, which own journals of their own,
	// and before the content store: a durable journal owns the store its
	// before-images live in.
	if s.journal != nil {
		closeErrors = append(closeErrors, s.journal.Close(ctx))
	}
	if s.content != nil {
		closeErrors = append(closeErrors, s.content.Close(ctx))
	}
	if s.ephemeralTasks != nil {
		closeErrors = append(closeErrors, s.ephemeralTasks.Close())
		s.ephemeralTasks = nil
	}
	if s.sandbox != nil {
		closeErrors = append(closeErrors, sandbox.CloseBackend(s.sandbox))
		s.sandbox = nil
	}
	if s.logger != nil {
		s.logger.Info("exec finished", "provider", s.providerID, "model", s.modelID)
	}
	if s.metricsPath != "" {
		closeErrors = append(closeErrors, writeMetricSnapshot(s.metricsPath, s.metrics))
	}
	for _, loadedPlugin := range s.plugins {
		closeErrors = append(closeErrors, loadedPlugin.Close())
	}
	if s.pluginTools != nil {
		closeErrors = append(closeErrors, s.pluginTools.Close())
	}
	if s.pluginRegistry != nil {
		closeErrors = append(closeErrors, s.pluginRegistry.Close())
	}
	if s.fixture != nil {
		closeErrors = append(closeErrors, s.fixture.Close(ctx))
	}
	if s.logFile != nil {
		closeErrors = append(closeErrors, s.logFile.Close())
	}
	return errors.Join(closeErrors...)
}
