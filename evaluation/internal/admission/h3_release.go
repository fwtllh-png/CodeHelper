package admission

import (
	"bufio"
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

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/qualification"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type H3ReleaseRequest struct {
	Root              string
	Output            string
	QualificationID   string
	Lock              freeze.Lock
	LockPath          string
	H1ReportPath      string
	H2ReportPath      string
	ReleaseReportPath string
	VSCodeRCPath      string
	PackageRoot       string
	Catalog           H3Catalog
	Development       bool
}

type h3LaneReport struct {
	SchemaVersion      int      `json:"schema_version"`
	Lane               string   `json:"lane"`
	Platform           string   `json:"platform"`
	Command            []string `json:"command"`
	StartedAt          string   `json:"started_at"`
	UnavailableReasons []string `json:"unavailable_reasons"`
	Status             string   `json:"status"`
	DurationMS         int64    `json:"duration_ms"`
	ExitCode           *int     `json:"exit_code"`
}

type h3PackageManifest struct {
	SchemaVersion    int      `json:"schema_version"`
	Product          string   `json:"product"`
	Version          string   `json:"version"`
	Commit           string   `json:"commit"`
	BuiltAt          string   `json:"built_at"`
	Stage            string   `json:"stage"`
	StageSequence    []string `json:"stage_sequence"`
	Tarball          string   `json:"tarball"`
	SBOM             string   `json:"sbom"`
	Checksums        string   `json:"checksums"`
	SHA256SUMSDigest string   `json:"sha256sums_digest"`
	GeneratedAt      string   `json:"generated_at"`
}

type h3RCReport struct {
	SchemaVersion   int            `json:"schema_version"`
	Status          string         `json:"status"`
	CandidateKind   string         `json:"candidate_kind"`
	Publishable     bool           `json:"publishable"`
	Uploaded        bool           `json:"uploaded"`
	Source          map[string]any `json:"source"`
	Gates           map[string]any `json:"gates"`
	Performance     map[string]any `json:"performance"`
	DependencyAudit map[string]any `json:"dependency_audit"`
	Limitations     []string       `json:"limitations"`
}

func PrepareH3ReleaseState(root, output string) (string, error) {
	paths := []string{
		filepath.Join(output, "test-lanes"),
		filepath.Join(output, "package"),
		filepath.Join(root, "extensions", "vscode", "dist", "matrix"),
		filepath.Join(root, "extensions", "vscode", "dist", "performance"),
		filepath.Join(root, "extensions", "vscode", "dist", "rc"),
		filepath.Join(root, "extensions", "vscode", "dist", "vscode-release"),
		filepath.Join(root, "extensions", "vscode", "dist", "binary-release"),
	}
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			return "", err
		}
	}
	return digestH2(paths), nil
}

func ReadH3EnduranceEvidence(
	path string,
	requireFormal bool,
) (H3EnduranceEvidence, error) {
	var evidence H3EnduranceEvidence
	if err := decodeStrictFile(path, &evidence); err != nil {
		return evidence, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return evidence, err
	}
	if info.Mode().Perm()&0o077 != 0 {
		return evidence, errors.New("H3 Endurance evidence permissions are not private")
	}
	if evidence.SchemaVersion != H3EnduranceSchemaVersion ||
		evidence.Status != "passed" ||
		!validID(evidence.QualificationID) ||
		!digestValidH2(evidence.SourceDigest) ||
		!digestValidH2(evidence.LockIdentity) ||
		!digestValidH2(evidence.RuntimeDigest) ||
		!digestValidH2(evidence.EvidenceDigest) ||
		evidence.EvidenceDigest != digestH3Endurance(evidence) ||
		evidence.ConfiguredDuration < 1 ||
		evidence.EffectiveDurationMS < 1_000 ||
		evidence.TurnIntervalMS < 1 ||
		evidence.EffectiveDurationMS < evidence.TurnIntervalMS*2 ||
		evidence.ObservedDurationMS < 1 ||
		evidence.TurnsScheduled < 2 ||
		evidence.TurnsCompleted != evidence.TurnsScheduled ||
		evidence.TurnsFailed != 0 ||
		evidence.TerminalCompleted != evidence.TurnsCompleted ||
		evidence.TerminalFailed != 0 ||
		evidence.TerminalCanceled != 0 ||
		len(evidence.Samples) != evidence.TurnsScheduled+1 {
		return evidence, errors.New("H3 Endurance evidence is invalid")
	}
	if requireFormal &&
		(evidence.DevelopmentOverride ||
			evidence.ConfiguredDuration < 14_400 ||
			evidence.EffectiveDurationMS !=
				evidence.ConfiguredDuration*1_000 ||
			evidence.ObservedDurationMS <
				(evidence.ConfiguredDuration-60)*1000) {
		return evidence, errors.New("H3 Endurance evidence is not a formal four-hour run")
	}
	return evidence, nil
}

