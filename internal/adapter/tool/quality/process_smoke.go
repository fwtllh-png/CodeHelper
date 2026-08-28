package quality

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type processSmokeInput struct {
	Path             string   `json:"path"`
	Args             []string `json:"args"`
	CoveredPaths     []string `json:"covered_paths"`
	MinimumRuntimeMS uint64   `json:"minimum_runtime_ms"`
}

type processSmokeTool struct {
	root     string
	resolver *sandbox.TrustedHostPathResolver
	run      func(context.Context, process.Options) (process.Result, error)
}

type processSmokeOutcome struct {
	result process.Result
	err    error
}

type processSmokeRuntime interface {
	tool.OutcomeExecutor
	tool.DispositionProvider
}

type processSmokeExecutor struct {
	processSmokeRuntime
	smoke *processSmokeTool
}

func (e *processSmokeExecutor) ExpandArguments(
	_ context.Context,
	raw json.RawMessage,
) (json.RawMessage, error) {
	var input processSmokeInput
	if err := json.Unmarshal(raw, &input); err != nil {
		return nil, err
	}
	path, err := e.smoke.resolver.Resolve(input.Path, sandbox.AllowMissing)
	if err != nil {
		return nil, err
	}
	input.Path = path
	return json.Marshal(input)
}

func registerProcessSmoke(
	registry *tool.Registry,
	root string,
	privateHome string,
) error {
	resolver, err := sandbox.NewTrustedHostPathResolver(root, privateHome)
	if err != nil {
		return err
	}
	executor, err := (&processSmokeTool{
		root: root, resolver: resolver,
	}).typedExecutor()
	if err != nil {
		return err
	}
	return registry.Register(executor, nil)
}

func (t *processSmokeTool) Descriptor() tool.Descriptor {
	return tool.Descriptor{
		Name: "quality_process_smoke",
		Description: "Launch an exact executable from the Workspace or its private " +
			"home outside the OS sandbox, verify that it remains alive for the " +
			"caller-declared interval, then terminate and reap it. This is for " +
			"desktop or host-integration smoke tests and always requires governed " +
			"host-process authorization",
		Visibility: tool.VisibleModel,
		Capability: tool.CapabilityProcess,
		AccessMode: tool.AccessRead,
		ResourceResolver: tool.ResourceResolver{
			Templates: []tool.ResourceTemplate{
				{
					Kind: "process", ID: "host",
					Access: tool.AccessWrite,
				},
			},
			ReadPathsField:       "covered_paths",
			TrustedHostPathField: "path",
		},
		ParallelPolicy:     tool.ParallelSerial,
		RepeatPolicy:       tool.RepeatExecute,
		SandboxRequirement: tool.SandboxNone,
		Availability:       tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{
					"type": "string", "minLength": 1,
					"description": "Executable path under the Workspace or its private home",
				},
				"args": map[string]any{
					"type": "array", "maxItems": 128,
					"items": map[string]any{"type": "string"},
				},
				"covered_paths": map[string]any{
					"type": "array", "minItems": 1, "maxItems": 128,
					"items": map[string]any{"type": "string", "minLength": 1},
				},
				"minimum_runtime_ms": map[string]any{
					"type": "integer", "minimum": 1,
					"description": "Minimum time the process must remain alive",
				},
			},
			"required":             []string{"path", "covered_paths", "minimum_runtime_ms"},
			"additionalProperties": false,
		},
	}
}

func (t *processSmokeTool) typedExecutor() (tool.Executor, error) {
	executor, err := typed.Define(typed.Spec[processSmokeInput, tool.Result]{
		Descriptor:  t.Descriptor(),
		Disposition: tool.DispositionWaitForTeardown,
		Run:         t.runTyped,
		Encode: func(value tool.Result) (tool.Result, error) {
			return value, nil
		},
		Outcome: verificationOutcome,
	})
	if err != nil {
		return nil, err
	}
	runtime, ok := executor.(processSmokeRuntime)
	if !ok {
		return nil, errors.New("process smoke typed runtime is incomplete")
	}
	return &processSmokeExecutor{processSmokeRuntime: runtime, smoke: t}, nil
}

