package extensionplan

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
)

func TestStoreRestoresRevisionForIdenticalPlanAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	plan := fixturePlan(t, 1, "source-one", "permission-one")
	receipt, err := first.Commit(t.Context(), "/workspace", plan)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := reopened.Commit(t.Context(), "/workspace", plan)
	if err != nil {
		t.Fatal(err)
	}
	if restored.PlanRevision != receipt.PlanRevision ||
		restored.PlanDigest != receipt.PlanDigest ||
		!restored.CommittedAt.Equal(receipt.CommittedAt) {
		t.Fatalf("restored receipt = %+v, want %+v", restored, receipt)
	}
	frozen, err := plan.WithRevision(restored.PlanRevision)
	if err != nil {
		t.Fatal(err)
	}
	if frozen.Digest != receipt.PlanDigest {
		t.Fatal("restored plan digest changed")
	}
}

func TestStoreAdvancesRevisionWhenResolvedPlanChanges(t *testing.T) {
	store, err := Open(filepath.Join(t.TempDir(), FileName))
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Commit(
		t.Context(), "/workspace", fixturePlan(t, 1, "source-one", "permission-one"),
	)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.Commit(
		t.Context(), "/workspace", fixturePlan(t, 2, "source-two", "permission-one"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if second.PlanRevision != first.PlanRevision+1 ||
		second.PlanDigest == first.PlanDigest {
		t.Fatalf("plan receipts = first=%+v second=%+v", first, second)
	}
}

func TestStoreRejectsWorkspaceAndSymlinkConfusion(t *testing.T) {
	root := t.TempDir()
	store, err := Open(filepath.Join(root, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(
		t.Context(), "/workspace-a", fixturePlan(t, 1, "source", "permission"),
	); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Commit(
		t.Context(), "/workspace-b", fixturePlan(t, 1, "source", "permission"),
	); !errors.Is(err, ErrPlanConflict) {
		t.Fatalf("workspace conflict error = %v", err)
	}
	link := filepath.Join(t.TempDir(), FileName)
	if err := os.Symlink(store.path, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := Open(link); err == nil {
		t.Fatal("symlink plan receipt was accepted")
	}
}

func fixturePlan(
	t *testing.T,
	revision uint64,
	sourceDigest string,
	permissionDigest string,
) extension.Plan {
	t.Helper()
	ref := extension.SourceRef{
		Kind: extension.SourceBuiltin, ID: "builtin", Priority: 10,
		Revision: revision, Digest: sourceDigest,
	}
	resolved, err := (extension.Resolver{}).Resolve(t.Context(), extension.StaticSource{
		Ref: ref,
		Candidates: []extension.Candidate{{
			ID: "fixture", Kind: "builtin", Name: "fixture",
			Version: "builtin", Digest: sourceDigest, Generation: revision,
			Enabled: true, Source: ref,
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	plan, err := (extension.Compiler{}).Compile(resolved, permissionDigest)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}
