package main

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryToolExecutionBaseline(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	measured, err := measure(root, "test-commit")
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := readReport(
		filepath.Join(root, "docs/tool-execution-ex0-baseline.json"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateCandidate(baseline, measured); err != nil {
		t.Fatal(err)
	}
	if measured.Catalog.ModelVisibleExecutionTools == 0 ||
		measured.Catalog.InputSchemaBytes == 0 {
		t.Fatalf("catalog metrics = %+v", measured.Catalog)
	}
	if !measured.Risks.ForegroundOutputBounded {
		t.Fatalf("EX1 bounded output was not detected: %+v", measured.Risks)
	}
	if measured.Risks.ApprovalWaitHoldsAdmission {
		t.Fatalf("EX2 approval admission split was not detected: %+v", measured.Risks)
	}
	if measured.Risks.SecurityReadsResultMetadata ||
		measured.Risks.CancellationLacksDisposition {
		t.Fatalf("EX2 typed execution core was not detected: %+v", measured.Risks)
	}
	if measured.Risks.SessionOwnerEnforced ||
		measured.Risks.EventDrivenSessionWait ||
		measured.Risks.UnifiedProcessProtocol {
		t.Fatalf("remaining EX0 risks were not reproduced: %+v", measured.Risks)
	}
}

func TestValidateCandidateAcceptsImprovements(t *testing.T) {
	baseline := fixtureReport()
	candidate := fixtureReport()
	candidate.Catalog.ModelVisibleExecutionTools--
	candidate.Catalog.InputSchemaBytes--
	candidate.Catalog.SerialExecutionTools--
	candidate.Risks.ForegroundOutputBounded = true
	candidate.Risks.ApprovalWaitHoldsAdmission = false
	candidate.Risks.SecurityReadsResultMetadata = false
	candidate.Risks.CancellationLacksDisposition = false
	candidate.Risks.SessionOwnerEnforced = true
	candidate.Risks.EventDrivenSessionWait = true
	candidate.Risks.UnifiedProcessProtocol = true
	if err := validateCandidate(baseline, candidate); err != nil {
		t.Fatal(err)
	}
}

func TestValidateCandidateRejectsSurfaceAndSafetyRegressions(t *testing.T) {
	tests := []struct {
		name   string
		change func(*report)
		want   string
	}{
		{
			name: "tool count",
			change: func(value *report) {
				value.Catalog.ModelVisibleExecutionTools++
			},
			want: "model-visible execution tools regressed",
		},
		{
			name: "schema bytes",
			change: func(value *report) {
				value.Catalog.InputSchemaBytes++
			},
			want: "execution schema bytes regressed",
		},
		{
			name: "serial tools",
			change: func(value *report) {
				value.Catalog.SerialExecutionTools++
			},
			want: "serial execution tools regressed",
		},
		{
			name: "claims",
			change: func(value *report) {
				value.Contracts.ResourceClaimsEnforced = false
			},
			want: "safety contract regressed",
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			candidate := fixtureReport()
			testCase.change(&candidate)
			err := validateCandidate(fixtureReport(), candidate)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("validateCandidate() error = %v", err)
			}
		})
	}
}

func fixtureReport() report {
	return report{
		SchemaVersion: schemaVersion,
		Stage:         stageEX0,
		Catalog: catalogMetrics{
			ModelVisibleExecutionTools: 15,
			InputSchemaBytes:           100,
			SerialExecutionTools:       15,
		},
		Contracts: contractMetrics{
			CatalogAuthorityBound:  true,
			ResourceClaimsEnforced: true,
			ResultHandlesAvailable: true,
			TypedAdapterAvailable:  true,
		},
	}
}
