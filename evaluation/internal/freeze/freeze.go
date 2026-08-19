package freeze

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/foundation"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/qualification"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

const SchemaVersion = 3

type InputDigest struct {
	Path   string `json:"path"`
	Digest string `json:"digest"`
}

type ScanResult struct {
	Status    string   `json:"status"`
	Artifacts []string `json:"artifacts"`
	Forbidden []string `json:"forbidden"`
	Digest    string   `json:"digest"`
}

type Lock struct {
	SchemaVersion        int           `json:"schema_version"`
	ID                   string        `json:"id"`
	Status               string        `json:"status"`
	SourceCommit         string        `json:"source_commit"`
	SourceDigest         string        `json:"source_digest"`
	FoundationDigest     string        `json:"foundation_digest"`
	HarnessDigest        string        `json:"harness_digest"`
	EvaluationDigest     string        `json:"evaluation_digest"`
	RuntimeDigest        string        `json:"runtime_digest"`
	VSIXDigest           string        `json:"vsix_digest"`
	HostDigest           string        `json:"host_digest"`
	ToolchainDigest      string        `json:"toolchain_digest"`
	ProductionScanDigest string        `json:"production_scan_digest"`
	LockIdentity         string        `json:"lock_identity"`
	InputRoots           []string      `json:"input_roots"`
	Inputs               []InputDigest `json:"inputs"`
	CleanIntegrationRuns []string      `json:"clean_integration_runs"`
	CreatedAt            time.Time     `json:"created_at"`
}

type CandidateOptions struct {
	Root             string
	ID               string
	Source           spec.SourceIdentity
	Foundation       foundation.Bundle
	EvaluationBinary string
	RuntimeBinary    string
	VSIX             string
	InputRoots       []string
	Now              func() time.Time
}

func BuildCandidate(options CandidateOptions) (Lock, ScanResult, error) {
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return Lock{}, ScanResult{}, err
	}
	if !validID(options.ID) {
		return Lock{}, ScanResult{}, errors.New("Harness Lock options are invalid")
	}
	inputRoots, err := normalizeInputRoots(options.InputRoots)
	if err != nil {
		return Lock{}, ScanResult{}, err
	}
	evaluationDigest, err := digestFile(options.EvaluationBinary)
	if err != nil {
		return Lock{}, ScanResult{}, fmt.Errorf("digest Evaluation binary: %w", err)
	}
	runtimeDigest, err := digestFile(options.RuntimeBinary)
	if err != nil {
		return Lock{}, ScanResult{}, fmt.Errorf("digest Runtime binary: %w", err)
	}
	vsixDigest, err := digestFile(options.VSIX)
	if err != nil {
		return Lock{}, ScanResult{}, fmt.Errorf("digest VSIX: %w", err)
	}
	inputs, err := collectInputs(root, inputRoots)
	if err != nil {
		return Lock{}, ScanResult{}, err
	}
	scan, err := ScanProductionArtifacts(options.RuntimeBinary, options.VSIX)
	if err != nil {
		return Lock{}, scan, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	lock := Lock{
		SchemaVersion: SchemaVersion,
		ID:            options.ID, Status: "candidate",
		SourceCommit:     options.Source.Commit,
		SourceDigest:     options.Source.DirtyDigest,
		FoundationDigest: options.Foundation.HarnessInputHash,
		EvaluationDigest: evaluationDigest,
		RuntimeDigest:    runtimeDigest,
		VSIXDigest:       vsixDigest,
		HostDigest:       vsixDigest,
		ToolchainDigest: spec.DigestString(strings.Join([]string{
			runtime.Version(), runtime.GOOS, runtime.GOARCH,
		}, "\x00")),
		ProductionScanDigest: scan.Digest,
		InputRoots:           inputRoots,
		Inputs:               inputs,
		CleanIntegrationRuns: []string{},
		CreatedAt:            now().UTC(),
	}
	lock.HarnessDigest = digestInputs(
		lock.FoundationDigest,
		lock.InputRoots,
		inputs,
	)
	lock.LockIdentity = identityDigest(lock)
	return lock, scan, lock.Validate()
}

func AppendIntegrationRun(lock Lock, report qualification.Report) (Lock, error) {
	if err := lock.Validate(); err != nil {
		return Lock{}, err
	}
	if err := report.Validate(); err != nil {
		return Lock{}, err
	}
	if report.Kind != "integration" || report.Status != spec.StatusPassed ||
		report.SourceDigest != lock.SourceDigest ||
		report.FoundationDigest != lock.FoundationDigest ||
		report.RuntimeDigest != lock.RuntimeDigest ||
		report.VSIXDigest != lock.VSIXDigest ||
		report.LockIdentity != lock.LockIdentity {
		return Lock{}, errors.New("Integration report identity does not match Harness Lock")
	}
	if slices.Contains(lock.CleanIntegrationRuns, report.EvidenceDigest) {
		return Lock{}, errors.New("duplicate Integration qualification evidence")
	}
	if len(lock.CleanIntegrationRuns) >= 3 {
		return Lock{}, errors.New("Harness Lock already has three Integration runs")
	}
	lock.CleanIntegrationRuns = append(
		lock.CleanIntegrationRuns,
		report.EvidenceDigest,
	)
	if len(lock.CleanIntegrationRuns) == 3 {
		lock.Status = "frozen_qualified"
	}
	return lock, lock.Validate()
}