func ValidateH3Prerequisite(
	path, kind string,
	lock freeze.Lock,
) (qualification.Report, error) {
	var report qualification.Report
	if err := decodeStrictFile(path, &report); err != nil {
		return report, err
	}
	if err := report.Validate(); err != nil {
		return report, err
	}
	if report.Kind != kind ||
		report.Status != spec.StatusPassed ||
		report.SourceDigest != lock.SourceDigest ||
		report.FoundationDigest != lock.FoundationDigest ||
		report.RuntimeDigest != lock.RuntimeDigest ||
		report.VSIXDigest != lock.VSIXDigest ||
		report.LockIdentity != lock.LockIdentity {
		return report, fmt.Errorf("H3 prerequisite %s identity is invalid", kind)
	}
	return report, nil
}

func ValidateH3ReleaseLane(path string) (string, error) {
	var report h3LaneReport
	if err := decodeStrictFile(path, &report); err != nil {
		return "", err
	}
	if report.SchemaVersion != 1 ||
		report.Lane != "release" ||
		report.Status != "passed" ||
		report.ExitCode == nil || *report.ExitCode != 0 ||
		report.DurationMS < 1 ||
		len(report.Command) == 0 ||
		len(report.UnavailableReasons) != 0 {
		return "", errors.New("H3 release lane evidence is invalid")
	}
	return digestH3File(path)
}

func ValidateH3VSCodeRC(path string) (string, error) {
	var report h3RCReport
	if err := decodeStrictFile(path, &report); err != nil {
		return "", err
	}
	if report.SchemaVersion != 1 ||
		report.Status != "passed" ||
		!slices.Contains(
			[]string{"validated-dry-run", "release-candidate"},
			report.CandidateKind,
		) ||
		report.Uploaded ||
		len(report.Source) == 0 ||
		len(report.Gates) == 0 ||
		len(report.Performance) == 0 ||
		len(report.DependencyAudit) == 0 {
		return "", errors.New("H3 VS Code RC evidence is invalid")
	}
	for _, gate := range []string{
		"matrix", "journey_evidence", "compatibility", "performance",
		"dependency_audit", "secret_scan", "vsix_allowlist", "sbom",
		"provenance", "signature", "checksums",
	} {
		if value, ok := report.Gates[gate]; !ok ||
			(value != "passed" &&
				!strings.Contains(fmt.Sprint(value), "/")) {
			return "", fmt.Errorf("H3 VS Code RC gate %s is invalid", gate)
		}
	}
	return digestH3File(path)
}

