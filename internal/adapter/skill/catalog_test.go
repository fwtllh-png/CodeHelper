package skill

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestDiscoveryFirstMatchPrecedenceAndLocale(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	configured := t.TempDir()
	locations := []struct {
		root        string
		description string
	}{
		{filepath.Join(workspace, ".agents", "skills"), "workspace agents"},
		{filepath.Join(workspace, "skills"), "workspace plain"},
		{filepath.Join(workspace, ".opencode", "skills"), "workspace opencode"},
		{filepath.Join(workspace, ".claude", "skills"), "workspace claude"},
		{filepath.Join(workspace, ".cursor", "skills"), "workspace cursor"},
		{filepath.Join(workspace, ".codehelper", "skills"), "workspace codehelper"},
		{configured, "configured"},
		{filepath.Join(home, ".agents", "skills"), "user agents"},
		{filepath.Join(home, ".claude", "skills"), "user claude"},
		{filepath.Join(home, ".codehelper", "skills"), "user codehelper"},
	}
	for _, location := range locations {
		if location.root == configured {
			writeGovernedSkill(
				t, location.root, "duplicate", "1.0.0",
				location.description, "instructions", nil,
			)
		} else {
			writeSkill(t, location.root, "duplicate", location.description, "instructions")
		}
	}
	writeRawSkill(t, filepath.Join(workspace, ".agents", "skills"), "localized", `---
name: localized
description: English description
description_zh-CN: 中文描述
---
# Localized
`)

	catalog, err := Discover(DiscoveryOptions{
		Workspace: workspace, ConfiguredDir: configured, UserHome: home, Locale: "zh_CN",
	})
	if err != nil {
		t.Fatal(err)
	}
	summaries := catalog.Summaries(context.Background())
	if len(summaries) != 2 {
		t.Fatalf("summaries = %+v", summaries)
	}
	byName := make(map[string]Summary)
	for _, summary := range summaries {
		byName[summary.Name] = summary
	}
	if got := byName["duplicate"].Description; got != "workspace agents" {
		t.Fatalf("duplicate description = %q", got)
	}
	if got := byName["localized"].Description; got != "中文描述" {
		t.Fatalf("localized description = %q", got)
	}
}

