// Package memory owns the user-scoped durable note file used for prompt
// injection and the remember tool. The configured path is a secure root
// directory; the canonical note file is always <root>/memory.md.
package memory

import (
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	FileName         = "memory.md"
	MaxPromptBytes   = 100 << 10
	MaxNoteBytes     = 2 << 10
	MaxFileBytes     = 1 << 20
	truncationPrefix = "\n<truncated bytes="
)

var (
	ErrDisabled     = errors.New("user memory is disabled")
	ErrEmptyNote    = errors.New("memory note is empty")
	ErrNoteTooLarge = errors.New("memory note exceeds limit")
	ErrFileTooLarge = errors.New("memory file exceeds limit")
	ErrEscape       = errors.New("memory path escapes configured root")
)

// Store is a locked, user-owned memory root. Concurrent appends are serialized
// per process; writes are flushed before the lock is released.
type Store struct {
	root string
	file string
	mu   sync.Mutex
}

// Open validates and prepares a memory root. The root is created with 0700
// permissions when missing. Symlink escapes out of root are rejected.
func Open(root string) (*Store, error) {
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
	file := filepath.Join(resolved, FileName)
	if err := assertInside(resolved, file); err != nil {
		return nil, err
	}
	return &Store{root: resolved, file: file}, nil
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
	return s.file
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
	if err := s.ensureCanonicalFile(); err != nil {
		return "", false, err
	}
	data, err := os.ReadFile(s.file)
	if errors.Is(err, os.ErrNotExist) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	content := string(data)
	if strings.TrimSpace(content) == "" {
		return "", false, nil
	}
	return content, true, nil
}

// ComposeBlock builds the bounded <user_memory> system partition. Missing or
// empty files yield ("", false, nil) so callers inject nothing.
func (s *Store) ComposeBlock() (string, bool, error) {
	content, ok, err := s.Load()
	if err != nil || !ok {
		return "", ok, err
	}
	return AsSystemBlock(content, s.file), true, nil
}

// Append adds one timestamped Markdown bullet. Notes are capped and the on-disk
// file may not grow past MaxFileBytes.
func (s *Store) Append(note string) error {
	if s == nil {
		return ErrDisabled
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(note), "#"))
	if trimmed == "" {
		return ErrEmptyNote
	}
	if utf8.RuneCountInString(trimmed) == 0 {
		return ErrEmptyNote
	}
	if len(trimmed) > MaxNoteBytes {
		return ErrNoteTooLarge
	}
	if containsCredentialMaterial(trimmed) {
		return errors.New("memory note must not contain secrets")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.ensureCanonicalFile(); err != nil {
		return err
	}
	info, err := os.Stat(s.file)
	size := int64(0)
	if err == nil {
		size = info.Size()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	line := fmt.Sprintf("- (%s) %s\n", time.Now().UTC().Format("2006-01-02 15:04 UTC"), trimmed)
	if size+int64(len(line)) > MaxFileBytes {
		return ErrFileTooLarge
	}
	if err := os.MkdirAll(s.root, 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(s.file, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := file.WriteString(line); err != nil {
		return err
	}
	return file.Sync()
}

func (s *Store) ensureCanonicalFile() error {
	if err := assertInside(s.root, s.file); err != nil {
		return err
	}
	parent := filepath.Dir(s.file)
	resolvedParent, err := filepath.EvalSymlinks(parent)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		resolvedParent = parent
	}
	if err := assertInside(s.root, filepath.Join(resolvedParent, filepath.Base(s.file))); err != nil {
		return err
	}
	if info, err := os.Lstat(s.file); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := filepath.EvalSymlinks(s.file)
			if err != nil {
				return err
			}
			if err := assertInside(s.root, target); err != nil {
				return err
			}
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// AsSystemBlock wraps content for prompt injection with a hard 100 KiB budget.
func AsSystemBlock(content, source string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	payload := trimmed
	if len(content) > MaxPromptBytes {
		cutoff := truncationCutoff(content, source)
		payload = content[:cutoff] + truncationMarker(len(content)-cutoff, source)
	}
	return fmt.Sprintf("<user_memory source=%q>\n%s\n</user_memory>", source, payload)
}

func truncationCutoff(content, source string) int {
	cutoff := previousUTF8Boundary(content, MaxPromptBytes)
	for {
		omitted := len(content) - cutoff
		maxHead := MaxPromptBytes - len(truncationMarker(omitted, source))
		if maxHead < 0 {
			maxHead = 0
		}
		next := previousUTF8Boundary(content, min(cutoff, maxHead))
		if next == cutoff {
			return cutoff
		}
		cutoff = next
	}
}

func truncationMarker(omitted int, source string) string {
	return fmt.Sprintf("%s%d source=%q>", truncationPrefix, omitted, source)
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
