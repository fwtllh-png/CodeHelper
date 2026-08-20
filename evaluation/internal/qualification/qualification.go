package qualification

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/runner"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

const SchemaVersion = 2

type Task struct {
	ID            string
	DependsOn     []string
	Timeout       time.Duration
	Command       []string
	Directory     string
	Env           []string
	Check         func(context.Context) (string, error)
	CleanupReport string
}

type TaskResult struct {
	ID                    string      `json:"id"`
	Status                spec.Status `json:"status"`
	EvidenceDigest        string      `json:"evidence_digest"`
	DependsOn             []string    `json:"depends_on"`
	DurationMS            int64       `json:"duration_ms"`
	ReasonCode            string      `json:"reason_code"`
	CleanupRequired       bool        `json:"cleanup_required"`
	CleanupEvidenceDigest string      `json:"cleanup_evidence_digest"`
	OwnedResources        int         `json:"owned_resources"`
	CleanupOutstanding    int         `json:"cleanup_outstanding"`
}

type Report struct {
	SchemaVersion    int          `json:"schema_version"`
	ID               string       `json:"id"`
	Kind             string       `json:"kind"`
	FoundationDigest string       `json:"foundation_digest"`
	SourceDigest     string       `json:"source_digest"`
	RuntimeDigest    string       `json:"runtime_digest"`
	VSIXDigest       string       `json:"vsix_digest"`
	LockIdentity     string       `json:"lock_identity"`
	Status           spec.Status  `json:"status"`
	StartedAt        time.Time    `json:"started_at"`
	EndedAt          time.Time    `json:"ended_at"`
	Scheduled        int          `json:"scheduled"`
	Settled          int          `json:"settled"`
	Passed           int          `json:"passed"`
	Failed           int          `json:"failed"`
	Unavailable      int          `json:"unavailable"`
	NotEvaluated     int          `json:"not_evaluated"`
	Invalid          int          `json:"invalid"`
	Results          []TaskResult `json:"results"`
	EvidenceDigest   string       `json:"evidence_digest"`
}

type Request struct {
	ID               string
	Kind             string
	Root             string
	FoundationDigest string
	SourceDigest     string
	RuntimeDigest    string
	VSIXDigest       string
	LockIdentity     string
	Tasks            []Task
	Now              func() time.Time
}

func Run(ctx context.Context, request Request) (Report, error) {
	if err := validateRequest(request); err != nil {
		return Report{}, err
	}
	now := request.Now
	if now == nil {
		now = time.Now
	}
	report := Report{
		SchemaVersion: SchemaVersion,
		ID:            request.ID, Kind: request.Kind,
		FoundationDigest: request.FoundationDigest,
		SourceDigest:     request.SourceDigest,
		RuntimeDigest:    request.RuntimeDigest,
		VSIXDigest:       request.VSIXDigest,
		LockIdentity:     request.LockIdentity,
		Status:           spec.StatusPassed,
		StartedAt:        now().UTC(),
		Scheduled:        len(request.Tasks),
	}
	statuses := make(map[string]spec.Status, len(request.Tasks))
	for _, task := range request.Tasks {
		result := runTask(ctx, request.ID, request.Root, task, statuses, now)
		report.Results = append(report.Results, result)
		statuses[task.ID] = result.Status
		report.Settled++
		switch result.Status {
		case spec.StatusPassed:
			report.Passed++
		case spec.StatusFailed:
			report.Failed++
		case spec.StatusUnavailable:
			report.Unavailable++
		case spec.StatusNotEvaluated:
			report.NotEvaluated++
		case spec.StatusInvalid:
			report.Invalid++
		}
		report.Status = mergeStatus(report.Status, result.Status)
	}
	report.EndedAt = now().UTC()
	if report.EndedAt.Before(report.StartedAt) {
		report.EndedAt = report.StartedAt
	}
	if report.Settled != report.Scheduled {
		report.Status = spec.StatusInvalid
		report.Invalid++
	}
	report.EvidenceDigest = digestReport(report)
	return report, report.Validate()
}

