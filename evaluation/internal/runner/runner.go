package runner

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type Request struct {
	Root                  string
	RunID                 string
	Variant               string
	Attempt               int
	Seed                  int64
	Suite                 spec.Suite
	Scenario              spec.Scenario
	Source                spec.SourceIdentity
	Artifacts             spec.ArtifactIdentity
	Environment           spec.Environment
	EvidencePath          string
	AvailableCapabilities []string
}

type Runner struct {
	Now func() time.Time
}

func (r Runner) Run(ctx context.Context, request Request) spec.RunRecord {
	now := r.Now
	if now == nil {
		now = time.Now
	}
	started := now().UTC()
	run := spec.RunRecord{
		SchemaVersion: spec.SchemaVersion,
		RunID:         request.RunID,
		SuiteID:       request.Suite.ID,
		ScenarioID:    request.Scenario.ID,
		Variant:       request.Variant,
		Attempt:       request.Attempt,
		Seed:          request.Seed,
		StartedAt:     started,
		Source:        request.Source,
		Artifacts:     request.Artifacts,
		Environment:   request.Environment,
		Execution: spec.ExecutionResult{
			StdoutDigest: spec.DigestString(""),
			StderrDigest: spec.DigestString(""),
		},
	}
	partition, partitionErr := spec.BuildRunPartition(
		request.Source,
		request.Artifacts,
		request.Seed,
		request.Attempt,
	)
	if partitionErr != nil {
		run.Status = spec.StatusInvalid
		run.Execution.ReasonCode = "run_partition_invalid"
		run.OracleResults = commandOracle(
			request.Scenario,
			spec.StatusInvalid,
			partitionErr.Error(),
			nil,
		)
		return finish(run, now)
	}
	run.RunPartition = partition
	if run.Variant == "" {
		run.Variant = "default"
	}
	switch request.Scenario.Driver {
	case spec.DriverCommand:
	default:
		run.Status = spec.StatusInvalid
		run.Execution.ReasonCode = "driver_unavailable"
		run.OracleResults = commandOracle(
			request.Scenario,
			spec.StatusInvalid,
			"typed Scenario Driver is not installed",
			nil,
		)
		return finish(run, now)
	}
	run.Execution.Command = append(
		[]string(nil),
		request.Scenario.Execution.Command...,
	)
	run.Execution.Directory = request.Scenario.Execution.WorkingDirectory
	if len(run.Execution.Command) == 0 {
		run.Status = spec.StatusInvalid
		run.Execution.ReasonCode = "command_missing"
		run.OracleResults = commandOracle(
			request.Scenario,
			spec.StatusInvalid,
			"scenario execution command is missing",
			nil,
		)
		return finish(run, now)
	}

	effective := spec.Effective(request.Suite, request.Scenario)
	unavailable := prerequisites(
		effective.Requirements,
		request.AvailableCapabilities,
	)
	if len(unavailable) != 0 {
		run.Status = spec.StatusUnavailable
		run.Execution.ReasonCode = "prerequisite_unavailable"
		run.OracleResults = commandOracle(
			request.Scenario,
			spec.StatusUnavailable,
			strings.Join(unavailable, "; "),
			nil,
		)
		return finish(run, now)
	}
	directory, err := resolveDirectory(
		request.Root,
		request.Scenario.Execution.WorkingDirectory,
	)
	if err != nil {
		run.Status = spec.StatusInvalid
		run.Execution.ReasonCode = "working_directory_invalid"
		run.OracleResults = commandOracle(
			request.Scenario,
			spec.StatusInvalid,
			err.Error(),
			nil,
		)
		return finish(run, now)
	}
	run.Execution.Directory = directory

	if request.EvidencePath != "" {
		if _, statErr := os.Stat(request.EvidencePath); statErr == nil {
			run.Status = spec.StatusInvalid
			run.Execution.ReasonCode = "evidence_preexisting"
			run.OracleResults = commandOracle(
				request.Scenario,
				spec.StatusInvalid,
				"evidence path existed before attempt",
				nil,
			)
			return finish(run, now)
		} else if !errors.Is(statErr, os.ErrNotExist) {
			run.Status = spec.StatusInvalid
			run.Execution.ReasonCode = "evidence_path_invalid"
			run.OracleResults = commandOracle(
				request.Scenario,
				spec.StatusInvalid,
				"evidence path cannot be inspected",
				nil,
			)
			return finish(run, now)
		}
		if mkdirErr := os.MkdirAll(filepath.Dir(request.EvidencePath), 0o700); mkdirErr != nil {
			run.Status = spec.StatusInvalid
			run.Execution.ReasonCode = "evidence_directory_invalid"
			run.OracleResults = commandOracle(
				request.Scenario,
				spec.StatusInvalid,
				"evidence directory cannot be created",
				nil,
			)
			return finish(run, now)
		}
	}

	timeout := time.Duration(effective.Budgets.WallTimeMS) * time.Millisecond
	executionContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	command := exec.Command(
		request.Scenario.Execution.Command[0],
		request.Scenario.Execution.Command[1:]...,
	)
	command.Dir = directory
	command.Env = append(
		os.Environ(),
		"CODEHELPER_EVALUATION_EVIDENCE_PATH="+request.EvidencePath,
		"CODEHELPER_EVALUATION_RUN_PARTITION="+run.RunPartition,
		"CODEHELPER_EVALUATION_RUN_ID="+run.RunID,
		"CODEHELPER_EVALUATION_SCENARIO_ID="+run.ScenarioID,
		fmt.Sprintf("CODEHELPER_EVALUATION_ATTEMPT=%d", run.Attempt),
	)
	limit := &sharedOutputLimit{
		remaining: effective.Budgets.MaxOutputBytes,
	}
	stdout := &boundedBuffer{limit: limit}
	stderr := &boundedBuffer{limit: limit}
	command.Stdout = stdout
	command.Stderr = stderr

	err = runProcess(executionContext, command)
	run.Execution.StdoutBytes = stdout.BytesSeen()
	run.Execution.StderrBytes = stderr.BytesSeen()
	run.Execution.StdoutDigest = stdout.Digest()
	run.Execution.StderrDigest = stderr.Digest()
	run.Execution.Truncated = limit.Truncated()
	switch {
	case errors.Is(executionContext.Err(), context.DeadlineExceeded):
		run.Status = spec.StatusFailed
		run.Execution.TimedOut = true
		run.Execution.ReasonCode = "command_timeout"
	case err == nil:
		exitCode := 0
		run.Execution.ExitCode = &exitCode
		run.Status = spec.StatusPassed
		run.Execution.ReasonCode = "command_passed"
	default:
		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			exitCode := exitError.ExitCode()
			run.Execution.ExitCode = &exitCode
			run.Status = spec.StatusFailed
			if exitCode < 0 {
				run.Execution.Signal = exitError.ProcessState.String()
				run.Execution.ReasonCode = "command_signaled"
			} else {
				run.Execution.ReasonCode = "command_failed"
			}
		} else if errors.Is(err, exec.ErrNotFound) ||
			errors.Is(err, os.ErrNotExist) {
			run.Status = spec.StatusUnavailable
			run.Execution.ReasonCode = "command_unavailable"
		} else {
			run.Status = spec.StatusInvalid
			run.Execution.ReasonCode = "command_start_invalid"
		}
	}
	commandEvidence := spec.EvidenceRecord{
		SchemaVersion: spec.SchemaVersion,
		RunPartition:  run.RunPartition,
		RunID:         run.RunID,
		ScenarioID:    run.ScenarioID,
		Attempt:       run.Attempt,
		Producer:      "runner",
		Kind:          "command_result",
		Digest: spec.DigestString(strings.Join([]string{
			run.Execution.ReasonCode,
			run.Execution.StdoutDigest,
			run.Execution.StderrDigest,
		}, "\x00")),
	}
	run.Evidence = append(run.Evidence, commandEvidence)
	run.OracleResults = commandOracle(
		request.Scenario,
		run.Status,
		executionSummary(run.Execution),
		[]string{"command_result"},
	)

	evidence, evidenceErr := ReadEvidence(request.EvidencePath)
	run.Evidence = append(run.Evidence, evidence...)
	if evidenceErr != nil {
		if !errors.Is(evidenceErr, os.ErrNotExist) {
			run.Status = spec.StatusInvalid
			run.OracleResults = append(run.OracleResults, spec.OracleResult{
				ID:       "evidence_integrity",
				Status:   spec.StatusInvalid,
				Severity: request.Scenario.Risk,
				Summary:  evidenceErr.Error(),
				Evidence: []string{},
			})
		}
	}
	if missing := missingEvidence(request.Scenario.RequiredEvidence, run.Evidence); len(missing) != 0 {
		run.Status = spec.StatusInvalid
		run.OracleResults = append(run.OracleResults, spec.OracleResult{
			ID:       "evidence_integrity",
			Status:   spec.StatusInvalid,
			Severity: request.Scenario.Risk,
			Summary:  "required evidence is missing",
			Evidence: []string{},
		})
	}
	return finish(run, now)
}

