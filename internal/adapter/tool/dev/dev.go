package dev

import (
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func Register(
	registry *tool.Registry,
	root string,
	backend sandbox.Backend,
) error {
	if registry == nil {
		return errors.New("developer tools require a registry")
	}
	if backend == nil {
		return errors.New("developer tools require an injected sandbox backend")
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return err
	}
	root = workspace.Root()
	if err := registerFormat(registry, root, backend); err != nil {
		return err
	}
	if err := registerDebugger(registry, root, backend); err != nil {
		return err
	}
	return registerDependency(registry, root, backend)
}