func (r Report) Validate() error {
	for name, value := range map[string]string{
		"foundation_digest": r.FoundationDigest,
		"source_digest":     r.SourceDigest,
		"runtime_digest":    r.RuntimeDigest,
		"vsix_digest":       r.VSIXDigest,
		"lock_identity":     r.LockIdentity,
		"evidence_digest":   r.EvidenceDigest,
	} {
		if !digestValid(value) {
			return fmt.Errorf("Qualification %s is invalid", name)
		}
	}
	if r.SchemaVersion != SchemaVersion || !validID(r.ID) ||
		(r.Kind != "foundation_epoch" &&
			r.Kind != "integration" &&
			r.Kind != "discovery" &&
			r.Kind != "chaos" &&
			r.Kind != "live" &&
			r.Kind != "endurance" &&
			r.Kind != "canary") ||
		!r.Status.Valid() ||
		r.StartedAt.IsZero() || r.EndedAt.Before(r.StartedAt) ||
		r.Scheduled < 1 || r.Settled != r.Scheduled ||
		len(r.Results) != r.Scheduled ||
		r.Passed+r.Failed+r.Unavailable+r.NotEvaluated+r.Invalid != r.Settled {
		return errors.New("Qualification report inventory is invalid")
	}
	if r.EvidenceDigest != digestReport(r) {
		return errors.New("Qualification evidence digest does not match report")
	}
	seen := make(map[string]struct{}, len(r.Results))
	for _, result := range r.Results {
		if !validID(result.ID) || !result.Status.Valid() ||
			!digestValid(result.EvidenceDigest) ||
			!digestValid(result.CleanupEvidenceDigest) ||
			result.DurationMS < 0 || !validID(result.ReasonCode) {
			return fmt.Errorf("Qualification result %q is invalid", result.ID)
		}
		if result.OwnedResources < 0 || result.CleanupOutstanding < 0 ||
			result.CleanupOutstanding > result.OwnedResources ||
			!result.CleanupRequired &&
				(result.OwnedResources != 0 || result.CleanupOutstanding != 0) ||
			result.CleanupRequired && result.CleanupOutstanding > 0 &&
				result.Status != spec.StatusInvalid {
			return fmt.Errorf(
				"Qualification result %q cleanup inventory is invalid",
				result.ID,
			)
		}
		if _, exists := seen[result.ID]; exists {
			return fmt.Errorf("duplicate Qualification result %q", result.ID)
		}
		seen[result.ID] = struct{}{}
	}
	return nil
}

func Write(directory string, report Report) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	path := filepath.Join(directory, "qualification.json")
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("Qualification report %q already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".qualification-*.tmp")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		temporary.Close()
		return err
	}
	if _, err := bytes.NewReader(raw).WriteTo(temporary); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Sync(); err != nil {
		temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryPath, path)
}

