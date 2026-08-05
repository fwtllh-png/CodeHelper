package rlm_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/rlm"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func requirePython(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 unavailable")
	}
}

func newTestStore(t *testing.T, subQuery rlm.SubQueryClient, governor *rlm.Governor) *rlm.Store {
	t.Helper()
	workspaceRoot := t.TempDir()
	workspace, err := sandbox.NewWorkspace(workspaceRoot)
	if err != nil {
		t.Fatal(err)
	}
	backend, err := sandbox.BindPolicy(passthroughBackend{}, sandbox.Options{WorkspaceRoot: workspaceRoot})
	if err != nil {
		t.Fatal(err)
	}
	store, err := rlm.NewStore(rlm.StoreOptions{
		Root: filepath.Join(workspaceRoot, "rlm"), Backend: backend, Workspace: workspace,
		SubQuery: subQuery, Governor: governor,
	})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func TestSubQueryMapIndependent(t *testing.T) {
	requirePython(t)
	client := rlm.FuncSubQuery(func(_ context.Context, prompt, slice string) (string, error) {
		return prompt + "->" + slice, nil
	})
	store := newTestStore(t, client, rlm.NewGovernor(rlm.Limits{}))
	if _, err := store.Open("demo", "inline", "", "ctx", 0); err != nil {
		t.Fatal(err)
	}
	result, _, err := store.Eval(context.Background(), "demo", `
answers = sub_query_map("tag", ["a", "b"])
print("|".join(answers))
`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != "passed" {
		t.Fatalf("eval failed: %+v", result)
	}
	if !strings.Contains(result.Stdout, "tag->a|tag->b") {
		t.Fatalf("stdout=%q", result.Stdout)
	}
}

func TestSubQueryMapRejectsOverCap(t *testing.T) {
	requirePython(t)
	client := rlm.FuncSubQuery(func(_ context.Context, prompt, slice string) (string, error) {
		return slice, nil
	})
	store := newTestStore(t, client, rlm.NewGovernor(rlm.Limits{}))
	if _, err := store.Open("demo", "inline", "", "ctx", 0); err != nil {
		t.Fatal(err)
	}
	slices := make([]string, 17)
	for i := range slices {
		slices[i] = `"x"`
	}
	code := "sub_query_map('p', [" + strings.Join(slices, ",") + "])"
	result, _, err := store.Eval(context.Background(), "demo", code)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification == "passed" {
		t.Fatalf("expected fail-closed for 17 items: %+v", result)
	}
	if !strings.Contains(result.Stderr, "at most 16") {
		t.Fatalf("stderr=%q", result.Stderr)
	}
}

func TestSubQueryBatchIndependent(t *testing.T) {
	requirePython(t)
	client := rlm.FuncSubQuery(func(_ context.Context, prompt, slice string) (string, error) {
		return prompt + ":" + slice, nil
	})
	store := newTestStore(t, client, rlm.NewGovernor(rlm.Limits{}))
	if _, err := store.Open("demo", "inline", "", "ctx", 0); err != nil {
		t.Fatal(err)
	}
	result, _, err := store.Eval(context.Background(), "demo", `
answers = sub_query_batch("sum", ["a", "b", "c"])
print("|".join(answers))
`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification != "passed" {
		t.Fatalf("eval failed: %+v", result)
	}
	if !strings.Contains(result.Stdout, "sum:a|sum:b|sum:c") {
		t.Fatalf("stdout=%q", result.Stdout)
	}
}

func TestSubQueryBatchRejectsOverCap(t *testing.T) {
	requirePython(t)
	client := rlm.FuncSubQuery(func(_ context.Context, prompt, slice string) (string, error) {
		return slice, nil
	})
	store := newTestStore(t, client, rlm.NewGovernor(rlm.Limits{}))
	if _, err := store.Open("demo", "inline", "", "ctx", 0); err != nil {
		t.Fatal(err)
	}
	slices := make([]string, 17)
	for i := range slices {
		slices[i] = `"x"`
	}
	code := "sub_query_batch('p', [" + strings.Join(slices, ",") + "])"
	result, _, err := store.Eval(context.Background(), "demo", code)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification == "passed" {
		t.Fatalf("expected fail-closed for 17 slices: %+v", result)
	}
	if !strings.Contains(result.Stderr, "at most 16") {
		t.Fatalf("stderr=%q", result.Stderr)
	}
}

func TestSubQueryUnavailableWithoutClient(t *testing.T) {
	requirePython(t)
	store := newTestStore(t, nil, nil)
	if _, err := store.Open("demo", "inline", "", "ctx", 0); err != nil {
		t.Fatal(err)
	}
	result, _, err := store.Eval(context.Background(), "demo", `sub_query("hello")`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification == "passed" {
		t.Fatalf("expected unavailable error: %+v", result)
	}
	if !strings.Contains(result.Stderr, "no SubQueryClient configured") {
		t.Fatalf("stderr=%q", result.Stderr)
	}
}

func TestSubQueryTimeout(t *testing.T) {
	requirePython(t)
	client := rlm.FuncSubQuery(func(ctx context.Context, prompt, slice string) (string, error) {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(3 * time.Second):
			return "late", nil
		}
	})
	store := newTestStore(t, client, rlm.NewGovernor(rlm.Limits{}))
	session, err := store.Open("demo", "inline", "", "ctx", 0)
	if err != nil {
		t.Fatal(err)
	}
	cfg := session.Config
	cfg.SubQueryTimeoutSecs = 1
	cfg.EvalTimeoutSecs = 10
	if _, err := store.ApplyConfig("demo", cfg); err != nil {
		t.Fatal(err)
	}
	result, _, err := store.Eval(context.Background(), "demo", `print(sub_query("slow"))`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification == "passed" {
		t.Fatalf("expected timeout failure: %+v", result)
	}
}

func TestSubQueryGovernorConcurrency(t *testing.T) {
	requirePython(t)
	var inFlight atomic.Int32
	var maxSeen atomic.Int32
	client := rlm.FuncSubQuery(func(ctx context.Context, prompt, slice string) (string, error) {
		cur := inFlight.Add(1)
		for {
			prev := maxSeen.Load()
			if cur <= prev || maxSeen.CompareAndSwap(prev, cur) {
				break
			}
		}
		time.Sleep(50 * time.Millisecond)
		inFlight.Add(-1)
		return slice, nil
	})
	gov := rlm.NewGovernor(rlm.Limits{MaxConcurrency: 2})
	store := newTestStore(t, client, gov)
	if _, err := store.Open("demo", "inline", "", "ctx", 0); err != nil {
		t.Fatal(err)
	}
	result, _, err := store.Eval(context.Background(), "demo", `
try:
    print(sub_query_batch("p", ["1","2","3","4"]))
except Exception as exc:
    print("ERR:"+str(exc))
`)
	if err != nil {
		t.Fatal(err)
	}
	if result.Classification == "passed" && !strings.Contains(result.Stdout, "ERR:") {
		if maxSeen.Load() > 2 {
			t.Fatalf("max in-flight %d exceeds governor concurrency 2; stdout=%q", maxSeen.Load(), result.Stdout)
		}
	}
}

type passthroughBackend struct{}

func (passthroughBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "fixture", Backend: "passthrough",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (passthroughBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	return command, nil
}
