package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestNamespacedNameAvoidsNormalizedPluginCollisions(t *testing.T) {
	first := NamespacedName("acme/review")
	second := NamespacedName("acme review")
	if first == second {
		t.Fatalf("normalized plugin names collided: %q", first)
	}
	if first == "plugin_run" || second == "plugin_run" {
		t.Fatal("lifecycle plugin retained legacy fixed tool name")
	}
}

func TestAdapterTracksDurableEnableReplaceDisableAndRevoke(t *testing.T) {
	base := t.TempDir()
	plugins := filepath.Join(base, "plugins")
	workspace := filepath.Join(base, "workspace")
	statePath := filepath.Join(base, "state", "plugins.json")
	staging := filepath.Join(base, "staging")
	for _, directory := range []string{
		plugins, workspace, filepath.Dir(statePath),
	} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeLifecycleBundle(t, plugins, "v1", 1)
	control := openLifecycleRegistry(t, plugins, staging, statePath, workspace)
	defer control.Close()
	runtimeRegistry := openLifecycleRegistry(t, plugins, staging, statePath, workspace)
	defer runtimeRegistry.Close()
	tools := tool.NewRegistry(nil, nil)
	adapter, err := NewAdapter(tools, runtimeRegistry)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	name := NamespacedName("fixture")
	assertToolRevoked(t, tools, name)

	if _, err := control.Trust("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := control.Enable("fixture"); err != nil {
		t.Fatal(err)
	}
	waitForRefreshPending(t, adapter)
	assertToolRevoked(t, tools, name)
	if err := adapter.Sync(); err != nil {
		t.Fatal(err)
	}
	waitForToolOutput(t, tools, name, "v1")
	before, err := tools.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	beforeEntry, ok := before.Lookup(name)
	if !ok {
		t.Fatal("enabled plugin tool is missing")
	}
	beforeBinding, ok := before.Binding(name)
	if !ok {
		t.Fatal("enabled plugin tool binding is missing")
	}

	writeLifecycleBundle(t, plugins, "v2", 2)
	if _, err := control.Trust("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Sync(); err != nil {
		t.Fatal(err)
	}
	waitForToolRevoked(t, tools, name)
	if err := control.Enable("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Sync(); err != nil {
		t.Fatal(err)
	}
	waitForToolOutput(t, tools, name, "v2")
	after, err := tools.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	afterEntry, ok := after.Lookup(name)
	if !ok || afterEntry.Revision <= beforeEntry.Revision {
		t.Fatalf("replacement entry = %+v, before = %+v", afterEntry, beforeEntry)
	}
	if _, _, _, err := tools.ResolveBound(
		name,
		beforeBinding,
	); !errors.Is(err, tool.ErrCatalogStale) {
		t.Fatalf("old binding error = %v, want catalog stale", err)
	}

	if err := control.Disable("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Sync(); err != nil {
		t.Fatal(err)
	}
	waitForToolRevoked(t, tools, name)
	if err := control.Enable("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Sync(); err != nil {
		t.Fatal(err)
	}
	waitForToolOutput(t, tools, name, "v2")
	if err := control.Revoke("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Sync(); err != nil {
		t.Fatal(err)
	}
	waitForToolRevoked(t, tools, name)
}

