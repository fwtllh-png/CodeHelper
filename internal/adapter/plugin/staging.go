package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// Stager atomically installs immutable, content-addressed plugin trees.
type Stager struct {
	root      string
	workspace *sandbox.Workspace
}

// StagedBundle describes an installed bundle.
type StagedBundle struct {
	ContentHash string
	Directory   string
	Manifest    Manifest
}

func NewStager(root string) (*Stager, error) {
	if root == "" {
		return nil, errors.New("plugin staging root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, err
	}
	canonical, err := safeDirectory(absolute, false)
	if err != nil {
		return nil, fmt.Errorf("validate plugin staging root: %w", err)
	}
	workspace, err := sandbox.NewWorkspace(canonical)
	if err != nil {
		return nil, err
	}
	return &Stager{root: canonical, workspace: workspace}, nil
}

func (s *Stager) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

// Stage validates, copies, fsyncs, and atomically publishes a bundle. Existing
// content with the same address is verified before reuse.
func (s *Stager) Stage(bundleRoot string) (StagedBundle, error) {
	if s == nil || s.root == "" {
		return StagedBundle{}, errors.New("plugin stager is required")
	}
	if _, err := s.workspace.Resolve(".", sandbox.MustExist); err != nil {
		return StagedBundle{}, fmt.Errorf("verify plugin staging root: %w", err)
	}
	manifest, err := ValidateBundle(bundleRoot)
	if err != nil {
		return StagedBundle{}, err
	}
	contentHash, err := HashBundle(bundleRoot)
	if err != nil {
		return StagedBundle{}, err
	}
	target := filepath.Join(s.root, contentHash)
	if info, statErr := os.Lstat(target); statErr == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return StagedBundle{}, errors.New("plugin staging target is unsafe")
		}
		actual, hashErr := HashBundle(target)
		if hashErr != nil || !equalHash(actual, contentHash) {
			return StagedBundle{}, errors.New("plugin staged content is corrupt")
		}
		return StagedBundle{contentHash, target, manifest}, nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return StagedBundle{}, statErr
	}

	temporary, err := os.MkdirTemp(s.root, ".stage-*")
	if err != nil {
		return StagedBundle{}, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = makeWritable(temporary)
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := copyBundle(bundleRoot, temporary); err != nil {
		return StagedBundle{}, fmt.Errorf("copy plugin bundle: %w", err)
	}
	copiedHash, err := HashBundle(temporary)
	if err != nil {
		return StagedBundle{}, err
	}
	if !equalHash(copiedHash, contentHash) {
		return StagedBundle{}, errors.New("plugin bundle changed while staging")
	}
	if err := syncTree(temporary); err != nil {
		return StagedBundle{}, fmt.Errorf("sync plugin staging tree: %w", err)
	}
	if err := makeReadOnly(temporary); err != nil {
		return StagedBundle{}, fmt.Errorf("seal plugin staging tree: %w", err)
	}
	if err := syncTree(temporary); err != nil {
		return StagedBundle{}, fmt.Errorf("sync sealed plugin staging tree: %w", err)
	}
	if _, err := s.workspace.Resolve(".", sandbox.MustExist); err != nil {
		return StagedBundle{}, fmt.Errorf("verify plugin staging root before publish: %w", err)
	}
	if err := os.Rename(temporary, target); err != nil {
		// A concurrent publisher may have won. Only accept its exact content.
		actual, hashErr := HashBundle(target)
		if hashErr != nil || !equalHash(actual, contentHash) {
			return StagedBundle{}, fmt.Errorf("publish plugin staging tree: %w", err)
		}
	} else {
		keep = true
	}
	if err := syncDirectory(s.root); err != nil {
		return StagedBundle{}, fmt.Errorf("sync plugin staging root: %w", err)
	}
	return StagedBundle{contentHash, target, manifest}, nil
}

func copyBundle(source, destination string) error {
	workspace, err := sandbox.NewWorkspace(source)
	if err != nil {
		return err
	}
	var copyDirectory func(string, string) error
	copyDirectory = func(relative, target string) error {
		name := relative
		if name == "" {
			name = "."
		}
		directory, err := workspace.OpenDirectory(filepath.FromSlash(name))
		if err != nil {
			return err
		}
		entries, readErr := directory.ReadDir(-1)
		closeErr := directory.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].Name() < entries[j].Name()
		})
		for _, entry := range entries {
			childRelative := filepath.ToSlash(filepath.Join(relative, entry.Name()))
			info, err := entry.Info()
			if err != nil {
				return err
			}
			childTarget := filepath.Join(target, entry.Name())
			if info.IsDir() {
				if err := os.Mkdir(childTarget, 0o700); err != nil {
					return err
				}
				if err := copyDirectory(childRelative, childTarget); err != nil {
					return err
				}
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("plugin bundle path %q is not a regular file", childRelative)
			}
			input, err := workspace.OpenFile(filepath.FromSlash(childRelative))
			if err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if info.Mode().Perm()&0o111 != 0 {
				mode = 0o700
			}
			output, err := os.OpenFile(childTarget, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				input.Close()
				return err
			}
			written, copyErr := io.Copy(output, io.LimitReader(input, maxBundleBytes+1))
			syncErr := output.Sync()
			closeErr := errors.Join(input.Close(), output.Close())
			if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
				return err
			}
			if written > maxBundleBytes {
				return errors.New("plugin bundle exceeds byte limit")
			}
		}
		return syncDirectory(target)
	}
	return copyDirectory("", destination)
}

func syncTree(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("plugin staging tree contains a symbolic link")
		}
		if entry.IsDir() {
			return syncDirectory(path)
		}
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		return errors.Join(file.Sync(), file.Close())
	})
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	return errors.Join(directory.Sync(), directory.Close())
}

func makeReadOnly(root string) error {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		paths = append(paths, path)
		return nil
	})
	if err != nil {
		return err
	}
	for index := len(paths) - 1; index >= 0; index-- {
		info, err := os.Lstat(paths[index])
		if err != nil {
			return err
		}
		mode := os.FileMode(0o400)
		if info.IsDir() {
			// Keep directories owner-writable so callers can garbage-collect
			// staging roots. Files remain sealed; every reuse is rehashed.
			mode = 0o700
		} else if info.Mode().Perm()&0o111 != 0 {
			mode = 0o500
		}
		if err := os.Chmod(paths[index], mode); err != nil {
			return err
		}
	}
	return nil
}

func makeWritable(root string) error {
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return os.Chmod(path, 0o700)
		}
		return os.Chmod(path, 0o600)
	})
}

func validContentAddress(value string) bool {
	if len(value) != 64 || value != strings.ToLower(value) ||
		strings.ContainsAny(value, `/\`) {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}
