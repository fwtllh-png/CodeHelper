package wire

import (
	"context"
	"errors"
	"os"

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
		if s.resources != nil {
			s.closeErr = s.resources.Close(ctx)
		}
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

// Subagents reports the child-agent control plane, or nil when this host runs without
// tools. A host needs it to show what child agents exist: the parent's thread
// history says a child was spawned, not what became of it.
func (s *Session) Subagents() *subagent.AgentControl {
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

// ContributionReceipts reports the construction identities published by
// extension contributors without exposing their runtime-owned services.
func (s *Session) ContributionReceipts() []ContributionReceipt {
	if s == nil {
		return nil
	}
	return s.extensions.contributionReceipts()
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

func (s *Session) registerResourceClosers() error {
	type resource struct {
		name  string
		close func(context.Context) error
	}
	// Registration is the reverse of shutdown dependency order. Reading fields
	// at close time makes the same stack valid for partial construction rollback.
	resources := []resource{
		{name: "exec-log", close: func(context.Context) error {
			if s.logFile == nil {
				return nil
			}
			return s.logFile.Close()
		}},
		{name: "provider-fixture", close: func(ctx context.Context) error {
			if s.fixture == nil {
				return nil
			}
			return s.fixture.Close(ctx)
		}},
		{name: "plugin-registry", close: func(context.Context) error {
			return s.extensions.closePluginRegistry()
		}},
		{name: "plugin-tools", close: func(context.Context) error {
			return s.extensions.closePluginTools()
		}},
		{name: "extension-lifecycle", close: func(ctx context.Context) error {
			return s.extensions.closeLifecycle(ctx)
		}},
		{name: "loaded-plugins", close: func(context.Context) error {
			closeErrors := make([]error, 0, len(s.plugins))
			for _, loadedPlugin := range s.plugins {
				closeErrors = append(closeErrors, loadedPlugin.Close())
			}
			return errors.Join(closeErrors...)
		}},
		{name: "metrics", close: func(context.Context) error {
			if s.metricsPath == "" {
				return nil
			}
			return writeMetricSnapshot(s.metricsPath, s.metrics)
		}},
		{name: "finish-log", close: func(context.Context) error {
			if s.logger != nil {
				s.logger.Info(
					"exec finished",
					"provider", s.providerID,
					"model", s.modelID,
				)
			}
			return nil
		}},
		{name: "sandbox", close: func(context.Context) error {
			if s.sandbox == nil {
				return nil
			}
			err := sandbox.CloseBackend(s.sandbox)
			s.sandbox = nil
			return err
		}},
		{name: "ephemeral-tasks", close: func(context.Context) error {
			var closeErr error
			if s.ephemeralTasks != nil {
				closeErr = s.ephemeralTasks.Close()
				s.ephemeralTasks = nil
			}
			var cleanupErr error
			if s.ephemeralTasksDir != "" {
				cleanupErr = os.RemoveAll(s.ephemeralTasksDir)
				s.ephemeralTasksDir = ""
			}
			return errors.Join(closeErr, cleanupErr)
		}},
		{name: "content-store", close: func(ctx context.Context) error {
			if s.content == nil {
				return nil
			}
			return s.content.Close(ctx)
		}},
		{name: "observation-router", close: func(ctx context.Context) error {
			if s.observability.router == nil {
				return nil
			}
			return s.observability.router.Close(ctx)
		}},
		{name: "workspace-journal", close: func(ctx context.Context) error {
			if s.journal == nil {
				return nil
			}
			return s.journal.Close(ctx)
		}},
		{name: "mcp-pool", close: func(ctx context.Context) error {
			if s.mcpPool == nil {
				return nil
			}
			return s.mcpPool.ShutdownAll(ctx)
		}},
		{name: "mcp-prewarm", close: func(context.Context) error {
			if s.mcpPrewarm != nil {
				s.mcpPrewarm.Stop()
			}
			return nil
		}},
		{name: "child-toolsets", close: func(context.Context) error {
			if s.childTools != nil {
				s.childTools.closeAll()
			}
			return nil
		}},
		{name: "job-logs", close: func(context.Context) error {
			if s.jobLogs == nil {
				return nil
			}
			return s.jobLogs.Close()
		}},
		{name: "processes", close: func(context.Context) error {
			if s.processes != nil {
				s.processes.CloseAll()
			}
			return nil
		}},
		{name: "turn-coordinators", close: func(ctx context.Context) error {
			if s.turnCoordinators == nil {
				return nil
			}
			err := s.turnCoordinators.Close(ctx)
			s.turnCoordinators = nil
			return err
		}},
		{name: "runtime", close: func(ctx context.Context) error {
			if s.Runtime == nil {
				return nil
			}
			return s.Runtime.Close(ctx)
		}},
		{name: "child-runtime", close: func(context.Context) error {
			if s.children != nil {
				s.children.close()
			}
			return nil
		}},
		{name: "scheduler", close: func(context.Context) error {
			if s.scheduler == nil {
				return nil
			}
			return s.scheduler.Close()
		}},
	}
	for _, resource := range resources {
		if err := s.resources.Add(resource.name, resource.close); err != nil {
			return err
		}
	}
	return nil
}