func (l Lock) Validate() error {
	for name, value := range map[string]string{
		"source_digest":          l.SourceDigest,
		"foundation_digest":      l.FoundationDigest,
		"harness_digest":         l.HarnessDigest,
		"evaluation_digest":      l.EvaluationDigest,
		"runtime_digest":         l.RuntimeDigest,
		"vsix_digest":            l.VSIXDigest,
		"host_digest":            l.HostDigest,
		"toolchain_digest":       l.ToolchainDigest,
		"production_scan_digest": l.ProductionScanDigest,
		"lock_identity":          l.LockIdentity,
	} {
		if !digestValid(value) {
			return fmt.Errorf("Harness Lock %s is invalid", name)
		}
	}
	if l.SchemaVersion != SchemaVersion || !validID(l.ID) ||
		strings.TrimSpace(l.SourceCommit) == "" ||
		(l.Status != "candidate" && l.Status != "frozen_qualified") ||
		len(l.InputRoots) == 0 || len(l.Inputs) == 0 || l.CreatedAt.IsZero() {
		return errors.New("Harness Lock identity is invalid")
	}
	normalizedRoots, err := normalizeInputRoots(l.InputRoots)
	if err != nil || !slices.Equal(normalizedRoots, l.InputRoots) {
		return errors.New("Harness Lock input roots are invalid")
	}
	if l.HarnessDigest != digestInputs(
		l.FoundationDigest,
		l.InputRoots,
		l.Inputs,
	) ||
		l.LockIdentity != identityDigest(l) {
		return errors.New("Harness Lock digest does not match its inputs")
	}
	seen := make(map[string]struct{}, len(l.Inputs))
	for _, input := range l.Inputs {
		if !validRelativePath(input.Path) || !digestValid(input.Digest) {
			return fmt.Errorf("Harness input %q is invalid", input.Path)
		}
		if _, exists := seen[input.Path]; exists {
			return fmt.Errorf("duplicate Harness input %q", input.Path)
		}
		seen[input.Path] = struct{}{}
	}
	if l.Status == "candidate" && len(l.CleanIntegrationRuns) >= 3 {
		return errors.New("candidate Harness Lock has too many clean runs")
	}
	if l.Status == "frozen_qualified" && len(l.CleanIntegrationRuns) != 3 {
		return errors.New("frozen Harness Lock requires exactly three clean runs")
	}
	for _, digest := range l.CleanIntegrationRuns {
		if !digestValid(digest) {
			return errors.New("Harness Lock Integration digest is invalid")
		}
	}
	return nil
}

func Write(path string, lock Lock) error {
	if err := lock.Validate(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	temporary, err := os.CreateTemp(filepath.Dir(path), ".harness-lock-*.tmp")
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

func Read(path string) (Lock, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Lock{}, err
	}
	var lock Lock
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&lock); err != nil {
		return Lock{}, err
	}
	if err := lock.Validate(); err != nil {
		return Lock{}, err
	}
	return lock, nil
}

