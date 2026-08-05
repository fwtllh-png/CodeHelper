// Package jsvm provides a goja-backed sandboxed Workflow script runtime.
// Scripts may only interact with the host through workflow.Driver. CodeHelper's
// goja port is intentionally single-threaded and synchronous: task/parallel
// complete before returning, while cancellation and timeouts are honored at
// every host call boundary.
package jsvm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
)

const (
	LifetimeCap      = 1000
	MaxConcurrent    = 16
	ParallelMaxItems = 1000
)

var (
	ErrCanceled         = errors.New("workflow run canceled")
	ErrTimeout          = errors.New("workflow run timed out")
	ErrLifetimeCap      = errors.New("workflow lifetime agent cap exceeded")
	ErrParallelTooLarge = errors.New("parallel fan-out exceeds limit")
	ErrDeterminism      = errors.New("non-deterministic host API is disabled")
)

type Options struct {
	Driver       workflow.Driver
	Timeout      time.Duration
	Args         any
	Workspace    string
	EnvAllowlist []string
}

type VM struct{}

func New() *VM { return &VM{} }

func (v *VM) RunScript(ctx context.Context, source string, options Options) (json.RawMessage, error) {
	if options.Driver == nil {
		return nil, errors.New("workflow driver is required")
	}
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	defer func() { _ = options.Driver.CancelAll() }()

	runtime := goja.New()
	state := &runState{
		ctx: ctx, driver: options.Driver, sem: make(chan struct{}, MaxConcurrent),
		workspace: options.Workspace, envAllow: options.EnvAllowlist,
	}
	installDeterminismBans(runtime)
	if err := installHost(runtime, state, options.Args); err != nil {
		return nil, err
	}

	done := make(chan struct{})
	var (
		result goja.Value
		runErr error
	)
	go func() {
		defer close(done)
		defer func() {
			if recovered := recover(); recovered != nil {
				runErr = fmt.Errorf("%v", recovered)
			}
		}()
		result, runErr = runtime.RunString(source)
	}()

	select {
	case <-ctx.Done():
		state.cancel()
		<-done
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, ErrTimeout
		}
		return nil, ErrCanceled
	case <-done:
		if runErr != nil {
			if state.canceled.Load() {
				return nil, ErrCanceled
			}
			return nil, runErr
		}
		return marshalResult(result.Export())
	}
}

type runState struct {
	ctx       context.Context
	driver    workflow.Driver
	sem       chan struct{}
	spawned   atomic.Uint64
	canceled  atomic.Bool
	workspace string
	envAllow  []string
}

func (s *runState) cancel() {
	s.canceled.Store(true)
	_ = s.driver.CancelAll()
}

func installDeterminismBans(runtime *goja.Runtime) {
	ban := func(goja.FunctionCall) goja.Value {
		panic(runtime.NewGoError(ErrDeterminism))
	}
	ctor := runtime.ToValue(func(call goja.ConstructorCall) *goja.Object {
		panic(runtime.NewGoError(fmt.Errorf("%w: new Date()", ErrDeterminism)))
	}).ToObject(runtime)
	_ = ctor.Set("now", ban)
	_ = ctor.Set("parse", ban)
	_ = ctor.Set("UTC", ban)
	_ = runtime.Set("Date", ctor)
	if math := runtime.Get("Math"); math != nil {
		_ = math.ToObject(runtime).Set("random", ban)
	}
}

