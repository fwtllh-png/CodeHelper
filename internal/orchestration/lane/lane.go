// Package lane owns inline and tmux worker session adapters.
package lane

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
)

type Backend string

const (
	BackendInline Backend = "inline"
	BackendTmux   Backend = "tmux"
)

type Status string

const (
	StatusQueued  Status = "queued"
	StatusRunning Status = "running"
	StatusStopped Status = "stopped"
	StatusFailed  Status = "failed"
	StatusExited  Status = "exited"
)

type Record struct {
	ID           string    `json:"id"`
	Backend      Backend   `json:"backend"`
	Status       Status    `json:"status"`
	Command      []string  `json:"command"`
	CWD          string    `json:"cwd,omitempty"`
	WorktreePath string    `json:"worktree_path,omitempty"`
	LogPath      string    `json:"log_path"`
	AttachCmd    string    `json:"attach_command,omitempty"`
	PID          int       `json:"pid,omitempty"`
	ExitCode     *int      `json:"exit_code,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	EnvKeys      []string  `json:"environment_keys,omitempty"`
	TmuxSocket   string    `json:"tmux_socket,omitempty"`
	TmuxSession  string    `json:"tmux_session,omitempty"`
}

type StartSpec struct {
	Command     []string
	CWD         string
	Environment []string
	Backend     Backend
	Worktree    bool
}

type PlacementSpec struct {
	Backend Backend
	CWD     string
}

type Registry struct {
	mu      sync.Mutex
	root    string
	records map[string]*Record
	cmds    map[string]*exec.Cmd
}

func Open(root string) (*Registry, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("lane root is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	registry := &Registry{
		root: root, records: make(map[string]*Record), cmds: make(map[string]*exec.Cmd),
	}
	if err := registry.loadPersisted(); err != nil {
		return nil, err
	}
	return registry, nil
}

func (r *Registry) loadPersisted() error {
	entries, err := os.ReadDir(r.root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(r.root, name))
		if err != nil {
			return err
		}
		var record Record
		if err := json.Unmarshal(data, &record); err != nil {
			continue
		}
		if record.ID == "" {
			continue
		}
		copied := record
		r.records[record.ID] = &copied
	}
	return nil
}

func (r *Registry) persist(record *Record) error {
	if record == nil || record.ID == "" {
		return errors.New("lane record is required")
	}
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(r.root, record.ID+".json"), append(data, '\n'), 0o600)
}

// List returns durable lane records sorted by id.
func (r *Registry) List() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	ids := make([]string, 0, len(r.records))
	for id := range r.records {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	out := make([]Record, 0, len(ids))
	for _, id := range ids {
		out = append(out, *r.records[id])
	}
	return out
}

func (r *Registry) Start(ctx context.Context, id string, spec StartSpec) (Record, error) {
	if id == "" || len(spec.Command) == 0 {
		return Record{}, errors.New("lane id and command are required")
	}
	backend := spec.Backend
	if backend == "" {
		backend = BackendInline
	}
	env, err := process.SanitizedEnvironment(spec.Environment)
	if err != nil {
		return Record{}, err
	}
	keys := make([]string, 0, len(env))
	for _, entry := range env {
		name, _, _ := strings.Cut(entry, "=")
		keys = append(keys, name)
	}
	now := time.Now().UTC()
	logPath := filepath.Join(r.root, id+".ndjson")
	cwd := spec.CWD
	worktreePath := ""
	if spec.Worktree {
		wt, err := provisionWorktree(r.root, id)
		if err != nil {
			return Record{}, err
		}
		worktreePath = wt
		if cwd == "" {
			cwd = wt
		}
	}
	record := &Record{
		ID: id, Backend: backend, Status: StatusRunning, Command: append([]string(nil), spec.Command...),
		CWD: cwd, WorktreePath: worktreePath, LogPath: logPath, CreatedAt: now, UpdatedAt: now, EnvKeys: keys,
	}
	switch backend {
	case BackendInline:
		if err := r.startInline(ctx, record, env); err != nil {
			return Record{}, err
		}
	case BackendTmux:
		if err := r.startTmux(ctx, record, env); err != nil {
			return Record{}, err
		}
	default:
		return Record{}, fmt.Errorf("unsupported lane backend %q", backend)
	}
	r.mu.Lock()
	r.records[id] = record
	snapshot := *record
	r.mu.Unlock()
	if err := r.persist(&snapshot); err != nil {
		return Record{}, err
	}
	return snapshot, nil
}

// Place records scheduler placement without starting a second worker process.
func (r *Registry) Place(id string, spec PlacementSpec) (Record, error) {
	if id == "" {
		return Record{}, errors.New("lane placement id is required")
	}
	backend := spec.Backend
	if backend == "" {
		backend = BackendInline
	}
	now := time.Now().UTC()
	record := &Record{
		ID: id, Backend: backend, Status: StatusQueued, CWD: spec.CWD,
		LogPath:   filepath.Join(r.root, id+".ndjson"),
		CreatedAt: now, UpdatedAt: now,
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, exists := r.records[id]; exists {
		if existing.Backend != backend || existing.CWD != spec.CWD {
			return Record{}, fmt.Errorf("lane placement %q conflicts with its durable placement", id)
		}
		copied := *existing
		record = &copied
		record.Status = StatusQueued
		record.UpdatedAt = now
		snapshot := *record
		if err := r.persist(&snapshot); err != nil {
			return Record{}, err
		}
		r.records[id] = record
		return snapshot, nil
	}
	snapshot := *record
	if err := r.persist(&snapshot); err != nil {
		return Record{}, err
	}
	r.records[id] = record
	return snapshot, nil
}

func (r *Registry) Mark(id string, status Status) (Record, error) {
	switch status {
	case StatusRunning, StatusStopped, StatusFailed, StatusExited:
	default:
		return Record{}, fmt.Errorf("lane placement status %q is invalid", status)
	}
	r.mu.Lock()
	record := r.records[id]
	if record == nil {
		r.mu.Unlock()
		return Record{}, errors.New("lane not found")
	}
	record.Status = status
	record.UpdatedAt = time.Now().UTC()
	snapshot := *record
	r.mu.Unlock()
	if err := r.persist(&snapshot); err != nil {
		return Record{}, err
	}
	return snapshot, nil
}

func (r *Registry) startInline(ctx context.Context, record *Record, env []string) error {
	cmd := exec.CommandContext(ctx, record.Command[0], record.Command[1:]...)
	cmd.Env = env
	if record.CWD != "" {
		cmd.Dir = record.CWD
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = cmd.Stdout
	if err := appendLog(record.LogPath, map[string]any{
		"type": "lane.started", "id": record.ID, "backend": record.Backend, "command": record.Command,
	}); err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	r.mu.Lock()
	record.PID = cmd.Process.Pid
	r.cmds[record.ID] = cmd
	r.mu.Unlock()
	go func() {
		logPath := record.LogPath
		laneID := record.ID
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			_ = appendLog(logPath, map[string]any{
				"type": "lane.stdout", "id": laneID, "line": scanner.Text(),
			})
		}
		err := cmd.Wait()
		if err == nil {
			err = scanner.Err()
		}
		code := 0
		if err != nil {
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else {
				code = 1
			}
		}
		// Publish the terminal event before the terminal status. Once callers
		// observe exited/failed, the durable log must already be complete.
		_ = appendLog(logPath, map[string]any{
			"type": "lane.exited", "id": laneID, "exit_code": code,
		})
		r.mu.Lock()
		var snapshot *Record
		if current := r.records[laneID]; current != nil {
			exitCode := code
			current.ExitCode = &exitCode
			if code == 0 {
				current.Status = StatusExited
			} else {
				current.Status = StatusFailed
			}
			current.UpdatedAt = time.Now().UTC()
			copied := *current
			snapshot = &copied
		}
		delete(r.cmds, laneID)
		r.mu.Unlock()
		if snapshot != nil {
			_ = r.persist(snapshot)
		}
	}()
	return nil
}

func (r *Registry) startTmux(ctx context.Context, record *Record, env []string) error {
	if _, err := exec.LookPath("tmux"); err != nil {
		return errors.New("tmux backend unavailable: tmux not found")
	}
	socket := filepath.Join(r.root, record.ID+".sock")
	session := "codehelper_" + record.ID
	record.TmuxSocket = socket
	record.TmuxSession = session
	record.AttachCmd = fmt.Sprintf("tmux -S %s attach -t %s", socket, session)
	script := strings.Join(quoteArgs(record.Command), " ")
	cmd := exec.CommandContext(ctx, "tmux", "-S", socket, "new-session", "-d", "-s", session, script)
	cmd.Env = env
	if record.CWD != "" {
		cmd.Dir = record.CWD
	}
	if err := appendLog(record.LogPath, map[string]any{
		"type": "lane.started", "id": record.ID, "backend": "tmux", "attach": record.AttachCmd,
	}); err != nil {
		return err
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("tmux start: %w", err)
	}
	return nil
}

func (r *Registry) Status(id string) (Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	record, ok := r.records[id]
	if !ok {
		return Record{}, errors.New("lane not found")
	}
	return *record, nil
}

func (r *Registry) Attach(id string) (string, error) {
	record, err := r.Status(id)
	if err != nil {
		return "", err
	}
	if record.Backend != BackendTmux || record.AttachCmd == "" {
		return "", errors.New("attach is only available for tmux lanes")
	}
	return record.AttachCmd, nil
}

func (r *Registry) Stop(id string) (Record, error) {
	r.mu.Lock()
	record, ok := r.records[id]
	cmd := r.cmds[id]
	r.mu.Unlock()
	if !ok {
		return Record{}, errors.New("lane not found")
	}
	switch record.Backend {
	case BackendInline:
		if cmd != nil && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
	case BackendTmux:
		if record.TmuxSocket != "" && record.TmuxSession != "" {
			_ = exec.Command("tmux", "-S", record.TmuxSocket, "kill-session", "-t", record.TmuxSession).Run()
		}
	}
	r.mu.Lock()
	record.Status = StatusStopped
	record.UpdatedAt = time.Now().UTC()
	snapshot := *record
	r.mu.Unlock()
	_ = r.persist(&snapshot)
	_ = appendLog(record.LogPath, map[string]any{"type": "lane.stopped", "id": id})
	return snapshot, nil
}

func (r *Registry) Log(id string, limit int) ([]json.RawMessage, error) {
	record, err := r.Status(id)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(record.LogPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []json.RawMessage
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		lines = append(lines, append(json.RawMessage(nil), scanner.Bytes()...))
	}
	if limit > 0 && len(lines) > limit {
		lines = lines[len(lines)-limit:]
	}
	return lines, scanner.Err()
}

func appendLog(path string, event map[string]any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	data, err := json.Marshal(event)
	if err != nil {
		return err
	}
	if _, err := file.Write(append(data, '\n')); err != nil {
		return err
	}
	return file.Sync()
}

func quoteArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		out = append(out, "'"+strings.ReplaceAll(arg, "'", `'\''`)+"'")
	}
	return out
}
