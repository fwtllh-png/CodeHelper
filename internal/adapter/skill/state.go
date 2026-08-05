package skill

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sync"
)

const stateSchemaVersion = 1

type enableState struct {
	SchemaVersion int             `json:"schema_version"`
	Skills        map[string]bool `json:"skills"`
}

type StateStore struct {
	path string
	lock *sync.Mutex
}

var stateLocks = struct {
	sync.Mutex
	values map[string]*sync.Mutex
}{values: make(map[string]*sync.Mutex)}

func NewStateStore(path string) (*StateStore, error) {
	if path == "" {
		return nil, errors.New("skill enable state path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parent := filepath.Dir(absolute)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return nil, err
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return nil, err
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return nil, errors.New("skill state parent must be a real directory")
	}
	stateLocks.Lock()
	lock := stateLocks.values[absolute]
	if lock == nil {
		lock = &sync.Mutex{}
		stateLocks.values[absolute] = lock
	}
	stateLocks.Unlock()
	return &StateStore{path: filepath.Clean(absolute), lock: lock}, nil
}

func (s *StateStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

func (s *StateStore) Snapshot() (map[string]bool, error) {
	if s == nil {
		return map[string]bool{}, nil
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	fileLock, err := acquireStateFileLock(s.path + ".lock")
	if err != nil {
		return nil, err
	}
	defer fileLock.Close()
	state, err := s.read()
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(state.Skills))
	maps.Copy(result, state.Skills)
	return result, nil
}

func (s *StateStore) SetEnabled(name string, enabled bool) error {
	if s == nil {
		return errors.New("skill enable state store is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New("skill name is invalid")
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	fileLock, err := acquireStateFileLock(s.path + ".lock")
	if err != nil {
		return err
	}
	defer fileLock.Close()
	state, err := s.read()
	if err != nil {
		state = enableState{SchemaVersion: stateSchemaVersion, Skills: make(map[string]bool)}
	}
	state.Skills[name] = enabled
	return s.write(state)
}

func (s *StateStore) Remove(name string) error {
	if s == nil {
		return errors.New("skill enable state store is required")
	}
	if !namePattern.MatchString(name) {
		return errors.New("skill name is invalid")
	}
	s.lock.Lock()
	defer s.lock.Unlock()
	fileLock, err := acquireStateFileLock(s.path + ".lock")
	if err != nil {
		return err
	}
	defer fileLock.Close()
	state, err := s.read()
	if err != nil {
		return err
	}
	delete(state.Skills, name)
	return s.write(state)
}

func (s *StateStore) read() (enableState, error) {
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return enableState{SchemaVersion: stateSchemaVersion, Skills: make(map[string]bool)}, nil
	}
	if err != nil {
		return enableState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return enableState{}, errors.New("skill enable state is not a regular file")
	}
	if err := rejectMultiplyLinkedState(s.path, info); err != nil {
		return enableState{}, err
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return enableState{}, err
	}
	var state enableState
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&state); err != nil {
		return enableState{}, fmt.Errorf("decode skill enable state: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return enableState{}, errors.New("decode skill enable state: multiple JSON values")
	}
	if state.SchemaVersion != stateSchemaVersion || state.Skills == nil {
		return enableState{}, errors.New("skill enable state is missing or unsupported")
	}
	for name := range state.Skills {
		if !namePattern.MatchString(name) {
			return enableState{}, fmt.Errorf("skill enable state contains invalid name %q", name)
		}
	}
	return state, nil
}

func (s *StateStore) write(state enableState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	parent := filepath.Dir(s.path)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	parentInfo, err := os.Lstat(parent)
	if err != nil {
		return err
	}
	if parentInfo.Mode()&os.ModeSymlink != 0 || !parentInfo.IsDir() {
		return errors.New("skill state parent must be a real directory")
	}
	temp, err := os.CreateTemp(parent, "."+filepath.Base(s.path)+".tmp-*")
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if err := temp.Chmod(0o600); err != nil {
		cleanup()
		return err
	}
	if _, err := temp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if info, err := os.Lstat(s.path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		_ = os.Remove(tempPath)
		return errors.New("skill state symlink rejected")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, s.path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	directory, err := os.Open(parent)
	if err == nil {
		err = directory.Sync()
		err = errors.Join(err, directory.Close())
	}
	return err
}
