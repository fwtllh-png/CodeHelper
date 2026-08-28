package wire

import (
	"path/filepath"
	"reflect"
	"testing"

	memoryextension "github.com/fwtllh-png/CodeHelper/internal/adapter/extension/memory"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/persist/extensionplan"
	extensionapp "github.com/fwtllh-png/CodeHelper/internal/runtime/app/extension"
	runtimeextension "github.com/fwtllh-png/CodeHelper/internal/runtime/extension"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestExtensionSessionRestoresPlanAndAdvancesNextSnapshot(t *testing.T) {
	registry := memoryExtensionRegistry(t)
	storePath := filepath.Join(t.TempDir(), extensionplan.FileName)
	store, err := extensionplan.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	state, err := runtimeextension.NewStateStore(runtimeextension.StateStoreOptions{})
	if err != nil {
		t.Fatal(err)
	}
	permissionDigest := "permission-one"
	status := map[runtimeextension.ID]runtimeextension.OutcomeStatus{
		"memory": runtimeextension.OutcomeSucceeded,
	}
	runtime, err := extensionapp.New(extensionapp.Config{
		Registry: registry, State: state, PlanStore: store, Workspace: "/workspace",
		Permission: func() (string, error) { return permissionDigest, nil },
		Status:     status,
	})
	if err != nil {
		t.Fatal(err)
	}
	session := &extensionSession{
		runtime: runtime,
		receipts: []ContributionReceipt{{
			Contributor: "memory",
			Typed: &runtimeextension.Receipt{
				Extension: "memory", Kind: runtimeextension.KindTool,
				Status: runtimeextension.OutcomeSucceeded,
			},
		}},
	}
	first, err := session.SnapshotPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if first.Revision != 1 || len(first.Extensions) != 1 ||
		first.Extensions[0].ID != "builtin/memory" ||
		!first.Extensions[0].Enabled {
		t.Fatalf("first plan = %+v", first)
	}
	permissionDigest = "permission-two"
	second, err := session.SnapshotPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision != 2 || second.Digest == first.Digest {
		t.Fatalf("second plan = %+v, first = %+v", second, first)
	}
	if first.PermissionDigest != "permission-one" {
		t.Fatal("frozen first plan followed permission mutation")
	}
	reopened, err := extensionplan.Open(storePath)
	if err != nil {
		t.Fatal(err)
	}
	restartedRuntime, err := extensionapp.New(extensionapp.Config{
		Registry: registry, State: state, PlanStore: reopened, Workspace: "/workspace",
		Permission: func() (string, error) { return "permission-two", nil },
		Status:     status,
	})
	if err != nil {
		t.Fatal(err)
	}
	restarted := &extensionSession{runtime: restartedRuntime, receipts: session.receipts}
	restored, err := restarted.SnapshotPlan(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if restored.Revision != second.Revision || restored.Digest != second.Digest {
		t.Fatalf("restored plan = %+v, want %+v", restored, second)
	}
}

func TestPolicyDigestChangesWithPermissionCeiling(t *testing.T) {
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
	first, err := extensionapp.PolicyDigest(runtime)
	if err != nil {
		t.Fatal(err)
	}
	runtime.Permission = policy.PermissionNever
	second, err := extensionapp.PolicyDigest(runtime)
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("permission mutation did not change extension plan binding")
	}
}

func TestSessionHasOneExtensionStateOwner(t *testing.T) {
	sessionType := reflect.TypeFor[Session]()
	if _, ok := sessionType.FieldByName("extensions"); !ok {
		t.Fatal("Session does not own the extension runtime")
	}
	for _, legacy := range []string{"contributionReceipts"} {
		if _, ok := sessionType.FieldByName(legacy); ok {
			t.Errorf("Session retains legacy extension state field %q", legacy)
		}
	}
}

func memoryExtensionRegistry(t *testing.T) *runtimeextension.Registry {
	t.Helper()
	builder := runtimeextension.NewBuilder()
	if err := builder.Register(memoryextension.New(config.Memory{})); err != nil {
		t.Fatal(err)
	}
	registry, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	return registry
}
