package wire

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestExtensionControlIsIdempotentReplayableAndNonBlocking(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	writeControlSkill(t, workspace, "review")
	paths, err := ResolveExtensionPaths(ExtensionOptions{
		DataDir: filepath.Join(workspace, "data"), UserHome: home,
	}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	control, err := OpenExtensionControlPlane(paths, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	initial, err := control.Plane.Snapshot(
		t.Context(), protocol.ExtensionControlAll,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(initial.Extensions) != 1 ||
		initial.Extensions[0].Name != "review" ||
		!initial.Extensions[0].Enabled {
		t.Fatalf("initial projection = %+v", initial.Extensions)
	}

	channel, unsubscribe, err := control.Plane.Subscribe(1)
	if err != nil {
		t.Fatal(err)
	}
	defer unsubscribe()
	disable := controlOperation(
		"operation-1", protocol.ExtensionActionDisable, "review",
	)
	first, err := control.Plane.Submit(t.Context(), disable)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := control.Plane.Submit(t.Context(), disable)
	if err != nil {
		t.Fatal(err)
	}
	if !duplicate.Duplicate || duplicate.Revision != first.Revision {
		t.Fatalf("duplicate result = %+v, first = %+v", duplicate, first)
	}
	conflict := disable
	conflict.Action = protocol.ExtensionActionEnable
	if _, err := control.Plane.Submit(t.Context(), conflict); err == nil {
		t.Fatal("conflicting operation ID was accepted")
	}

	enable := controlOperation(
		"operation-2", protocol.ExtensionActionEnable, "review",
	)
	if _, err := control.Plane.Submit(t.Context(), enable); err != nil {
		t.Fatal(err)
	}
	select {
	case <-channel:
	case <-time.After(time.Second):
		t.Fatal("extension subscriber did not receive first event")
	}
	// The second event overflowed the size-one channel. Kernel progress above
	// completed and the slow subscriber is disconnected rather than blocking.
	if _, open := <-channel; open {
		t.Fatal("slow extension subscriber remained connected")
	}

	events, more, err := control.Plane.Replay(t.Context(), 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if more || len(events) != 2 {
		t.Fatalf("events = %+v more=%t", events, more)
	}
	replayed, err := protocol.ReduceExtensionControlEvents(events)
	if err != nil {
		t.Fatal(err)
	}
	current, err := control.Plane.Snapshot(
		t.Context(), protocol.ExtensionControlAll,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(replayed, current.Extensions) {
		t.Fatalf("replayed = %+v current = %+v", replayed, current.Extensions)
	}
	receiptsOperation := controlOperation(
		"receipts-query", protocol.ExtensionActionReceipts, "",
	)
	receipts, err := control.Plane.Submit(t.Context(), receiptsOperation)
	if err != nil {
		t.Fatal(err)
	}
	if len(receipts.Receipts) != 2 {
		t.Fatalf("receipts = %+v", receipts.Receipts)
	}
	healthOperation := controlOperation(
		"health-query", protocol.ExtensionActionHealth, "",
	)
	health, err := control.Plane.Submit(t.Context(), healthOperation)
	if err != nil {
		t.Fatal(err)
	}
	if health.Diagnostics == nil ||
		health.Diagnostics.Metrics.Operations < 7 ||
		health.Diagnostics.Metrics.Duplicates != 1 ||
		health.Diagnostics.Metrics.SubscriberDrops != 1 ||
		len(health.Diagnostics.Alerts) != 2 {
		t.Fatalf("health diagnostics = %+v", health.Diagnostics)
	}
}

func TestExtensionControlPluginLintReturnsDetail(t *testing.T) {
	workspace := t.TempDir()
	bundle := filepath.Join(workspace, "fixture")
	writeControlPlugin(t, bundle)
	paths, err := ResolveExtensionPaths(ExtensionOptions{
		DataDir:  filepath.Join(workspace, "data"),
		UserHome: t.TempDir(),
	}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	control, err := OpenExtensionControlPlane(paths, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	operation, err := protocol.NewExtensionControlOperation(
		protocol.ExtensionControlPlugin,
		protocol.ExtensionActionLint,
	)
	if err != nil {
		t.Fatal(err)
	}
	operation.Name = bundle
	result, err := control.Plane.Submit(t.Context(), operation)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Detail) == 0 {
		t.Fatal("plugin lint omitted compiled detail")
	}
}

func TestExtensionControlProjectsDurablePluginSourceAndTrust(t *testing.T) {
	workspace := t.TempDir()
	writeControlPlugin(
		t,
		filepath.Join(workspace, ".codehelper", "plugins", "lint-fixture"),
	)
	paths, err := ResolveExtensionPaths(ExtensionOptions{
		DataDir:  filepath.Join(workspace, "data"),
		UserHome: t.TempDir(),
	}, workspace)
	if err != nil {
		t.Fatal(err)
	}
	control, err := OpenExtensionControlPlane(paths, workspace)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	trust, err := protocol.NewExtensionControlOperation(
		protocol.ExtensionControlPlugin,
		protocol.ExtensionActionTrust,
	)
	if err != nil {
		t.Fatal(err)
	}
	trust.Name = "lint-fixture"
	if _, err := control.Plane.Submit(t.Context(), trust); err != nil {
		t.Fatal(err)
	}
	result, err := control.Plane.Snapshot(
		t.Context(), protocol.ExtensionControlPlugin,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Extensions) != 1 ||
		result.Extensions[0].Source != "workspace" ||
		result.Extensions[0].Trust != pluginruntime.TrustUnsignedLocal {
		t.Fatalf("plugin projection = %+v", result.Extensions)
	}
}

func writeControlPlugin(t *testing.T, bundle string) {
	t.Helper()
	if err := os.MkdirAll(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := []byte("#!/bin/sh\nprintf 'ok\\n'\n")
	if err := os.WriteFile(
		filepath.Join(bundle, "run.sh"), executable, 0o700,
	); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(executable)
	manifest := pluginruntime.Manifest{
		SchemaVersion: pluginruntime.ManifestSchemaV1,
		Name:          "lint-fixture", Generation: 1, Executable: "run.sh",
		ExecutableSHA256: hex.EncodeToString(sum[:]),
		Capabilities: pluginruntime.CapabilityInventory{
			Tools: []string{"plugin_run"}, FilesystemRoots: []string{"workspace"},
			AllowProcess: true,
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(bundle, pluginruntime.ManifestName), data, 0o600,
	); err != nil {
		t.Fatal(err)
	}
}

func controlOperation(
	id string,
	action protocol.ExtensionControlAction,
	name string,
) protocol.ExtensionControlOperation {
	return protocol.ExtensionControlOperation{
		Version: protocol.Version, ID: id,
		Kind: protocol.ExtensionControlSkill, Action: action, Name: name,
		CreatedAt: time.Now().UTC(),
	}
}

func writeControlSkill(t *testing.T, workspace, name string) {
	t.Helper()
	directory := filepath.Join(workspace, ".agents", "skills", name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	content := "---\nname: " + name +
		"\ndescription: Review code changes.\n---\nInstructions.\n"
	if err := os.WriteFile(
		filepath.Join(directory, "SKILL.md"), []byte(content), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}
