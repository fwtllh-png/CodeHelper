package d2

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type DiscoveryLock struct {
	SchemaVersion        int                  `json:"schema_version"`
	ID                   string               `json:"id"`
	Status               string               `json:"status"`
	BaseLockIdentity     string               `json:"base_lock_identity"`
	BaseFoundationDigest string               `json:"base_foundation_digest"`
	SourceDigest         string               `json:"source_digest"`
	EvaluationDigest     string               `json:"evaluation_digest"`
	RuntimeDigest        string               `json:"runtime_digest"`
	HostDigest           string               `json:"host_digest"`
	VSIXDigest           string               `json:"vsix_digest"`
	CampaignDigest       string               `json:"campaign_digest"`
	PlannerDigest        string               `json:"planner_digest"`
	DiscoveryDigest      string               `json:"discovery_digest"`
	LockIdentity         string               `json:"lock_identity"`
	DiscoveryInputRoots  []string             `json:"discovery_input_roots"`
	Inputs               []freeze.InputDigest `json:"inputs"`
	QualificationDigest  string               `json:"qualification_digest,omitempty"`
	CreatedAt            time.Time            `json:"created_at"`
}

type LockOptions struct {
	Root       string
	ID         string
	Base       freeze.Lock
	Campaign   CampaignBundle
	Plan       Plan
	InputRoots []string
	Now        func() time.Time
}

func DefaultInputRoots() []string {
	return []string{
		"evaluation/d2",
		"evaluation/schema/discovery-campaign.schema.json",
		"evaluation/schema/discovery-driver-inventory.schema.json",
		"evaluation/schema/discovery-driver-qualification.schema.json",
		"evaluation/schema/discovery-lock.schema.json",
		"evaluation/schema/discovery-observation.schema.json",
		"evaluation/schema/discovery-plan.schema.json",
		"evaluation/schema/discovery-qualification.schema.json",
		"evaluation/schema/discovery-round.schema.json",
		"evaluation/spec/d2-campaign.json",
	}
}

func BuildDiscoveryLock(options LockOptions) (DiscoveryLock, error) {
	root, err := filepath.Abs(options.Root)
	if err != nil {
		return DiscoveryLock{}, err
	}
	if !validID(options.ID) ||
		options.Base.Status != "frozen_qualified" ||
		len(options.Base.CleanIntegrationRuns) != 3 {
		return DiscoveryLock{}, errors.New(
			"D2 Discovery Lock requires a frozen qualified base Lock",
		)
	}
	if err := options.Base.Validate(); err != nil {
		return DiscoveryLock{}, err
	}
	if err := options.Campaign.Campaign.Validate(); err != nil {
		return DiscoveryLock{}, err
	}
	if options.Campaign.Digest != spec.DigestString(string(options.Campaign.Raw)) ||
		options.Plan.CampaignID != options.Campaign.Campaign.ID ||
		options.Plan.EvidenceDigest != digestPlan(options.Plan) {
		return DiscoveryLock{}, errors.New(
			"D2 campaign or planner identity is invalid",
		)
	}
	inputRoots, err := normalizeDiscoveryRoots(options.InputRoots)
	if err != nil {
		return DiscoveryLock{}, err
	}
	inputs, err := collectDiscoveryInputs(root, inputRoots)
	if err != nil {
		return DiscoveryLock{}, err
	}
	now := options.Now
	if now == nil {
		now = time.Now
	}
	lock := DiscoveryLock{
		SchemaVersion:        SchemaVersion,
		ID:                   options.ID,
		Status:               "candidate",
		BaseLockIdentity:     options.Base.LockIdentity,
		BaseFoundationDigest: options.Base.FoundationDigest,
		SourceDigest:         options.Base.SourceDigest,
		EvaluationDigest:     options.Base.EvaluationDigest,
		RuntimeDigest:        options.Base.RuntimeDigest,
		HostDigest:           options.Base.HostDigest,
		VSIXDigest:           options.Base.VSIXDigest,
		CampaignDigest:       options.Campaign.Digest,
		PlannerDigest:        options.Plan.EvidenceDigest,
		DiscoveryInputRoots:  inputRoots,
		Inputs:               inputs,
		CreatedAt:            now().UTC(),
	}
	lock.DiscoveryDigest = discoveryDigest(lock)
	lock.LockIdentity = discoveryLockIdentity(lock)
	return lock, lock.Validate()
}

