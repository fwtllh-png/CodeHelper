// Package extensionlifecycle persists redacted Extension Effect receipts.
package extensionlifecycle

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

	"github.com/fwtllh-png/CodeHelper/internal/persist/atomicfile"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

const (
	FileName     = "extension-lifecycle.json"
	storeVersion = 1
	maxReceipts  = 1024
)

type document struct {
	Version  int                                 `json:"version"`
	Receipts []runtimeextension.LifecycleReceipt `json:"receipts,omitempty"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("extension lifecycle receipt path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if filepath.Base(absolute) != FileName {
		return nil, fmt.Errorf("extension lifecycle receipt must be named %s", FileName)
	}
	if err := os.MkdirAll(filepath.Dir(absolute), 0o700); err != nil {
		return nil, err
	}
	if err := validateFile(absolute); err != nil {
		return nil, err
	}
	return &Store{path: absolute}, nil
}

func (s *Store) Load(
	ctx context.Context,
) ([]runtimeextension.LifecycleReceipt, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if s == nil {
		return nil, errors.New("extension lifecycle store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.loadLocked()
	if err != nil {
		return nil, err
	}
	return append([]runtimeextension.LifecycleReceipt(nil), value.Receipts...), nil
}

func (s *Store) Append(
	ctx context.Context,
	receipt runtimeextension.LifecycleReceipt,
) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if s == nil {
		return errors.New("extension lifecycle store is required")
	}
	if err := receipt.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	value, err := s.loadLocked()
	if err != nil {
		return err
	}
	if len(value.Receipts) != 0 {
		last := value.Receipts[len(value.Receipts)-1]
		if receipt.Sequence <= last.Sequence {
			return errors.New("extension lifecycle receipt sequence is stale")
		}
	}
	value.Receipts = append(value.Receipts, receipt)
	if len(value.Receipts) > maxReceipts {
		value.Receipts = append(
			[]runtimeextension.LifecycleReceipt(nil),
			value.Receipts[len(value.Receipts)-maxReceipts:]...,
		)
	}
	return s.writeLocked(value)
}

func (s *Store) LastSequence(ctx context.Context) (uint64, error) {
	receipts, err := s.Load(ctx)
	if err != nil || len(receipts) == 0 {
		return 0, err
	}
	return receipts[len(receipts)-1].Sequence, nil
}

func (s *Store) loadLocked() (document, error) {
	if err := validateFile(s.path); err != nil {
		return document{}, err
	}
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return document{Version: storeVersion}, nil
	}
	if err != nil {
		return document{}, err
	}
	var value document
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return document{}, fmt.Errorf("decode extension lifecycle receipts: %w", err)
	}
	if value.Version != storeVersion || len(value.Receipts) > maxReceipts {
		return document{}, errors.New("extension lifecycle receipt document is invalid")
	}
	var previous uint64
	for _, receipt := range value.Receipts {
		if err := receipt.Validate(); err != nil || receipt.Sequence <= previous {
			return document{}, errors.New("extension lifecycle receipt history is invalid")
		}
		previous = receipt.Sequence
	}
	return value, nil
}

func (s *Store) writeLocked(value document) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return atomicfile.Replace(s.path, data, 0o600)
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
		return errors.New("extension lifecycle receipts must be a regular non-symlink file")
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return errors.New("extension lifecycle context is required")
	}
	return ctx.Err()
}
