package admission

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/capture"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/corpus"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/qualification"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type H4AdmissionRequest struct {
	Output          string
	QualificationID string
	Lock            freeze.Lock
	H3ReportPath    string
	H3ReleasePath   string
	H3PackageRoot   string
	RollbackDigest  string
	Catalog         H4Catalog
	Development     bool
}

func ValidateH4Prerequisite(
	reportPath, releasePath, packageRoot string,
	lock freeze.Lock,
) (qualification.Report, H3ReleaseEvidence, string, error) {
	var report qualification.Report
	if err := decodeStrictFile(reportPath, &report); err != nil {
		return report, H3ReleaseEvidence{}, "", err
	}
	if err := report.Validate(); err != nil ||
		report.Kind != "endurance" ||
		report.Status != spec.StatusPassed ||
		report.SourceDigest != lock.SourceDigest ||
		report.FoundationDigest != lock.FoundationDigest ||
		report.RuntimeDigest != lock.RuntimeDigest ||
		report.VSIXDigest != lock.VSIXDigest ||
		report.LockIdentity != lock.LockIdentity {
		return report, H3ReleaseEvidence{}, "", errors.New(
			"H4 H3 qualification prerequisite is invalid",
		)
	}
	var release H3ReleaseEvidence
	if err := decodeStrictFile(releasePath, &release); err != nil {
		return report, release, "", err
	}
	if release.SchemaVersion != H3ReleaseSchemaVersion ||
		release.QualificationID != report.ID ||
		release.SourceDigest != lock.SourceDigest ||
		release.FoundationDigest != lock.FoundationDigest ||
		release.RuntimeDigest != lock.RuntimeDigest ||
		release.VSIXDigest != lock.VSIXDigest ||
		release.LockIdentity != lock.LockIdentity ||
		release.Decision != "admit" ||
		len(release.RequiredLanes) != len(h3RequiredLanes) {
		return report, release, "", errors.New(
			"H4 H3 release prerequisite is invalid",
		)
	}
	for index, lane := range release.RequiredLanes {
		if lane.ID != h3RequiredLanes[index] ||
			lane.Status != "passed" ||
			!digestValidH2(lane.EvidenceDigest) {
			return report, release, "", errors.New(
				"H4 H3 release lane prerequisite is invalid",
			)
		}
	}
	manifestPath := filepath.Join(packageRoot, "package-manifest.json")
	var manifest h3PackageManifest
	if err := decodeStrictFile(manifestPath, &manifest); err != nil {
		return report, release, "", err
	}
	manifestDigest, err := digestH3File(manifestPath)
	if err != nil || manifestDigest != release.Package.ManifestDigest {
		return report, release, "", errors.New(
			"H4 package manifest prerequisite is invalid",
		)
	}
	checksumsPath := filepath.Join(
		packageRoot,
		filepath.FromSlash(manifest.Checksums),
	)
	if err := verifyH3Checksums(packageRoot, checksumsPath); err != nil {
		return report, release, "", fmt.Errorf(
			"H4 package checksums prerequisite: %w",
			err,
		)
	}
	checksumsDigest, err := digestH3File(checksumsPath)
	if err != nil || checksumsDigest != release.Package.ChecksumsDigest {
		return report, release, "", errors.New(
			"H4 package checksum evidence is invalid",
		)
	}
	sbomPath := filepath.Join(packageRoot, filepath.FromSlash(manifest.SBOM))
	sbomDigest, err := digestH3File(sbomPath)
	if err != nil || sbomDigest != release.Package.SBOMDigest {
		return report, release, "", errors.New(
			"H4 package SBOM evidence is invalid",
		)
	}
	packageBinary := filepath.Join(packageRoot, "bin", "codehelper")
	packageDigest, err := digestH3File(packageBinary)
	if err != nil {
		return report, release, "", err
	}
	return report, release, packageDigest, nil
}

