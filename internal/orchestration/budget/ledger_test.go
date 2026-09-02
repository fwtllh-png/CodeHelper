package budget_test

import (
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/fwtllh-png/QCode/internal/orchestration/budget"
)

func TestHierarchicalReservationChargeAndRefund(t *testing.T) {
	ledger := budget.NewLedger()
	for _, scope := range []struct {
		id, parent string
		limits     budget.Limits
	}{
		{"workspace", "", budget.Limits{MaxTokens: 100, MaxCostMicros: 1000, MaxSlots: 2}},
		{"session", "workspace", budget.Limits{MaxTokens: 80, MaxCostMicros: 800, MaxSlots: 2}},
		{"run", "session", budget.Limits{MaxTokens: 60, MaxCostMicros: 600, MaxSlots: 2}},
	} {
		if err := ledger.EnsureScope(scope.id, scope.parent, scope.limits); err != nil {
			t.Fatal(err)
		}
	}
	first := budget.Reservation{
		ID: "first", ScopeID: "run",
		Amount: budget.Usage{Tokens: 30, CostMicros: 300, Slots: 1},
	}
	if err := ledger.Reserve(first); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Settle(
		first.ID,
		budget.Usage{Tokens: 20, CostMicros: 200},
	); err != nil {
		t.Fatal(err)
	}
	second := budget.Reservation{
		ID: "second", ScopeID: "run",
		Amount: budget.Usage{Tokens: 40, CostMicros: 400, Slots: 1},
	}
	if err := ledger.Reserve(second); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Refund(second.ID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"run", "session", "workspace"} {
		snapshot, err := ledger.Snapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Reserved != (budget.Usage{}) ||
			snapshot.Spent.Tokens != 20 ||
			snapshot.Spent.CostMicros != 200 {
			t.Fatalf("scope %s = %+v", id, snapshot)
		}
	}
}

func TestParentLimitRejectsChildReservationAtomically(t *testing.T) {
	ledger := budget.NewLedger()
	if err := ledger.EnsureScope(
		"workspace",
		"",
		budget.Limits{MaxTokens: 10, MaxSlots: 1},
	); err != nil {
		t.Fatal(err)
	}
	if err := ledger.EnsureScope(
		"run",
		"workspace",
		budget.Limits{MaxTokens: 100, MaxSlots: 10},
	); err != nil {
		t.Fatal(err)
	}
	if err := ledger.Reserve(budget.Reservation{
		ID: "too-large", ScopeID: "run",
		Amount: budget.Usage{Tokens: 11, Slots: 1},
	}); !errors.Is(err, budget.ErrExhausted) {
		t.Fatalf("reserve error = %v", err)
	} else {
		var exhausted *budget.ExhaustedError
		if !errors.As(err, &exhausted) ||
			exhausted.ScopeID != "workspace" ||
			exhausted.Resource != budget.ResourceTokens ||
			exhausted.Used != 11 ||
			exhausted.Limit != 10 {
			t.Fatalf("typed exhaustion = %+v", exhausted)
		}
	}
	for _, id := range []string{"workspace", "run"} {
		snapshot, err := ledger.Snapshot(id)
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.Reserved != (budget.Usage{}) {
			t.Fatalf("failed reservation changed %s: %+v", id, snapshot)
		}
	}
}

func TestConcurrentReservationsNeverOversellSlots(t *testing.T) {
	ledger := budget.NewLedger()
	if err := ledger.EnsureScope(
		"run",
		"",
		budget.Limits{MaxSlots: 4},
	); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	var mu sync.Mutex
	admitted := 0
	for index := range 64 {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			err := ledger.Reserve(budget.Reservation{
				ID:      fmt.Sprintf("reservation-%d", index),
				ScopeID: "run", Amount: budget.Usage{Slots: 1},
			})
			if err == nil {
				mu.Lock()
				admitted++
				mu.Unlock()
				return
			}
			if !errors.Is(err, budget.ErrExhausted) {
				t.Errorf("reserve %d: %v", index, err)
			}
		}(index)
	}
	wait.Wait()
	if admitted != 4 {
		t.Fatalf("admitted = %d, want 4", admitted)
	}
	snapshot, err := ledger.Snapshot("run")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Reserved.Slots != 4 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