func (l DiscoveryLock) Validate() error {
	for name, value := range map[string]string{
		"base_lock_identity":     l.BaseLockIdentity,
		"base_foundation_digest": l.BaseFoundationDigest,
		"source_digest":          l.SourceDigest,
		"evaluation_digest":      l.EvaluationDigest,
		"runtime_digest":         l.RuntimeDigest,
		"host_digest":            l.HostDigest,
		"vsix_digest":            l.VSIXDigest,
		"campaign_digest":        l.CampaignDigest,
		"planner_digest":         l.PlannerDigest,
		"discovery_digest":       l.DiscoveryDigest,
		"lock_identity":          l.LockIdentity,
	} {
		if !validDigest(value) {
			return fmt.Errorf("D2 Discovery Lock %s is invalid", name)
		}
	}
	if l.SchemaVersion != SchemaVersion || !validID(l.ID) ||
		(l.Status != "candidate" && l.Status != "qualified") ||
		l.CreatedAt.IsZero() || len(l.Inputs) == 0 {
		return errors.New("D2 Discovery Lock identity is invalid")
	}
	roots, err := normalizeDiscoveryRoots(l.DiscoveryInputRoots)
	if err != nil || !slices.Equal(roots, l.DiscoveryInputRoots) {
		return errors.New("D2 Discovery Lock input roots are invalid")
	}
	seen := make(map[string]struct{}, len(l.Inputs))
	for _, input := range l.Inputs {
		if !validRelativePath(input.Path) || !validDigest(input.Digest) {
			return fmt.Errorf("D2 Discovery input %q is invalid", input.Path)
		}
		if _, duplicate := seen[input.Path]; duplicate {
			return fmt.Errorf("duplicate D2 Discovery input %q", input.Path)
		}
		seen[input.Path] = struct{}{}
	}
	if l.DiscoveryDigest != discoveryDigest(l) ||
		l.LockIdentity != discoveryLockIdentity(l) {
		return errors.New("D2 Discovery Lock digest does not match its inputs")
	}
	if l.Status == "candidate" && l.QualificationDigest != "" {
		return errors.New("candidate D2 Discovery Lock has qualification evidence")
	}
	if l.Status == "qualified" && !validDigest(l.QualificationDigest) {
		return errors.New("qualified D2 Discovery Lock lacks qualification evidence")
	}
	return nil
}

func VerifyDiscoveryInputs(root string, lock DiscoveryLock) (string, error) {
	if err := lock.Validate(); err != nil {
		return "", err
	}
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	inputs, err := collectDiscoveryInputs(
		absoluteRoot,
		lock.DiscoveryInputRoots,
	)
	if err != nil {
		return "", err
	}
	if !slices.Equal(inputs, lock.Inputs) {
		return "", errors.New("D2 Discovery input set drifted")
	}
	return spec.DigestString(strings.Join([]string{
		lock.BaseLockIdentity,
		lock.LockIdentity,
		lock.DiscoveryDigest,
	}, "\x00")), nil
}

func WriteJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("D2 artifact %q already exists", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".d2-*.tmp")
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

func WriteQualificationBundle(
	output string,
	plan Plan,
	report QualificationReport,
	lock DiscoveryLock,
) error {
	if err := report.Validate(); err != nil {
		return err
	}
	if err := lock.Validate(); err != nil {
		return err
	}
	if lock.Status != "qualified" ||
		lock.QualificationDigest != report.EvidenceDigest ||
		report.DiscoveryLockIdentity != lock.LockIdentity ||
		plan.EvidenceDigest != lock.PlannerDigest {
		return errors.New("D2 qualification bundle identity is inconsistent")
	}
	parent := filepath.Dir(output)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return err
	}
	if _, err := os.Stat(output); err == nil {
		return fmt.Errorf("D2 output %q already exists", output)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	staging, err := os.MkdirTemp(parent, ".d2-qualification-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(staging)
	for _, artifact := range []struct {
		name  string
		value any
	}{
		{"campaign-plan.json", plan},
		{"discovery-qualification.json", report},
		{"discovery-lock.json", lock},
	} {
		if err := WriteJSON(
			filepath.Join(staging, artifact.name),
			artifact.value,
		); err != nil {
			return err
		}
	}
	return os.Rename(staging, output)
}

func ReadDiscoveryLock(path string) (DiscoveryLock, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return DiscoveryLock{}, err
	}
	var lock DiscoveryLock
	if err := decodeStrict(raw, &lock); err != nil {
		return DiscoveryLock{}, err
	}
	return lock, lock.Validate()
}