func TestStateDisableAndMalformedStateRecovery(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	writeSkill(t, filepath.Join(workspace, ".agents", "skills"), "native", "native", "native body")
	statePath := filepath.Join(t.TempDir(), "skills.json")
	state, err := NewStateStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(DiscoveryOptions{
		Workspace: workspace, UserHome: home, State: state,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.SetEnabled("native", false); err != nil {
		t.Fatal(err)
	}
	if summaries := catalog.Summaries(context.Background()); len(summaries) != 0 {
		t.Fatalf("disabled summaries = %+v", summaries)
	}
	if _, err := catalog.Load(context.Background(), "native"); err == nil {
		t.Fatal("disabled native skill loaded")
	}

	pluginRoot := t.TempDir()
	writeGovernedSkill(t, pluginRoot, "plugin-skill", "1.0.0", "plugin", "plugin body", nil)
	verifier := AuthorityVerifierFunc(func(context.Context, Authority) error { return nil })
	snapshot, err := StagePluginSnapshot(context.Background(), pluginRoot, Authority{
		Plugin: "example", Generation: 1, Token: "trusted",
	}, verifier, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte("{malformed"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := Discover(DiscoveryOptions{
		Workspace: workspace, UserHome: home, State: state, Plugins: []PluginSnapshot{snapshot},
	})
	if err != nil {
		t.Fatal(err)
	}
	summaries, issues := recovered.List(context.Background())
	if len(summaries) != 1 || summaries[0].Name != "native" {
		t.Fatalf("malformed-state summaries = %+v", summaries)
	}
	if len(issues) == 0 || !strings.Contains(issues[len(issues)-1].Reason, "enable state") {
		t.Fatalf("malformed-state issues = %+v", issues)
	}
}

func TestStateConcurrentUpdatesAreAtomic(t *testing.T) {
	store, err := NewStateStore(filepath.Join(t.TempDir(), "skills.json"))
	if err != nil {
		t.Fatal(err)
	}
	const count = 64
	var wait sync.WaitGroup
	for index := range count {
		wait.Add(1)
		go func() {
			defer wait.Done()
			name := fmt.Sprintf("skill-%02d", index)
			if err := store.SetEnabled(name, index%2 == 0); err != nil {
				t.Errorf("SetEnabled(%q): %v", name, err)
			}
		}()
	}
	wait.Wait()
	state, err := store.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(state) != count {
		t.Fatalf("state entries = %d, want %d", len(state), count)
	}
	for index := range count {
		name := fmt.Sprintf("skill-%02d", index)
		if state[name] != (index%2 == 0) {
			t.Fatalf("state[%q] = %t", name, state[name])
		}
	}
}

func TestPluginAuthorityRevocationFailsClosed(t *testing.T) {
	workspace := t.TempDir()
	pluginRoot := t.TempDir()
	writeGovernedSkill(
		t, pluginRoot, "plugin-skill", "1.0.0", "plugin", "trusted instructions", nil,
	)
	var revoked atomic.Bool
	verifier := AuthorityVerifierFunc(func(_ context.Context, authority Authority) error {
		if authority.Token != "token" || revoked.Load() {
			return errors.New("authority revoked")
		}
		return nil
	})
	snapshot, err := StagePluginSnapshot(context.Background(), pluginRoot, Authority{
		Plugin: "plugin", Generation: 7, Token: "token",
	}, verifier, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	lock, err := NewLockStore(filepath.Join(t.TempDir(), "skills.lock.json"))
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(DiscoveryOptions{
		Workspace: workspace, UserHome: t.TempDir(), Plugins: []PluginSnapshot{snapshot},
		RuntimeVersion: "1.0.0", Lock: lock,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.WriteLock(t.Context()); err != nil {
		t.Fatal(err)
	}
	loaded, err := catalog.Load(context.Background(), "plugin-skill")
	if err != nil || loaded.Content != "trusted instructions" {
		t.Fatalf("loaded = %+v, err = %v", loaded, err)
	}
	revoked.Store(true)
	if _, err := catalog.Load(context.Background(), "plugin-skill"); err == nil ||
		!strings.Contains(err.Error(), "revoked") {
		t.Fatalf("revoked load error = %v", err)
	}
	if summaries := catalog.Summaries(context.Background()); len(summaries) != 0 {
		t.Fatalf("revoked plugin remains visible: %+v", summaries)
	}
}

func TestSymlinkTraversalRejectedAtDiscoveryAndLoad(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	writeSkill(t, root, "safe", "safe", "safe body")
	outside := t.TempDir()
	writeSkill(t, outside, "escape", "escape", "outside body")
	if err := os.Symlink(filepath.Join(outside, "escape"), filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	catalog, err := Discover(DiscoveryOptions{Workspace: workspace, UserHome: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if names := catalog.Names(); len(names) != 1 || names[0] != "safe" {
		t.Fatalf("discovered names = %v", names)
	}
	safeDirectory := filepath.Join(root, "safe")
	if err := os.RemoveAll(safeDirectory); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "escape"), safeDirectory); err != nil {
		t.Fatal(err)
	}
	if _, err := catalog.Load(context.Background(), "safe"); err == nil ||
		!strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink swap load error = %v", err)
	}
}

func TestStrictMetadataAndDiscoveryBounds(t *testing.T) {
	workspace := t.TempDir()
	root := filepath.Join(workspace, ".agents", "skills")
	writeRawSkill(t, root, "unknown", `---
name: unknown
description: unknown
arbitrary: rejected
---
body
`)
	deep := filepath.Join(root, "one", "two", "three")
	writeSkill(t, deep, "too-deep", "deep", "body")
	catalog, err := Discover(DiscoveryOptions{
		Workspace: workspace, UserHome: t.TempDir(), Limits: Limits{MaxDepth: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	if names := catalog.Names(); len(names) != 0 {
		t.Fatalf("bounded discovery names = %v", names)
	}
	if len(catalog.Issues()) == 0 {
		t.Fatal("strict metadata rejection was not reported")
	}
}

func writeSkill(t *testing.T, root, name, description, body string) {
	t.Helper()
	writeRawSkill(t, root, name, fmt.Sprintf(`---
name: %s
description: %s
---
%s
`, name, description, body))
}

func writeRawSkill(t *testing.T, root, name, content string) {
	t.Helper()
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeGovernedSkill(
	t *testing.T,
	root, name, version, description, body string,
	dependencies map[string]string,
) {
	t.Helper()
	writeSkill(t, root, name, description, body)
	var dependencyLines strings.Builder
	if len(dependencies) != 0 {
		dependencyLines.WriteString("\n[dependencies]\n")
		var names []string
		for dependency := range dependencies {
			names = append(names, dependency)
		}
		sort.Strings(names)
		for _, dependency := range names {
			fmt.Fprintf(&dependencyLines, "%s = %q\n", dependency, dependencies[dependency])
		}
	}
	content := fmt.Sprintf(
		"schema_version = 1\nname = %q\nversion = %q\ncodehelper = \">=1.0.0 <2.0.0\"\n%s",
		name, version, dependencyLines.String(),
	)
	if err := os.WriteFile(
		filepath.Join(root, name, ManifestFileName), []byte(content), 0o600,
	); err != nil {
		t.Fatal(err)
	}
}