func RunH4IncidentClosure(
	output string,
	sourceDigest string,
	policy H4IncidentSpec,
) (H4IncidentEvidence, error) {
	result := H4IncidentEvidence{
		SchemaVersion:    H4EvidenceSchemaVersion,
		Status:           "failed",
		BatchID:          policy.BatchID,
		SourceDigest:     sourceDigest,
		FailureSignature: "turn_failed",
		SecretScan:       "failed",
		ReplayLevel:      "structural",
	}
	if !digestValidH2(sourceDigest) {
		return result, errors.New("H4 incident source digest is invalid")
	}
	staging := filepath.Join(output, "incident-staging")
	slice := capture.Slice{
		Kind:        "full",
		Index:       1,
		SourceCount: 2,
		Signature:   "turn_failed",
		Events: []evidence.RawEnvelope{
			{
				ObservedSequence: 1,
				ObservedAt:       policy.ReviewedOn + "T00:00:00Z",
				Source:           evidence.SourceRuntime,
				Kind:             "turn.started",
				Identity: evidence.Identity{
					Capture:   "capture-001",
					Session:   "session-001",
					Turn:      "turn-001",
					Operation: "operation-001",
				},
				Data: map[string]any{
					"incident_class":  "canary_health_gate",
					"content_dropped": true,
				},
				Redacted: true,
			},
			{
				ObservedSequence: 2,
				ObservedAt:       policy.ReviewedOn + "T00:00:01Z",
				Source:           evidence.SourceRuntime,
				Kind:             "turn.failed",
				Identity: evidence.Identity{
					Capture:   "capture-001",
					Session:   "session-001",
					Turn:      "turn-001",
					Operation: "operation-001",
				},
				Data: map[string]any{
					"reason_code":     "canary_health_gate_failed",
					"content_dropped": true,
				},
				Redacted: true,
			},
		},
	}
	batch, err := corpus.PromoteBatch(corpus.BatchPromotion{
		BatchID:      policy.BatchID,
		OutputRoot:   staging,
		EntryPrefix:  policy.EntryPrefix,
		SourceFormat: capture.FormatObservation,
		SourceClass:  corpus.SourceSynthetic,
		SourceDigest: sourceDigest,
		Slices:       []capture.Slice{slice},
		Sanitizer:    capture.SanitizerOptions{},
		Review: corpus.PromotionReview{
			SchemaVersion: corpus.QualifiedSchemaVersion,
			ID:            "review-001",
			BatchID:       policy.BatchID,
			Reviewer:      policy.Reviewer,
			Decision:      "approved",
			SourceDigest:  sourceDigest,
			ReviewedOn:    policy.ReviewedOn,
		},
	})
	if err != nil {
		return result, err
	}
	verified, err := corpus.VerifyBatch(
		filepath.Join(staging, policy.BatchID),
		capture.SanitizerOptions{},
	)
	if err != nil {
		return result, err
	}
	if len(verified.Entries) != 1 ||
		verified.Entries[0].Outcome.FailureSignature != "turn_failed" ||
		verified.Entries[0].Manifest.ContentMode != "metadata_only" ||
		verified.Entries[0].Manifest.SecretScan != "passed" {
		return result, errors.New("H4 incident closure evidence is invalid")
	}
	result.Status = "passed"
	result.ReviewDigest = batch.ReviewDigest
	result.Entries = len(verified.Entries)
	result.SecretScan = "passed"
	result.EvidenceDigest = digestH4Incident(result)
	if err := writePrivateJSON(
		filepath.Join(output, "incident-closure-evidence.json"),
		result,
	); err != nil {
		return result, err
	}
	return result, nil
}