func installHost(runtime *goja.Runtime, state *runState, args any) error {
	if err := runtime.Set("args", args); err != nil {
		return err
	}
	if err := runtime.Set("log", func(call goja.FunctionCall) goja.Value {
		msg := ""
		if len(call.Arguments) > 0 {
			msg = call.Argument(0).String()
		}
		if err := state.driver.Progress(workflow.ProgressEvent{
			Kind: workflow.ProgressLog, Message: msg,
		}); err != nil {
			panic(runtime.NewGoError(err))
		}
		return goja.Undefined()
	}); err != nil {
		return err
	}
	if err := runtime.Set("phase", func(call goja.FunctionCall) goja.Value {
		msg := ""
		if len(call.Arguments) > 0 {
			msg = call.Argument(0).String()
		}
		if err := state.driver.Progress(workflow.ProgressEvent{
			Kind: workflow.ProgressPhase, Message: msg,
		}); err != nil {
			panic(runtime.NewGoError(err))
		}
		return goja.Undefined()
	}); err != nil {
		return err
	}
	budget := runtime.NewObject()
	_ = budget.Set("total", nil)
	_ = budget.Set("spent", func(goja.FunctionCall) goja.Value {
		return runtime.ToValue(state.driver.Budget().SpentTokens)
	})
	_ = budget.Set("remaining", func(goja.FunctionCall) goja.Value {
		snapshot := state.driver.Budget()
		if snapshot.RemainingTokens == nil {
			return runtime.ToValue(float64(1 << 62))
		}
		return runtime.ToValue(*snapshot.RemainingTokens)
	})
	if err := runtime.Set("budget", budget); err != nil {
		return err
	}
	if err := runtime.Set("task", func(call goja.FunctionCall) goja.Value {
		result, err := state.spawn(call)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(result.Content)
	}); err != nil {
		return err
	}
	if err := runtime.Set("parallel", func(call goja.FunctionCall) goja.Value {
		items, err := arrayArg(call, 0)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if len(items) > ParallelMaxItems {
			panic(runtime.NewGoError(ErrParallelTooLarge))
		}
		results := make([]any, len(items))
		var wait sync.WaitGroup
		var firstErr error
		var errMu sync.Mutex
		for index := range items {
			wait.Add(1)
			go func(i int) {
				defer wait.Done()
				result, err := state.spawnRequest(workflow.TaskRequest{Prompt: fmt.Sprint(items[i])})
				if err != nil {
					errMu.Lock()
					if firstErr == nil {
						firstErr = err
					}
					errMu.Unlock()
					return
				}
				if result.Success {
					results[i] = result.Content
				} else {
					results[i] = nil
				}
			}(index)
		}
		wait.Wait()
		if firstErr != nil {
			panic(runtime.NewGoError(firstErr))
		}
		return runtime.ToValue(results)
	}); err != nil {
		return err
	}
	if err := runtime.Set("pipeline", func(call goja.FunctionCall) goja.Value {
		items, err := arrayArg(call, 0)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		if len(items) > ParallelMaxItems {
			panic(runtime.NewGoError(ErrParallelTooLarge))
		}
		out := append([]any(nil), items...)
		for range call.Arguments[1:] {
			for i := range out {
				if out[i] == nil {
					continue
				}
				result, err := state.spawnRequest(workflow.TaskRequest{Prompt: fmt.Sprint(out[i])})
				if err != nil {
					panic(runtime.NewGoError(err))
				}
				if result.Success {
					out[i] = result.Content
				} else {
					out[i] = nil
				}
			}
		}
		return runtime.ToValue(out)
	}); err != nil {
		return err
	}
	if err := runtime.Set("sleep", func(call goja.FunctionCall) goja.Value {
		ms := 0
		if len(call.Arguments) > 0 {
			ms = int(call.Argument(0).ToInteger())
		}
		if ms < 0 {
			ms = 0
		}
		if ms > 30_000 {
			ms = 30_000
		}
		timer := time.NewTimer(time.Duration(ms) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-state.ctx.Done():
			panic(runtime.NewGoError(ErrCanceled))
		case <-timer.C:
		}
		return goja.Undefined()
	}); err != nil {
		return err
	}
	if err := runtime.Set("env", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(runtime.NewGoError(errors.New("env key required")))
		}
		key := call.Argument(0).String()
		if !envAllowed(key, state.envAllow) {
			panic(runtime.NewGoError(fmt.Errorf("env key %q not allowlisted", key)))
		}
		return runtime.ToValue(os.Getenv(key))
	}); err != nil {
		return err
	}
	pathObj := runtime.NewObject()
	_ = pathObj.Set("join", func(call goja.FunctionCall) goja.Value {
		parts := make([]string, 0, len(call.Arguments))
		for _, arg := range call.Arguments {
			parts = append(parts, arg.String())
		}
		return runtime.ToValue(filepath.Join(parts...))
	})
	_ = pathObj.Set("normalize", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return runtime.ToValue(".")
		}
		return runtime.ToValue(filepath.Clean(call.Argument(0).String()))
	})
	if err := runtime.Set("path", pathObj); err != nil {
		return err
	}
	return runtime.Set("read", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(runtime.NewGoError(errors.New("read path required")))
		}
		rel := call.Argument(0).String()
		content, err := state.readWorkspace(rel)
		if err != nil {
			panic(runtime.NewGoError(err))
		}
		return runtime.ToValue(content)
	})
}

func envAllowed(key string, allow []string) bool {
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, "=\x00") {
		return false
	}
	upper := strings.ToUpper(key)
	if strings.Contains(upper, "SECRET") || strings.Contains(upper, "TOKEN") ||
		strings.Contains(upper, "PASSWORD") || strings.HasSuffix(upper, "_KEY") ||
		strings.Contains(upper, "CREDENTIAL") {
		return false
	}
	if len(allow) == 0 {
		return strings.HasPrefix(upper, "CODEHELPER_") || upper == "HOME" || upper == "USER" ||
			upper == "LANG" || upper == "TZ"
	}
	for _, item := range allow {
		if item == key {
			return true
		}
	}
	return false
}