func runTask(
	ctx context.Context,
	qualificationID string,
	root string,
	task Task,
	statuses map[string]spec.Status,
	now func() time.Time,
) (result TaskResult) {
	cleanupEvidenceDigest := spec.DigestString(
		task.ID + "\x00cleanup_not_required",
	)
	if task.CleanupReport != "" {
		cleanupEvidenceDigest = spec.DigestString(
			task.ID + "\x00cleanup_missing",
		)
	}
	result = TaskResult{
		ID:                    task.ID,
		Status:                spec.StatusPassed,
		DependsOn:             append([]string{}, task.DependsOn...),
		ReasonCode:            "passed",
		CleanupRequired:       task.CleanupReport != "",
		CleanupEvidenceDigest: cleanupEvidenceDigest,
	}
	started := now()
	defer func() {
		ended := now()
		if ended.Before(started) {
			ended = started
		}
		result.DurationMS = ended.Sub(started).Milliseconds()
		if result.EvidenceDigest == "" {
			result.EvidenceDigest = spec.DigestString(
				result.ID + "\x00" + string(result.Status) + "\x00" + result.ReasonCode,
			)
		}
	}()
	for _, dependency := range task.DependsOn {
		if statuses[dependency] != spec.StatusPassed {
			result.Status = spec.StatusNotEvaluated
			result.ReasonCode = "dependency_blocked"
			return result
		}
	}
	if ctx.Err() != nil {
		result.Status = spec.StatusInvalid
		result.ReasonCode = "epoch_canceled"
		return result
	}
	timeout := task.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	taskContext, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	if task.CleanupReport != "" {
		if _, err := os.Lstat(task.CleanupReport); err == nil {
			result.Status = spec.StatusInvalid
			result.ReasonCode = "cleanup_evidence_stale"
			return result
		} else if !errors.Is(err, os.ErrNotExist) {
			result.Status = spec.StatusInvalid
			result.ReasonCode = "cleanup_evidence_invalid"
			return result
		}
	}
	switch {
	case task.Check != nil:
		digest, err := task.Check(taskContext)
		if err != nil {
			result.Status = spec.StatusFailed
			result.ReasonCode = "check_failed"
			result.EvidenceDigest = spec.DigestString(
				task.ID + "\x00" + err.Error(),
			)
			return result
		}
		if !digestValid(digest) {
			result.Status = spec.StatusInvalid
			result.ReasonCode = "evidence_invalid"
			return result
		}
		result.EvidenceDigest = digest
	case len(task.Command) != 0:
		directory := task.Directory
		if directory == "" {
			directory = root
		}
		commandResult, err := runner.RunOwnedCommand(
			taskContext,
			directory,
			task.Command,
			task.Env,
			2<<20,
		)
		if err != nil {
			result.Status = spec.StatusFailed
			result.ReasonCode = "command_failed"
			if commandResult.TimedOut {
				result.Status = spec.StatusInvalid
				result.ReasonCode = "command_timeout"
			}
		}
		if task.CleanupReport != "" {
			cleanup, digest, cleanupErr := readCleanupReport(
				task.CleanupReport,
				qualificationID,
				task.ID,
			)
			result.CleanupEvidenceDigest = digest
			if cleanupErr != nil {
				result.Status = spec.StatusInvalid
				if commandResult.TimedOut {
					result.ReasonCode = "command_timeout_cleanup_unproven"
				} else {
					result.ReasonCode = "cleanup_evidence_invalid"
				}
			} else {
				result.OwnedResources = len(cleanup.Resources)
				result.CleanupOutstanding = cleanup.Outstanding
				if cleanup.Outstanding != 0 {
					result.Status = spec.StatusInvalid
					result.ReasonCode = "cleanup_outstanding"
				}
			}
		}
		result.EvidenceDigest = spec.DigestString(strings.Join([]string{
			task.ID,
			commandResult.StdoutDigest,
			commandResult.StderrDigest,
			fmt.Sprint(commandResult.ExitCode),
			fmt.Sprint(commandResult.TimedOut),
			result.CleanupEvidenceDigest,
		}, "\x00"))
	default:
		result.Status = spec.StatusInvalid
		result.ReasonCode = "task_missing"
	}
	return result
}

func validateRequest(request Request) error {
	if !validID(request.ID) ||
		(request.Kind != "foundation_epoch" &&
			request.Kind != "integration" &&
			request.Kind != "discovery" &&
			request.Kind != "chaos" &&
			request.Kind != "live" &&
			request.Kind != "endurance" &&
			request.Kind != "canary") ||
		!digestValid(request.FoundationDigest) ||
		!digestValid(request.SourceDigest) ||
		!digestValid(request.RuntimeDigest) ||
		!digestValid(request.VSIXDigest) ||
		!digestValid(request.LockIdentity) ||
		len(request.Tasks) == 0 {
		return errors.New("Qualification request is invalid")
	}
	seen := make(map[string]int, len(request.Tasks))
	for index, task := range request.Tasks {
		if !validID(task.ID) {
			return fmt.Errorf("Qualification task %q is invalid", task.ID)
		}
		if _, exists := seen[task.ID]; exists {
			return fmt.Errorf("duplicate Qualification task %q", task.ID)
		}
		seen[task.ID] = index
	}
	for index, task := range request.Tasks {
		for _, dependency := range task.DependsOn {
			dependencyIndex, exists := seen[dependency]
			if !exists || dependencyIndex >= index {
				return fmt.Errorf("Qualification task %q has invalid dependency %q", task.ID, dependency)
			}
		}
		if task.CleanupReport != "" {
			if len(task.Command) == 0 || task.Check != nil ||
				!filepath.IsAbs(task.CleanupReport) {
				return fmt.Errorf(
					"Qualification task %q has invalid cleanup evidence contract",
					task.ID,
				)
			}
		}
	}
	return nil
}