func (t *processSmokeTool) runTyped(
	ctx context.Context,
	input processSmokeInput,
) (tool.Result, error) {
	if input.MinimumRuntimeMS == 0 {
		return tool.Result{}, errors.New("minimum_runtime_ms must be positive")
	}
	minimumRuntime := time.Duration(input.MinimumRuntimeMS) * time.Millisecond
	if minimumRuntime <= 0 ||
		uint64(minimumRuntime/time.Millisecond) != input.MinimumRuntimeMS {
		return tool.Result{}, errors.New("minimum_runtime_ms is out of range")
	}
	coveredPaths, err := (&Tool{root: t.root}).
		canonicalCoveredPaths(input.CoveredPaths)
	if err != nil {
		return tool.Result{}, err
	}
	executable, err := t.resolver.Resolve(input.Path, sandbox.MustExist)
	if err != nil {
		return t.failedProcessSmokeResult(
			input.Path, input.Args, coveredPaths, -1, err.Error(),
		)
	}
	info, err := os.Lstat(executable)
	if err != nil {
		return t.failedProcessSmokeResult(
			input.Path, input.Args, coveredPaths, -1, err.Error(),
		)
	}
	if !info.Mode().IsRegular() {
		return t.failedProcessSmokeResult(
			input.Path,
			input.Args,
			coveredPaths,
			-1,
			"process smoke path must be a regular file",
		)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
		return t.failedProcessSmokeResult(
			input.Path,
			input.Args,
			coveredPaths,
			-1,
			"process smoke path is not executable",
		)
	}

	runContext, cancel := context.WithCancel(ctx)
	defer cancel()
	finished := make(chan processSmokeOutcome, 1)
	go func() {
		run := t.run
		if run == nil {
			run = process.Run
		}
		result, runErr := run(runContext, process.Options{
			Path: executable,
			Args: append([]string(nil), input.Args...),
			Dir:  t.root,
		})
		finished <- processSmokeOutcome{result: result, err: runErr}
	}()

	timer := time.NewTimer(minimumRuntime)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		cancel()
		<-finished
		return tool.Result{}, ctx.Err()
	case completed := <-finished:
		return t.processSmokeResult(
			input.Path,
			input.Args,
			coveredPaths,
			completed,
			false,
		)
	case <-timer.C:
		select {
		case completed := <-finished:
			return t.processSmokeResult(
				input.Path,
				input.Args,
				coveredPaths,
				completed,
				false,
			)
		default:
		}
		cancel()
		completed := <-finished
		return t.processSmokeResult(
			input.Path,
			input.Args,
			coveredPaths,
			completed,
			true,
		)
	}
}

func (t *processSmokeTool) processSmokeResult(
	path string,
	args []string,
	coveredPaths []string,
	completed processSmokeOutcome,
	survived bool,
) (tool.Result, error) {
	if completed.err != nil && !errors.Is(completed.err, context.Canceled) {
		exitCode := completed.result.ExitCode
		if exitCode == 0 {
			exitCode = -1
		}
		return t.failedProcessSmokeResult(
			path,
			args,
			coveredPaths,
			exitCode,
			"process smoke could not start: "+completed.err.Error(),
		)
	}
	status := verify.StatusFailed
	message := fmt.Sprintf(
		"%s exited before the declared minimum runtime",
		filepath.ToSlash(path),
	)
	if survived {
		status = verify.StatusPassed
		message = ""
	}
	return t.encodeProcessSmokeResult(
		path,
		args,
		coveredPaths,
		status,
		completed.result,
		message,
	)
}

func (t *processSmokeTool) failedProcessSmokeResult(
	path string,
	args []string,
	coveredPaths []string,
	exitCode int,
	message string,
) (tool.Result, error) {
	return t.encodeProcessSmokeResult(
		path,
		args,
		coveredPaths,
		verify.StatusFailed,
		process.Result{ExitCode: exitCode},
		message,
	)
}

func (t *processSmokeTool) encodeProcessSmokeResult(
	path string,
	args []string,
	coveredPaths []string,
	status string,
	processResult process.Result,
	message string,
) (tool.Result, error) {
	passed := status == verify.StatusPassed
	payload := map[string]any{
		"schema_version": 1,
		"kind":           "process_smoke",
		"status":         status,
		"exit_code":      processResult.ExitCode,
		"stdout":         processResult.Stdout,
		"stderr":         processResult.Stderr,
		"summary": map[string]any{
			"passed": passed,
			"path":   filepath.ToSlash(path),
		},
	}
	if message != "" {
		payload["message"] = message
	}
	commandIdentity, err := json.Marshal(struct {
		Path string   `json:"path"`
		Args []string `json:"args,omitempty"`
	}{
		Path: filepath.ToSlash(path),
		Args: append([]string(nil), args...),
	})
	if err != nil {
		return tool.Result{}, err
	}
	commandDigest := sha256.Sum256(commandIdentity)
	encoded, err := json.Marshal(payload)
	if err != nil {
		return tool.Result{}, err
	}
	result := tool.Result{
		Content: string(encoded),
		IsError: !passed,
		Metadata: map[string]any{
			verify.EvidenceMetadataKey: verify.Evidence{
				SchemaVersion: 1,
				Kind:          "process_smoke",
				Status:        status,
				ExitCode:      processResult.ExitCode,
				CommandDigest: fmt.Sprintf("sha256:%x", commandDigest),
				CoveredPaths:  append([]string(nil), coveredPaths...),
			},
		},
	}
	return result, nil
}