func AggregateH4(
	request H4AdmissionRequest,
) (H4AdmissionEvidence, error) {
	var result H4AdmissionEvidence
	h3, release, packageDigest, err := ValidateH4Prerequisite(
		request.H3ReportPath,
		request.H3ReleasePath,
		request.H3PackageRoot,
		request.Lock,
	)
	if err != nil {
		return result, err
	}
	var canary H4CanaryEvidence
	if err := decodeStrictFile(
		filepath.Join(request.Output, "canary-evidence.json"),
		&canary,
	); err != nil {
		return result, err
	}
	if canary.SchemaVersion != H4EvidenceSchemaVersion ||
		canary.Status != "passed" ||
		canary.QualificationID != request.QualificationID ||
		canary.SourceDigest != request.Lock.SourceDigest ||
		canary.LockIdentity != request.Lock.LockIdentity ||
		canary.PackageDigest != packageDigest ||
		canary.EvidenceDigest != digestH4Canary(canary) ||
		(!request.Development && canary.DevelopmentOverride) {
		return result, errors.New("H4 Canary evidence is invalid")
	}
	var stop H4StopEvidence
	if err := decodeStrictFile(
		filepath.Join(request.Output, "rollout-stop-evidence.json"),
		&stop,
	); err != nil {
		return result, err
	}
	if stop.Status != "passed" ||
		stop.Decision != "stop" ||
		stop.BlockedSlots < 1 ||
		stop.EvidenceDigest != digestH4Stop(stop) {
		return result, errors.New("H4 rollout stop evidence is invalid")
	}
	var incident H4IncidentEvidence
	if err := decodeStrictFile(
		filepath.Join(request.Output, "incident-closure-evidence.json"),
		&incident,
	); err != nil {
		return result, err
	}
	if incident.Status != "passed" ||
		incident.SourceDigest != stop.EvidenceDigest ||
		incident.Entries < 1 ||
		incident.SecretScan != "passed" ||
		incident.EvidenceDigest != digestH4Incident(incident) {
		return result, errors.New("H4 incident closure evidence is invalid")
	}
	if !digestValidH2(request.RollbackDigest) {
		return result, errors.New("H4 rollback evidence is invalid")
	}
	releaseDigest := DigestH3Release(release)
	lanes := []H4LaneEvidence{
		{ID: "h3_rc", Status: "passed", EvidenceDigest: releaseDigest},
		{ID: "canary", Status: "passed", EvidenceDigest: canary.EvidenceDigest},
		{ID: "rollout_stop", Status: "passed", EvidenceDigest: stop.EvidenceDigest},
		{ID: "rollback", Status: "passed", EvidenceDigest: request.RollbackDigest},
		{ID: "incident_closure", Status: "passed", EvidenceDigest: incident.EvidenceDigest},
	}
	for index, lane := range lanes {
		if lane.ID != h4RequiredLanes[index] ||
			!digestValidH2(lane.EvidenceDigest) {
			return result, errors.New("H4 lane inventory drifted")
		}
	}
	result = H4AdmissionEvidence{
		SchemaVersion:     H4EvidenceSchemaVersion,
		QualificationID:   request.QualificationID,
		SourceDigest:      request.Lock.SourceDigest,
		LockIdentity:      request.Lock.LockIdentity,
		FoundationDigest:  request.Lock.FoundationDigest,
		RuntimeDigest:     request.Lock.RuntimeDigest,
		VSIXDigest:        request.Lock.VSIXDigest,
		H3QualificationID: h3.ID,
		H3ReleaseDigest:   releaseDigest,
		PackageDigest:     packageDigest,
		RequiredLanes:     lanes,
		Scope:             "controlled_local_inventory",
		PublicRelease:     false,
		Decision:          "admit",
	}
	if request.Development {
		result.Decision = "deny"
	}
	if err := writePrivateJSON(
		filepath.Join(request.Output, "h4-admission-evidence.json"),
		result,
	); err != nil {
		return result, err
	}
	return result, nil
}

func PrepareH4State(output string) (string, error) {
	paths := []string{
		filepath.Join(output, "incident-staging"),
	}
	for _, path := range paths {
		if err := os.RemoveAll(path); err != nil {
			return "", err
		}
	}
	return digestH2(paths), nil
}

func H4IncidentSourceDigest(stop H4StopEvidence) string {
	return stop.EvidenceDigest
}

func ReadH4StopEvidence(path string) (H4StopEvidence, error) {
	var evidence H4StopEvidence
	if err := decodeStrictFile(path, &evidence); err != nil {
		return evidence, err
	}
	if evidence.EvidenceDigest != digestH4Stop(evidence) {
		return evidence, fmt.Errorf("H4 stop evidence digest does not match")
	}
	return evidence, nil
}
