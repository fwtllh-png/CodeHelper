// Package memory owns the scoped durable records used for prompt injection and
// memory tools. The configured path is a secure root directory.
package memory

import (
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode/utf8"
)

const (
	RecordsFileName = "records.json"
	RecordsLockName = ".records.lock"
	MaxPromptBytes  = 100 << 10
	MaxNoteBytes    = 2 << 10
	MaxFileBytes    = 1 << 20
)

var (
	ErrDisabled     = errors.New("user memory is disabled")
	ErrEmptyNote    = errors.New("memory note is empty")
	ErrNoteTooLarge = errors.New("memory note exceeds limit")
	ErrFileTooLarge = errors.New("memory file exceeds limit")
	ErrEscape       = errors.New("memory path escapes configured root")
)

// Store is a locked, user-owned memory root. Read-modify-write operations are
// serialized across Store instances and processes that share the same root.
type Store struct {
	root        string
	recordsFile string
	lockFile    string
	options     Options
	mu          sync.Mutex
}

type Options struct {
	MaxCandidates  int
	MaxPromptBytes int
	WorkspaceID    string
	RepositoryID   string
}

// Open validates and prepares a memory root. The root is created with 0700
// permissions when missing. Symlink escapes out of root are rejected.
func Open(root string, options ...Options) (*Store, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, errors.New("memory root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve memory root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create memory root: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return nil, fmt.Errorf("resolve memory root: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, fmt.Errorf("stat memory root: %w", err)
	}
	if !info.IsDir() {
		return nil, errors.New("memory path must be a directory root")
	}
	opts := Options{MaxCandidates: 32, MaxPromptBytes: 16 << 10}
	if len(options) > 1 {
		return nil, errors.New("memory Open accepts at most one Options value")
	}
	if len(options) == 1 {
		opts = options[0]
		if opts.MaxCandidates <= 0 {
			opts.MaxCandidates = 32
		}
		if opts.MaxPromptBytes <= 0 {
			opts.MaxPromptBytes = 16 << 10
		}
	}
	recordsFile := filepath.Join(resolved, RecordsFileName)
	if err := assertInside(resolved, recordsFile); err != nil {
		return nil, err
	}
	lockFile := filepath.Join(resolved, RecordsLockName)
	if err := assertInside(resolved, lockFile); err != nil {
		return nil, err
	}
	return &Store{
		root: resolved, recordsFile: recordsFile, lockFile: lockFile,
		options: opts,
	}, nil
}

func (s *Store) Root() string {
	if s == nil {
		return ""
	}
	return s.root
}

func (s *Store) Path() string {
	if s == nil {
		return ""
	}
	return s.recordsFile
}

// Load returns the trimmed memory content, or ("", false) when missing/empty.
func (s *Store) Load() (string, bool, error) {
	if s == nil {
		return "", false, ErrDisabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadLocked()
}

func (s *Store) loadLocked() (string, bool, error) {
	records, found, err := s.loadRecordFileLocked()
	if err != nil {
		return "", false, err
	}
	if !found {
		return "", false, nil
	}
	content := renderRecords(records.Records)
	if strings.TrimSpace(content) == "" {
		return "", false, nil
	}
	return content, true, nil
}

// ComposeBlock builds the bounded <user_memory> system partition. Missing or
// empty files yield ("", false, nil) so callers inject nothing.
func (s *Store) ComposeBlock() (string, bool, error) {
	return s.ComposeBlockFor(Query{})
}

// Append preserves the original API while writing a user-scoped fact record.
func (s *Store) Append(note string) error {
	_, _, err := s.Remember(CreateRequest{
		Scope: ScopeUser, Category: CategoryFact,
		Text: note, Source: "remember",
	})
	return err
}

func (s *Store) ensureRecordsFile() error {
	if err := assertInside(s.root, s.recordsFile); err != nil {
		return err
	}
	if info, err := os.Lstat(s.recordsFile); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrEscape
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// AsSystemBlock wraps content for prompt injection with a hard 100 KiB budget.
func AsSystemBlock(content, source string) string {
	return AsSystemBlockBounded(content, source, MaxPromptBytes)
}

func previousUTF8Boundary(value string, index int) int {
	if index >= len(value) {
		return len(value)
	}
	if index <= 0 {
		return 0
	}
	for index > 0 && !utf8.RuneStart(value[index]) {
		index--
	}
	return index
}

func assertInside(root, path string) error {
	cleanRoot := filepath.Clean(root)
	cleanPath := filepath.Clean(path)
	rel, err := filepath.Rel(cleanRoot, cleanPath)
	if err != nil {
		return ErrEscape
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ErrEscape
	}
	return nil
}

func containsCredentialMaterial(note string) bool {
	if block, _ := pem.Decode([]byte(note)); block != nil &&
		strings.Contains(strings.ToUpper(block.Type), "PRIVATE KEY") {
		return true
	}
	credentialFields := map[string]struct{}{
		"access_token": {}, "api_key": {}, "apikey": {}, "client_secret": {},
		"password": {}, "refresh_token": {}, "token": {},
	}
	for line := range strings.Lines(note) {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			key, value, ok = strings.Cut(line, ":")
		}
		if !ok || strings.TrimSpace(value) == "" {
			continue
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if key == "authorization" &&
			strings.HasPrefix(strings.ToLower(strings.TrimSpace(value)), "bearer ") {
			return true
		}
		if _, sensitive := credentialFields[key]; sensitive {
			return true
		}
	}
	return false
}
