package admission_test

import (
	"errors"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/admission"
)

func TestGovernorEnforcesDepthConcurrencyAndTokenSpend(t *testing.T) {
	governor := admission.NewGovernor(admission.Limits{
		MaxTokens: 10, MaxDepth: 1, MaxConcurrency: 1,
	})
	if _, err := governor.Admit(2, 0, 0); !errors.Is(err, admission.ErrDepthBudget) {
		t.Fatalf("depth error = %v", err)
	}
	lease, err := governor.Admit(1, 4, 0.5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := governor.Admit(1, 0, 0); !errors.Is(err, admission.ErrConcurrency) {
		t.Fatalf("concurrency error = %v", err)
	}
	governor.Release(lease)
	if err := governor.Record(7, 0); !errors.Is(err, admission.ErrTokenBudget) {
		t.Fatalf("token error = %v", err)
	}
	snapshot := governor.Snapshot()
	if snapshot.SpentTokens != 11 || snapshot.SpentCostUSD != 0.5 ||
		snapshot.InFlight != 0 || snapshot.MaxDepthSeen != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}

func TestGovernorEnforcesCostSpend(t *testing.T) {
	governor := admission.NewGovernor(admission.Limits{MaxCostUSD: 2})
	if err := governor.Record(0, 2.5); !errors.Is(err, admission.ErrCostBudget) {
		t.Fatalf("cost error = %v", err)
	}
}

func TestGovernorAllowsUnboundedZeroLimits(t *testing.T) {
	governor := admission.NewGovernor(admission.Limits{})
	lease, err := governor.Admit(100, 1000, 25)
	if err != nil {
		t.Fatal(err)
	}
	governor.Release(lease)
	if err := governor.Record(1000, 25); err != nil {
		t.Fatal(err)
	}
}
