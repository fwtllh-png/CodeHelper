package rlm

import (
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
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const (
	DefaultEvalTimeoutSecs = 30
	MaxInlineContent       = 200_000
	MaxEvalOutputBytes     = 64 << 10
)

// SessionObject is a compact card from rlm_session_objects.
type SessionObject struct {
	Ref     string `json:"ref"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Preview string `json:"preview,omitempty"`
	Bytes   int    `json:"bytes,omitempty"`
}

// SessionConfig holds mutable RLM session policy.
type SessionConfig struct {
	OutputFeedback      string `json:"output_feedback"`
	EvalTimeoutSecs     int    `json:"eval_timeout_secs"`
	SubQueryTimeoutSecs int    `json:"sub_query_timeout_secs"`
	SubRLMMaxDepth      int    `json:"sub_rlm_max_depth"`
	ShareSession        bool   `json:"share_session"`
}

func (c SessionConfig) WithDefaults() SessionConfig {
	if c.OutputFeedback == "" {
		c.OutputFeedback = "full"
	}
	if c.EvalTimeoutSecs <= 0 {
		c.EvalTimeoutSecs = DefaultEvalTimeoutSecs
	}
	if c.EvalTimeoutSecs > 600 {
		c.EvalTimeoutSecs = 600
	}
	if c.SubQueryTimeoutSecs <= 0 {
		c.SubQueryTimeoutSecs = 60
	}
	return c
}

// Session is a named RLM Python REPL over a loaded context.
type Session struct {
	Name        string
	Context     string
	SourceKind  string
	SourceRef   string
	Config      SessionConfig
	Dir         string
	ContextPath string
	StatePath   string
	RunnerPath  string
	EvalCount   int
	Depth       int
	Transcript  string
	Closed      bool
	CreatedAt   time.Time
	LastEvalAt  *time.Time
}

// EvalResult is the bounded outcome of one rlm_eval.
type EvalResult struct {
	Stdout         string
	Stderr         string
	ExitCode       int
	TimedOut       bool
	Truncated      bool
	DurationMS     int64
	Classification string
}

// Store holds named RLM sessions for a process.
type Store struct {
	mu        sync.Mutex
	root      string
	sessions  map[string]*Session
	objects   map[string]SessionObject
	payloads  map[string]string
	python    string
	backend   sandbox.Backend
	workspace *sandbox.Workspace
	subQuery  SubQueryClient
	governor  *Governor
}

type StoreOptions struct {
	Root      string
	Python    string
	Backend   sandbox.Backend
	Workspace *sandbox.Workspace
	Objects   []SessionObject
	Payloads  map[string]string
	SubQuery  SubQueryClient
	Governor  *Governor
}

func NewStore(options StoreOptions) (*Store, error) {
	root := strings.TrimSpace(options.Root)
	if root == "" {
		return nil, errors.New("rlm store root is required")
	}
	if options.Workspace == nil {
		return nil, errors.New("rlm workspace is required")
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		return nil, err
	}
	python := strings.TrimSpace(options.Python)
	if python == "" {
		python = strings.TrimSpace(os.Getenv("CODEHELPER_PYTHON_BINARY"))
	}
	if python == "" {
		python = "python3"
	}
	objects := map[string]SessionObject{}
	payloads := map[string]string{}
	for _, object := range options.Objects {
		objects[object.Ref] = object
	}
	for ref, body := range options.Payloads {
		payloads[ref] = body
	}
	if len(objects) == 0 {
		for _, object := range DefaultSessionObjects() {
			objects[object.Ref] = object
		}
	}
	return &Store{
		root: root, sessions: make(map[string]*Session),
		objects: objects, payloads: payloads, python: python,
		backend: options.Backend, workspace: options.Workspace,
		subQuery: options.SubQuery, governor: options.Governor,
	}, nil
}

func DefaultSessionObjects() []SessionObject {
	return []SessionObject{
		{Ref: "session://active/system_prompt", Kind: "prompt", Title: "system prompt"},
		{Ref: "session://active/transcript", Kind: "transcript", Title: "transcript"},
		{Ref: "session://active/latest_user", Kind: "message", Title: "latest user message"},
		{Ref: "session://active/metadata", Kind: "metadata", Title: "session metadata"},
	}
}

func (s *Store) PythonAvailable() bool {
	_, err := exec.LookPath(s.python)
	return err == nil
}

func (s *Store) Python() string { return s.python }

func (s *Store) ListObjects() []SessionObject {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionObject, 0, len(s.objects))
	for _, object := range s.objects {
		copy := object
		if body, ok := s.payloads[object.Ref]; ok {
			copy.Bytes = len(body)
			if copy.Preview == "" {
				copy.Preview = truncate(body, 120)
			}
		}
		out = append(out, copy)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Ref < out[j].Ref })
	return out
}

func (s *Store) SetObject(ref, kind, title, body string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[ref] = SessionObject{Ref: ref, Kind: kind, Title: title, Bytes: len(body), Preview: truncate(body, 120)}
	s.payloads[ref] = body
}

func (s *Store) Open(name, sourceKind, sourceRef, contextBody string, depth int) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("session name is required")
	}
	if !validName(name) {
		return nil, errors.New("session name contains unsupported characters")
	}
	if existing, ok := s.sessions[name]; ok && !existing.Closed {
		return nil, fmt.Errorf("rlm session %q already open", name)
	}
	if len(contextBody) > MaxInlineContent {
		return nil, fmt.Errorf("context exceeds %d bytes", MaxInlineContent)
	}
	dir := filepath.Join(s.root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	contextPath := filepath.Join(dir, "context.txt")
	statePath := filepath.Join(dir, "state.json")
	runnerPath := filepath.Join(dir, "runner.py")
	if err := os.WriteFile(contextPath, []byte(contextBody), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(runnerPath, []byte(pythonRunner), 0o600); err != nil {
		return nil, err
	}
	_ = os.Remove(statePath)
	session := &Session{
		Name: name, Context: contextBody, SourceKind: sourceKind, SourceRef: sourceRef,
		Config: SessionConfig{}.WithDefaults(), Dir: dir,
		ContextPath: contextPath, StatePath: statePath, RunnerPath: runnerPath,
		Depth: depth, CreatedAt: time.Now().UTC(),
	}
	s.sessions[name] = session
	return cloneSession(session), nil
}

func (s *Store) Get(name string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[strings.TrimSpace(name)]
	if !ok || session.Closed {
		return nil, fmt.Errorf("rlm session %q not found", name)
	}
	return cloneSession(session), nil
}

// ListSessions returns open RLM session names (sorted).
func (s *Store) ListSessions() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.sessions))
	for name, session := range s.sessions {
		if session != nil && !session.Closed {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

func (s *Store) ApplyConfig(name string, cfg SessionConfig) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[strings.TrimSpace(name)]
	if !ok || session.Closed {
		return nil, fmt.Errorf("rlm session %q not found", name)
	}
	session.Config = cfg.WithDefaults()
	return cloneSession(session), nil
}

func (s *Store) ResolveObject(ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	body, ok := s.payloads[ref]
	if !ok {
		return "", fmt.Errorf("session object %q not found", ref)
	}
	return body, nil
}

func (s *Store) Eval(ctx context.Context, name, code string) (EvalResult, *Session, error) {
	s.mu.Lock()
	session, ok := s.sessions[strings.TrimSpace(name)]
	if !ok || session.Closed {
		s.mu.Unlock()
		return EvalResult{}, nil, fmt.Errorf("rlm session %q not found", name)
	}
	if strings.TrimSpace(code) == "" {
		s.mu.Unlock()
		return EvalResult{}, nil, errors.New("code is required")
	}
	timeout := time.Duration(session.Config.EvalTimeoutSecs) * time.Second
	subQueryTimeout := session.Config.SubQueryTimeoutSecs
	runner := session.RunnerPath
	contextPath := session.ContextPath
	statePath := session.StatePath
	dir := session.Dir
	codePath := filepath.Join(dir, fmt.Sprintf("eval-%d.py", session.EvalCount+1))
	outPath := filepath.Join(dir, fmt.Sprintf("out-%d.json", session.EvalCount+1))
	subQuery := s.subQuery
	governor := s.governor
	s.mu.Unlock()

	if err := os.WriteFile(codePath, []byte(code), 0o600); err != nil {
		return EvalResult{}, nil, err
	}
	runCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	directory, err := s.workspace.ResolveDirectory(".")
	if err != nil {
		return EvalResult{}, nil, err
	}
	directoryFile, err := s.workspace.OpenDirectory(".")
	if err != nil {
		return EvalResult{}, nil, err
	}
	defer directoryFile.Close()

	var bridge *subQueryBridge
	if subQuery != nil {
		started, startErr := startSubQueryBridge(subQuery, governor, subQueryTimeout)
		if startErr != nil {
			return EvalResult{}, nil, startErr
		}
		bridge = started
		defer bridge.Close()
		_ = os.WriteFile(filepath.Join(dir, "bridge_url.txt"), []byte(bridge.BaseURL()), 0o600)
		_ = os.WriteFile(
			filepath.Join(dir, "bridge_timeout.txt"),
			[]byte(fmt.Sprintf("%d", subQueryTimeout)),
			0o600,
		)
	} else {
		_ = os.Remove(filepath.Join(dir, "bridge_url.txt"))
		_ = os.Remove(filepath.Join(dir, "bridge_timeout.txt"))
	}

	started := time.Now().UTC()
	result, err := process.Run(runCtx, process.Options{
		Path: s.python,
		Args: []string{runner, contextPath, statePath, codePath, outPath},
		Dir:  directory, DirFile: directoryFile,
		Sandbox: s.backend, RequireStrongSandbox: true,
	})
	timedOut := errors.Is(runCtx.Err(), context.DeadlineExceeded) ||
		(err != nil && errors.Is(err, context.DeadlineExceeded))
	if err != nil && !timedOut {
		return EvalResult{}, nil, err
	}
	if timedOut && result.ExitCode == 0 && result.Stdout == "" && result.Stderr == "" {
		result.ExitCode = -1
	}
	duration := time.Since(started).Milliseconds()
	stdout := result.Stdout
	stderr := result.Stderr
	if payload, readErr := os.ReadFile(outPath); readErr == nil && len(payload) > 0 {
		var encoded struct {
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
			Error  string `json:"error"`
		}
		if json.Unmarshal(payload, &encoded) == nil {
			if encoded.Stdout != "" {
				stdout = encoded.Stdout
			}
			if encoded.Stderr != "" {
				stderr = encoded.Stderr
			}
			if encoded.Error != "" && stderr == "" {
				stderr = encoded.Error
			}
		}
	}
	truncated := false
	if len(stdout) > MaxEvalOutputBytes {
		stdout = stdout[:MaxEvalOutputBytes] + "…"
		truncated = true
	}
	if len(stderr) > MaxEvalOutputBytes {
		stderr = stderr[:MaxEvalOutputBytes] + "…"
		truncated = true
	}
	classification := "passed"
	if timedOut {
		classification = "timeout"
	} else if result.ExitCode != 0 {
		classification = "failed"
	}
	eval := EvalResult{
		Stdout: stdout, Stderr: stderr, ExitCode: result.ExitCode,
		TimedOut: timedOut, Truncated: truncated, DurationMS: duration,
		Classification: classification,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	session = s.sessions[strings.TrimSpace(name)]
	if session == nil || session.Closed {
		return eval, nil, fmt.Errorf("rlm session %q closed during eval", name)
	}
	session.EvalCount++
	now := time.Now().UTC()
	session.LastEvalAt = &now
	entry := fmt.Sprintf("\n--- eval %d (%s) ---\n%s\n", session.EvalCount, classification, code)
	if stdout != "" {
		entry += "[stdout]\n" + stdout + "\n"
	}
	if stderr != "" {
		entry += "[stderr]\n" + stderr + "\n"
	}
	session.Transcript += entry
	return eval, cloneSession(session), nil
}

func (s *Store) Close(name string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	session, ok := s.sessions[strings.TrimSpace(name)]
	if !ok {
		return nil, fmt.Errorf("rlm session %q not found", name)
	}
	if session.Closed {
		return cloneSession(session), nil
	}
	session.Closed = true
	_ = os.RemoveAll(session.Dir)
	return cloneSession(session), nil
}

func cloneSession(session *Session) *Session {
	copy := *session
	if session.LastEvalAt != nil {
		value := *session.LastEvalAt
		copy.LastEvalAt = &value
	}
	return &copy
}

func validName(name string) bool {
	if name == "" || len(name) > 64 {
		return false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') ||
			(r >= '0' && r <= '9') || r == '-' || r == '_' {
			continue
		}
		return false
	}
	return true
}

func truncate(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	return value[:limit] + "…"
}

// pythonRunner executes user code with _context/_ctx/content bindings and
// persists JSON-serializable locals between evals.
const pythonRunner = `#!/usr/bin/env python3
import io
import json
import sys
import traceback
import urllib.error
import urllib.request
from concurrent.futures import ThreadPoolExecutor, as_completed
from pathlib import Path

context_path, state_path, code_path, out_path = map(Path, sys.argv[1:5])
_session_dir = context_path.parent
_context = context_path.read_text(encoding="utf-8")
_ctx = _context
content = _context

def _bridge_url():
    path = _session_dir / "bridge_url.txt"
    if not path.exists():
        return ""
    return path.read_text(encoding="utf-8").strip()

def _default_timeout():
    path = _session_dir / "bridge_timeout.txt"
    if not path.exists():
        return 60
    try:
        return int(path.read_text(encoding="utf-8").strip() or "60")
    except ValueError:
        return 60

def sub_query(prompt, slice=None, timeout_secs=None):
    url = _bridge_url()
    if not url:
        raise RuntimeError("sub_query unavailable: no SubQueryClient configured")
    body = {"prompt": "" if prompt is None else str(prompt)}
    if slice is not None:
        body["slice"] = str(slice)
    if timeout_secs is not None:
        body["timeout_secs"] = int(timeout_secs)
    else:
        body["timeout_secs"] = _default_timeout()
    req = urllib.request.Request(
        url.rstrip("/") + "/v1/sub_query",
        data=json.dumps(body).encode("utf-8"),
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    timeout = body["timeout_secs"] + 5
    try:
        with urllib.request.urlopen(req, timeout=timeout) as resp:
            payload = json.loads(resp.read().decode("utf-8"))
    except urllib.error.HTTPError as exc:
        raw = exc.read().decode("utf-8", errors="replace")
        try:
            payload = json.loads(raw)
            message = payload.get("error") or raw
        except Exception:
            message = raw or str(exc)
        raise RuntimeError(message) from exc
    except Exception as exc:
        raise RuntimeError(str(exc)) from exc
    if isinstance(payload, dict) and payload.get("error"):
        raise RuntimeError(str(payload["error"]))
    return "" if not isinstance(payload, dict) else str(payload.get("text") or "")

def sub_query_batch(prompt, slices, dependency_mode="independent", safety_note="", timeout_secs=None):
    if dependency_mode != "independent":
        raise RuntimeError(
            'sub_query_batch only supports dependency_mode="independent"; '
            "use sequential sub_query for ordered dependencies"
        )
    if slices is None:
        slices = []
    if not isinstance(slices, (list, tuple)):
        raise TypeError("slices must be a list")
    if len(slices) > 16:
        raise RuntimeError("sub_query_batch supports at most 16 slices (MaxSubQueryBatch=16)")
    if not _bridge_url():
        raise RuntimeError("sub_query unavailable: no SubQueryClient configured")
    results = [None] * len(slices)
    with ThreadPoolExecutor(max_workers=min(16, max(1, len(slices)))) as pool:
        futures = {
            pool.submit(sub_query, prompt, slice=item, timeout_secs=timeout_secs): index
            for index, item in enumerate(slices)
        }
        for future in as_completed(futures):
            index = futures[future]
            results[index] = future.result()
    return results

def sub_query_map(prompt, items, timeout_secs=None):
    """Map prompt over items with the same concurrency ceiling as sub_query_batch."""
    if items is None:
        items = []
    if not isinstance(items, (list, tuple)):
        raise TypeError("items must be a list")
    if len(items) > 16:
        raise RuntimeError("sub_query_map supports at most 16 items (MaxSubQueryBatch=16)")
    return sub_query_batch(prompt, list(items), dependency_mode="independent", timeout_secs=timeout_secs)

ns = {
    "_context": _context, "_ctx": _ctx, "content": content, "__name__": "__main__",
    "sub_query": sub_query, "sub_query_batch": sub_query_batch, "sub_query_map": sub_query_map,
}
if state_path.exists():
    try:
        saved = json.loads(state_path.read_text(encoding="utf-8"))
        if isinstance(saved, dict):
            for key, value in saved.items():
                if key not in ("_context", "_ctx", "content", "sub_query", "sub_query_batch", "sub_query_map"):
                    ns[key] = value
    except Exception:
        pass
code = code_path.read_text(encoding="utf-8")
stdout_buf = io.StringIO()
stderr_buf = io.StringIO()
real_stdout, real_stderr = sys.stdout, sys.stderr
sys.stdout, sys.stderr = stdout_buf, stderr_buf
error = ""
exit_code = 0
try:
    compiled = compile(code, "<rlm_eval>", "exec")
    exec(compiled, ns, ns)
except SystemExit as exc:
    exit_code = int(exc.code) if isinstance(exc.code, int) else 1
except Exception:
    error = traceback.format_exc()
    exit_code = 1
finally:
    sys.stdout, sys.stderr = real_stdout, real_stderr

serializable = {}
for key, value in ns.items():
    if key.startswith("__") or key in ("_context", "_ctx", "content", "sub_query", "sub_query_batch", "sub_query_map"):
        continue
    try:
        json.dumps(value)
        serializable[key] = value
    except TypeError:
        continue
state_path.write_text(json.dumps(serializable), encoding="utf-8")
payload = {
    "stdout": stdout_buf.getvalue(),
    "stderr": stderr_buf.getvalue(),
    "error": error,
    "exit_code": exit_code,
}
out_path.write_text(json.dumps(payload), encoding="utf-8")
if error:
    print(error, file=sys.stderr)
if payload["stdout"]:
    print(payload["stdout"], end="")
sys.exit(exit_code)
`
