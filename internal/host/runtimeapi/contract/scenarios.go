package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	pluginruntime "github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// fixturePath resolves a provider fixture relative to a host package's directory,
// which is four levels below the module root.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join(
		"..", "..", "..", "..", "testdata", "providers", name,
	))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("provider fixture %q: %v", name, err)
	}
	return path
}

// Scenarios is the list every transport runs. Each one is a statement about the
// protocol, not about a transport: if a scenario can only be written one way for
// one envelope, it belongs in that transport's own suite instead.
func Scenarios() []Scenario {
	return []Scenario{
		{
			Name: "a turn reaches exactly one terminal event",
			Setup: func(t *testing.T) Setup {
				return Setup{Fixture: fixturePath(t, "openai"), Prompt: "say hello"}
			},
			Run: turnReachesOneTerminal,
		},
		{
			Name: "a client resuming from its cursor sees no gap and no duplicate",
			Setup: func(t *testing.T) Setup {
				return Setup{Fixture: fixturePath(t, "openai"), Prompt: "say hello"}
			},
			Run: resumeFromCursorIsLossless,
		},
		{
			Name: "history omits the live-only deltas and keeps the rest",
			Setup: func(t *testing.T) Setup {
				return Setup{Fixture: fixturePath(t, "openai"), Prompt: "say hello"}
			},
			Run: historyKeepsWhatReplayNeeds,
		},
		{
			Name: "read models expose the completed thread and its usage",
			Setup: func(t *testing.T) Setup {
				return Setup{Fixture: fixturePath(t, "openai"), Prompt: "say hello"}
			},
			Run: readModelsExposeCompletedThread,
		},
		{
			Name: "session profile revision and cache reset are shared",
			Setup: func(t *testing.T) Setup {
				return Setup{Fixture: fixturePath(t, "openai"), Prompt: "say hello"}
			},
			Run: sessionProfileRevisionIsShared,
		},
		{
			Name: "session tool catalog and allowlist are shared",
			Setup: func(t *testing.T) Setup {
				return Setup{
					Fixture:   fixturePath(t, "openai"),
					Workspace: t.TempDir(), Tools: true,
				}
			},
			Run: sessionToolCatalogIsShared,
		},
		{
			Name: "session lifecycle query and protection are shared",
			Setup: func(t *testing.T) Setup {
				return Setup{
					Fixture:   fixturePath(t, "openai"),
					Workspace: t.TempDir(),
				}
			},
			Run: sessionLifecycleIsShared,
		},
		{
			Name: "Checkpoint Restore is state-only and Fork lineage is shared",
			Setup: func(t *testing.T) Setup {
				return Setup{
					Fixture:   fixturePath(t, "openai"),
					Prompt:    "say hello",
					Workspace: t.TempDir(),
				}
			},
			Run: checkpointRestoreAndForkAreShared,
		},
		{
			Name:  "editor context receipts are shared and durable",
			Setup: editorContextSetup,
			Run:   editorContextReceiptsAreSharedAndDurable,
		},
		{
			Name: "catalog change and receipt use the same sampling snapshot",
			Setup: func(t *testing.T) Setup {
				return Setup{Fixture: fixturePath(t, "openai"), Prompt: "say hello"}
			},
			Run: catalogChangeMatchesReceipt,
		},
		{
			Name: "MCP health changes are visible on the shared event stream",
			Setup: func(t *testing.T) Setup {
				return Setup{
					Fixture: fixturePath(t, "openai"), Prompt: "say hello",
					Workspace: t.TempDir(), Tools: true, MCPConfig: mcpFixtureConfig(t),
				}
			},
			Run: mcpHealthIsVisible,
		},
		{
			Name: "extension lifecycle is visible on the shared event stream",
			Setup: func(t *testing.T) Setup {
				return pluginLifecycleSetup(t)
			},
			Run: extensionLifecycleIsVisible,
		},
		{
			Name: "trusted dynamic tools register execute replace and revoke",
			Setup: func(t *testing.T) Setup {
				return Setup{
					Fixture: fixturePath(t, "dynamic-tools"), Prompt: "call host echo",
					Workspace: t.TempDir(), Tools: true, TrustedDynamicTools: true,
					MaxSteps: 2,
				}
			},
			Run: dynamicToolsCompleteLifecycle,
		},
		{
			Name: "a canceled turn reaches exactly one terminal event",
			Setup: func(t *testing.T) Setup {
				return Setup{Fixture: fixturePath(t, "slow"), Prompt: "wait for interrupt"}
			},
			Run: cancelReachesOneTerminal,
		},
		{
			Name: "an operation naming a turn that does not exist is refused",
			Setup: func(t *testing.T) Setup {
				return Setup{Fixture: fixturePath(t, "openai"), Prompt: "say hello"}
			},
			Run: unknownTurnIsRefused,
		},
		{
			Name: "an approval parks the turn and a decision resumes it",
			Setup: func(t *testing.T) Setup {
				workspace := t.TempDir()
				rules := filepath.Join(workspace, "repository-rules.json")
				if err := os.WriteFile(
					rules, []byte(`[{"tool":"file_apply","action":"ask"}]`), 0o600,
				); err != nil {
					t.Fatal(err)
				}
				return Setup{
					Fixture: fixturePath(t, "tools"), Prompt: "create result",
					Workspace: workspace, Tools: true, RepositoryRules: rules, MaxSteps: 8,
				}
			},
			Run: approvalParksAndResumes,
		},
	}
}