func VerifyIdentity(
	root string,
	lock Lock,
	_ spec.SourceIdentity,
	bundle foundation.Bundle,
	evaluationBinary, runtimeBinary, vsixPath string,
) (string, error) {
	if err := lock.Validate(); err != nil {
		return "", err
	}
	if bundle.HarnessInputHash != lock.FoundationDigest {
		return "", errors.New("Foundation identity drifted")
	}
	for _, artifact := range []struct {
		name     string
		path     string
		expected string
	}{
		{"evaluation", evaluationBinary, lock.EvaluationDigest},
		{"runtime", runtimeBinary, lock.RuntimeDigest},
		{"vsix", vsixPath, lock.VSIXDigest},
	} {
		digest, err := digestFile(artifact.path)
		if err != nil {
			return "", err
		}
		if digest != artifact.expected {
			return "", fmt.Errorf("%s artifact identity drifted", artifact.name)
		}
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	currentInputs, err := collectInputs(absoluteRoot, lock.InputRoots)
	if err != nil {
		return "", err
	}
	if !slices.Equal(currentInputs, lock.Inputs) {
		return "", errors.New("Harness input set drifted")
	}
	scan, err := ScanProductionArtifacts(runtimeBinary, vsixPath)
	if err != nil {
		return "", err
	}
	if scan.Digest != lock.ProductionScanDigest {
		return "", errors.New("production isolation scan drifted")
	}
	return spec.DigestString(strings.Join([]string{
		lock.LockIdentity,
		lock.SourceDigest,
		bundle.HarnessInputHash,
		scan.Digest,
	}, "\x00")), nil
}

func ScanProductionArtifacts(runtimeBinary, vsixPath string) (ScanResult, error) {
	result := ScanResult{
		Status:    "passed",
		Artifacts: []string{"runtime", "vsix"},
		Forbidden: []string{},
	}
	forbidden := [][]byte{
		[]byte("CODEHELPER_EVALUATION_"),
		[]byte("codehelper-eval"),
		[]byte("FixtureControl"),
		[]byte("CrashPoint"),
		[]byte("fixture_control"),
		[]byte("crash_point"),
	}
	runtimeRaw, err := os.ReadFile(runtimeBinary)
	if err != nil {
		return result, err
	}
	for _, marker := range forbidden {
		if bytes.Contains(runtimeRaw, marker) {
			result.Forbidden = append(
				result.Forbidden,
				"runtime:"+string(marker),
			)
		}
	}
	archive, err := zip.OpenReader(vsixPath)
	if err != nil {
		return result, err
	}
	defer archive.Close()
	for _, file := range archive.File {
		name := filepath.ToSlash(file.Name)
		lower := strings.ToLower(name)
		if strings.Contains(lower, "evaluation/") ||
			strings.Contains(lower, "codehelper-eval") {
			result.Forbidden = append(result.Forbidden, "vsix-path:"+name)
			continue
		}
		if file.UncompressedSize64 > 8<<20 {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			return result, err
		}
		content, err := io.ReadAll(io.LimitReader(reader, 8<<20))
		reader.Close()
		if err != nil {
			return result, err
		}
		for _, marker := range forbidden {
			if bytes.Contains(content, marker) {
				result.Forbidden = append(
					result.Forbidden,
					"vsix:"+name+":"+string(marker),
				)
			}
		}
	}
	slices.Sort(result.Forbidden)
	result.Digest = spec.DigestString(strings.Join(result.Forbidden, "\x00"))
	if len(result.Forbidden) != 0 {
		result.Status = "failed"
		return result, errors.New("production artifacts contain Evaluation controls")
	}
	return result, nil
}

func collectInputs(root string, roots []string) ([]InputDigest, error) {
	var inputs []InputDigest
	for _, relativeRoot := range roots {
		if !validRelativePath(relativeRoot) {
			return nil, fmt.Errorf("Harness input root %q is invalid", relativeRoot)
		}
		absolute := filepath.Join(root, filepath.FromSlash(relativeRoot))
		if err := filepath.WalkDir(
			absolute,
			func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				slashed := filepath.ToSlash(relative)
				if entry.IsDir() {
					if shouldSkipDirectory(slashed) {
						return filepath.SkipDir
					}
					return nil
				}
				if !entry.Type().IsRegular() {
					return nil
				}
				digest, err := digestFile(path)
				if err != nil {
					return err
				}
				inputs = append(inputs, InputDigest{Path: slashed, Digest: digest})
				return nil
			},
		); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(inputs, func(left, right InputDigest) int {
		return strings.Compare(left.Path, right.Path)
	})
	return inputs, nil
}

func normalizeInputRoots(roots []string) ([]string, error) {
	normalized := append([]string(nil), roots...)
	slices.Sort(normalized)
	for index, root := range normalized {
		if !validRelativePath(root) {
			return nil, fmt.Errorf("Harness input root %q is invalid", root)
		}
		if index == 0 {
			continue
		}
		previous := normalized[index-1]
		if root == previous || strings.HasPrefix(root, previous+"/") {
			return nil, fmt.Errorf(
				"Harness input roots %q and %q overlap",
				previous,
				root,
			)
		}
	}
	if len(normalized) == 0 {
		return nil, errors.New("Harness input roots are empty")
	}
	return normalized, nil
}

func shouldSkipDirectory(path string) bool {
	for _, part := range strings.Split(path, "/") {
		switch part {
		case ".tmp", "node_modules", "dist", "bin", "assessments":
			return true
		}
	}
	return false
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		return "", err
	}
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func digestInputs(
	foundationDigest string,
	roots []string,
	inputs []InputDigest,
) string {
	parts := []string{foundationDigest}
	for _, root := range roots {
		parts = append(parts, "root\x00"+root)
	}
	for _, input := range inputs {
		parts = append(parts, input.Path+"\x00"+input.Digest)
	}
	return spec.DigestString(strings.Join(parts, "\x00"))
}

func identityDigest(lock Lock) string {
	return spec.DigestString(strings.Join([]string{
		lock.SourceCommit,
		lock.SourceDigest,
		lock.FoundationDigest,
		lock.HarnessDigest,
		lock.EvaluationDigest,
		lock.RuntimeDigest,
		lock.VSIXDigest,
		lock.HostDigest,
		lock.ToolchainDigest,
		lock.ProductionScanDigest,
	}, "\x00"))
}

func validRelativePath(value string) bool {
	return strings.TrimSpace(value) != "" &&
		!filepath.IsAbs(value) &&
		value != ".." &&
		!strings.HasPrefix(value, "../") &&
		!strings.Contains(value, "\\") &&
		filepath.ToSlash(filepath.Clean(value)) == value
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
		digit := character >= '0' && character <= '9'
		hex := character >= 'a' && character <= 'f'
		if !digit && !hex {
			return false
		}
	}
	return true
}