func ValidateH3Package(
	root, qualificationID, version, sourceCommit string,
	targets []string,
) (H3PackageEvidence, error) {
	evidence := H3PackageEvidence{
		SchemaVersion: 1, QualificationID: qualificationID,
		Status: "failed", Version: version, Stage: "candidate",
	}
	manifestPath := filepath.Join(root, "package-manifest.json")
	var manifest h3PackageManifest
	if err := decodeStrictFile(manifestPath, &manifest); err != nil {
		return evidence, err
	}
	if manifest.SchemaVersion != 1 ||
		manifest.Product != "codehelper" ||
		manifest.Version != version ||
		manifest.Stage != "candidate" ||
		!strings.HasPrefix(sourceCommit, manifest.Commit) ||
		manifest.Tarball == "" ||
		manifest.SBOM == "" ||
		manifest.Checksums != "SHA256SUMS" ||
		len(manifest.StageSequence) != 4 {
		return evidence, errors.New("H3 package manifest is invalid")
	}
	checksumsPath := filepath.Join(root, manifest.Checksums)
	if err := verifyH3Checksums(root, checksumsPath); err != nil {
		return evidence, err
	}
	checksumsDigest, err := digestH3File(checksumsPath)
	if err != nil ||
		strings.TrimPrefix(checksumsDigest, "sha256:") !=
			manifest.SHA256SUMSDigest {
		return evidence, errors.New("H3 package checksum manifest digest is invalid")
	}
	if info, err := os.Stat(filepath.Join(root, manifest.Tarball)); err != nil ||
		info.IsDir() || info.Size() < 1 {
		return evidence, errors.New("H3 package tarball is invalid")
	}
	sbomPath := filepath.Join(root, filepath.FromSlash(manifest.SBOM))
	var sbom map[string]any
	if raw, err := os.ReadFile(sbomPath); err != nil {
		return evidence, err
	} else if json.Unmarshal(raw, &sbom) != nil ||
		sbom["bomFormat"] != "CycloneDX" {
		return evidence, errors.New("H3 package SBOM is invalid")
	}
	for _, target := range targets {
		parts := strings.Split(target, "/")
		name := fmt.Sprintf(
			"codehelper-%s-%s-%s",
			version, parts[0], parts[1],
		)
		if parts[0] == "windows" {
			name += ".exe"
		}
		info, err := os.Stat(filepath.Join(root, "bin", name))
		if err != nil || info.IsDir() || info.Size() < 1 {
			return evidence, fmt.Errorf("H3 package target %s is missing", target)
		}
	}
	manifestDigest, err := digestH3File(manifestPath)
	if err != nil {
		return evidence, err
	}
	sbomDigest, err := digestH3File(sbomPath)
	if err != nil {
		return evidence, err
	}
	evidence.Status = "passed"
	evidence.ManifestDigest = manifestDigest
	evidence.ChecksumsDigest = checksumsDigest
	evidence.SBOMDigest = sbomDigest
	evidence.Targets = append([]string(nil), targets...)
	evidence.EvidenceDigest = digestH3Package(evidence)
	return evidence, nil
}

func WriteH3PackageEvidence(
	output string,
	evidence H3PackageEvidence,
) error {
	if evidence.Status != "passed" ||
		evidence.EvidenceDigest != digestH3Package(evidence) {
		return errors.New("H3 package evidence is invalid")
	}
	return writePrivateJSON(
		filepath.Join(output, "package-evidence.json"),
		evidence,
	)
}