func checkpointRestoreAndForkAreShared(
	t *testing.T,
	host Host,
	setup Setup,
) {
	live, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	turn, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	var created *protocol.CheckpointCreatedData
	deadline := time.After(waitTimeout)
	for created == nil {
		select {
		case event, open := <-live:
			if !open {
				t.Fatal("live stream closed before Checkpoint creation")
			}
			if event.TurnID != turn.TurnID {
				continue
			}
			if data, ok := event.Data.(*protocol.CheckpointCreatedData); ok {
				created = data
			}
		case <-deadline:
			t.Fatal("Checkpoint creation was not observed")
		}
	}
	list, err := host.ListCheckpoints(t.Context(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := list.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(list.Checkpoints) != 1 ||
		list.Checkpoints[0].ID != created.Checkpoint.ID {
		t.Fatalf("%s: Checkpoints = %+v", host.Transport(), list)
	}
	restored, err := host.RestoreCheckpoint(
		t.Context(),
		created.Checkpoint.ID,
	)
	if err != nil {
		t.Fatal(err)
	}
	if restored.SideEffectsReplayed {
		t.Fatal("Checkpoint Restore replayed side effects")
	}
	forked, err := host.ForkCheckpoint(
		t.Context(),
		created.Checkpoint.ID,
		"Contract Fork",
	)
	if err != nil {
		t.Fatal(err)
	}
	if forked.ParentID != created.Checkpoint.ThreadID ||
		forked.ThreadID == created.Checkpoint.ThreadID ||
		forked.Checkpoint.ID != created.Checkpoint.ID {
		t.Fatalf("%s: Checkpoint Fork = %+v", host.Transport(), forked)
	}
}

func sessionLifecycleIsShared(t *testing.T, host Host, _ Setup) {
	list, err := host.ListSessions(t.Context(), protocol.SessionListQuery{
		Query: "contract", Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := list.Validate(); err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 {
		t.Fatalf("%s: lifecycle list = %+v", host.Transport(), list)
	}
	current := list.Sessions[0]
	title := "Pinned lifecycle fixture"
	pinned := true
	updated, err := host.UpdateSessionLifecycle(
		t.Context(),
		current.Revision,
		protocol.SessionLifecyclePatch{Title: &title, Pinned: &pinned},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Session.Title != title || !updated.Session.Pinned ||
		updated.Session.Revision != current.Revision+1 {
		t.Fatalf("%s: lifecycle update = %+v", host.Transport(), updated)
	}
	archived := true
	updated, err = host.UpdateSessionLifecycle(
		t.Context(),
		updated.Session.Revision,
		protocol.SessionLifecyclePatch{Archived: &archived},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.Session.Archived {
		t.Fatalf("%s: archived lifecycle = %+v", host.Transport(), updated)
	}
	list, err = host.ListSessions(t.Context(), protocol.SessionListQuery{
		IncludeArchived: true, PinnedOnly: true, Limit: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Sessions) != 1 || !list.Sessions[0].Archived {
		t.Fatalf("%s: archived list = %+v", host.Transport(), list)
	}
	if _, err := host.DeleteSession(
		t.Context(),
		updated.Session.Revision,
	); err == nil {
		t.Fatalf("%s: deleted the last session", host.Transport())
	}
}

func sessionToolCatalogIsShared(t *testing.T, host Host, _ Setup) {
	snapshot, err := host.SessionProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := host.SessionToolCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := catalog.Validate(); err != nil {
		t.Fatalf("%s: invalid tool catalog: %v", host.Transport(), err)
	}
	if len(catalog.Tools) < 2 {
		t.Fatalf("%s: tool catalog = %+v", host.Transport(), catalog)
	}
	for _, entry := range catalog.Tools {
		if !entry.Enabled || !entry.Guarded {
			t.Fatalf("%s: default tool entry = %+v", host.Transport(), entry)
		}
	}
	selected := []string{catalog.Tools[0].ID}
	updated, err := host.UpdateSessionProfile(
		t.Context(),
		snapshot.Profile.Revision,
		protocol.SessionProfilePatch{EnabledToolIDs: &selected},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !updated.PromptCacheReset || updated.ResetReason != "enabled_tool_ids" {
		t.Fatalf("%s: tool profile update = %+v", host.Transport(), updated)
	}
	catalog, err = host.SessionToolCatalog(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range catalog.Tools {
		if entry.Enabled != (entry.ID == selected[0]) {
			t.Fatalf("%s: selected tool entry = %+v", host.Transport(), entry)
		}
	}
}

func sessionProfileRevisionIsShared(t *testing.T, host Host, _ Setup) {
	snapshot, err := host.SessionProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Profile.Revision == 0 ||
		snapshot.Profile.PromptCacheRevision == 0 ||
		snapshot.Profile.Provider == "" ||
		snapshot.Profile.Model == "" ||
		snapshot.Capabilities.Provider != snapshot.Profile.Provider ||
		snapshot.Capabilities.Model != snapshot.Profile.Model {
		t.Fatalf("%s: session profile = %+v", host.Transport(), snapshot)
	}
	mode := "plan"
	updated, err := host.UpdateSessionProfile(
		t.Context(),
		snapshot.Profile.Revision,
		protocol.SessionProfilePatch{Mode: &mode},
	)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Profile.Revision != snapshot.Profile.Revision+1 ||
		updated.Profile.PromptCacheRevision !=
			snapshot.Profile.PromptCacheRevision+1 ||
		!updated.PromptCacheReset ||
		updated.ResetReason != "mode" {
		t.Fatalf("%s: profile update = %+v", host.Transport(), updated)
	}
	if _, err := host.UpdateSessionProfile(
		t.Context(),
		snapshot.Profile.Revision,
		protocol.SessionProfilePatch{Mode: &mode},
	); err == nil {
		t.Fatalf("%s: stale session profile revision was accepted", host.Transport())
	}
	recovered, err := host.SessionProfile(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if recovered.Profile.Revision != updated.Profile.Revision ||
		recovered.Profile.Mode != mode {
		t.Fatalf("%s: recovered profile = %+v", host.Transport(), recovered)
	}
}

func editorContextSetup(t *testing.T) Setup {
	t.Helper()
	workspace := t.TempDir()
	content := []byte("package fixture\n\nconst Value = \"context-sentinel-4821\"\n")
	path := filepath.Join(workspace, "context.go")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	identity, err := protocol.NewWorkspaceIdentity(
		(&url.URL{
			Scheme: "vscode-remote", Host: "ssh-remote+contract",
			Path: workspace,
		}).String(),
		workspace,
		"ssh-remote",
	)
	if err != nil {
		t.Fatal(err)
	}
	return Setup{
		Fixture: fixturePath(t, "editor-context"), Prompt: "inspect active file",
		Workspace: workspace, WorkspaceIdentity: &identity,
	}
}

func editorContextReceiptsAreSharedAndDurable(
	t *testing.T,
	host Host,
	setup Setup,
) {
	content, err := os.ReadFile(filepath.Join(setup.Workspace, "context.go"))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(content)
	if setup.WorkspaceIdentity == nil {
		t.Fatal("editor context setup omitted workspace identity")
	}
	reference := protocol.EditorContextReference{
		Kind:   protocol.EditorContextDiagnostics,
		Source: protocol.EditorContextSourceCodeAction,
		URI: (&url.URL{
			Scheme: "vscode-remote", Host: "ssh-remote+contract",
			Path: filepath.Join(setup.Workspace, "context.go"),
		}).String(),
		Path: "context.go", DocumentVersion: 1,
		Digest: hex.EncodeToString(sum[:]), Explicit: true,
		Diagnostics: []protocol.EditorDiagnostic{{
			Range: protocol.EditorRange{
				Start: protocol.EditorPosition{Line: 2, Character: 6},
				End:   protocol.EditorPosition{Line: 2, Character: 11},
			},
			Severity: "error", Code: "fixture",
			Message: "context fixture diagnostic", Source: "contract",
		}},
		OmittedDiagnostics: 2,
	}
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	started, err := host.StartTurnWithContext(
		t.Context(), setup.Prompt, setup.WorkspaceIdentity,
		[]protocol.EditorContextReference{reference},
	)
	if err != nil {
		t.Fatal(err)
	}
	seen := collectUntilTerminal(t, host, events, started.TurnID)
	var opened, completed []protocol.EditorContextReceipt
	for _, event := range seen {
		switch data := event.Data.(type) {
		case *protocol.TurnStartedData:
			opened = data.EditorContext
		case *protocol.ExecutionReceiptData:
			completed = data.EditorContext
		}
	}
	if len(opened) != 1 || len(completed) != 1 ||
		opened[0].Kind != protocol.EditorContextDiagnostics ||
		opened[0].Source != protocol.EditorContextSourceCodeAction ||
		opened[0].Path != "context.go" ||
		opened[0].DiagnosticCount != 1 ||
		opened[0].OmittedDiagnostics != 2 ||
		opened[0].OriginalBytes != len(content) ||
		opened[0].RetainedBytes != len(content) {
		t.Fatalf("%s: turn.started editor context = %+v", host.Transport(), opened)
	}
	if !reflect.DeepEqual(opened, completed) {
		t.Fatalf(
			"%s: started context=%+v receipt context=%+v",
			host.Transport(), opened, completed,
		)
	}
	history, err := host.History(t.Context(), 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range history {
		if data, ok := event.Data.(*protocol.ExecutionReceiptData); ok &&
			reflect.DeepEqual(data.EditorContext, completed) {
			return
		}
	}
	t.Fatalf("%s: durable history omitted editor context receipt", host.Transport())
}

func readModelsExposeCompletedThread(t *testing.T, host Host, setup Setup) {
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	started, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	seen := collectUntilTerminal(t, host, events, started.TurnID)
	if seen[len(seen)-1].Kind != protocol.EventTurnCompleted {
		t.Fatalf("%s: read-model turn ended with %s", host.Transport(), seen[len(seen)-1].Kind)
	}
	state, err := host.ReadState(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Threads) != 1 || state.Threads[0].ID != started.ThreadID {
		t.Fatalf("%s: threads = %+v", host.Transport(), state.Threads)
	}
	if state.Thread.ID != started.ThreadID || len(state.Thread.Turns) != 1 ||
		state.Thread.Turns[0].ID != started.TurnID ||
		state.Thread.Turns[0].Status != "completed" {
		t.Fatalf("%s: thread detail = %+v", host.Transport(), state.Thread)
	}
	if len(state.Tasks) != 0 || len(state.Agents) != 0 {
		t.Fatalf(
			"%s: unexpected tasks/agents = %+v / %+v",
			host.Transport(), state.Tasks, state.Agents,
		)
	}
	if len(state.Usage) != 1 || state.Usage[0].ThreadID != started.ThreadID ||
		state.Usage[0].TurnID != started.TurnID || state.Rollup.Turns != 1 ||
		state.Rollup.Calls != state.Usage[0].Calls {
		t.Fatalf(
			"%s: usage/rollup = %+v / %+v",
			host.Transport(), state.Usage, state.Rollup,
		)
	}
}

func dynamicToolsCompleteLifecycle(t *testing.T, host Host, setup Setup) {
	spec := protocol.DynamicToolSpec{
		Version: protocol.DynamicToolSpecVersion, Name: "host_echo",
		Description: "Echo a value through the trusted host",
		InputSchema: map[string]any{
			"type":       "object",
			"properties": map[string]any{"value": map[string]any{"type": "string"}},
			"required":   []any{"value"}, "additionalProperties": false,
		},
	}
	registered, err := host.RegisterDynamic(t.Context(), spec)
	if err != nil {
		t.Fatal(err)
	}
	if len(registered.Tools) != 1 || registered.Tools[0].ToolName() != "host_echo" {
		t.Fatalf("%s: registered catalog = %+v", host.Transport(), registered)
	}
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	started, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	seen := collectUntilTerminal(t, host, events, started.TurnID)
	if seen[len(seen)-1].Kind != protocol.EventTurnCompleted {
		t.Fatalf(
			"%s: dynamic turn ended with %s: %+v",
			host.Transport(), seen[len(seen)-1].Kind, seen[len(seen)-1].Data,
		)
	}

	replacement := spec
	replacement.Description = "Echo a replacement value through the trusted host"
	if _, err := host.ReplaceDynamic(
		t.Context(), replacement, registered.Generation-1,
	); err == nil {
		t.Fatalf("%s: stale replace succeeded", host.Transport())
	}
	replaced, err := host.ReplaceDynamic(t.Context(), replacement, registered.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := host.RevokeDynamic(
		t.Context(), "host_echo", registered.Generation,
	); err == nil {
		t.Fatalf("%s: stale revoke succeeded", host.Transport())
	}
	revoked, err := host.RevokeDynamic(t.Context(), "host_echo", replaced.Generation)
	if err != nil {
		t.Fatal(err)
	}
	if len(revoked.Tools) != 0 {
		t.Fatalf("%s: revoked catalog = %+v", host.Transport(), revoked)
	}
}

func pluginLifecycleSetup(t *testing.T) Setup {
	t.Helper()
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	workspacePlugins := filepath.Join(root, "plugins")
	userPlugins := filepath.Join(root, "user-plugins")
	builtinPlugins := filepath.Join(root, "builtin-plugins")
	stateDirectory := filepath.Join(root, "state")
	stagingRoot := filepath.Join(root, "staging")
	for _, path := range []string{
		workspace, workspacePlugins, userPlugins, builtinPlugins, stateDirectory,
	} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	bundle := filepath.Join(workspacePlugins, "fixture")
	if err := os.Mkdir(bundle, 0o700); err != nil {
		t.Fatal(err)
	}
	executable := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(
		filepath.Join(bundle, "run.sh"), executable, 0o700,
	); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(executable)
	manifest := pluginruntime.Manifest{
		SchemaVersion: 1, Name: "fixture", Executable: "run.sh",
		ExecutableSHA256: hex.EncodeToString(sum[:]), Generation: 1,
		Capabilities: pluginruntime.CapabilityInventory{
			Tools: []string{"plugin_run"}, FilesystemRoots: []string{"workspace"},
			AllowProcess: true,
		},
	}
	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(bundle, pluginruntime.ManifestName), manifestData, 0o600,
	); err != nil {
		t.Fatal(err)
	}
	reviewedAt := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	receipt, err := pluginruntime.Review(
		bundle, manifest.Capabilities, manifest.Generation, reviewedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	stager, err := pluginruntime.NewStager(stagingRoot)
	if err != nil {
		t.Fatal(err)
	}
	staged, err := stager.Stage(bundle)
	if err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(stateDirectory, "plugins.json")
	store, err := pluginruntime.OpenStateStore(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Update(func(state *pluginruntime.PersistentState) error {
		state.Plugins["fixture"] = pluginruntime.PluginState{
			Receipt: receipt, Enabled: true, Source: pluginruntime.RootWorkspace,
			StagedHash: staged.ContentHash,
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return Setup{
		Fixture: fixturePath(t, "openai"), Prompt: "say hello",
		Workspace: workspace, Tools: true,
		PluginWorkspaceRoot: workspacePlugins, PluginUserRoot: userPlugins,
		PluginBuiltinRoot: builtinPlugins, PluginStatePath: statePath,
		PluginStagingRoot: stagingRoot,
	}
}

func mcpFixtureConfig(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	config := map[string]any{
		"version": 1,
		"servers": map[string]any{
			"fixture": map[string]any{
				"transport": "stdio",
				"command":   "go",
				"args": []string{
					"run", "./internal/adapter/mcp/testdata/fixture", "--transport=stdio",
				},
				"working_directory": root,
				"connect_timeout":   "30s",
				"tools": map[string]any{
					"fixture.echo": map[string]any{
						"capability": "read", "access_mode": "read",
						"parallel_policy": "concurrent", "sandbox_requirement": "none",
					},
				},
			},
		},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "mcp.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func mcpHealthIsVisible(t *testing.T, host Host, setup Setup) {
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	started, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	seen := collectUntilTerminal(t, host, events, started.TurnID)
	for _, event := range seen {
		data, ok := event.Data.(*protocol.MCPHealthChangedData)
		if !ok {
			continue
		}
		if data.Server != "fixture" || data.State != "healthy" {
			t.Fatalf("%s: MCP health = %+v", host.Transport(), data)
		}
		return
	}
	t.Fatalf("%s: no mcp.health.changed in %s", host.Transport(), kindsOf(seen))
}

func extensionLifecycleIsVisible(t *testing.T, host Host, setup Setup) {
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	started, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	seen := collectUntilTerminal(t, host, events, started.TurnID)
	for _, event := range seen {
		data, ok := event.Data.(*protocol.ExtensionLifecycleData)
		if !ok {
			continue
		}
		if data.ExtensionKind != "plugin" || data.Name != "fixture" ||
			data.Action != "active" || data.Version != "local" ||
			data.Source != "workspace" || data.Trust != "unsigned-local" ||
			!data.Enabled || data.Digest == "" {
			t.Fatalf("%s: extension lifecycle = %+v", host.Transport(), data)
		}
		return
	}
	t.Fatalf("%s: no extension.lifecycle in %s", host.Transport(), kindsOf(seen))
}

func catalogChangeMatchesReceipt(t *testing.T, host Host, setup Setup) {
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	started, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	seen := collectUntilTerminal(t, host, events, started.TurnID)
	var changed *protocol.ToolCatalogChangedData
	var receipt *protocol.ExecutionReceiptData
	for _, event := range seen {
		switch data := event.Data.(type) {
		case *protocol.ToolCatalogChangedData:
			changed = data
		case *protocol.ExecutionReceiptData:
			receipt = data
		}
	}
	if changed == nil {
		t.Fatalf("%s: no tool.catalog.changed in %s", host.Transport(), kindsOf(seen))
	}
	if receipt == nil || receipt.Catalog == nil {
		t.Fatalf("%s: turn receipt has no catalog in %s", host.Transport(), kindsOf(seen))
	}
	if changed.CatalogID != receipt.Catalog.CatalogID ||
		changed.Generation != receipt.Catalog.Generation ||
		changed.Digest != receipt.Catalog.Digest {
		t.Fatalf(
			"%s: catalog event=%+v receipt=%+v",
			host.Transport(), changed, receipt.Catalog,
		)
	}
}

func turnReachesOneTerminal(t *testing.T, host Host, setup Setup) {
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.ThreadID == "" || receipt.TurnID == "" || receipt.ItemID == "" {
		t.Fatalf("%s: receipt = %+v, want all three references", host.Transport(), receipt)
	}
	seen := collectUntilTerminal(t, host, events, receipt.TurnID)
	if count := countTerminals(seen); count != 1 {
		t.Fatalf("%s: %d terminal events in %s", host.Transport(), count, kindsOf(seen))
	}
	if seen[0].Kind != protocol.EventTurnStarted {
		t.Fatalf("%s: first event = %s, want turn.started", host.Transport(), seen[0].Kind)
	}
	if seen[len(seen)-1].Kind != protocol.EventTurnCompleted {
		t.Fatalf("%s: ended with %s in %s", host.Transport(), seen[len(seen)-1].Kind, kindsOf(seen))
	}
	// Every event names the turn and thread it belongs to, or a client cannot route
	// it, and sequences only ever go up.
	var previous protocol.Cursor
	for _, event := range seen {
		if event.ThreadID != receipt.ThreadID {
			t.Fatalf("%s: %s carries thread %s, want %s",
				host.Transport(), event.Kind, event.ThreadID, receipt.ThreadID)
		}
		if event.Sequence <= previous {
			t.Fatalf("%s: sequence %d did not advance past %d",
				host.Transport(), event.Sequence, previous)
		}
		previous = event.Sequence
	}
}

func resumeFromCursorIsLossless(t *testing.T, host Host, setup Setup) {
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	seen := collectUntilTerminal(t, host, events, receipt.TurnID)

	// A client that stored the cursor of the first event it consumed must get the
	// rest exactly once when it comes back.
	resumed, err := host.History(t.Context(), seen[0].Sequence, 256)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[protocol.EventID]int{}
	for _, event := range resumed {
		if event.Sequence <= seen[0].Sequence {
			t.Fatalf("%s: resume returned sequence %d at or before the cursor %d",
				host.Transport(), event.Sequence, seen[0].Sequence)
		}
		byID[event.ID]++
		if byID[event.ID] > 1 {
			t.Fatalf("%s: event %s delivered twice on resume", host.Transport(), event.ID)
		}
	}
	// Nothing persisted for this turn may be missing from the resumed page.
	for _, event := range seen {
		if event.Sequence <= seen[0].Sequence || !persisted(event.Kind) {
			continue
		}
		if byID[event.ID] == 0 {
			t.Fatalf("%s: resume lost %s (sequence %d); resumed %s",
				host.Transport(), event.Kind, event.Sequence, kindsOf(resumed))
		}
	}
	// The terminal event is the one a client must not miss: it is what tells the
	// client the turn is over.
	if countTerminals(resumed) != 1 {
		t.Fatalf("%s: resumed page terminals = %s", host.Transport(), kindsOf(resumed))
	}
}

func historyKeepsWhatReplayNeeds(t *testing.T, host Host, setup Setup) {
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	seen := collectUntilTerminal(t, host, events, receipt.TurnID)
	history, err := host.History(t.Context(), 0, 256)
	if err != nil {
		t.Fatal(err)
	}
	inHistory := map[protocol.EventID]protocol.Event{}
	for _, event := range history {
		inHistory[event.ID] = event
	}
	// Both transports must draw the persisted/live-only line in the same place: a
	// client that reconstructs a turn from history gets the receipts, not the
	// keystroke-by-keystroke deltas it would have no use for after the fact.
	for _, event := range seen {
		_, kept := inHistory[event.ID]
		if persisted(event.Kind) && !kept {
			t.Fatalf("%s: history dropped %s, which a resuming client needs",
				host.Transport(), event.Kind)
		}
		if !persisted(event.Kind) && kept {
			t.Fatalf("%s: history kept live-only %s", host.Transport(), event.Kind)
		}
	}
	// And the other direction: a host must not filter the live stream down to the
	// kinds it happens to understand. Anything durable enough to be in history had
	// to reach the client while it was happening.
	live := map[protocol.EventID]bool{}
	for _, event := range seen {
		live[event.ID] = true
	}
	for _, event := range history {
		if event.TurnID != receipt.TurnID {
			continue
		}
		if !live[event.ID] {
			t.Fatalf("%s: %s was persisted but never delivered live",
				host.Transport(), event.Kind)
		}
	}
	// A live stream that carried no deltas at all would make the assertion above
	// vacuous, so the scenario checks its own premise.
	sawLiveOnly := false
	for _, event := range seen {
		if !persisted(event.Kind) {
			sawLiveOnly = true
		}
	}
	if !sawLiveOnly {
		t.Fatalf("%s: the fixture turn produced no live-only event: %s",
			host.Transport(), kindsOf(seen))
	}
}

// persisted mirrors the event log's policy. It is duplicated here on purpose: the
// contract is what a client observes, and a change to the policy has to be a
// deliberate change to this list rather than something the test absorbs silently.
func persisted(kind protocol.EventKind) bool {
	switch kind {
	case protocol.EventOutputDelta, protocol.EventReasoningDelta,
		protocol.EventToolState, protocol.EventToolOutput, protocol.EventTurnCompaction:
		return false
	default:
		return true
	}
}

func cancelReachesOneTerminal(t *testing.T, host Host, setup Setup) {
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	// Cancel only means something once the turn is running.
	waitForKind(t, host, events, receipt.TurnID, protocol.EventTurnStarted)
	if _, err := host.Cancel(t.Context(), receipt, protocol.CancelReasonUserInterrupted); err != nil {
		t.Fatal(err)
	}
	seen := collectUntilTerminal(t, host, events, receipt.TurnID)
	if count := countTerminals(seen); count != 1 {
		t.Fatalf("%s: %d terminal events in %s", host.Transport(), count, kindsOf(seen))
	}
	last := seen[len(seen)-1]
	if last.Kind != protocol.EventTurnCanceled {
		t.Fatalf("%s: cancel ended with %s (%+v)", host.Transport(), last.Kind, last.Data)
	}
	data, ok := last.Data.(*protocol.TurnCanceledData)
	if !ok || data.Reason == "" {
		t.Fatalf("%s: turn.canceled data = %+v, want a reason", host.Transport(), last.Data)
	}
}

func unknownTurnIsRefused(t *testing.T, host Host, setup Setup) {
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	// The thread exists; the turn does not. A host must say so rather than invent a
	// turn or cancel the wrong one.
	real, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	collectUntilTerminal(t, host, events, real.TurnID)

	invented := Receipt{ThreadID: real.ThreadID, TurnID: "turn_does_not_exist"}
	_, err = host.Cancel(t.Context(), invented, protocol.CancelReasonUserInterrupted)
	if err == nil {
		t.Fatalf("%s: canceling a turn that does not exist was accepted", host.Transport())
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("%s: refusal = %v (%T), want a protocol error code", host.Transport(), err, err)
	}
	// Both transports must agree on which code this is, or a client has to special
	// case the transport it happens to be on. The protocol vocabulary has no
	// not_found: naming something that does not exist is an invalid argument, and
	// each transport adds its own detail (a 404 status, a JSON-RPC code) on top.
	if refusal.Code != protocol.CodeInvalidArgument {
		t.Fatalf("%s: refusal code = %q, want %q",
			host.Transport(), refusal.Code, protocol.CodeInvalidArgument)
	}
	if refusal.Retryable {
		t.Fatalf("%s: refusal claims retryable; the turn will never exist", host.Transport())
	}
}

func approvalParksAndResumes(t *testing.T, host Host, setup Setup) {
	events, err := host.Live(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := host.StartTurn(t.Context(), setup.Prompt)
	if err != nil {
		t.Fatal(err)
	}
	required := waitForKind(t, host, events, receipt.TurnID, protocol.EventApprovalRequired)
	approval, ok := required.Data.(*protocol.ApprovalRequiredData)
	if !ok || approval.RequestID == "" {
		t.Fatalf("%s: approval.required data = %+v", host.Transport(), required.Data)
	}
	planID := ""
	if approval.EditPlan != nil {
		planID = approval.EditPlan.ID
	}
	// The decision arrives while the turn is parked, which is only possible if the
	// event subscription outlives the request that started the turn.
	if _, err := host.Decide(
		t.Context(), receipt, approval.RequestID, protocol.ApprovalApprove,
		planID,
	); err != nil {
		t.Fatal(err)
	}
	resolved := waitForKind(t, host, events, receipt.TurnID, protocol.EventApprovalResolved)
	if data, ok := resolved.Data.(*protocol.ApprovalResolvedData); !ok ||
		data.RequestID != approval.RequestID {
		t.Fatalf("%s: approval.resolved = %+v, want request %s",
			host.Transport(), resolved.Data, approval.RequestID)
	}
	seen := collectUntilTerminal(t, host, events, receipt.TurnID)
	if last := seen[len(seen)-1]; last.Kind != protocol.EventTurnCompleted {
		t.Fatalf("%s: approved turn ended with %s", host.Transport(), last.Kind)
	}
}
