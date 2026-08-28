package wire

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// ValidateExternalStateDirectory rejects a Runtime state root that overlaps
// the untrusted Workspace before the host creates any control-plane files.
func ValidateExternalStateDirectory(workspace, dataDir string) (string, error) {
	return sandbox.ExternalStateDirectory(workspace, dataDir)
}

func securityStateDataDir(state *buildState) string {
	if state.options.PersistentStore != nil {
		return state.options.PersistentStore.Root()
	}
	return state.config.snapshot.Config.State.DataDir
}

func resolveRuntimeSkillPaths(
	state *buildState,
	workspace string,
) (SkillPaths, error) {
	options := state.options.Skills
	if state.options.PersistentStore == nil {
		return ResolveSkillPaths(options, workspace)
	}
	storeRoot := state.options.PersistentStore.Root()
	if options.DataDir != "" &&
		filepath.Clean(options.DataDir) != filepath.Clean(storeRoot) {
		return SkillPaths{}, errors.New(
			"extension state directory must match the Runtime state store",
		)
	}
	options.DataDir = storeRoot
	return ResolveSkillPaths(options, workspace)
}

func externalWorkspaceStateRoot(
	dataDir, workspace string,
	identity protocol.WorkspaceIdentity,
) (string, string, error) {
	if dataDir == "" {
		return "", "", nil
	}
	workspaceRoot, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace links for state directory: %w", err)
	}
	workspaceID := identity.RootID
	if identity != (protocol.WorkspaceIdentity{}) {
		if err := identity.Validate(); err != nil {
			return "", "", fmt.Errorf("validate workspace identity for state directory: %w", err)
		}
		identityRoot, resolveErr := filepath.EvalSymlinks(identity.RuntimePath)
		if resolveErr != nil || filepath.Clean(identityRoot) != filepath.Clean(workspaceRoot) {
			return "", "", fmt.Errorf("workspace identity does not match the runtime workspace")
		}
	}
	layout, err := sandbox.PrepareStateLayout(
		dataDir,
		workspaceRoot,
		workspaceID,
	)
	if err != nil {
		return "", "", err
	}
	return layout.Root, layout.WorkspaceID, nil
}
