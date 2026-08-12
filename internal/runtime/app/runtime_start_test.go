package app

import (
	"errors"
	"testing"
)

func TestPreparedRuntimeDoesNotAcceptWorkBeforeStart(t *testing.T) {
	runtime, err := PrepareRuntime(t.Context(), Options{Engine: &testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.Submit(t.Context(), startOperation(t, 1)); !errors.Is(err, ErrClosed) {
		t.Fatalf("prepared Submit error = %v, want ErrClosed", err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Start(t.Context()); err != nil {
		t.Fatalf("idempotent Start error = %v", err)
	}
	if err := runtime.Submit(t.Context(), startOperation(t, 2)); err != nil {
		t.Fatal(err)
	}
	closeRuntime(t, runtime)
}

func TestPreparedRuntimeCanCloseWithoutStart(t *testing.T) {
	runtime, err := PrepareRuntime(t.Context(), Options{Engine: &testEngine{}})
	if err != nil {
		t.Fatal(err)
	}
	closeRuntime(t, runtime)
	if !runtime.Snapshot(t.Context()).Closed {
		t.Fatal("prepared Runtime did not close")
	}
	if err := runtime.Start(t.Context()); err == nil {
		t.Fatal("closed prepared Runtime started")
	}
}