func prerequisites(
	requirements spec.Requirements,
	availableCapabilities []string,
) []string {
	var unavailable []string
	if !spec.CurrentPlatformAllowed(requirements) {
		unavailable = append(
			unavailable,
			"current platform is unsupported",
		)
	}
	for _, name := range requirements.Commands {
		if _, err := exec.LookPath(name); err != nil {
			unavailable = append(
				unavailable,
				fmt.Sprintf("required command %s is unavailable", name),
			)
		}
	}
	for _, capability := range requirements.Capabilities {
		if !slices.Contains(availableCapabilities, capability) {
			unavailable = append(
				unavailable,
				fmt.Sprintf("required capability %s is unavailable", capability),
			)
		}
	}
	return unavailable
}

func commandOracle(
	scenario spec.Scenario,
	status spec.Status,
	summary string,
	evidence []string,
) []spec.OracleResult {
	return []spec.OracleResult{{
		ID:       "command_verification",
		Status:   status,
		Severity: scenario.Risk,
		Summary:  summary,
		Evidence: append([]string(nil), evidence...),
	}}
}

func finish(run spec.RunRecord, now func() time.Time) spec.RunRecord {
	run.EndedAt = now().UTC()
	if run.EndedAt.Before(run.StartedAt) {
		run.EndedAt = run.StartedAt
	}
	run.DurationMS = run.EndedAt.Sub(run.StartedAt).Milliseconds()
	return run
}

