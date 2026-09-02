package skill

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestManifestStrictSchemaAndCompatibility(t *testing.T) {
	_, err := ParseManifest([]byte(`
schema_version = 1
name = "review"
version = "1.2"
qcode = ">=1.0.0"
unknown = true
`))
	if err == nil {
		t.Fatal("invalid manifest was accepted")
	}
	manifest, err := ParseManifest([]byte(`
schema_version = 1
name = "review"
version = "1.2.0"
qcode = ">=1.0.0 <2.0.0"

[dependencies]
repository-context = "^2.1.0"
`))
	if err != nil {
		t.Fatal(err)
	}
	if err := checkVersion(manifest.QCode, "1.5.0"); err != nil {
		t.Fatal(err)
	}
	if err := checkVersion(manifest.QCode, "2.0.0"); err == nil {
		t.Fatal("incompatible runtime was accepted")
	}
}

func TestResolveLockLoadPlanAndDigestDrift(t *testing.T) {
	workspace := t.TempDir()
	configured := t.TempDir()
	writeGovernedSkill(
		t, configured, "repository-context", "2.1.3",
		"Repository context", "Load repository context.", nil,
	)
	writeGovernedSkill(
		t, configured, "review", "1.2.0", "Review", "Run the review.",
		map[string]string{"repository-context": "^2.1.0"},
	)
	lock, err := NewLockStore(filepath.Join(t.TempDir(), "skills.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(DiscoveryOptions{
		Workspace: workspace, ConfiguredDir: configured, UserHome: t.TempDir(),
		RuntimeVersion: "1.4.0", Lock: lock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.LoadPlan(t.Context(), "review"); err == nil ||
		!strings.Contains(err.Error(), "skill lock") {
		t.Fatalf("unlocked load error = %v", err)
	}
	lockfile, err := catalog.WriteLock(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(lockfile.Skills) != 2 ||
		lockfile.Skills[0].Name != "repository-context" ||
		lockfile.Skills[1].Name != "review" {
		t.Fatalf("lockfile = %+v", lockfile)
	}
	plan, err := catalog.LoadPlan(t.Context(), "review")
	if err != nil {
		t.Fatal(err)
	}
	if len(plan) != 2 || plan[0].Name != "repository-context" ||
		plan[1].Name != "review" || !plan[0].Locked || !plan[1].Locked {
		t.Fatalf("plan = %+v", plan)
	}
	path := filepath.Join(configured, "repository-context", "SKILL.md")
	if err := os.WriteFile(path, []byte(`---
name: repository-context
description: Repository context
---
Changed after lock.
`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := catalog.Verify(t.Context()); err == nil ||
		!strings.Contains(err.Error(), "digest drifted") {
		t.Fatalf("Verify() error = %v", err)
	}
}

func TestResolverRejectsCycleConflictAndLegacyShadow(t *testing.T) {
	tests := map[string]func(*testing.T, string, string){
		"cycle": func(t *testing.T, workspace, configured string) {
			writeGovernedSkill(t, configured, "alpha", "1.0.0", "alpha", "alpha",
				map[string]string{"beta": "^1.0.0"})
			writeGovernedSkill(t, configured, "beta", "1.0.0", "beta", "beta",
				map[string]string{"alpha": "^1.0.0"})
		},
		"conflict": func(t *testing.T, workspace, configured string) {
			writeGovernedSkill(t, configured, "alpha", "1.0.0", "alpha", "alpha",
				map[string]string{"beta": "^2.0.0"})
			writeGovernedSkill(t, configured, "beta", "1.0.0", "beta", "beta", nil)
		},
		"legacy shadow": func(t *testing.T, workspace, configured string) {
			writeSkill(
				t, filepath.Join(workspace, ".agents", "skills"),
				"beta", "legacy beta", "legacy beta",
			)
			writeGovernedSkill(t, configured, "alpha", "1.0.0", "alpha", "alpha",
				map[string]string{"beta": "^1.0.0"})
			writeGovernedSkill(t, configured, "beta", "1.0.0", "beta", "beta", nil)
		},
	}
	for name, setup := range tests {
		t.Run(name, func(t *testing.T) {
			workspace := t.TempDir()
			configured := t.TempDir()
			setup(t, workspace, configured)
			lock, err := NewLockStore(filepath.Join(t.TempDir(), "skills.lock.json"))
			if err != nil {
				t.Fatal(err)
			}
			catalog, err := Discover(DiscoveryOptions{
				Workspace: workspace, ConfiguredDir: configured, UserHome: t.TempDir(),
				RuntimeVersion: "1.0.0", Lock: lock,
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = catalog.WriteLock(t.Context())
			if err == nil {
				t.Fatal("invalid dependency graph was locked")
			}
			want := ErrDependencyConflict
			if name == "cycle" {
				want = ErrDependencyCycle
			}
			if !errors.Is(err, want) {
				t.Fatalf("error = %v, want %v", err, want)
			}
		})
	}
}

func TestLegacyWorkspaceSkillRemainsLocalUnlocked(t *testing.T) {
	workspace := t.TempDir()
	writeSkill(
		t, filepath.Join(workspace, ".agents", "skills"),
		"review", "Review", "Run the review.",
	)
	catalog, err := Discover(DiscoveryOptions{
		Workspace: workspace, UserHome: t.TempDir(), RuntimeVersion: "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Load(t.Context(), "review")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != legacySkillVersion || loaded.Locked {
		t.Fatalf("loaded = %+v", loaded)
	}
}

func TestConfiguredSkillWithoutManifestFailsDiscovery(t *testing.T) {
	configured := t.TempDir()
	writeSkill(t, configured, "review", "Review", "Run the review.")
	_, err := Discover(DiscoveryOptions{
		Workspace: t.TempDir(), ConfiguredDir: configured, UserHome: t.TempDir(),
		RuntimeVersion: "1.0.0",
	})
	if err == nil || !strings.Contains(err.Error(), "requires skill.toml") {
		t.Fatalf("Discover() error = %v", err)
	}
}

func TestLockStoreConcurrentWritesRemainDecodable(t *testing.T) {
	store, err := NewLockStore(filepath.Join(t.TempDir(), "skills.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	lockfile := Lockfile{
		SchemaVersion: LockSchemaV1, RuntimeVersion: "1.0.0",
		Skills: []LockEntry{{
			Name: "review", Version: "1.0.0", Source: SourceConfigured,
			Digest: strings.Repeat("a", 64),
		}},
	}
	const count = 32
	var wait sync.WaitGroup
	for range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := store.Write(lockfile); err != nil {
				t.Errorf("Write(): %v", err)
			}
			if _, err := store.Read(); err != nil {
				t.Errorf("Read(): %v", err)
			}
		}()
	}
	wait.Wait()
	if _, err := store.Read(); err != nil {
		t.Fatal(err)
	}
}

func TestLockStoreRejectsUnknownFieldsAndSymlinkParent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "skills.lock.json")
	store, err := NewLockStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{
		"schema_version": 1,
		"runtime_version": "1.0.0",
		"skills": [],
		"unknown": true
	}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(); err == nil {
		t.Fatal("lock with unknown field was accepted")
	}
	realParent := t.TempDir()
	linkRoot := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(realParent, linkRoot); err != nil {
		t.Fatal(err)
	}
	if _, err := NewLockStore(filepath.Join(linkRoot, "lock.json")); err == nil {
		t.Fatal("lock store accepted symlink parent")
	}
}
