package quality

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/typed"
	"github.com/fwtllh-png/CodeHelper/internal/observability/verify"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/artifactbroker"
	"github.com/fwtllh-png/CodeHelper/internal/security/authority"
	"github.com/fwtllh-png/CodeHelper/internal/security/processbroker"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

type processSmokeInput struct {
	Path             string   `json:"path"`
	Args             []string `json:"args"`
	CoveredPaths     []string `json:"covered_paths"`
	MinimumRuntimeMS uint64   `json:"minimum_runtime_ms"`
}

const ProcessSmokeUnavailableReason = "host process smoke is disabled until immutable artifact and desktop broker enforcement is available"

type processSmokeTool struct {
	root      string
	resolver  *sandbox.TrustedHostPathResolver
	artifacts *artifactbroker.Broker
	processes *processbroker.Broker
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
	runtime RuntimeDependencies,
) error {
	resolver, err := sandbox.NewTrustedHostPathResolver(root, privateHome)
	if err != nil {
		return err
	}
	executor, err := (&processSmokeTool{
		root: root, resolver: resolver,
		artifacts: runtime.ArtifactBroker,
		processes: runtime.ProcessBroker,
	}).typedExecutor()
	if err != nil {
		return err
	}
	return registry.Register(executor, nil)
}

func (t *processSmokeTool) Descriptor() tool.Descriptor {
	availability := tool.AvailabilityUnavailable
	unavailableReason := ProcessSmokeUnavailableReason
	if t.artifacts != nil && t.processes != nil {
		availability = tool.AvailabilityAvailable
		unavailableReason = ""
	}
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
		Availability:       availability,
		UnavailableReason:  unavailableReason,
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
	_ context.Context,
	input processSmokeInput,
) (tool.Result, error) {
	return tool.Result{}, errors.New(
		"process smoke requires an authorized Process Broker grant",
	)
}

func (e *processSmokeExecutor) PrepareAuthorizedProcess(
	_ context.Context,
	invocation tool.PreparedInvocation,
	producerOperationDigest string,
) (authority.ArtifactBinding, error) {
	if e.smoke.artifacts == nil {
		return authority.ArtifactBinding{}, errors.New("artifact broker is unavailable")
	}
	var input processSmokeInput
	if err := json.Unmarshal(invocation.Arguments, &input); err != nil {
		return authority.ArtifactBinding{}, err
	}
	snapshot, err := e.smoke.artifacts.Prepare(artifactbroker.PrepareRequest{
		SourcePath:              input.Path,
		ProducerOperationDigest: producerOperationDigest,
	})
	if err != nil {
		return authority.ArtifactBinding{}, err
	}
	return authority.ArtifactBinding{
		ManifestDigest: snapshot.Manifest.Digest,
		Generation:     snapshot.Manifest.Generation,
		Value:          snapshot,
	}, nil
}

func (e *processSmokeExecutor) ReleaseAuthorizedProcess(
	_ context.Context,
	binding authority.ArtifactBinding,
) error {
	snapshot, ok := binding.Value.(artifactbroker.Snapshot)
	if !ok {
		return errors.New("process Artifact is invalid")
	}
	return e.smoke.artifacts.Release(snapshot)
}

func (e *processSmokeExecutor) ExecuteAuthorizedProcess(
	ctx context.Context,
	invocation tool.PreparedInvocation,
	grant authority.AuthorizedProcessGrant,
) (tool.Result, tool.Outcome, error) {
	snapshot, ok := grant.Artifact.(artifactbroker.Snapshot)
	if !ok {
		return tool.Result{}, tool.Outcome{}, errors.New("process Artifact is invalid")
	}
	var input processSmokeInput
	if err := json.Unmarshal(invocation.Arguments, &input); err != nil {
		return tool.Result{}, tool.Outcome{}, err
	}
	if input.MinimumRuntimeMS == 0 {
		return tool.Result{}, tool.Outcome{}, errors.New("minimum_runtime_ms must be positive")
	}
	minimumRuntime := time.Duration(input.MinimumRuntimeMS) * time.Millisecond
	if minimumRuntime <= 0 ||
		uint64(minimumRuntime/time.Millisecond) != input.MinimumRuntimeMS {
		return tool.Result{}, tool.Outcome{}, errors.New("minimum_runtime_ms is out of range")
	}
	coveredPaths, err := (&Tool{root: e.smoke.root}).
		canonicalCoveredPaths(input.CoveredPaths)
	if err != nil {
		return tool.Result{}, tool.Outcome{}, err
	}
	identity := tool.InvocationIdentityFrom(ctx)
	if identity.SessionID == "" {
		identity.SessionID = grant.Operation.WorkspaceID
	}
	if identity.ThreadID == "" {
		identity.ThreadID = grant.Operation.ID
	}
	if identity.TurnID == "" {
		identity.TurnID = grant.Operation.ID
	}
	brokerResult, err := e.smoke.processes.RunSmoke(
		ctx,
		processbroker.Request{
			Lease: grant.Lease, Validation: grant.Validation, Artifact: snapshot,
			Args: input.Args, Dir: e.smoke.root,
			Identity: processbroker.Identity{
				SessionID: identity.SessionID,
				ThreadID:  identity.ThreadID,
				TurnID:    identity.TurnID,
			},
			MinimumRuntime: minimumRuntime,
		},
	)
	if err != nil {
		failed, encodeErr := e.smoke.encodeProcessSmokeResult(
			input.Path,
			input.Args,
			coveredPaths,
			verify.StatusFailed,
			process.Result{ExitCode: -1},
			"process smoke runner failed: "+err.Error(),
		)
		return failed, tool.OutcomeFromResult(failed), errors.Join(err, encodeErr)
	}
	result, encodeErr := e.smoke.encodeProcessSmokeResult(
		input.Path,
		input.Args,
		coveredPaths,
		map[bool]string{true: verify.StatusPassed, false: verify.StatusFailed}[brokerResult.Survived],
		brokerResult.Process,
		map[bool]string{
			true: "",
			false: fmt.Sprintf(
				"%s exited before the declared minimum runtime",
				filepath.ToSlash(input.Path),
			),
		}[brokerResult.Survived],
	)
	return result, tool.OutcomeFromResult(result), encodeErr
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