func normalizeDiscoveryRoots(roots []string) ([]string, error) {
	normalized := append([]string(nil), roots...)
	slices.Sort(normalized)
	if len(normalized) == 0 {
		return nil, errors.New("D2 Discovery input roots are empty")
	}
	for index, root := range normalized {
		if !validRelativePath(root) || !allowedDiscoveryRoot(root) {
			return nil, fmt.Errorf("D2 Discovery input root %q is invalid", root)
		}
		if index == 0 {
			continue
		}
		previous := normalized[index-1]
		if root == previous || strings.HasPrefix(root, previous+"/") {
			return nil, fmt.Errorf(
				"D2 Discovery input roots %q and %q overlap",
				previous,
				root,
			)
		}
	}
	return normalized, nil
}

func allowedDiscoveryRoot(path string) bool {
	return path == "evaluation/d2" ||
		strings.HasPrefix(path, "evaluation/d2/") ||
		strings.HasPrefix(path, "evaluation/schema/discovery-") ||
		strings.HasPrefix(path, "evaluation/spec/d2-")
}

func collectDiscoveryInputs(
	root string,
	roots []string,
) ([]freeze.InputDigest, error) {
	var inputs []freeze.InputDigest
	for _, relativeRoot := range roots {
		absolute := filepath.Join(root, filepath.FromSlash(relativeRoot))
		info, err := os.Stat(absolute)
		if err != nil {
			return nil, err
		}
		if info.Mode().IsRegular() {
			digest, err := digestFile(absolute)
			if err != nil {
				return nil, err
			}
			inputs = append(inputs, freeze.InputDigest{
				Path: relativeRoot, Digest: digest,
			})
			continue
		}
		if !info.IsDir() {
			return nil, fmt.Errorf(
				"D2 Discovery input root %q is not regular",
				relativeRoot,
			)
		}
		if err := filepath.WalkDir(
			absolute,
			func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() {
					return nil
				}
				if !entry.Type().IsRegular() {
					return nil
				}
				relative, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				digest, err := digestFile(path)
				if err != nil {
					return err
				}
				inputs = append(inputs, freeze.InputDigest{
					Path: filepath.ToSlash(relative), Digest: digest,
				})
				return nil
			},
		); err != nil {
			return nil, err
		}
	}
	slices.SortFunc(inputs, func(left, right freeze.InputDigest) int {
		return strings.Compare(left.Path, right.Path)
	})
	return inputs, nil
}

func discoveryDigest(lock DiscoveryLock) string {
	parts := []string{
		lock.BaseLockIdentity,
		lock.CampaignDigest,
		lock.PlannerDigest,
	}
	for _, root := range lock.DiscoveryInputRoots {
		parts = append(parts, "root\x00"+root)
	}
	for _, input := range lock.Inputs {
		parts = append(parts, input.Path+"\x00"+input.Digest)
	}
	return spec.DigestString(strings.Join(parts, "\x00"))
}

func discoveryLockIdentity(lock DiscoveryLock) string {
	return spec.DigestString(strings.Join([]string{
		lock.ID,
		lock.BaseLockIdentity,
		lock.BaseFoundationDigest,
		lock.SourceDigest,
		lock.EvaluationDigest,
		lock.RuntimeDigest,
		lock.HostDigest,
		lock.VSIXDigest,
		lock.CampaignDigest,
		lock.PlannerDigest,
		lock.DiscoveryDigest,
		lock.CreatedAt.UTC().Format(time.RFC3339Nano),
	}, "\x00"))
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

func validRelativePath(value string) bool {
	return strings.TrimSpace(value) != "" &&
		!filepath.IsAbs(value) &&
		value != ".." &&
		!strings.HasPrefix(value, "../") &&
		!strings.Contains(value, "\\") &&
		filepath.ToSlash(filepath.Clean(value)) == value
}

func validDigest(value string) bool {
	if len(value) != len("sha256:")+64 ||
		!strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if !(character >= '0' && character <= '9') &&
			!(character >= 'a' && character <= 'f') {
			return false
		}
	}
	return true
}
