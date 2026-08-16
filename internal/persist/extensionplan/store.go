// Package extensionplan persists the committed identity of an Extension Plan.
package extensionplan

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

const (
	receiptVersion = 1
	FileName       = "extension-plan.json"
)

var ErrPlanConflict = errors.New("durable extension plan conflicts with resolved plan")

type Receipt struct {
	Version          int       `json:"version"`
	Workspace        string    `json:"workspace"`
	PlanRevision     uint64    `json:"plan_revision"`
	PlanDigest       string    `json:"plan_digest"`
	PermissionDigest string    `json:"permission_digest"`
	SourceDigest     string    `json:"source_digest"`
	CommittedAt      time.Time `json:"committed_at"`
}

func (r Receipt) Validate() error {
	if r.Version != receiptVersion || strings.TrimSpace(r.Workspace) == "" ||
		r.PlanRevision == 0 || strings.TrimSpace(r.PlanDigest) == "" ||
		strings.TrimSpace(r.PermissionDigest) == "" ||
		strings.TrimSpace(r.SourceDigest) == "" || r.CommittedAt.IsZero() {
		return errors.New("durable extension plan receipt is invalid")
	}
	return nil
}

type Store struct {
	path string
	now  func() time.Time
	mu   sync.Mutex
}

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("extension plan receipt path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if filepath.Base(absolute) != FileName {
		return nil, fmt.Errorf("extension plan receipt must be named %s", FileName)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, err
	}
	if err := validateFile(absolute); err != nil {
		return nil, err
	}
	return &Store{path: absolute, now: time.Now}, nil
}

func (s *Store) Load(ctx context.Context) (Receipt, bool, error) {
	if err := contextError(ctx); err != nil {
		return Receipt{}, false, err
	}
	if s == nil {
		return Receipt{}, false, errors.New("extension plan store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) Commit(
	ctx context.Context,
	workspace string,
	plan extension.Plan,
) (Receipt, error) {
	if err := contextError(ctx); err != nil {
		return Receipt{}, err
	}
	if s == nil {
		return Receipt{}, errors.New("extension plan store is required")
	}
	if err := plan.Validate(); err != nil {
		return Receipt{}, err
	}
	workspace = strings.TrimSpace(workspace)
	if workspace == "" {
		return Receipt{}, errors.New("extension plan workspace is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current, exists, err := s.loadLocked()
	if err != nil {
		return Receipt{}, err
	}
	if exists && current.Workspace != workspace {
		return Receipt{}, ErrPlanConflict
	}
	if exists && current.PlanDigest == plan.Digest {
		if current.PermissionDigest != plan.PermissionDigest ||
			current.SourceDigest != plan.SourceDigest {
			return Receipt{}, ErrPlanConflict
		}
		return current, nil
	}
	revision := uint64(1)
	if exists {
		revision = current.PlanRevision + 1
	}
	receipt := Receipt{
		Version: receiptVersion, Workspace: workspace,
		PlanRevision: revision, PlanDigest: plan.Digest,
		PermissionDigest: plan.PermissionDigest, SourceDigest: plan.SourceDigest,
		CommittedAt: s.now().UTC(),
	}
	if err := receipt.Validate(); err != nil {
		return Receipt{}, err
	}
	if err := s.writeLocked(receipt); err != nil {
		return Receipt{}, err
	}
	return receipt, nil
}

func (s *Store) loadLocked() (Receipt, bool, error) {
	if err := validateFile(s.path); err != nil {
		return Receipt{}, false, err
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return Receipt{}, false, nil
	}
	if err != nil {
		return Receipt{}, false, err
	}
	var receipt Receipt
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return Receipt{}, false, fmt.Errorf("decode extension plan receipt: %w", err)
	}
	if err := receipt.Validate(); err != nil {
		return Receipt{}, false, err
	}
	return receipt, true, nil
}

func (s *Store) writeLocked(receipt Receipt) error {
	data, err := json.MarshalIndent(receipt, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	directory := filepath.Dir(s.path)
	temporary, err := os.CreateTemp(directory, ".extension-plan-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	cleanup := func() {
		_ = temporary.Close()
		_ = os.Remove(temporaryPath)
	}
	defer cleanup()
	if chmodErr := temporary.Chmod(0o600); chmodErr != nil {
		return chmodErr
	}
	if _, writeErr := temporary.Write(data); writeErr != nil {
		return writeErr
	}
	if syncErr := temporary.Sync(); syncErr != nil {
		return syncErr
	}
	if closeErr := temporary.Close(); closeErr != nil {
		return closeErr
	}
	if renameErr := os.Rename(temporaryPath, s.path); renameErr != nil {
		return renameErr
	}
	directoryHandle, err := os.Open(directory)
	if err != nil {
		return err
	}
	defer directoryHandle.Close()
	return directoryHandle.Sync()
}

func validateFile(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return errors.New("extension plan receipt must be a regular non-symlink file")
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("extension plan context is required")
	}
	return ctx.Err()
}
