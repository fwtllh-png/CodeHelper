package ownerlease

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const metadataOffset = 1

type Metadata struct {
	Version   int       `json:"version"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
	Build     string    `json:"build"`
	OwnerKind string    `json:"owner_kind"`
	PublicURL string    `json:"public_url,omitempty"`
}

type HeldError struct {
	Path     string
	Metadata Metadata
}

func (e *HeldError) Error() string {
	if e.Metadata.PublicURL != "" {
		return fmt.Sprintf("interactive Runtime is already owned at %s", e.Metadata.PublicURL)
	}
	return "interactive Runtime is already owned"
}

type Lease struct {
	file     *os.File
	path     string
	metadata Metadata
}

func Path(dataDir, workspaceRootID string) string {
	root, err := filepath.Abs(dataDir)
	if err != nil {
		root = filepath.Clean(dataDir)
	}
	sum := sha256.Sum256([]byte(root + "\x00" + workspaceRootID))
	return filepath.Join(root, "leases", "interactive-"+hex.EncodeToString(sum[:12])+".lock")
}

func Acquire(path string, metadata Metadata) (*Lease, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("owner lease path is required")
	}
	if metadata.Version == 0 {
		metadata.Version = 1
	}
	if metadata.PID == 0 {
		metadata.PID = os.Getpid()
	}
	if metadata.StartedAt.IsZero() {
		metadata.StartedAt = time.Now().UTC()
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create owner lease directory: %w", err)
	}
	file, err := openLeaseFile(path)
	if err != nil {
		return nil, fmt.Errorf("open owner lease: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("secure owner lease: %w", err)
	}
	if err := tryLock(file); err != nil {
		held := HeldError{Path: path}
		_, _ = file.Seek(metadataOffset, 0)
		_ = json.NewDecoder(file).Decode(&held.Metadata)
		_ = file.Close()
		if isWouldBlock(err) {
			return nil, &held
		}
		return nil, fmt.Errorf("lock interactive Runtime owner: %w", err)
	}
	lease := &Lease{file: file, path: path, metadata: metadata}
	if err := lease.Update(metadata); err != nil {
		_ = lease.Close()
		return nil, err
	}
	return lease, nil
}

func (l *Lease) Update(metadata Metadata) error {
	if l == nil || l.file == nil {
		return errors.New("owner lease is closed")
	}
	metadata.Version = 1
	if metadata.PID == 0 {
		metadata.PID = l.metadata.PID
	}
	if metadata.StartedAt.IsZero() {
		metadata.StartedAt = l.metadata.StartedAt
	}
	data, err := json.Marshal(metadata)
	if err != nil {
		return fmt.Errorf("encode owner lease: %w", err)
	}
	data = append(data, '\n')
	if err := l.file.Truncate(metadataOffset); err != nil {
		return fmt.Errorf("truncate owner lease: %w", err)
	}
	if _, err := l.file.Seek(metadataOffset, 0); err != nil {
		return fmt.Errorf("seek owner lease: %w", err)
	}
	if _, err := l.file.Write(data); err != nil {
		return fmt.Errorf("write owner lease: %w", err)
	}
	if err := l.file.Sync(); err != nil {
		return fmt.Errorf("sync owner lease: %w", err)
	}
	l.metadata = metadata
	return nil
}

func (l *Lease) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	file := l.file
	l.file = nil
	return errors.Join(unlock(file), file.Close())
}
