package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositorySecurityGovernanceBaseline(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	measured, err := measure(root, "test-commit")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := readReport(
		filepath.Join(root, "docs/security-governance-sg0-baseline.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCandidate(baseline, measured); err != nil {
		t.Fatal(err)
	}
	if !measured.Controls.GuardValidatesBeforePolicy ||
		!measured.Controls.StrongSandboxFailsClosed ||
		!measured.Controls.PreparedPolicyIdentityVerified ||
		!measured.Controls.ApprovalFingerprintBound ||
		!measured.Controls.ExactWorkspaceWritesBounded ||
		!measured.Controls.TeardownReceiptOwned {
		t.Fatalf("existing security controls were not detected: %+v", measured.Controls)
	}
	if !measured.Controls.ControlPlaneProtected ||
		!measured.Controls.AuthorityStoreOutsideWorkspace ||
		!measured.Controls.UnifiedPermissionProfile ||
		!measured.Controls.RestrictedProcessEgress ||
		!measured.Controls.ManagedNetworkProxy ||
		!measured.Controls.TypedSandboxDenialProducer ||
		!measured.Controls.CoherentSandboxEscalation ||
		!measured.Controls.LinuxSyscallFilter ||
		!measured.Controls.AttemptPermissionDigest {
		t.Fatalf("SG1-SG7 controls were not detected: %+v", measured.Controls)
	}
	if len(measured.AttackCases) != 5 ||
		measured.AttackCases[0].CurrentOutcome != "blocked" ||
		measured.AttackCases[2].CurrentOutcome != "blocked" ||
		measured.AttackCases[3].CurrentOutcome != "blocked" ||
		measured.AttackCases[1].CurrentOutcome != "blocked" ||
		measured.AttackCases[4].CurrentOutcome != "blocked" ||
		len(measured.KnownGaps) != 0 {
		t.Fatalf("SG0 corpus = %+v, gaps = %+v", measured.AttackCases, measured.KnownGaps)
	}
}

func TestValidateCandidateAllowsClosingKnownGaps(t *testing.T) {
	baseline := fixtureReport()
	candidate := fixtureReport()
	candidate.Controls.ControlPlaneProtected = true
	candidate.Controls.UnifiedPermissionProfile = true
	candidate.KnownGaps = knownGaps(candidate.Controls)
	candidate.AttackCases = securityAttackCases(candidate.Controls)
	if err := validateCandidate(baseline, candidate); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCandidateRejectsControlRegression(t *testing.T) {
	baseline := fixtureReport()
	candidate := fixtureReport()
	candidate.Controls.StrongSandboxFailsClosed = false
	err := validateCandidate(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "StrongSandboxFailsClosed") {
		t.Fatalf("validateCandidate() error = %v", err)
	}
}

func TestValidateCandidateRejectsAttackCorpusDrift(t *testing.T) {
	baseline := fixtureReport()
	candidate := fixtureReport()
	candidate.AttackCases[0].ID = "changed"
	err := validateCandidate(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "attack corpus case") {
		t.Fatalf("validateCandidate() error = %v", err)
	}
}

func TestUpdateBaselineOnlyAllowsMonotonicImprovement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	baseline := fixtureReport()
	if err := writeJSON(path, baseline); err != nil {
		t.Fatal(err)
	}

	regression := fixtureReport()
	regression.Controls.StrongSandboxFailsClosed = false
	if err := updateBaseline(path, regression); err == nil ||
		!strings.Contains(err.Error(), "refuse to relax") {
		t.Fatalf("updateBaseline(regression) error = %v", err)
	}

	improvement := fixtureReport()
	improvement.Controls.ControlPlaneProtected = true
	improvement.KnownGaps = knownGaps(improvement.Controls)
	improvement.AttackCases = securityAttackCases(improvement.Controls)
	if err := updateBaseline(path, improvement); err != nil {
		t.Fatal(err)
	}
	updated, err := readReport(path)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Controls.ControlPlaneProtected {
		t.Fatalf("updated controls = %+v", updated.Controls)
	}
}

func fixtureReport() report {
	value := controls{
		GuardValidatesBeforePolicy:     true,
		StrongSandboxFailsClosed:       true,
		PreparedPolicyIdentityVerified: true,
		ApprovalFingerprintBound:       true,
		ExactWorkspaceWritesBounded:    true,
		TeardownReceiptOwned:           true,
	}
	return report{
		SchemaVersion: schemaVersion,
		Stage:         stageSG0,
		Controls:      value,
		AttackCases:   securityAttackCases(value),
		KnownGaps:     knownGaps(value),
	}
}
