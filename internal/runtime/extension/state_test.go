package extension

import (
	"errors"
	"testing"
)

func TestStateStoreSupportsAllScopesWithCAS(t *testing.T) {
	store, err := NewStateStore(StateStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		scope   Scope
		scopeID string
	}{
		{scope: ScopeProcess},
		{scope: ScopeSession, scopeID: "session-1"},
		{scope: ScopeThread, scopeID: "thread-1"},
		{scope: ScopeTurn, scopeID: "turn-1"},
	} {
		key := StateKey{
			Extension: "fixture", Scope: test.scope, ScopeID: test.scopeID,
			Name: "value", Version: 1,
		}
		value, err := store.CompareAndSwap(t.Context(), "fixture", key, 0, []byte("one"))
		if err != nil {
			t.Fatal(err)
		}
		if value.Revision != 1 {
			t.Fatalf("%s revision = %d", test.scope, value.Revision)
		}
		value.Data[0] = 'X'
		loaded, ok, err := store.Load(t.Context(), "fixture", key)
		if err != nil || !ok || string(loaded.Data) != "one" {
			t.Fatalf("%s load = (%+v, %t, %v)", test.scope, loaded, ok, err)
		}
		if _, err := store.CompareAndSwap(
			t.Context(), "fixture", key, 0, []byte("stale"),
		); !errors.Is(err, ErrStateConflict) {
			t.Fatalf("%s stale CAS error = %v", test.scope, err)
		}
		if err := store.Delete(t.Context(), "fixture", key, 1); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStateStoreRejectsCrossExtensionAccess(t *testing.T) {
	store, err := NewStateStore(StateStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	key := StateKey{
		Extension: "owner", Scope: ScopeThread, ScopeID: "thread-1",
		Name: "private", Version: 1,
	}
	if _, err := store.CompareAndSwap(
		t.Context(), "intruder", key, 0, []byte("secret"),
	); !errors.Is(err, ErrStateCrossExtension) {
		t.Fatalf("cross-extension write error = %v", err)
	}
	if _, _, err := store.Load(
		t.Context(), "intruder", key,
	); !errors.Is(err, ErrStateCrossExtension) {
		t.Fatalf("cross-extension read error = %v", err)
	}
}

func TestStateStoreReturnsTypedBudgetFailure(t *testing.T) {
	store, err := NewStateStore(StateStoreOptions{Budgets: map[Scope]StateBudget{
		ScopeTurn: {MaxEntries: 1, MaxBytes: 4, MaxValueBytes: 3},
	}})
	if err != nil {
		t.Fatal(err)
	}
	key := StateKey{
		Extension: "fixture", Scope: ScopeTurn, ScopeID: "turn-1",
		Name: "value", Version: 1,
	}
	_, err = store.CompareAndSwap(t.Context(), "fixture", key, 0, []byte("four"))
	var budgetError *StateBudgetError
	if !errors.As(err, &budgetError) ||
		!errors.Is(err, ErrStateBudgetExceeded) ||
		budgetError.ValueLimit != 3 {
		t.Fatalf("budget error = %T %+v", err, err)
	}
}

func TestStateStoreClearsOneScopeIdentity(t *testing.T) {
	store, err := NewStateStore(StateStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	for _, scopeID := range []string{"turn-1", "turn-2"} {
		key := StateKey{
			Extension: "fixture", Scope: ScopeTurn, ScopeID: scopeID,
			Name: "value", Version: 1,
		}
		if _, err := store.CompareAndSwap(
			t.Context(), "fixture", key, 0, []byte(scopeID),
		); err != nil {
			t.Fatal(err)
		}
	}
	if removed := store.ClearScope(ScopeTurn, "turn-1"); removed != 1 {
		t.Fatalf("removed = %d", removed)
	}
	key := StateKey{
		Extension: "fixture", Scope: ScopeTurn, ScopeID: "turn-2",
		Name: "value", Version: 1,
	}
	if _, ok, err := store.Load(t.Context(), "fixture", key); err != nil || !ok {
		t.Fatalf("unrelated scope was cleared: ok=%t err=%v", ok, err)
	}
}