type cleanupReport struct {
	SchemaVersion   int               `json:"schema_version"`
	QualificationID string            `json:"qualification_id"`
	TaskID          string            `json:"task_id"`
	Resources       []cleanupResource `json:"resources"`
	Outstanding     int               `json:"outstanding"`
}

type cleanupResource struct {
	Kind             string `json:"kind"`
	Owner            string `json:"owner"`
	Identity         string `json:"identity"`
	PID              int    `json:"pid,omitempty"`
	CleanupAttempted bool   `json:"cleanup_attempted"`
	CleanupSucceeded bool   `json:"cleanup_succeeded"`
}

func readCleanupReport(
	path, qualificationID, taskID string,
) (cleanupReport, string, error) {
	missingDigest := spec.DigestString(taskID + "\x00cleanup_missing")
	info, err := os.Lstat(path)
	if err != nil {
		return cleanupReport{}, missingDigest, err
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > 1<<20 {
		return cleanupReport{}, missingDigest, errors.New(
			"cleanup evidence file is not a bounded regular file",
		)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cleanupReport{}, missingDigest, err
	}
	digest := spec.DigestString(string(raw))
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var report cleanupReport
	if err := decoder.Decode(&report); err != nil {
		return cleanupReport{}, digest, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return cleanupReport{}, digest, err
	}
	if report.SchemaVersion != 1 ||
		report.QualificationID != qualificationID ||
		report.TaskID != taskID ||
		len(report.Resources) == 0 ||
		report.Outstanding < 0 {
		return cleanupReport{}, digest, errors.New(
			"cleanup evidence identity or inventory is invalid",
		)
	}
	seen := make(map[string]struct{}, len(report.Resources))
	outstanding := 0
	for _, resource := range report.Resources {
		switch resource.Kind {
		case "process":
			if resource.PID < 1 {
				return cleanupReport{}, digest, errors.New(
					"process cleanup evidence has no PID",
				)
			}
		case "temporary_directory":
			if resource.PID != 0 {
				return cleanupReport{}, digest, errors.New(
					"temporary-directory cleanup evidence has a PID",
				)
			}
		default:
			return cleanupReport{}, digest, errors.New(
				"cleanup evidence resource kind is invalid",
			)
		}
		if !validID(resource.Owner) ||
			!digestValid(resource.Identity) ||
			resource.CleanupSucceeded && !resource.CleanupAttempted {
			return cleanupReport{}, digest, errors.New(
				"cleanup evidence resource is invalid",
			)
		}
		key := resource.Kind + "\x00" + resource.Identity
		if _, exists := seen[key]; exists {
			return cleanupReport{}, digest, errors.New(
				"cleanup evidence contains duplicate resources",
			)
		}
		seen[key] = struct{}{}
		if !resource.CleanupAttempted || !resource.CleanupSucceeded {
			outstanding++
		}
	}
	if report.Outstanding != outstanding {
		return cleanupReport{}, digest, errors.New(
			"cleanup evidence outstanding count does not match inventory",
		)
	}
	return report, digest, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("cleanup evidence contains multiple JSON values")
		}
		return err
	}
	return nil
}

func digestReport(report Report) string {
	copy := report
	copy.EvidenceDigest = ""
	raw, _ := json.Marshal(copy)
	return spec.DigestString(string(raw))
}

func Digest(report Report) string {
	return digestReport(report)
}

func mergeStatus(current, next spec.Status) spec.Status {
	rank := func(status spec.Status) int {
		switch status {
		case spec.StatusInvalid:
			return 5
		case spec.StatusFailed:
			return 4
		case spec.StatusUnavailable:
			return 3
		case spec.StatusNotEvaluated:
			return 2
		case spec.StatusPassed:
			return 1
		default:
			return 6
		}
	}
	if rank(next) > rank(current) {
		return next
	}
	return current
}

func validID(value string) bool {
	if value == "" || len(value) > 96 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			index > 0 && (character == '-' || character == '_') {
			continue
		}
		return false
	}
	return true
}

func digestValid(value string) bool {
	if len(value) != len("sha256:")+64 ||
		!strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if !slices.Contains([]rune("0123456789abcdef"), character) {
			return false
		}
	}
	return true
}
