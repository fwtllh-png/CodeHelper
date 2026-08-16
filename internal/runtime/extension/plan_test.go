package extension

import (
	"strings"
	"testing"
)

func TestResolverUsesSourcePriorityAndRejectsAmbiguity(t *testing.T) {
	builtin := fixtureSource(SourceBuiltin, "builtin", 10, 1, "builtin-digest", "1.0.0")
	managed := fixtureSource(SourceManaged, "managed", 40, 2, "managed-digest", "2.0.0")
	resolved, err := (Resolver{}).Resolve(t.Context(), builtin, managed)
	if err != nil {
		t.Fatal(err)
	}
	if len(resolved) != 1 || resolved[0].Version != "2.0.0" ||
		resolved[0].Source.ID != "managed" {
		t.Fatalf("resolved = %+v", resolved)
	}
	duplicate := fixtureSource(SourceUser, "duplicate", 40, 1, "duplicate-digest", "3.0.0")
	if _, err := (Resolver{}).Resolve(
		t.Context(), managed, duplicate,
	); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous resolution error = %v", err)
	}
}

func TestCompilerBindsSourceAndPermissionDigests(t *testing.T) {
	source := fixtureSource(SourceBuiltin, "builtin", 10, 1, "source-one", "1.0.0")
	candidates, err := (Resolver{}).Resolve(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	first, err := (Compiler{}).Compile(candidates, "permission-one")
	if err != nil {
		t.Fatal(err)
	}
	permissionChanged, err := (Compiler{}).Compile(candidates, "permission-two")
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == permissionChanged.Digest {
		t.Fatal("permission digest did not affect extension plan")
	}
	source.Ref.Revision++
	source.Ref.Digest = "source-two"
	candidates, err = (Resolver{}).Resolve(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	sourceChanged, err := (Compiler{}).Compile(candidates, "permission-one")
	if err != nil {
		t.Fatal(err)
	}
	if first.Digest == sourceChanged.Digest ||
		first.SourceDigest == sourceChanged.SourceDigest {
		t.Fatal("source revision did not affect extension plan")
	}
	if err := first.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestPlanSnapshotDoesNotFollowSourceMutation(t *testing.T) {
	source := fixtureSource(SourceBuiltin, "builtin", 10, 1, "source-one", "1.0.0")
	candidates, err := (Resolver{}).Resolve(t.Context(), source)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := (Compiler{}).Compile(candidates, "permission-one")
	if err != nil {
		t.Fatal(err)
	}
	source.Candidates[0].Version = "2.0.0"
	source.Candidates[0].Enabled = false
	if plan.Extensions[0].Version != "1.0.0" || !plan.Extensions[0].Enabled {
		t.Fatalf("plan followed mutable source: %+v", plan.Extensions[0])
	}
	clone := plan.Clone()
	clone.Extensions[0].Version = "mutated"
	if plan.Extensions[0].Version != "1.0.0" {
		t.Fatal("plan clone aliases extension candidates")
	}
}

func fixtureSource(
	kind SourceKind,
	id string,
	priority int,
	revision uint64,
	digest string,
	version string,
) StaticSource {
	ref := SourceRef{
		Kind: kind, ID: id, Priority: priority,
		Revision: revision, Digest: digest,
	}
	return StaticSource{
		Ref: ref,
		Candidates: []Candidate{{
			ID: "fixture", Kind: "plugin", Name: "fixture",
			Version: version, Digest: digest, Generation: revision,
			Enabled: true, Source: ref,
		}},
	}
}
