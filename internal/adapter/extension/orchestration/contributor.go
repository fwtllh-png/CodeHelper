// Package orchestration contributes durable task and automation tools as one
// extension unit. Runtime wiring provides the repositories and shared Registry.
package orchestration

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	automationtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/automation"
	tasktool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/task"
	automationstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/automation"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type Options struct {
	Tasks       *taskstate.Repository
	Automations *automationstate.Repository
	SessionID   string
	Workspace   string
	Backend     sandbox.Backend
}

func Contribute(registry *tool.Registry, options Options) error {
	if err := tasktool.Register(registry, tasktool.Options{
		Repository: options.Tasks,

		Backend: options.Backend, Workspace: options.Workspace, SessionID: options.SessionID,
	}); err != nil {
		return fmt.Errorf("task tools: %w", err)
	}
	if err := automationtool.Register(
		registry,
		automationtool.Options{
			Repository: options.Automations, Workspace: options.Workspace, SessionID: options.SessionID,
		},
	); err != nil {
		return fmt.Errorf("automation tools: %w", err)
	}
	return nil
}