func TestAdapterSwitchesSignedUpdateAndRollbackWhileOldExecutorDrains(t *testing.T) {
	fixture := newSignedLifecycleFixture(t)
	fixture.addScript(t, "1.0.0", 1, "#!/bin/sh\nsleep 0.3\nprintf 'v1'\n")
	fixture.add(t, "2.0.0", 2, "v2")
	fixture.writeIndex(t, 1, "1.0.0")
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	statePath := filepath.Join(base, "state", "plugins.json")
	staging := filepath.Join(base, "staging")
	cache := filepath.Join(base, "cache")
	for _, directory := range []string{workspace, filepath.Dir(statePath)} {
		if err := os.MkdirAll(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	control := openSignedLifecycleRegistry(
		t, fixture, workspace, statePath, staging, cache,
	)
	defer control.Close()
	runtimeRegistry := openSignedLifecycleRegistry(
		t, fixture, workspace, statePath, staging, cache,
	)
	defer runtimeRegistry.Close()
	tools := tool.NewRegistry(nil, nil)
	adapter, err := NewAdapter(tools, runtimeRegistry)
	if err != nil {
		t.Fatal(err)
	}
	defer adapter.Close()
	name := NamespacedName("fixture")

	if _, err := control.Install(t.Context(), "fixture", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Sync(); err != nil {
		t.Fatal(err)
	}
	waitForToolOutput(t, tools, name, "v1")
	_, _, oldExecutor, err := tools.Resolve(name)
	if err != nil {
		t.Fatal(err)
	}
	oldDone := make(chan struct {
		result tool.Result
		err    error
	}, 1)
	go func() {
		result, executeErr := oldExecutor.Execute(
			t.Context(), json.RawMessage(`{}`),
		)
		oldDone <- struct {
			result tool.Result
			err    error
		}{result: result, err: executeErr}
	}()
	time.Sleep(50 * time.Millisecond)

	fixture.writeIndex(t, 2, "2.0.0")
	if _, err := control.Update(t.Context(), "fixture", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Sync(); err != nil {
		t.Fatal(err)
	}
	waitForToolOutput(t, tools, name, "v2")
	oldCall := <-oldDone
	if oldCall.err != nil || strings.TrimSpace(oldCall.result.Content) != "v1" {
		t.Fatalf(
			"old executor did not drain: result=%+v err=%v",
			oldCall.result, oldCall.err,
		)
	}
	if _, err := oldExecutor.Execute(
		t.Context(), json.RawMessage(`{}`),
	); err == nil || !strings.Contains(err.Error(), "replaced or revoked") {
		t.Fatalf("retired executor accepted a new call: %v", err)
	}

	if _, err := control.Rollback("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Sync(); err != nil {
		t.Fatal(err)
	}
	waitForToolOutput(t, tools, name, "v1")
	_, _, rollbackExecutor, err := tools.Resolve(name)
	if err != nil {
		t.Fatal(err)
	}
	revokeDone := make(chan error, 1)
	go func() {
		_, executeErr := rollbackExecutor.Execute(
			t.Context(), json.RawMessage(`{}`),
		)
		revokeDone <- executeErr
	}()
	time.Sleep(50 * time.Millisecond)
	if err := control.SecurityRevoke("fixture"); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Sync(); err != nil {
		t.Fatal(err)
	}
	waitForToolRevoked(t, tools, name)
	select {
	case executeErr := <-revokeDone:
		if executeErr == nil {
			t.Fatal("security revoke did not cancel the in-flight tool call")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("security revoke did not promptly cancel the in-flight tool call")
	}
}

func openLifecycleRegistry(
	t *testing.T,
	plugins, staging, statePath, workspace string,
) *pluginruntime.Registry {
	t.Helper()
	registry, err := pluginruntime.NewRegistry(pluginruntime.RegistryConfig{
		Discovery:   pluginruntime.DiscoveryOptions{WorkspaceRoot: plugins},
		StagingRoot: staging, StatePath: statePath, WorkspaceRoot: workspace,
		Backend: lifecycleTestBackend{}, WatchInterval: 5 * time.Millisecond,
		Now: func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func writeLifecycleBundle(
	t *testing.T,
	root, output string,
	generation uint64,
) {
	t.Helper()
	bundle := filepath.Join(root, "fixture")
	if err := os.RemoveAll(bundle); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := []byte("#!/bin/sh\nprintf '" + output + "'\n")
	if err := os.WriteFile(filepath.Join(bundle, "run.sh"), executable, 0o700); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(executable)
	manifest := pluginruntime.Manifest{
		SchemaVersion: pluginruntime.ManifestSchemaV1,
		Name:          "fixture", Executable: "run.sh",
		ExecutableSHA256: hex.EncodeToString(sum[:]),
		Generation:       generation,
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

func waitForToolOutput(
	t *testing.T,
	registry *tool.Registry,
	name, expected string,
) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		result, err := registry.Execute(t.Context(), tool.Call{
			Name: name, Authorized: true, Arguments: json.RawMessage(`{}`),
		})
		if err == nil && strings.TrimSpace(result.Content) == expected {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("tool %q did not produce %q; adapter error = %v", name, expected, nil)
}

func waitForRefreshPending(t *testing.T, adapter *Adapter) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if adapter.RefreshPending() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("plugin watcher did not request a refresh operation")
}

func waitForToolRevoked(t *testing.T, registry *tool.Registry, name string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, _, _, err := registry.Resolve(name); errors.Is(err, tool.ErrToolRevoked) {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("tool %q was not revoked", name)
}

func assertToolRevoked(t *testing.T, registry *tool.Registry, name string) {
	t.Helper()
	if _, _, _, err := registry.Resolve(name); !errors.Is(err, tool.ErrUnknownTool) {
		t.Fatalf("initial tool error = %v, want unknown", err)
	}
}

type lifecycleTestBackend struct{}

func (lifecycleTestBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "test", Backend: "passthrough",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (lifecycleTestBackend) Prepare(
	_ context.Context,
	command sandbox.Command,
) (sandbox.Command, error) {
	command.PreparedReadOnly = command.WorkspaceReadOnly
	command.PreparedWritePaths = append(
		[]string(nil),
		command.WorkspaceWritePaths...,
	)
	return command, nil
}

type signedLifecycleFixture struct {
	root      string
	indexPath string
	public    ed25519.PublicKey
	private   ed25519.PrivateKey
	releases  map[string]pluginruntime.RegistryRelease
	artifacts map[string][]byte
}

func newSignedLifecycleFixture(t *testing.T) *signedLifecycleFixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	return &signedLifecycleFixture{
		root: root, indexPath: filepath.Join(root, "index.json"),
		public: public, private: private,
		releases:  make(map[string]pluginruntime.RegistryRelease),
		artifacts: make(map[string][]byte),
	}
}

func (f *signedLifecycleFixture) add(
	t *testing.T,
	version string,
	generation uint64,
	output string,
) {
	t.Helper()
	f.addScript(
		t, version, generation, "#!/bin/sh\nprintf '"+output+"'\n",
	)
}

func (f *signedLifecycleFixture) addScript(
	t *testing.T,
	version string,
	generation uint64,
	script string,
) {
	t.Helper()
	executable := []byte(script)
	manifest := fmt.Sprintf(
		"schema_version = 1\nname = \"fixture\"\nversion = %q\n"+
			"publisher = \"publisher.test\"\ncodehelper = \">=0.4.0 <0.5.0\"\n"+
			"executable = \"run.sh\"\nexecutable_sha256 = %q\n"+
			"generation = %d\n\n[capabilities]\ntools = [\"plugin_run\"]\n"+
			"filesystem_roots = [\"workspace\"]\nnetwork_hosts = []\n"+
			"allow_process = true\n",
		version, hashLifecycleBytes(executable), generation,
	)
	artifact := lifecycleArchive(t, map[string]struct {
		body []byte
		mode int64
	}{
		pluginruntime.TOMLManifestName: {body: []byte(manifest), mode: 0o600},
		"run.sh":                       {body: executable, mode: 0o700},
	})
	capabilityHash, err := pluginruntime.HashCapabilities(
		pluginruntime.CapabilityInventory{
			Tools: []string{"plugin_run"}, FilesystemRoots: []string{"workspace"},
			AllowProcess: true,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	fileName := "fixture-" + version + ".tar.gz"
	release := pluginruntime.RegistryRelease{
		SchemaVersion: 1, Name: "fixture", Version: version,
		Generation: generation, Publisher: "publisher.test",
		Artifact: fileName, ArtifactSHA256: hashLifecycleBytes(artifact),
		ManifestSHA256:   hashLifecycleBytes([]byte(manifest)),
		CapabilitySHA256: capabilityHash,
	}
	release.Signature, err = pluginruntime.SignRegistryRelease(release, f.private)
	if err != nil {
		t.Fatal(err)
	}
	f.releases[version] = release
	f.artifacts[fileName] = artifact
	if err := os.WriteFile(filepath.Join(f.root, fileName), artifact, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *signedLifecycleFixture) writeIndex(
	t *testing.T,
	generation uint64,
	versions ...string,
) {
	t.Helper()
	releases := make([]pluginruntime.RegistryRelease, 0, len(versions))
	for _, version := range versions {
		releases = append(releases, f.releases[version])
	}
	data, err := json.Marshal(pluginruntime.RegistryIndex{
		SchemaVersion: 1, Generation: generation, Releases: releases,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.indexPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func openSignedLifecycleRegistry(
	t *testing.T,
	fixture *signedLifecycleFixture,
	workspace, statePath, staging, cache string,
) *pluginruntime.Registry {
	t.Helper()
	source := (&url.URL{Scheme: "file", Path: fixture.indexPath}).String()
	registry, err := pluginruntime.NewRegistry(pluginruntime.RegistryConfig{
		StagingRoot: staging, StatePath: statePath, WorkspaceRoot: workspace,
		Backend: lifecycleTestBackend{}, RuntimeVersion: "0.4.0",
		Publishers: map[string]ed25519.PublicKey{"publisher.test": fixture.public},
		Distribution: &pluginruntime.DistributionConfig{
			Source: pluginruntime.RegistrySource{URL: source}, CacheRoot: cache,
		},
		WatchInterval: 5 * time.Millisecond,
		Now:           func() time.Time { return time.Now().UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func lifecycleArchive(
	t *testing.T,
	files map[string]struct {
		body []byte
		mode int64
	},
) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	compressed.Header.ModTime = time.Unix(0, 0)
	archive := tar.NewWriter(compressed)
	for _, name := range []string{pluginruntime.TOMLManifestName, "run.sh"} {
		file := files[name]
		if err := archive.WriteHeader(&tar.Header{
			Name: name, Mode: file.mode, Typeflag: tar.TypeReg,
			Size: int64(len(file.body)),
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(file.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func hashLifecycleBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
