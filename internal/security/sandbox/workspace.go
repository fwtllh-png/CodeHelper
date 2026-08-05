package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type ResolveMode uint8

const (
	MustExist ResolveMode = iota
	AllowMissing
)

type Workspace struct {
	root     string
	identity fileIdentity
}

func NewWorkspace(root string) (*Workspace, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("workspace root must be a real directory")
	}
	identity, err := identityOf(absolute, info)
	if err != nil {
		return nil, err
	}
	return &Workspace{root: filepath.Clean(absolute), identity: identity}, nil
}

func (w *Workspace) Root() string {
	return w.root
}

func (w *Workspace) Resolve(name string, mode ResolveMode) (string, error) {
	if name == "" {
		return "", errors.New("workspace path is required")
	}
	if strings.IndexByte(name, 0) >= 0 {
		return "", errors.New("workspace path contains NUL")
	}
	target := name
	if !filepath.IsAbs(target) {
		target = filepath.Join(w.root, target)
	}
	target = filepath.Clean(target)
	relative, err := filepath.Rel(w.root, target)
	if err != nil || outside(relative) {
		return "", fmt.Errorf("path %q is outside workspace", name)
	}
	if err := w.verifyRoot(); err != nil {
		return "", err
	}
	if relative == "." {
		return w.root, nil
	}

	current := w.root
	parts := strings.Split(relative, string(filepath.Separator))
	for index, part := range parts {
		exact, err := exactEntryName(current, part)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && mode == AllowMissing {
				return target, nil
			}
			return "", fmt.Errorf("resolve workspace path %q: %w", name, err)
		}
		current = filepath.Join(current, exact)
		info, err := os.Lstat(current)
		if err != nil {
			return "", fmt.Errorf("resolve workspace path %q: %w", name, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("workspace path %q contains a symbolic link", name)
		}
		identity, err := identityOf(current, info)
		if err != nil {
			return "", err
		}
		if identity.device != w.identity.device {
			return "", fmt.Errorf("workspace path %q crosses a device boundary", name)
		}
		if info.Mode().IsRegular() && identity.links > 1 {
			return "", fmt.Errorf("workspace path %q is a multiply linked file", name)
		}
		if index != len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("workspace path %q has a non-directory component", name)
		}
	}
	return current, nil
}

func (w *Workspace) ResolveDirectory(name string) (string, error) {
	if name == "" {
		name = "."
	}
	path, err := w.Resolve(name, MustExist)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("workspace cwd is not a directory")
	}
	return path, nil
}

func (w *Workspace) verifyRoot() error {
	info, err := os.Lstat(w.root)
	if err != nil {
		return fmt.Errorf("verify workspace root: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return errors.New("workspace root was replaced")
	}
	identity, err := identityOf(w.root, info)
	if err != nil {
		return err
	}
	if identity.device != w.identity.device || identity.inode != w.identity.inode {
		return errors.New("workspace root identity changed")
	}
	return nil
}

func exactEntryName(directory, requested string) (string, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return "", err
	}
	for _, entry := range entries {
		if entry.Name() == requested {
			return requested, nil
		}
	}
	for _, entry := range entries {
		if strings.EqualFold(entry.Name(), requested) {
			return "", fmt.Errorf("path component %q has incorrect case", requested)
		}
	}
	return "", os.ErrNotExist
}

func outside(relative string) bool {
	return relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
