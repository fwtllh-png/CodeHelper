package plugin

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReadManifestAdaptsTOMLAndRejectsAmbiguity(t *testing.T) {
	root := t.TempDir()
	executable := []byte("#!/bin/sh\nexit 0\n")
	writeExecutable(t, root, executable)
	hash := sha256Bytes(executable)
	tomlManifest := fmt.Sprintf(`
schema_version = 1
name = "fixture"
executable = "run.sh"
executable_sha256 = %q
arguments = ["fixed"]
generation = 2

[capabilities]
tools = ["plugin_run", "plugin_run"]
filesystem_roots = ["workspace"]
network_hosts = []
allow_process = true
`, hash)
	if err := os.WriteFile(
		filepath.Join(root, TOMLManifestName), []byte(tomlManifest), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	manifest, err := ReadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || manifest.Generation != 2 ||
		len(manifest.Capabilities.Tools) != 1 {
		t.Fatalf("normalized manifest = %+v", manifest)
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ManifestName), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadManifest(root); err == nil || !strings.Contains(err.Error(), "both") {
		t.Fatalf("ambiguous manifest error = %v", err)
	}
}

func TestDiscoverUsesDeterministicRootPrecedence(t *testing.T) {
	base := t.TempDir()
	roots := DiscoveryOptions{
		WorkspaceRoot: filepath.Join(base, "workspace"),
		UserRoot:      filepath.Join(base, "user"),
		BuiltinRoot:   filepath.Join(base, "builtin"),
	}
	for _, root := range []string{roots.WorkspaceRoot, roots.UserRoot, roots.BuiltinRoot} {
		if err := os.Mkdir(root, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeBundle(t, roots.BuiltinRoot, "fixture", "builtin")
	writeBundle(t, roots.UserRoot, "fixture", "user")
	writeBundle(t, roots.WorkspaceRoot, "fixture", "workspace")
	writeBundle(t, roots.BuiltinRoot, "another", "another")

	candidates, err := Discover(roots)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 2 || candidates[0].Name != "another" ||
		candidates[1].Root != RootWorkspace ||
		candidates[1].Manifest.Arguments[0] != "workspace" {
		t.Fatalf("discovery = %+v", candidates)
	}

	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(roots.WorkspaceRoot, "unsafe")); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(roots); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("unsafe discovery error = %v", err)
	}
}

func TestStagerPublishesOneCompleteContentAddressConcurrently(t *testing.T) {
	base := t.TempDir()
	sourceRoot := filepath.Join(base, "source")
	stagingRoot := filepath.Join(base, "staging")
	if err := os.Mkdir(sourceRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	bundle := writeBundle(t, sourceRoot, "fixture", "fixed")
	stager, err := NewStager(stagingRoot)
	if err != nil {
		t.Fatal(err)
	}
	const workers = 24
	results := make(chan StagedBundle, workers)
	failures := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			staged, err := stager.Stage(bundle)
			if err != nil {
				failures <- err
				return
			}
			results <- staged
		}()
	}
	group.Wait()
	close(results)
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	var expected string
	for staged := range results {
		if expected == "" {
			expected = staged.ContentHash
		}
		if staged.ContentHash != expected || filepath.Base(staged.Directory) != expected {
			t.Fatalf("staged bundle = %+v, expected %s", staged, expected)
		}
		if actual, err := HashBundle(staged.Directory); err != nil || actual != expected {
			t.Fatalf("published hash = %s, %v", actual, err)
		}
	}
	entries, err := os.ReadDir(stagingRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != expected {
		t.Fatalf("staging entries = %+v", entries)
	}
}

func TestRegistryLifecycleDriftTamperAndAuthorityRevocation(t *testing.T) {
	base := t.TempDir()
	discoveryRoot := filepath.Join(base, "plugins")
	workspaceRoot := filepath.Join(base, "workspace")
	stateRoot := filepath.Join(base, "state")
	for _, path := range []string{discoveryRoot, workspaceRoot, stateRoot} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bundle := writeBundle(t, discoveryRoot, "fixture", "fixed")
	registry, err := NewRegistry(RegistryConfig{
		Discovery:     DiscoveryOptions{WorkspaceRoot: discoveryRoot},
		StagingRoot:   filepath.Join(base, "staging"),
		StatePath:     filepath.Join(stateRoot, "plugins.json"),
		WorkspaceRoot: workspaceRoot,
		Backend:       loaderTestBackend{},
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	if _, err := registry.Trust("fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Load("fixture"); err == nil {
		t.Fatal("trust unexpectedly enabled the plugin")
	}
	if err := registry.Enable("fixture"); err != nil {
		t.Fatal(err)
	}
	loaded, err := registry.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()

	// A loaded handle remains bound to its immutable executable snapshot, but
	// registry reconciliation invalidates it as soon as source drift is seen.
	if err := os.WriteFile(
		filepath.Join(bundle, "run.sh"), []byte("#!/bin/sh\nprintf tampered\n"), 0o700,
	); err != nil {
		t.Fatal(err)
	}
	if err := registry.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.Run(t.Context(), nil); err == nil ||
		!strings.Contains(err.Error(), "authority revoked") {
		t.Fatalf("revoked run error = %v", err)
	}
	info, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(info) != 1 || info[0].Trusted || info[0].Enabled {
		t.Fatalf("drifted registry info = %+v", info)
	}

	// Re-trust still requires an explicit enable.
	manifest, err := ReadManifest(bundle)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.ReadFile(filepath.Join(bundle, "run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	manifest.ExecutableSHA256 = sha256Bytes(executable)
	manifest.Generation++
	writeManifest(t, bundle, manifest)
	if _, err := registry.Trust("fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Load("fixture"); err == nil {
		t.Fatal("re-trust unexpectedly enabled the plugin")
	}
	if err := registry.Enable("fixture"); err != nil {
		t.Fatal(err)
	}
	active, err := registry.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	if err := registry.Disable("fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := active.Run(t.Context(), nil); err == nil ||
		!strings.Contains(err.Error(), "authority revoked") {
		t.Fatalf("disabled run error = %v", err)
	}

	if err := registry.Enable("fixture"); err != nil {
		t.Fatal(err)
	}
	tamperedHandle, err := registry.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer tamperedHandle.Close()
	status, err := registry.List()
	if err != nil {
		t.Fatal(err)
	}
	stagedExecutable := filepath.Join(
		base, "staging", status[0].StagedHash, "run.sh",
	)
	if err := os.Chmod(stagedExecutable, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		stagedExecutable, []byte("#!/bin/sh\nprintf staged-tamper\n"), 0o700,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Load("fixture"); err == nil ||
		!strings.Contains(err.Error(), "staged content was tampered") {
		t.Fatalf("staging tamper error = %v", err)
	}
	if _, err := tamperedHandle.Run(t.Context(), nil); err == nil ||
		!strings.Contains(err.Error(), "authority revoked") {
		t.Fatalf("tampered authority error = %v", err)
	}
	status, err = registry.List()
	if err != nil {
		t.Fatal(err)
	}
	if status[0].Trusted || status[0].Enabled {
		t.Fatalf("tampered registry info = %+v", status)
	}
}

func TestRegistryReloadInvalidatesContentCapabilityAndGenerationDrift(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{
			name: "content",
			mutate: func(t *testing.T, bundle string) {
				if err := os.WriteFile(
					filepath.Join(bundle, "payload"), []byte("drift"), 0o600,
				); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "capability",
			mutate: func(t *testing.T, bundle string) {
				manifest, err := ReadManifest(bundle)
				if err != nil {
					t.Fatal(err)
				}
				manifest.Capabilities.Tools = append(
					manifest.Capabilities.Tools, "unexpected",
				)
				writeManifest(t, bundle, manifest)
			},
		},
		{
			name: "generation",
			mutate: func(t *testing.T, bundle string) {
				manifest, err := ReadManifest(bundle)
				if err != nil {
					t.Fatal(err)
				}
				manifest.Generation++
				writeManifest(t, bundle, manifest)
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			base := t.TempDir()
			discovery := filepath.Join(base, "plugins")
			workspace := filepath.Join(base, "workspace")
			stateDirectory := filepath.Join(base, "state")
			for _, path := range []string{discovery, workspace, stateDirectory} {
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
			}
			bundle := writeBundle(t, discovery, "fixture", "fixed")
			statePath := filepath.Join(stateDirectory, "plugins.json")
			registry, err := NewRegistry(RegistryConfig{
				Discovery:     DiscoveryOptions{WorkspaceRoot: discovery},
				StagingRoot:   filepath.Join(base, "staging"),
				StatePath:     statePath,
				WorkspaceRoot: workspace,
				Backend:       loaderTestBackend{},
			})
			if err != nil {
				t.Fatal(err)
			}
			defer registry.Close()
			if _, err := registry.Trust("fixture"); err != nil {
				t.Fatal(err)
			}
			if err := registry.Enable("fixture"); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, bundle)
			_ = registry.Reload()
			store, err := OpenStateStore(statePath)
			if err != nil {
				t.Fatal(err)
			}
			state, err := store.Read()
			if err != nil {
				t.Fatal(err)
			}
			if _, trusted := state.Plugins["fixture"]; trusted {
				t.Fatal("drifted plugin remained trusted")
			}
		})
	}
}

func TestStateStoreConcurrentUpdatesAndMalformedStateFailClosed(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "plugins.json")
	const workers = 32
	var group sync.WaitGroup
	for index := range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			store, err := OpenStateStore(path)
			if err != nil {
				t.Error(err)
				return
			}
			name := fmt.Sprintf("plugin-%02d", index)
			receipt := Receipt{
				SchemaVersion:  1,
				ContentHash:    strings.Repeat(fmt.Sprintf("%x", index%16), 64),
				CapabilityHash: strings.Repeat("a", 64),
				Generation:     1, ReviewedAt: time.Unix(1, 0).UTC(),
			}
			if err := store.Update(func(state *PersistentState) error {
				state.Plugins[name] = PluginState{
					Receipt: receipt, Source: RootWorkspace,
					StagedHash: receipt.ContentHash,
				}
				return nil
			}); err != nil {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	store, err := OpenStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Plugins) != workers {
		t.Fatalf("plugin state count = %d", len(state.Plugins))
	}
	if err := os.WriteFile(path, []byte(`{"schema_version":1,"plugins":null}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); err == nil {
		t.Fatal("malformed state was accepted")
	}
}

func TestStateStoreRejectsLinkedStateAndLockFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "plugins.json")
	store, err := OpenStateStore(path)
	if err != nil {
		t.Fatal(err)
	}
	receipt := Receipt{
		SchemaVersion: 1, ContentHash: strings.Repeat("a", 64),
		CapabilityHash: strings.Repeat("b", 64), Generation: 1,
		ReviewedAt: time.Unix(1, 0).UTC(),
	}
	if err := store.Update(func(state *PersistentState) error {
		state.Plugins["fixture"] = PluginState{
			Receipt: receipt, Source: RootWorkspace,
			StagedHash: receipt.ContentHash,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Link(path, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); err == nil || !strings.Contains(err.Error(), "multiply linked") {
		t.Fatalf("hard-linked state error = %v", err)
	}
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "real-state")
	if err := os.Rename(path, real); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, path); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("symlinked state error = %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	lockPath := path + ".lock"
	if err := os.Remove(lockPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, lockPath); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); err == nil {
		t.Fatal("symlinked lock was accepted")
	}
}

func writeBundle(t *testing.T, root, name, argument string) string {
	t.Helper()
	bundle := filepath.Join(root, name)
	if err := os.Mkdir(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := []byte("#!/bin/sh\nprintf '%s' \"$1\"\n")
	writeExecutable(t, bundle, executable)
	manifest := validManifest(executable)
	manifest.Name = name
	manifest.Arguments = []string{argument}
	writeManifest(t, bundle, manifest)
	return bundle
}

func writeExecutable(t *testing.T, root string, executable []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "run.sh"), executable, 0o700); err != nil {
		t.Fatal(err)
	}
}

func sha256Bytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