func AggregateH3Release(
	request H3ReleaseRequest,
) (H3ReleaseEvidence, error) {
	var release H3ReleaseEvidence
	if err := request.Lock.Validate(); err != nil {
		return release, err
	}
	epochPath := filepath.Join(
		filepath.Dir(filepath.Clean(request.LockPath)),
		"epoch",
		"qualification.json",
	)
	var epoch qualification.Report
	if err := decodeStrictFile(epochPath, &epoch); err != nil {
		return release, err
	}
	if err := epoch.Validate(); err != nil ||
		epoch.Kind != "foundation_epoch" ||
		epoch.Status != spec.StatusPassed ||
		epoch.SourceDigest != request.Lock.SourceDigest ||
		epoch.FoundationDigest != request.Lock.FoundationDigest ||
		epoch.RuntimeDigest != request.Lock.RuntimeDigest ||
		epoch.VSIXDigest != request.Lock.VSIXDigest ||
		epoch.LockIdentity != request.Lock.LockIdentity {
		return release, errors.New("H3 Foundation evidence is invalid")
	}
	h1, err := ValidateH3Prerequisite(
		request.H1ReportPath,
		"chaos",
		request.Lock,
	)
	if err != nil {
		return release, err
	}
	h2, err := ValidateH3Prerequisite(
		request.H2ReportPath,
		"live",
		request.Lock,
	)
	if err != nil {
		return release, err
	}
	endurance, err := ReadH3EnduranceEvidence(
		filepath.Join(request.Output, "endurance-evidence.json"),
		!request.Development,
	)
	if err != nil {
		return release, err
	}
	if endurance.QualificationID != request.QualificationID ||
		endurance.SourceDigest != request.Lock.SourceDigest ||
		endurance.LockIdentity != request.Lock.LockIdentity ||
		endurance.RuntimeDigest != request.Lock.RuntimeDigest {
		return release, errors.New("H3 Endurance identity is invalid")
	}
	releaseDigest, err := ValidateH3ReleaseLane(request.ReleaseReportPath)
	if err != nil {
		return release, err
	}
	rcDigest, err := ValidateH3VSCodeRC(request.VSCodeRCPath)
	if err != nil {
		return release, err
	}
	var packageEvidence H3PackageEvidence
	if err := decodeStrictFile(
		filepath.Join(request.Output, "package-evidence.json"),
		&packageEvidence,
	); err != nil {
		return release, err
	}
	if packageEvidence.Status != "passed" ||
		packageEvidence.QualificationID != request.QualificationID ||
		packageEvidence.EvidenceDigest != digestH3Package(packageEvidence) {
		return release, errors.New("H3 package evidence identity is invalid")
	}
	var h2Summary H2Summary
	if err := decodeStrictFile(
		filepath.Join(filepath.Dir(request.H2ReportPath), "h2-summary.json"),
		&h2Summary,
	); err != nil {
		return release, err
	}
	if h2Summary.QualificationID != h2.ID ||
		h2Summary.SourceDigest != request.Lock.SourceDigest ||
		h2Summary.LockIdentity != request.Lock.LockIdentity ||
		h2Summary.Passed != h2Summary.Scheduled {
		return release, errors.New("H3 Live summary identity is invalid")
	}
	lanes := []H3LaneEvidence{
		{ID: "foundation", Status: "passed", EvidenceDigest: epoch.EvidenceDigest},
		{ID: "integration", Status: "passed", EvidenceDigest: digestH2(request.Lock.CleanIntegrationRuns)},
		{ID: "chaos", Status: "passed", EvidenceDigest: h1.EvidenceDigest},
		{ID: "live", Status: "passed", EvidenceDigest: h2.EvidenceDigest},
		{ID: "endurance", Status: "passed", EvidenceDigest: endurance.EvidenceDigest},
		{ID: "release", Status: "passed", EvidenceDigest: releaseDigest},
		{ID: "vscode_rc", Status: "passed", EvidenceDigest: rcDigest},
		{ID: "package", Status: "passed", EvidenceDigest: packageEvidence.EvidenceDigest},
	}
	for index, lane := range lanes {
		if request.Catalog.Release.RequiredLanes[index] != lane.ID ||
			!digestValidH2(lane.EvidenceDigest) {
			return release, errors.New("H3 release lane inventory drifted")
		}
	}
	release = H3ReleaseEvidence{
		SchemaVersion:    H3ReleaseSchemaVersion,
		QualificationID:  request.QualificationID,
		SourceDigest:     request.Lock.SourceDigest,
		LockIdentity:     request.Lock.LockIdentity,
		FoundationDigest: request.Lock.FoundationDigest,
		RuntimeDigest:    request.Lock.RuntimeDigest,
		VSIXDigest:       request.Lock.VSIXDigest,
		HostDigest:       request.Lock.HostDigest,
		ProviderDigest:   spec.DigestString(h2Summary.Provider),
		ModelDigest:      spec.DigestString(h2Summary.Model),
		ConfigDigest:     h2Summary.ConfigSHA256,
		RequiredLanes:    lanes,
		Package: H3ReleasePackage{
			ManifestDigest:  packageEvidence.ManifestDigest,
			ChecksumsDigest: packageEvidence.ChecksumsDigest,
			SBOMDigest:      packageEvidence.SBOMDigest,
			Targets:         append([]string(nil), packageEvidence.Targets...),
		},
		Decision: "admit",
	}
	if request.Development {
		release.Decision = "deny"
	}
	if err := writePrivateJSON(
		filepath.Join(request.Output, "release-evidence.json"),
		release,
	); err != nil {
		return release, err
	}
	return release, nil
}

func verifyH3Checksums(root, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	seen := 0
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 2 || len(fields[0]) != 64 {
			return errors.New("H3 package checksum line is invalid")
		}
		relative := strings.TrimPrefix(fields[1], "./")
		if relative == "" || filepath.IsAbs(relative) ||
			strings.HasPrefix(filepath.Clean(relative), "..") {
			return errors.New("H3 package checksum path is invalid")
		}
		actual, err := digestH3File(filepath.Join(root, relative))
		if err != nil ||
			strings.TrimPrefix(actual, "sha256:") != fields[0] {
			return fmt.Errorf("H3 package checksum failed for %s", relative)
		}
		seen++
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if seen < 7 {
		return errors.New("H3 package checksum inventory is incomplete")
	}
	return nil
}

func digestH3Package(evidence H3PackageEvidence) string {
	evidence.EvidenceDigest = ""
	return digestH2(evidence)
}

func digestH3File(path string) (string, error) {
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

func DigestH3Release(evidence H3ReleaseEvidence) string {
	return digestH2(evidence)
}
