package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type registryFixture struct {
	private   ed25519.PrivateKey
	public    ed25519.PublicKey
	root      string
	indexPath string
	releases  map[string]RegistryRelease
	artifacts map[string][]byte
}

func newRegistryFixture(t *testing.T) *registryFixture {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	return &registryFixture{
		private: private, public: public, root: root,
		indexPath: filepath.Join(root, "index.json"),
		releases:  make(map[string]RegistryRelease),
		artifacts: make(map[string][]byte),
	}
}

func (f *registryFixture) addRelease(
	t *testing.T,
	version string,
	generation uint64,
	output string,
) RegistryRelease {
	t.Helper()
	return f.addScriptRelease(
		t, version, generation, []byte("#!/bin/sh\nprintf '"+output+"'\n"),
	)
}

func (f *registryFixture) addScriptRelease(
	t *testing.T,
	version string,
	generation uint64,
	executable []byte,
) RegistryRelease {
	t.Helper()
	manifest := fmt.Sprintf(
		"schema_version = 1\nname = \"fixture\"\nversion = %q\n"+
			"publisher = \"publisher.test\"\ncodehelper = \">=0.4.0 <0.5.0\"\n"+
			"executable = \"run.sh\"\nexecutable_sha256 = %q\n"+
			"generation = %d\n\n[capabilities]\ntools = [\"plugin_run\"]\n"+
			"filesystem_roots = [\"workspace\"]\nnetwork_hosts = []\n"+
			"allow_process = true\n",
		version, sha256Bytes(executable), generation,
	)
	artifact := archiveBytes(t, []archiveEntry{
		{name: TOMLManifestName, body: []byte(manifest), mode: 0o600, kind: tar.TypeReg},
		{name: "run.sh", body: executable, mode: 0o700, kind: tar.TypeReg},
	})
	capabilityHash, err := HashCapabilities(CapabilityInventory{
		Tools: []string{"plugin_run"}, FilesystemRoots: []string{"workspace"},
		AllowProcess: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	fileName := "fixture-" + version + ".tar.gz"
	release := RegistryRelease{
		SchemaVersion: 1, Name: "fixture", Version: version,
		Generation: generation, Publisher: "publisher.test",
		Artifact: fileName, ArtifactSHA256: hashBytes(artifact),
		ManifestSHA256:   hashBytes([]byte(manifest)),
		CapabilitySHA256: capabilityHash,
	}
	release.Signature, err = SignRegistryRelease(release, f.private)
	if err != nil {
		t.Fatal(err)
	}
	f.releases[version] = release
	f.artifacts[fileName] = artifact
	if err := os.WriteFile(filepath.Join(f.root, fileName), artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	return release
}

func TestSecurityRevokeCancelsInflightPluginCall(t *testing.T) {
	fixture := newRegistryFixture(t)
	fixture.addScriptRelease(
		t, "1.0.0", 1,
		[]byte("#!/bin/sh\nsleep 30\nprintf completed\n"),
	)
	fixture.writeIndex(t, 1, "1.0.0")
	registry := newDistributionRegistry(t, fixture.source(), fixture.public)
	defer registry.Close()
	if _, err := registry.Install(t.Context(), "fixture", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	loaded, err := registry.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	done := make(chan error, 1)
	go func() {
		_, runErr := loaded.Run(t.Context(), nil)
		done <- runErr
	}()
	time.Sleep(150 * time.Millisecond)
	if err := registry.SecurityRevoke("fixture"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("security revoke did not cancel in-flight call")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("security revoke did not promptly cancel in-flight call")
	}
}

func TestExternalRegistryRevokeCancelsInflightPluginCall(t *testing.T) {
	fixture := newRegistryFixture(t)
	fixture.addScriptRelease(
		t, "1.0.0", 1,
		[]byte("#!/bin/sh\nsleep 30\nprintf completed\n"),
	)
	fixture.writeIndex(t, 1, "1.0.0")
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	stateDirectory := filepath.Join(base, "state")
	for _, path := range []string{workspace, stateDirectory} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := RegistryConfig{
		StagingRoot:   filepath.Join(base, "staging"),
		StatePath:     filepath.Join(stateDirectory, "plugins.json"),
		WorkspaceRoot: workspace, Backend: loaderTestBackend{},
		RuntimeVersion: "0.4.0", WatchInterval: 10 * time.Millisecond,
		Publishers: map[string]ed25519.PublicKey{"publisher.test": fixture.public},
		Distribution: &DistributionConfig{
			Source: fixture.source(), CacheRoot: filepath.Join(base, "cache"),
		},
	}
	runtimeRegistry, err := NewRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeRegistry.Close()
	controlRegistry, err := NewRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	defer controlRegistry.Close()
	if _, err := runtimeRegistry.Install(t.Context(), "fixture", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runtimeRegistry.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer loaded.Close()
	done := make(chan error, 1)
	go func() {
		_, runErr := loaded.Run(t.Context(), nil)
		done <- runErr
	}()
	time.Sleep(150 * time.Millisecond)
	if err := controlRegistry.SecurityRevoke("fixture"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("external security revoke did not cancel in-flight call")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("external security revoke did not propagate to runtime")
	}
}

func (f *registryFixture) writeIndex(
	t *testing.T,
	generation uint64,
	versions ...string,
) {
	t.Helper()
	releases := make([]RegistryRelease, 0, len(versions))
	for _, version := range versions {
		releases = append(releases, f.releases[version])
	}
	data, err := json.Marshal(RegistryIndex{
		SchemaVersion: 1, Generation: generation, Releases: releases,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.indexPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (f *registryFixture) source() RegistrySource {
	return RegistrySource{URL: (&url.URL{Scheme: "file", Path: f.indexPath}).String()}
}

func TestSignedRegistryInstallUpdateDrainRollbackAndSecurityRevoke(t *testing.T) {
	fixture := newRegistryFixture(t)
	fixture.addRelease(t, "1.0.0", 1, "v1")
	fixture.addRelease(t, "2.0.0", 2, "v2")
	fixture.writeIndex(t, 1, "1.0.0")
	registry := newDistributionRegistry(t, fixture.source(), fixture.public)
	defer registry.Close()

	installed, err := registry.Install(t.Context(), "fixture", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if installed.Action != "install" || installed.Active.Version != "1.0.0" {
		t.Fatalf("install receipt = %+v", installed)
	}
	lifecycle, err := registry.LifecycleSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(lifecycle) != 1 || lifecycle[0].Name != "fixture" ||
		lifecycle[0].Version != "1.0.0" ||
		lifecycle[0].Trust != TrustSignedRegistry ||
		lifecycle[0].LastAction != "install" || !lifecycle[0].Enabled {
		t.Fatalf("install lifecycle = %+v", lifecycle)
	}
	publishers := registry.config.Publishers
	registry.config.Publishers = nil
	if err := registry.Reload(); !errors.Is(err, ErrUnknownPublisher) {
		t.Fatalf("missing publisher reload error = %v", err)
	}
	stateAfterFailedReload, err := registry.state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if stateAfterFailedReload.Plugins["fixture"].Activation == nil {
		t.Fatal("failed signed verification deleted durable activation")
	}
	registry.config.Publishers = publishers
	old, err := registry.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()

	fixture.writeIndex(t, 2, "2.0.0")
	updated, err := registry.Update(t.Context(), "fixture", "2.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Previous) != 1 || updated.Previous[0].Version != "1.0.0" {
		t.Fatalf("update receipt = %+v", updated)
	}
	assertPluginOutput(t, old, "v1")
	current, err := registry.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	assertPluginOutput(t, current, "v2")

	rolledBack, err := registry.Rollback("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.Action != "rollback" || rolledBack.Active.Version != "1.0.0" ||
		rolledBack.Previous[0].Version != "2.0.0" {
		t.Fatalf("rollback receipt = %+v", rolledBack)
	}
	restored, err := registry.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	assertPluginOutput(t, restored, "v1")
	assertPluginOutput(t, current, "v2")
	fixture.addRelease(t, "1.5.0", 3, "downgrade")
	fixture.writeIndex(t, 3, "1.5.0")
	if _, err := registry.Update(
		t.Context(), "fixture", "1.5.0",
	); !errors.Is(err, ErrPluginDowngrade) {
		t.Fatalf("post-rollback downgrade error = %v", err)
	}
	state, err := registry.state.Read()
	if err != nil {
		t.Fatal(err)
	}
	receipts := state.LifecycleReceipts["fixture"]
	if len(receipts) != 3 || receipts[0].Action != "install" ||
		receipts[1].Action != "update" || receipts[2].Action != "rollback" {
		t.Fatalf("lifecycle receipt journal = %+v", receipts)
	}

	if err := registry.SecurityRevoke("fixture"); err != nil {
		t.Fatal(err)
	}
	state, err = registry.state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.LifecycleReceipts["fixture"]) != 3 {
		t.Fatal("security revoke removed lifecycle receipt history")
	}
	lifecycle, err = registry.LifecycleSnapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(lifecycle) != 0 {
		t.Fatalf("revoked lifecycle = %+v, want empty", lifecycle)
	}
	fixture.writeIndex(t, 1, "1.0.0")
	if _, err := registry.Install(
		t.Context(), "fixture", "1.0.0",
	); !errors.Is(err, ErrRegistryReplay) {
		t.Fatalf("post-revoke replay error = %v", err)
	}
	for _, loaded := range []*Loaded{old, current, restored} {
		if _, err := loaded.Run(t.Context(), nil); err == nil ||
			!strings.Contains(err.Error(), "authority revoked") {
			t.Fatalf("security revoke error = %v", err)
		}
	}
}

func TestPluginManifestGovernanceFieldsAreStrictAndCompatible(t *testing.T) {
	manifest := validManifest([]byte("#!/bin/sh\nexit 0\n"))
	manifest.Version = "1.2.3"
	manifest.Publisher = "publisher.test"
	manifest.CodeHelper = ">=0.4.0 <0.5.0"
	if _, err := NormalizeManifest(manifest); err != nil {
		t.Fatal(err)
	}
	if err := checkCompatibility(manifest.CodeHelper, "0.4.1"); err != nil {
		t.Fatal(err)
	}
	manifest.Version = "1.2"
	if _, err := NormalizeManifest(manifest); err == nil {
		t.Fatal("non-strict plugin version was accepted")
	}
	manifest.Version = "1.2.3"
	manifest.Publisher = ""
	if _, err := NormalizeManifest(manifest); err == nil {
		t.Fatal("partial plugin governance identity was accepted")
	}
	if err := checkCompatibility(">=0.5.0", "0.4.1"); err == nil {
		t.Fatal("incompatible CodeHelper runtime was accepted")
	}
}

func TestRegistryRejectsSignatureDigestReplayDowngradeAndInterruptedUpdate(
	t *testing.T,
) {
	fixture := newRegistryFixture(t)
	fixture.addRelease(t, "1.0.0", 1, "v1")
	fixture.writeIndex(t, 1, "1.0.0")
	registry := newDistributionRegistry(t, fixture.source(), fixture.public)
	defer registry.Close()
	if _, err := registry.Install(t.Context(), "fixture", "1.0.0"); err != nil {
		t.Fatal(err)
	}

	release2 := fixture.addRelease(t, "2.0.0", 2, "v2")
	tampered := release2
	tampered.Signature = base64.StdEncoding.EncodeToString(make([]byte, ed25519.SignatureSize))
	fixture.releases["2.0.0"] = tampered
	fixture.writeIndex(t, 2, "2.0.0")
	if _, err := registry.Update(t.Context(), "fixture", "2.0.0"); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("invalid signature error = %v", err)
	}
	current, err := registry.Load("fixture")
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	assertPluginOutput(t, current, "v1")

	fixture.releases["2.0.0"] = release2
	broken := release2
	broken.ArtifactSHA256 = strings.Repeat("a", 64)
	broken.Signature, err = SignRegistryRelease(broken, fixture.private)
	if err != nil {
		t.Fatal(err)
	}
	fixture.releases["2.0.0"] = broken
	fixture.writeIndex(t, 2, "2.0.0")
	if _, err := registry.Update(t.Context(), "fixture", "2.0.0"); err == nil ||
		!strings.Contains(err.Error(), "artifact digest") {
		t.Fatalf("artifact digest error = %v", err)
	}
	assertPluginOutput(t, current, "v1")

	for _, test := range []struct {
		name   string
		mutate func(*RegistryRelease)
		want   string
	}{
		{
			name: "manifest",
			mutate: func(release *RegistryRelease) {
				release.ManifestSHA256 = strings.Repeat("b", 64)
			},
			want: "manifest digest",
		},
		{
			name: "capability",
			mutate: func(release *RegistryRelease) {
				release.CapabilitySHA256 = strings.Repeat("c", 64)
			},
			want: "capability digest",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			changed := release2
			test.mutate(&changed)
			var signErr error
			changed.Signature, signErr = SignRegistryRelease(changed, fixture.private)
			if signErr != nil {
				t.Fatal(signErr)
			}
			fixture.releases["2.0.0"] = changed
			fixture.writeIndex(t, 2, "2.0.0")
			if _, err := registry.Update(
				t.Context(), "fixture", "2.0.0",
			); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("%s error = %v", test.name, err)
			}
			assertPluginOutput(t, current, "v1")
		})
	}

	fixture.releases["2.0.0"] = release2
	fixture.writeIndex(t, 2, "2.0.0")
	if _, err := registry.Update(t.Context(), "fixture", "2.0.0"); err != nil {
		t.Fatal(err)
	}
	fixture.addRelease(t, "1.5.0", 3, "downgrade")
	fixture.writeIndex(t, 3, "1.5.0")
	if _, err := registry.Update(t.Context(), "fixture", "1.5.0"); !errors.Is(err, ErrPluginDowngrade) {
		t.Fatalf("downgrade error = %v", err)
	}
	fixture.writeIndex(t, 2, "2.0.0")
	if _, err := registry.Update(t.Context(), "fixture", "2.0.0"); !errors.Is(err, ErrRegistryReplay) {
		t.Fatalf("replay error = %v", err)
	}
}

func TestRegistrySafeExtractionRejectsTraversalAndLinkEntries(t *testing.T) {
	for _, test := range []struct {
		name  string
		entry archiveEntry
	}{
		{name: "traversal", entry: archiveEntry{name: "../outside", kind: tar.TypeReg}},
		{name: "symlink", entry: archiveEntry{name: "linked", kind: tar.TypeSymlink, link: "outside"}},
		{name: "hardlink", entry: archiveEntry{name: "linked", kind: tar.TypeLink, link: "target"}},
		{name: "device", entry: archiveEntry{name: "device", kind: tar.TypeChar}},
	} {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRegistryFixture(t)
			artifact := archiveBytes(t, []archiveEntry{test.entry})
			release := RegistryRelease{
				SchemaVersion: 1, Name: "fixture", Version: "1.0.0",
				Generation: 1, Publisher: "publisher.test",
				Artifact: "unsafe.tar.gz", ArtifactSHA256: hashBytes(artifact),
				ManifestSHA256:   strings.Repeat("a", 64),
				CapabilitySHA256: strings.Repeat("b", 64),
			}
			var err error
			release.Signature, err = SignRegistryRelease(release, fixture.private)
			if err != nil {
				t.Fatal(err)
			}
			fixture.releases["1.0.0"] = release
			if err := os.WriteFile(
				filepath.Join(fixture.root, release.Artifact), artifact, 0o600,
			); err != nil {
				t.Fatal(err)
			}
			fixture.writeIndex(t, 1, "1.0.0")
			distributor := newTestDistributor(t, fixture.source(), fixture.public)
			if _, err := distributor.ResolveAndStage(
				t.Context(), "fixture", "1.0.0",
			); err == nil {
				t.Fatal("unsafe archive was accepted")
			}
		})
	}
}

func TestRegistrySafeExtractionEnforcesEntryAndSizeLimits(t *testing.T) {
	entries := make([]archiveEntry, maxBundleFiles+1)
	for index := range entries {
		entries[index] = archiveEntry{
			name: fmt.Sprintf("entry-%04d", index), kind: tar.TypeDir,
		}
	}
	if err := extractPluginArtifact(archiveBytes(t, entries), t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "entry limit") {
		t.Fatalf("entry limit error = %v", err)
	}

	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(compressed)
	if err := archive.WriteHeader(&tar.Header{
		Name: "oversized", Mode: 0o600, Typeflag: tar.TypeReg,
		Size: maxBundleBytes + 1,
	}); err != nil {
		t.Fatal(err)
	}
	_ = archive.Close() // Deliberately truncated; the declared size must fail first.
	if err := compressed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := extractPluginArtifact(buffer.Bytes(), t.TempDir()); err == nil ||
		!strings.Contains(err.Error(), "extracted size limit") {
		t.Fatalf("size limit error = %v", err)
	}
}

func TestFileAndHTTPSRegistryUseIdenticalVerification(t *testing.T) {
	fixture := newRegistryFixture(t)
	fixture.addRelease(t, "1.0.0", 1, "same")
	fixture.writeIndex(t, 1, "1.0.0")
	fileRelease, err := newTestDistributor(
		t, fixture.source(), fixture.public,
	).ResolveAndStage(t.Context(), "fixture", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewTLSServer(http.FileServer(http.Dir(fixture.root)))
	defer server.Close()
	httpsRelease, err := newTestDistributor(t, RegistrySource{
		URL: server.URL + "/index.json", HTTPClient: server.Client(),
	}, fixture.public).ResolveAndStage(t.Context(), "fixture", "1.0.0")
	if err != nil {
		t.Fatal(err)
	}
	if fileRelease.ContentHash != httpsRelease.ContentHash ||
		fileRelease.Signature != httpsRelease.Signature {
		t.Fatalf("file release = %+v, HTTPS release = %+v", fileRelease, httpsRelease)
	}
}

func TestConcurrentRegistryUpdatesConvergeOnHighestGeneration(t *testing.T) {
	fixture := newRegistryFixture(t)
	fixture.addRelease(t, "1.0.0", 1, "v1")
	fixture.addRelease(t, "2.0.0", 2, "v2")
	fixture.addRelease(t, "3.0.0", 3, "v3")
	fixture.writeIndex(t, 3, "1.0.0", "2.0.0", "3.0.0")
	base := t.TempDir()
	workspace := filepath.Join(base, "workspace")
	stateDirectory := filepath.Join(base, "state")
	for _, path := range []string{workspace, stateDirectory} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := RegistryConfig{
		StagingRoot:   filepath.Join(base, "staging"),
		StatePath:     filepath.Join(stateDirectory, "plugins.json"),
		WorkspaceRoot: workspace, Backend: loaderTestBackend{},
		RuntimeVersion: "0.4.0",
		Publishers:     map[string]ed25519.PublicKey{"publisher.test": fixture.public},
		Distribution: &DistributionConfig{
			Source: fixture.source(), CacheRoot: filepath.Join(base, "cache"),
		},
	}
	first, err := NewRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := NewRegistry(config)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	if _, err := first.Install(t.Context(), "fixture", "1.0.0"); err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 2)
	go func() {
		_, updateErr := first.Update(t.Context(), "fixture", "2.0.0")
		results <- updateErr
	}()
	go func() {
		_, updateErr := second.Update(t.Context(), "fixture", "3.0.0")
		results <- updateErr
	}()
	var successes int
	for range 2 {
		err := <-results
		if err == nil {
			successes++
		} else if !errors.Is(err, ErrRegistryReplay) &&
			!errors.Is(err, ErrPluginDowngrade) {
			t.Fatalf("concurrent update error = %v", err)
		}
	}
	if successes == 0 {
		t.Fatal("no concurrent update succeeded")
	}
	state, err := first.state.Read()
	if err != nil {
		t.Fatal(err)
	}
	if got := state.Plugins["fixture"].Activation.Active.Version; got != "3.0.0" {
		t.Fatalf("active version = %s, want 3.0.0", got)
	}
}

func TestPublisherAllowlistIsStrictAndRejectsUnknownPublisher(t *testing.T) {
	fixture := newRegistryFixture(t)
	fixture.addRelease(t, "1.0.0", 1, "v1")
	fixture.writeIndex(t, 1, "1.0.0")
	distributor := newTestDistributor(
		t, fixture.source(), ed25519.PublicKey(make([]byte, ed25519.PublicKeySize)),
	)
	if _, err := distributor.ResolveAndStage(
		t.Context(), "fixture", "1.0.0",
	); !errors.Is(err, ErrInvalidSignature) {
		t.Fatalf("untrusted key error = %v", err)
	}
	base := t.TempDir()
	stager, err := NewStager(filepath.Join(base, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	unknownDistributor, err := NewDistributor(DistributionConfig{
		Source:     fixture.source(),
		Publishers: map[string]ed25519.PublicKey{"another.publisher": fixture.public},
		CacheRoot:  filepath.Join(base, "cache"), Stager: stager,
		RuntimeVersion: "0.4.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unknownDistributor.ResolveAndStage(
		t.Context(), "fixture", "1.0.0",
	); !errors.Is(err, ErrUnknownPublisher) {
		t.Fatalf("unknown publisher error = %v", err)
	}

	path := filepath.Join(t.TempDir(), "publishers.json")
	data := fmt.Sprintf(
		`{"schema_version":1,"publishers":{"publisher.test":%q}}`,
		base64.StdEncoding.EncodeToString(fixture.public),
	)
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	keys, err := LoadPublisherAllowlist(path)
	if err != nil || !bytes.Equal(keys["publisher.test"], fixture.public) {
		t.Fatalf("publisher keys = %v, %v", keys, err)
	}
	if err := os.WriteFile(
		path, []byte(`{"schema_version":1,"publishers":{},"unknown":true}`), 0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadPublisherAllowlist(path); err == nil {
		t.Fatal("unknown allowlist field was accepted")
	}
}

func TestRegistryVerificationErrorCategoriesAreStable(t *testing.T) {
	if got := ErrorCategory(fmt.Errorf("verify: %w", ErrInvalidSignature)); got !=
		ErrorCategorySignatureInvalid {
		t.Fatalf("signature category = %q", got)
	}
	if got := ErrorCategory(fmt.Errorf("verify: %w", ErrUnknownPublisher)); got !=
		ErrorCategorySignatureInvalid {
		t.Fatalf("publisher category = %q", got)
	}
	if got := ErrorCategory(fmt.Errorf("verify: %w", ErrDigestMismatch)); got !=
		ErrorCategoryDigestMismatch {
		t.Fatalf("digest category = %q", got)
	}
	if got := ErrorCategory(errors.New("network unavailable")); got != "" {
		t.Fatalf("unclassified category = %q", got)
	}
}

func newDistributionRegistry(
	t *testing.T,
	source RegistrySource,
	public ed25519.PublicKey,
) *Registry {
	t.Helper()
	base := t.TempDir()
	for _, directory := range []string{
		filepath.Join(base, "workspace"), filepath.Join(base, "state"),
	} {
		if err := os.Mkdir(directory, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	registry, err := NewRegistry(RegistryConfig{
		StagingRoot:   filepath.Join(base, "staging"),
		StatePath:     filepath.Join(base, "state", "plugins.json"),
		WorkspaceRoot: filepath.Join(base, "workspace"),
		Backend:       loaderTestBackend{}, RuntimeVersion: "0.4.0",
		Publishers: map[string]ed25519.PublicKey{"publisher.test": public},
		Distribution: &DistributionConfig{
			Source: source, CacheRoot: filepath.Join(base, "cache"),
		},
		Now: func() time.Time { return time.Unix(1_800_000_000, 0).UTC() },
	})
	if err != nil {
		t.Fatal(err)
	}
	return registry
}

func newTestDistributor(
	t *testing.T,
	source RegistrySource,
	public ed25519.PublicKey,
) *Distributor {
	t.Helper()
	base := t.TempDir()
	stager, err := NewStager(filepath.Join(base, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	distributor, err := NewDistributor(DistributionConfig{
		Source:     source,
		Publishers: map[string]ed25519.PublicKey{"publisher.test": public},
		CacheRoot:  filepath.Join(base, "cache"), Stager: stager,
		RuntimeVersion: "0.4.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	return distributor
}

func assertPluginOutput(t *testing.T, loaded *Loaded, expected string) {
	t.Helper()
	result, err := loaded.Run(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode != 0 || result.Stdout != expected {
		t.Fatalf("plugin result = %+v, want %q", result, expected)
	}
}

type archiveEntry struct {
	name string
	body []byte
	mode int64
	kind byte
	link string
}

func archiveBytes(t *testing.T, entries []archiveEntry) []byte {
	t.Helper()
	var buffer bytes.Buffer
	compressed := gzip.NewWriter(&buffer)
	compressed.Header.ModTime = time.Unix(0, 0)
	archive := tar.NewWriter(compressed)
	for _, entry := range entries {
		mode := entry.mode
		if mode == 0 {
			mode = 0o600
		}
		kind := entry.kind
		if kind == 0 {
			kind = tar.TypeReg
		}
		header := &tar.Header{
			Name: entry.name, Mode: mode, Typeflag: kind,
			Size: int64(len(entry.body)), Linkname: entry.link,
		}
		if kind != tar.TypeReg {
			header.Size = 0
		}
		if err := archive.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
		if len(entry.body) > 0 && kind == tar.TypeReg {
			if _, err := archive.Write(entry.body); err != nil {
				t.Fatal(err)
			}
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
