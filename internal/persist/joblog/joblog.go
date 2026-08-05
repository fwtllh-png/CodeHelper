// Package joblog keeps the whole output of a background job on disk.
//
// A background shell holds its recent output in memory so a poller can read
// incrementally. That buffer is bounded, which means a job that prints more than
// the bound loses its beginning, and a poller that was slow gets told its cursor
// expired — the bytes are simply gone. Writing the same stream to a file makes
// the loss recoverable: the cursor still addresses a position in the stream, and
// the bytes behind it are still there to read.
package joblog

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// ErrNotFound reports a job with no log, which happens for a job that started
// before archiving was configured, or whose file was removed.
var ErrNotFound = errors.New("job log not found")

// Store is the set of job logs under one directory. One file per job, appended
// in the order the job produced output, so a file offset is the job's cursor.
type Store struct {
	directory string

	mu    sync.Mutex
	files map[string]*os.File
}

// New prepares a store under directory, creating it when needed.
func New(directory string) (*Store, error) {
	if strings.TrimSpace(directory) == "" {
		return nil, errors.New("job log directory is required")
	}
	if !filepath.IsAbs(directory) {
		// A relative directory would follow the process's working directory, which
		// changes; the logs of one workspace would end up split across places.
		return nil, fmt.Errorf("job log directory must be absolute: %q", directory)
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil, err
	}
	return &Store{directory: directory, files: map[string]*os.File{}}, nil
}

// Append adds data to the job's log. It is called from the job's reader loop, so
// it keeps the file open rather than paying for an open per chunk.
func (s *Store) Append(id string, data []byte) error {
	if len(data) == 0 {
		return nil
	}
	file, err := s.writer(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	_, err = file.Write(data)
	return err
}

// Range returns up to limit bytes of the job's output starting at offset, along
// with the total number of bytes the job has produced. An offset past the end
// yields no data rather than an error: a poller that is up to date is not wrong.
func (s *Store) Range(id string, offset uint64, limit int) ([]byte, uint64, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, 0, err
	}
	file, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, 0, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return nil, 0, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	total := uint64(info.Size())
	if offset >= total {
		return nil, total, nil
	}
	available := total - offset
	if limit > 0 && uint64(limit) < available {
		available = uint64(limit)
	}
	data := make([]byte, available)
	if _, err := file.ReadAt(data, int64(offset)); err != nil && !errors.Is(err, io.EOF) {
		return nil, total, err
	}
	return data, total, nil
}

// Size reports how many bytes the job has written.
func (s *Store) Size(id string) (uint64, error) {
	path, err := s.path(id)
	if err != nil {
		return 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return 0, fmt.Errorf("%w: %s", ErrNotFound, id)
		}
		return 0, err
	}
	return uint64(info.Size()), nil
}

// Remove discards a job's log. Callers own retention: nothing here expires a log
// on its own, because "the job is over" is not the same as "nobody will read it".
func (s *Store) Remove(id string) error {
	path, err := s.path(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if file, open := s.files[id]; open {
		_ = file.Close()
		delete(s.files, id)
	}
	s.mu.Unlock()
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// Close releases the open append handles. The logs stay on disk.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var failures []error
	for id, file := range s.files {
		if err := file.Close(); err != nil {
			failures = append(failures, err)
		}
		delete(s.files, id)
	}
	return errors.Join(failures...)
}

func (s *Store) writer(id string) (*os.File, error) {
	path, err := s.path(id)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if file, open := s.files[id]; open {
		return file, nil
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, err
	}
	s.files[id] = file
	return file, nil
}

// path refuses an id that would name a file outside the store. Job ids are
// already constrained where they are minted; this is the check that keeps that
// invariant local to the code that depends on it.
func (s *Store) path(id string) (string, error) {
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return "", fmt.Errorf("job id is not usable as a log name: %q", id)
	}
	return filepath.Join(s.directory, id+".log"), nil
}
