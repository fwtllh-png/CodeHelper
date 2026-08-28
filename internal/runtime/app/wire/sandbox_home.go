package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/security/egress"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func newWorkspaceSandbox(
	state *buildState,
	helperPath string,
) (sandbox.Backend, error) {
	privateHome := ""
	if state.options.PersistentStore != nil {
		var err error
		privateHome, err = persistentSandboxHome(
			state.options.PersistentStore.Root(),
			state.config.execution.Workspace,
		)
		if err != nil {
			return nil, err
		}
	}
	return egress.NewManagedBackend(state.provider.egress, sandbox.Options{
		WorkspaceRoot: state.config.execution.Workspace,
		HelperPath:    helperPath,
		PrivateTemp:   privateHome,
		HostReadRoots: state.config.diagnosticReadRoots,
		HostReadFiles: state.config.diagnosticReadFiles,
	}, newPlatformBackend)
}

func childSandboxHomeRoot(state *buildState) string {
	if state.options.PersistentStore == nil {
		return ""
	}
	return filepath.Join(
		state.options.PersistentStore.Root(),
		"sandboxes",
		"children",
	)
}

func persistentSandboxHome(dataDir, workspace string) (string, error) {
	workspace, err := filepath.Abs(workspace)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(filepath.Clean(workspace)))
	base, err := filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	if pathsOverlap(base, workspace) {
		base, err = os.UserCacheDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(base, "codehelper")
	}
	root := filepath.Join(
		base,
		"sandboxes",
		hex.EncodeToString(sum[:16]),
		"home",
	)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", err
	}
	if err := os.Chmod(root, 0o700); err != nil {
		return "", err
	}
	return root, nil
}

func pathsOverlap(left, right string) bool {
	contains := func(parent, child string) bool {
		relative, err := filepath.Rel(parent, child)
		return err == nil && relative != ".." &&
			relative != "." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator))
	}
	left, right = filepath.Clean(left), filepath.Clean(right)
	return left == right || contains(left, right) || contains(right, left)
}