func executionSummary(result spec.ExecutionResult) string {
	switch result.ReasonCode {
	case "command_passed":
		return "scenario command completed successfully"
	case "command_timeout":
		return "scenario command exceeded its wall-time budget"
	case "command_signaled":
		return "scenario command terminated by signal"
	case "command_failed":
		if result.ExitCode != nil {
			return fmt.Sprintf(
				"scenario command exited with status %d",
				*result.ExitCode,
			)
		}
	}
	return result.ReasonCode
}

func resolveDirectory(root, relative string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(relative) {
		return "", errors.New("scenario working directory must be relative")
	}
	directory := filepath.Join(root, filepath.FromSlash(relative))
	back, err := filepath.Rel(root, directory)
	if err != nil || back == ".." ||
		strings.HasPrefix(filepath.ToSlash(back), "../") {
		return "", errors.New("scenario working directory escapes repository")
	}
	info, err := os.Stat(directory)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", errors.New("scenario working directory is not a directory")
	}
	return directory, nil
}

func ReadEvidence(path string) ([]spec.EvidenceRecord, error) {
	if strings.TrimSpace(path) == "" {
		return nil, os.ErrNotExist
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var records []spec.EvidenceRecord
	line := 0
	for scanner.Scan() {
		line++
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var record spec.EvidenceRecord
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return records, fmt.Errorf(
				"evidence line %d is incomplete or invalid: %w",
				line,
				err,
			)
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			return records, fmt.Errorf(
				"evidence line %d contains multiple JSON values",
				line,
			)
		}
		records = append(records, record)
	}
	if err := scanner.Err(); err != nil {
		return records, fmt.Errorf("read evidence: %w", err)
	}
	if len(records) == 0 {
		return nil, errors.New("evidence file is empty")
	}
	return records, nil
}

func missingEvidence(required []string, records []spec.EvidenceRecord) []string {
	available := make(map[string]bool, len(records))
	for _, record := range records {
		available[record.Kind] = true
	}
	var missing []string
	for _, kind := range required {
		if !available[kind] {
			missing = append(missing, kind)
		}
	}
	return missing
}

type sharedOutputLimit struct {
	mu        sync.Mutex
	remaining int64
	truncated bool
}

func (l *sharedOutputLimit) write(buffer *bytes.Buffer, content []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.remaining <= 0 {
		l.truncated = true
		return
	}
	count := int64(len(content))
	if count > l.remaining {
		count = l.remaining
		l.truncated = true
	}
	_, _ = buffer.Write(content[:count])
	l.remaining -= count
}

func (l *sharedOutputLimit) Truncated() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.truncated
}

type boundedBuffer struct {
	buffer    bytes.Buffer
	limit     *sharedOutputLimit
	digest    hashState
	bytesSeen int64
}

func (b *boundedBuffer) Write(content []byte) (int, error) {
	b.digest.Write(content)
	b.bytesSeen += int64(len(content))
	b.limit.write(&b.buffer, content)
	return len(content), nil
}

func (b *boundedBuffer) BytesSeen() int64 {
	return b.bytesSeen
}

func (b *boundedBuffer) Digest() string {
	return b.digest.Sum()
}

type hashState struct {
	digest hash.Hash
}

func (h *hashState) Write(content []byte) {
	if h.digest == nil {
		h.digest = sha256.New()
	}
	_, _ = h.digest.Write(content)
}

func (h *hashState) Sum() string {
	if h.digest == nil {
		return spec.DigestString("")
	}
	return "sha256:" + hex.EncodeToString(h.digest.Sum(nil))
}
