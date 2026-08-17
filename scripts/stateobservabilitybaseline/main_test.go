package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryStateObservabilityBaseline(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	measured, err := measure(root, "test-commit")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := readReport(
		filepath.Join(root, "docs/state-observability-so0-baseline.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCandidate(baseline, measured); err != nil {
		t.Fatal(err)
	}
	if measured.Events.TotalKinds != measured.Events.TraitsCovered {
		t.Fatalf("event audit = %+v", measured.Events)
	}
	if len(measured.SessionMatrix) != 10 || len(measured.FailureMatrix) < 10 {
		t.Fatalf(
			"session matrix=%d failure matrix=%d",
			len(measured.SessionMatrix),
			len(measured.FailureMatrix),
		)
	}
}

func TestEventDurabilityMatchesTraits(t *testing.T) {
	audit, err := measureEvents()
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Mismatches) != 0 {
		t.Fatalf("durability mismatches = %+v", audit.Mismatches)
	}
}

func TestValidateCandidateAllowsClosingGaps(t *testing.T) {
	baseline := fixtureReport()
	candidate := fixtureReport()
	candidate.Contracts.EventPolicySingleSource = true
	candidate.Events.Mismatches = nil
	candidate.KnownGaps = knownGaps(candidate.Contracts)
	if err := validateCandidate(baseline, candidate); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCandidateRejectsContractRegression(t *testing.T) {
	baseline := fixtureReport()
	candidate := fixtureReport()
	candidate.Contracts.AtomicTerminalEnvelope = false
	err := validateCandidate(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "AtomicTerminalEnvelope") {
		t.Fatalf("validateCandidate() error = %v", err)
	}
}

func TestValidateCandidateRejectsNewDurabilityDrift(t *testing.T) {
	baseline := fixtureReport()
	candidate := fixtureReport()
	candidate.Events.Mismatches = append(
		candidate.Events.Mismatches,
		policyDrift{Kind: "usage", Declared: "retained"},
	)
	err := validateCandidate(baseline, candidate)
	if err == nil || !strings.Contains(err.Error(), "new event durability drift") {
		t.Fatalf("validateCandidate() error = %v", err)
	}
}

func fixtureReport() report {
	sessions := standardSessions()
	for index := range sessions {
		sessions[index].Measured = true
	}
	failures := failureCases()
	for index := range failures {
		failures[index].Measured = true
	}
	value := contracts{
		TurnKernelDigestAuthority:      true,
		AtomicTerminalEnvelope:         true,
		EventReservationReconciliation: true,
		UsageIdempotentProjection:      true,
		ContextLedgerAuthority:         true,
		WorkGraphFactReplay:            true,
		ExtensionDiagnosticsBounded:    true,
		PermissionDigestInReceipt:      true,
	}
	return report{
		SchemaVersion: schemaVersion,
		Stage:         stageSO0,
		Contracts:     value,
		Events: eventAudit{
			TotalKinds: 50, TraitsCovered: 50,
			Mismatches: []policyDrift{
				{Kind: "reasoning.delta", Declared: "retained"},
				{Kind: "turn.compaction", Declared: "retained"},
			},
		},
		Storage: storageMetrics{
			TurnState: 1, DomainFact: 1, TraceSpan: 1,
		},
		SessionMatrix: sessions,
		FailureMatrix: failures,
		KnownGaps:     knownGaps(value),
	}
}

func BenchmarkSO0EventPolicyAudit(b *testing.B) {
	for index := 0; index < b.N; index++ {
		if _, err := measureEvents(); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkSO0RepresentativeStorage(b *testing.B) {
	for index := 0; index < b.N; index++ {
		if _, _, err := measureStorage(); err != nil {
			b.Fatal(err)
		}
	}
}
