package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// TrustedHostPathResolver resolves exact read-only host paths under the two
// roots owned by one Workspace runtime: the Workspace and its private home.
type TrustedHostPathResolver struct {
	workspace   *Workspace
	privateHome *Workspace
}

func NewTrustedHostPathResolver(
	workspaceRoot string,
	privateHome string,
) (*TrustedHostPathResolver, error) {
	workspace, err := NewWorkspace(workspaceRoot)
	if err != nil {
		return nil, fmt.Errorf("open trusted workspace root: %w", err)
	}
	home, err := NewWorkspace(privateHome)
	if err != nil {
		return nil, fmt.Errorf("open trusted private home: %w", err)
	}
	return &TrustedHostPathResolver{
		workspace: workspace, privateHome: home,
	}, nil
}

func (r *TrustedHostPathResolver) Resolve(
	name string,
	mode ResolveMode,
) (string, error) {
	if filepath.IsAbs(name) {
		if path, ok, err := resolveAbsoluteUnder(r.workspace, name, mode); ok {
			return path, err
		}
		if path, ok, err := resolveAbsoluteUnder(r.privateHome, name, mode); ok {
			return path, err
		}
		return "", fmt.Errorf(
			"path %q is outside the Workspace and its private home",
			name,
		)
	}

	path, workspaceErr := r.workspace.Resolve(name, MustExist)
	if workspaceErr == nil {
		return path, nil
	}
	if !errors.Is(workspaceErr, os.ErrNotExist) {
		return "", workspaceErr
	}
	path, homeErr := r.privateHome.Resolve(name, MustExist)
	if homeErr == nil {
		return path, nil
	}
	if !errors.Is(homeErr, os.ErrNotExist) {
		return "", homeErr
	}
	if mode == AllowMissing {
		return r.workspace.Resolve(name, AllowMissing)
	}
	return "", workspaceErr
}

func resolveAbsoluteUnder(
	root *Workspace,
	name string,
	mode ResolveMode,
) (string, bool, error) {
	relative, err := filepath.Rel(root.Root(), filepath.Clean(name))
	if err != nil || outside(relative) {
		return "", false, nil
	}
	path, err := root.Resolve(name, mode)
	return path, true, err
}