func (s *runState) readWorkspace(rel string) (string, error) {
	root := strings.TrimSpace(s.workspace)
	if root == "" {
		return "", errors.New("workspace is required for read()")
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	cleaned := filepath.Clean(rel)
	if filepath.IsAbs(cleaned) {
		return "", errors.New("read path must be relative")
	}
	full := filepath.Join(rootAbs, cleaned)
	fullAbs, err := filepath.Abs(full)
	if err != nil {
		return "", err
	}
	if fullAbs != rootAbs && !strings.HasPrefix(fullAbs, rootAbs+string(os.PathSeparator)) {
		return "", errors.New("read path escapes workspace")
	}
	data, err := os.ReadFile(fullAbs)
	if err != nil {
		return "", err
	}
	if len(data) > 1<<20 {
		return "", errors.New("read exceeds 1MiB cap")
	}
	return string(data), nil
}

func arrayArg(call goja.FunctionCall, index int) ([]any, error) {
	if len(call.Arguments) <= index {
		return nil, errors.New("array argument is required")
	}
	items, ok := call.Argument(index).Export().([]any)
	if !ok {
		return nil, errors.New("argument must be an array")
	}
	return items, nil
}

func (s *runState) spawn(call goja.FunctionCall) (workflow.TaskResult, error) {
	req := workflow.TaskRequest{}
	if len(call.Arguments) > 0 {
		switch value := call.Argument(0).Export().(type) {
		case string:
			req.Prompt = value
		case map[string]any:
			if prompt, ok := value["prompt"].(string); ok {
				req.Prompt = prompt
			}
			if role, ok := value["role"].(string); ok {
				req.Role = role
			}
			if profile, ok := value["profile"].(string); ok {
				req.Profile = profile
			}
			if schema, ok := value["response_schema"]; ok {
				encoded, err := json.Marshal(schema)
				if err != nil {
					return workflow.TaskResult{}, fmt.Errorf(
						"encode task response_schema: %w",
						err,
					)
				}
				req.Schema = encoded
			}
		}
	}
	if req.Prompt == "" {
		return workflow.TaskResult{}, errors.New("task prompt is required")
	}
	return s.spawnRequest(req)
}

func (s *runState) spawnRequest(req workflow.TaskRequest) (workflow.TaskResult, error) {
	if s.canceled.Load() {
		return workflow.TaskResult{}, ErrCanceled
	}
	if err := s.ctx.Err(); err != nil {
		return workflow.TaskResult{}, ErrCanceled
	}
	if s.spawned.Add(1) > LifetimeCap {
		return workflow.TaskResult{}, ErrLifetimeCap
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-s.ctx.Done():
		return workflow.TaskResult{}, ErrCanceled
	}
	return s.driver.SpawnTask(s.ctx, req)
}

func marshalResult(value any) (json.RawMessage, error) {
	if value == nil {
		return json.RawMessage("null"), nil
	}
	return json.Marshal(value)
}

// FakeDriver is a hermetic Driver for tests.
type FakeDriver struct {
	mu      sync.Mutex
	Tasks   []workflow.TaskRequest
	Events  []workflow.ProgressEvent
	Cancels int
	Handler func(workflow.TaskRequest) (workflow.TaskResult, error)
	Spent   uint64
	Block   chan struct{}
}

func (d *FakeDriver) SpawnTask(
	ctx context.Context,
	req workflow.TaskRequest,
) (workflow.TaskResult, error) {
	if d.Block != nil {
		select {
		case <-d.Block:
		case <-ctx.Done():
			return workflow.TaskResult{}, ctx.Err()
		}
	}
	d.mu.Lock()
	d.Tasks = append(d.Tasks, req)
	handler := d.Handler
	d.mu.Unlock()
	if handler != nil {
		return handler(req)
	}
	return workflow.TaskResult{Success: true, Content: "ok:" + req.Prompt}, nil
}

func (d *FakeDriver) CancelAll() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Cancels++
	if d.Block != nil {
		select {
		case <-d.Block:
		default:
			close(d.Block)
		}
	}
	return nil
}

func (d *FakeDriver) Budget() workflow.BudgetSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return workflow.BudgetSnapshot{SpentTokens: d.Spent}
}

func (d *FakeDriver) Progress(event workflow.ProgressEvent) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.Events = append(d.Events, event)
	return nil
}
