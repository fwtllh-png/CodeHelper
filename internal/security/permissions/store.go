package permissions

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

var ErrAuthorityInsideWorkspace = errors.New("security authority data directory must be outside the workspace")

func Path(dataDir, workspace string) (string, error) {
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", err
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", errors.New("permissions workspace must be an existing directory")
	}
	dataDir, err = filepath.EvalSymlinks(dataDir)
	if err != nil {
		return "", err
	}
	dataDir, err = filepath.Abs(dataDir)
	if err != nil {
		return "", err
	}
	if relative, relErr := filepath.Rel(root, dataDir); relErr == nil &&
		(relative == "." || (relative != ".." &&
			!strings.HasPrefix(relative, ".."+string(filepath.Separator)))) {
		return "", ErrAuthorityInsideWorkspace
	}
	identity := reflect.Indirect(reflect.ValueOf(info.Sys()))
	device, inode := identityField(identity, "Dev"), identityField(identity, "Ino")
	sum := sha256.Sum256([]byte(root + "\x00" + device + "\x00" + inode))
	return filepath.Join(
		dataDir, "security", "workspaces", hex.EncodeToString(sum[:]), FileName,
	), nil
}
func identityField(value reflect.Value, name string) string {
	if value.IsValid() {
		if field := value.FieldByName(name); field.IsValid() && field.CanInterface() {
			return fmt.Sprint(field.Interface())
		}
	}
	return "0"
}
func OpenWorkspaceStore(dataDir, workspace string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, err
	}
	path, err := Path(dataDir, workspace)
	if err != nil {
		return nil, err
	}
	return OpenStore(path)
}

type Store struct {
	Path   string
	bundle Bundle
	mu     sync.Mutex
}

func OpenStore(path string) (*Store, error) {
	bundle, err := Load(path)
	if err != nil {
		return nil, err
	}
	return &Store{Path: path, bundle: bundle}, nil
}
func (s *Store) Rules() []policy.Rule {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]policy.Rule(nil), s.bundle.Rules...)
}
func (s *Store) AppendAllow(
	invocation policy.Invocation,
) (policy.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	rule, err := RuleFromInvocation(invocation)
	if err != nil {
		return policy.Rule{}, err
	}
	bundle, err := AppendAllow(s.Path, rule)
	if err != nil {
		return policy.Rule{}, err
	}
	s.bundle = bundle
	return rule, nil
}
