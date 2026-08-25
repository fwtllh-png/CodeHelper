package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestLoadPrecedence(t *testing.T) {
	path := writeConfig(t, `
[runtime]
operation_buffer = 10
event_history = 20
subscriber_buffer = 30

[state]
data_dir = "/file/state"
busy_timeout = "7s"
event_retention = 5000

[telemetry]
log_level = "warn"

[credential]
kind = "env"
name = "FILE_API_KEY"

[execution]
provider = "file-provider"
model = "file-model"
protocol = "anthropic"
mode = "plan"
workspace = "/file"
tools = true
max_output_tokens = 2048
max_steps = 3
timeout = "45s"
idle_timeout = "15s"
max_concurrent = 2
rate_limit = 4.5
budget_tokens = 9000
budget_usd = 2.5
reasoning_effort = "medium"
native_search = true
`)
	cliBuffer := 40
	cliLevel := "error"
	cliSteps := 9
	startupStateDir := "/startup/state"
	snapshot, err := Load(LoadOptions{
		Path: path,
		LookupEnv: envLookup(map[string]string{
			"CODEHELPER_RUNTIME_OPERATION_BUFFER": "15",
			"CODEHELPER_RUNTIME_EVENT_HISTORY":    "25",
			"CODEHELPER_STATE_BUSY_TIMEOUT":       "9s",
			"CODEHELPER_LOG_LEVEL":                "debug",
			"CODEHELPER_CREDENTIAL_NAME":          "ENV_API_KEY",
			"CODEHELPER_PROVIDER":                 "env-provider",
			"CODEHELPER_MAX_STEPS":                "6",
		}),
		Overrides: Overrides{
			OperationBuffer: &cliBuffer,
			StateDataDir:    &startupStateDir,
			LogLevel:        &cliLevel,
			MaxSteps:        &cliSteps,
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	if snapshot.Config.Runtime.OperationBuffer != 40 ||
		snapshot.Config.Runtime.EventHistory != 25 ||
		snapshot.Config.Runtime.SubscriberBuffer != 30 {
		t.Fatalf("unexpected runtime config: %+v", snapshot.Config.Runtime)
	}
	if snapshot.Config.Telemetry.LogLevel != "error" {
		t.Fatalf("log level = %q, want error", snapshot.Config.Telemetry.LogLevel)
	}
	if snapshot.Config.State.DataDir != "/startup/state" ||
		snapshot.Config.State.BusyTimeout.String() != "9s" ||
		snapshot.Config.State.EventRetention != 5000 {
		t.Fatalf("unexpected state config: %+v", snapshot.Config.State)
	}
	if snapshot.Config.Credential != (SecretRef{Kind: "env", Name: "ENV_API_KEY"}) {
		t.Fatalf("credential = %+v", snapshot.Config.Credential)
	}
	if snapshot.Config.Execution.Provider != "env-provider" ||
		snapshot.Config.Execution.Model != "file-model" ||
		snapshot.Config.Execution.MaxSteps != 9 ||
		snapshot.Provenance[fieldProvider] != SourceEnv ||
		snapshot.Provenance[fieldModel] != SourceFile ||
		snapshot.Provenance[fieldMaxSteps] != SourceStartup {
		t.Fatalf("execution precedence = %+v provenance=%+v", snapshot.Config.Execution, snapshot.Provenance)
	}
	wantSources := map[string]Source{
		fieldOperationBuffer:  SourceStartup,
		fieldEventHistory:     SourceEnv,
		fieldSubscriberBuffer: SourceFile,
		fieldStateDataDir:     SourceStartup,
		fieldStateBusyTimeout: SourceEnv,
		fieldStateRetention:   SourceFile,
		fieldLogLevel:         SourceStartup,
		fieldCredentialKind:   SourceFile,
		fieldCredentialName:   SourceEnv,
	}
	for field, want := range wantSources {
		if got := snapshot.Provenance[field]; got != want {
			t.Errorf("provenance[%q] = %q, want %q", field, got, want)
		}
	}
}

func TestLoadAcceptsSerializedSameWorkspaceChildren(t *testing.T) {
	path := writeConfig(t, `
[execution.subagent]
delegation = "adaptive"
workspace = "same_workspace_serialized"
`)
	snapshot, err := Load(LoadOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Config.Execution.Subagent.Workspace; got != SubagentWorkspaceSerialized {
		t.Fatalf("subagent workspace = %q", got)
	}
	if got := snapshot.Config.Execution.Subagent.Delegation; got != SubagentDelegationAdaptive {
		t.Fatalf("subagent delegation = %q", got)
	}
	if got := snapshot.Provenance[fieldSubagentDelegation]; got != SourceFile {
		t.Fatalf("delegation provenance = %q", got)
	}
}

func TestLoadSubagentTreeLimitsFromFile(t *testing.T) {
	path := writeConfig(t, `
[execution.subagent]
max_parallel = 3
max_resident = 6
max_total = 12
`)
	snapshot, err := Load(LoadOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	child := snapshot.Config.Execution.Subagent
	if child.MaxParallel != 3 || child.MaxResident != 6 || child.MaxTotal != 12 {
		t.Fatalf("subagent tree limits = %+v", child)
	}
	for _, field := range []string{
		fieldSubagentMaxParallel,
		fieldSubagentMaxResident,
		fieldSubagentMaxTotal,
	} {
		if snapshot.Provenance[field] != SourceFile {
			t.Fatalf("provenance[%s] = %q", field, snapshot.Provenance[field])
		}
	}
}

func TestLoadRejectsUnknownSubagentDelegation(t *testing.T) {
	path := writeConfig(t, `
[execution.subagent]
delegation = "proactive"
`)
	_, err := Load(LoadOptions{Path: path})
	if err == nil || !strings.Contains(err.Error(), fieldSubagentDelegation) {
		t.Fatalf("delegation error = %v", err)
	}
}

func TestLoadSubagentConfigurationFromEnvironment(t *testing.T) {
	snapshot, err := Load(LoadOptions{LookupEnv: envLookup(map[string]string{
		"CODEHELPER_SUBAGENT_DELEGATION":   "disabled",
		"CODEHELPER_SUBAGENT_MAX_DEPTH":    "7",
		"CODEHELPER_SUBAGENT_MAX_PARALLEL": "3",
		"CODEHELPER_SUBAGENT_MAX_RESIDENT": "5",
		"CODEHELPER_SUBAGENT_MAX_TOTAL":    "9",
		"CODEHELPER_SUBAGENT_MAX_STEPS":    "31",
		"CODEHELPER_SUBAGENT_MAX_TOKENS":   "12000",
		"CODEHELPER_SUBAGENT_MAX_COST_USD": "2.5",
		"CODEHELPER_SUBAGENT_WALL_TIME":    "90s",
		"CODEHELPER_SUBAGENT_WORKSPACE":    "read_only",
	})})
	if err != nil {
		t.Fatal(err)
	}
	got := snapshot.Config.Execution.Subagent
	if got.Delegation != SubagentDelegationDisabled ||
		got.MaxDepth != 7 || got.MaxParallel != 3 ||
		got.MaxResident != 5 || got.MaxTotal != 9 ||
		got.MaxSteps != 31 || got.MaxTokens != 12000 ||
		got.MaxCostUSD != 2.5 || got.WallTime != 90*time.Second ||
		got.Workspace != SubagentWorkspaceReadOnly {
		t.Fatalf("subagent environment config = %+v", got)
	}
	for _, field := range []string{
		fieldSubagentDelegation, fieldSubagentMaxDepth,
		fieldSubagentMaxParallel, fieldSubagentMaxResident,
		fieldSubagentMaxTotal, fieldSubagentMaxSteps,
		fieldSubagentMaxTokens, fieldSubagentMaxCostUSD,
		fieldSubagentWallTime, fieldSubagentWorkspace,
	} {
		if source := snapshot.Provenance[field]; source != SourceEnv {
			t.Fatalf("provenance[%s] = %q", field, source)
		}
	}
}

func TestSubagentResidentAndTotalOverridesWin(t *testing.T) {
	resident, total := 11, 13
	snapshot, err := Load(LoadOptions{Overrides: Overrides{
		SubagentMaxResident: &resident,
		SubagentMaxTotal:    &total,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Config.Execution.Subagent; got.MaxResident != resident ||
		got.MaxTotal != total {
		t.Fatalf("subagent overrides = %+v", got)
	}
	if snapshot.Provenance[fieldSubagentMaxResident] != SourceStartup ||
		snapshot.Provenance[fieldSubagentMaxTotal] != SourceStartup {
		t.Fatalf("subagent override provenance = %+v", snapshot.Provenance)
	}
}

func TestLoadPreservesAbsentAndExplicitFalseZeroOverrides(t *testing.T) {
	path := writeConfig(t, `
[execution]
tools = true
native_search = true
budget_tokens = 700
budget_usd = 3.5
`)
	fromFile, err := Load(LoadOptions{
		Path: path, LookupEnv: envLookup(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !fromFile.Config.Execution.Tools ||
		!fromFile.Config.Execution.NativeSearch ||
		fromFile.Config.Execution.BudgetTokens != 700 ||
		fromFile.Config.Execution.BudgetUSD != 3.5 {
		t.Fatalf("absent startup flags did not preserve file values: %+v", fromFile.Config.Execution)
	}

	disabled := false
	zeroTokens := uint64(0)
	zeroUSD := float64(0)
	overridden, err := Load(LoadOptions{
		Path: path,
		LookupEnv: envLookup(map[string]string{
			"CODEHELPER_TOOLS":         "true",
			"CODEHELPER_NATIVE_SEARCH": "true",
			"CODEHELPER_BUDGET_TOKENS": "900",
			"CODEHELPER_BUDGET_USD":    "4.5",
		}),
		Overrides: Overrides{
			Tools: &disabled, NativeSearch: &disabled,
			BudgetTokens: &zeroTokens, BudgetUSD: &zeroUSD,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if overridden.Config.Execution.Tools ||
		overridden.Config.Execution.NativeSearch ||
		overridden.Config.Execution.BudgetTokens != 0 ||
		overridden.Config.Execution.BudgetUSD != 0 {
		t.Fatalf("explicit false/zero overrides were lost: %+v", overridden.Config.Execution)
	}
	for _, field := range []string{fieldTools, fieldNativeSearch, fieldBudgetTokens, fieldBudgetUSD} {
		if overridden.Provenance[field] != SourceStartup {
			t.Fatalf("provenance[%q] = %q, want startup", field, overridden.Provenance[field])
		}
	}
}

func TestLoadInvalidValueReportsSource(t *testing.T) {
	_, err := Load(LoadOptions{
		LookupEnv: envLookup(map[string]string{"CODEHELPER_LOG_LEVEL": "verbose"}),
	})
	var fieldErr *FieldError
	if !errors.As(err, &fieldErr) {
		t.Fatalf("Load() error = %v, want FieldError", err)
	}
	if fieldErr.Field != fieldLogLevel || fieldErr.Source != SourceEnv {
		t.Fatalf("FieldError = %+v", fieldErr)
	}
}

func TestLoadRejectsUnknownTOMLField(t *testing.T) {
	path := writeConfig(t, "[runtime]\nunknown = 1\n")
	if _, err := Load(LoadOptions{Path: path, LookupEnv: envLookup(nil)}); err == nil {
		t.Fatal("Load() error = nil, want unknown field error")
	}
}

func TestSecretReferenceDoesNotResolveOrLeakValue(t *testing.T) {
	const secret = "credential-value-that-must-not-leak"
	snapshot, err := Load(LoadOptions{
		LookupEnv: envLookup(map[string]string{
			"CODEHELPER_CREDENTIAL_KIND": "env",
			"CODEHELPER_CREDENTIAL_NAME": "PROVIDER_API_KEY",
			"PROVIDER_API_KEY":           secret,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) {
		t.Fatalf("snapshot contains secret value: %s", data)
	}
	if !strings.Contains(string(data), "PROVIDER_API_KEY") {
		t.Fatalf("snapshot does not contain reference name: %s", data)
	}
}

func TestLoadAcceptsReferenceKindsAndRejectsUnknownKind(t *testing.T) {
	for _, kind := range []string{"env", "file", "keyring"} {
		snapshot, err := Load(LoadOptions{
			LookupEnv: envLookup(map[string]string{
				"CODEHELPER_CREDENTIAL_KIND": kind,
				"CODEHELPER_CREDENTIAL_NAME": "provider/default",
			}),
		})
		if err != nil {
			t.Fatalf("Load(%s): %v", kind, err)
		}
		if snapshot.Config.Credential.Kind != kind {
			t.Fatalf("credential = %+v", snapshot.Config.Credential)
		}
	}
	_, err := Load(LoadOptions{LookupEnv: envLookup(map[string]string{
		"CODEHELPER_CREDENTIAL_KIND": "inline",
		"CODEHELPER_CREDENTIAL_NAME": "provider/default",
	})})
	var fieldErr *FieldError
	if !errors.As(err, &fieldErr) || fieldErr.Field != fieldCredentialKind {
		t.Fatalf("Load(inline) error = %v, want credential kind FieldError", err)
	}
}

func TestDefaultProvenanceFieldSetGolden(t *testing.T) {
	provenance := defaultProvenance()
	fields := make([]string, 0, len(provenance))
	for field, source := range provenance {
		if source != SourceDefault {
			t.Fatalf("default provenance[%q] = %q", field, source)
		}
		fields = append(fields, field)
	}
	sort.Strings(fields)
	got := strings.Join(fields, "\n") + "\n"
	path := filepath.Join("testdata", "default-provenance.golden")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf(
			"default provenance field set drifted; review every source path and "+
				"refresh with UPDATE_GOLDEN=1\ngot:\n%s\nwant:\n%s",
			got, want,
		)
	}
}

func TestDefaultExecutionTokenBudgetsAreDerivedAtRuntime(t *testing.T) {
	defaults := Defaults()
	if defaults.Execution.TurnBudgetTokens != 0 ||
		defaults.Execution.Subagent.MaxTokens != 0 {
		t.Fatalf("default execution budgets = %+v", defaults.Execution)
	}
	if defaults.Execution.BudgetTokens != 0 {
		t.Fatalf(
			"session budget=%d, want unlimited independently of turn budget",
			defaults.Execution.BudgetTokens,
		)
	}
}

func TestProviderPhaseDeadlinesOverrideCompatibleTimeout(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "codehelper.toml")
	err := os.WriteFile(path, []byte(`
[execution]
timeout = "2m"
connection_timeout = "3s"
tls_handshake_timeout = "4s"
response_header_timeout = "5s"
`), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	settings, err := Load(LoadOptions{
		Path:      path,
		LookupEnv: envLookup(nil),
	})
	if err != nil {
		t.Fatal(err)
	}
	execution := settings.Config.Execution
	if execution.Timeout != 2*time.Minute ||
		execution.ConnectionTimeout != 3*time.Second ||
		execution.TLSHandshakeTimeout != 4*time.Second ||
		execution.ResponseHeaderTimeout != 5*time.Second {
		t.Fatalf("execution deadlines = %+v", execution)
	}
}

func TestVisionConfigFileAndValidation(t *testing.T) {
	path := writeConfig(t, `
[vision]
enabled = true
provider = "openai"
model = "gpt-4.1"
`)
	snapshot, err := Load(LoadOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	if !snapshot.Config.Vision.Enabled ||
		snapshot.Config.Vision.Provider != "openai" ||
		snapshot.Config.Vision.Model != "gpt-4.1" {
		t.Fatalf("vision = %+v", snapshot.Config.Vision)
	}
	_, err = Load(LoadOptions{
		LookupEnv: envLookup(map[string]string{"CODEHELPER_VISION_ENABLED": "true"}),
	})
	if err == nil {
		t.Fatal("expected validation error when vision enabled without provider/model")
	}
}

func TestVerifyGateConfigResolvesAcrossSources(t *testing.T) {
	defaults, err := Load(LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := Verify{
		Mode: "soft", Scope: "diagnostics", OnFailure: "fail",
		MaxRepairSteps: 1, Timeout: 2 * time.Minute,
	}
	if defaults.Config.Execution.Verify != want {
		t.Fatalf("default verify = %+v, want %+v", defaults.Config.Execution.Verify, want)
	}

	path := writeConfig(t, `
[execution.verify]
mode = "soft"
scope = "repository"
on_failure = "revert"
max_repair_steps = 3
timeout = "45s"
command = "make verify"
`)
	fromFile, err := Load(LoadOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	verify := fromFile.Config.Execution.Verify
	if verify.Mode != "soft" || verify.Scope != "repository" || verify.OnFailure != "revert" ||
		verify.MaxRepairSteps != 3 || verify.Timeout != 45*time.Second ||
		verify.Command != "make verify" {
		t.Fatalf("verify from file = %+v", verify)
	}
	if fromFile.Provenance[fieldVerifyMode] != SourceFile ||
		fromFile.Provenance[fieldVerifyTimeout] != SourceFile {
		t.Fatalf("provenance = %+v", fromFile.Provenance)
	}

	// The command belongs to the repository scope, so narrowing the scope has to
	// clear it in the same load.
	mode, repair, command := "hard", 0, ""
	timeout := 90 * time.Second
	fromStartup, err := Load(LoadOptions{
		Path:      path,
		LookupEnv: envLookup(map[string]string{"CODEHELPER_VERIFY_SCOPE": "diagnostics"}),
		Overrides: Overrides{
			VerifyMode: &mode, VerifyRepair: &repair, VerifyTimeout: &timeout,
			VerifyCommand: &command,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	verify = fromStartup.Config.Execution.Verify
	if verify.Mode != "hard" || verify.Scope != "diagnostics" || verify.MaxRepairSteps != 0 ||
		verify.Timeout != 90*time.Second {
		t.Fatalf("verify from env/startup = %+v", verify)
	}
	if fromStartup.Provenance[fieldVerifyScope] != SourceEnv ||
		fromStartup.Provenance[fieldVerifyMode] != SourceStartup {
		t.Fatalf("provenance = %+v", fromStartup.Provenance)
	}
}

func TestDiagnosticCommandsLoadAndValidate(t *testing.T) {
	path := writeConfig(t, `
[diagnostics.commands.".md"]
name = "fixture-lint"
args = ["--no-globs", "--", "{path}"]
`)
	snapshot, err := Load(LoadOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	command := snapshot.Config.Diagnostics.Commands[".md"]
	if command.Name != "fixture-lint" ||
		!slices.Equal(command.Args, []string{"--no-globs", "--", "{path}"}) {
		t.Fatalf("Markdown diagnostics command = %+v", command)
	}
	if snapshot.Provenance[fieldDiagnosticCommandName(".md")] != SourceFile ||
		snapshot.Provenance[fieldDiagnosticCommandArgs(".md")] != SourceFile {
		t.Fatalf("diagnostics provenance = %+v", snapshot.Provenance)
	}

	tests := map[string]struct {
		table     string
		wantField string
	}{
		"uppercase extension": {
			table:     `[diagnostics.commands.".MD"]`,
			wantField: fieldDiagnosticCommandName(".MD"),
		},
		"path command": {
			table:     `[diagnostics.commands.".md"]`,
			wantField: fieldDiagnosticCommandName(".md"),
		},
		"missing path argument": {
			table:     `[diagnostics.commands.".md"]`,
			wantField: fieldDiagnosticCommandArgs(".md"),
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			commandName := "fixture-lint"
			args := `["--no-globs"]`
			if name == "path command" {
				commandName = "./node_modules/.bin/fixture-lint"
				args = `["{path}"]`
			}
			configPath := writeConfig(
				t,
				test.table+"\nname = "+strconv.Quote(commandName)+"\nargs = "+args+"\n",
			)
			_, loadErr := Load(LoadOptions{Path: configPath})
			var fieldErr *FieldError
			if !errors.As(loadErr, &fieldErr) {
				t.Fatalf("Load() error = %v, want field error", loadErr)
			}
			if fieldErr.Field != test.wantField {
				t.Fatalf("field = %q, want %q", fieldErr.Field, test.wantField)
			}
		})
	}
}

func TestIndexConfigResolvesAcrossSourcesAndBoundsItsCeilings(t *testing.T) {
	defaults, err := Load(LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := Index{Enabled: true, MaxFileBytes: 1 << 20, MaxFiles: 20000}
	if defaults.Config.Context.Index != want {
		t.Fatalf("default index = %+v, want %+v", defaults.Config.Context.Index, want)
	}

	path := writeConfig(t, `
[context.index]
enabled = true
max_file_bytes = 4096
max_files = 100
`)
	files := 250
	loaded, err := Load(LoadOptions{
		Path:      path,
		LookupEnv: envLookup(map[string]string{"CODEHELPER_INDEX_MAX_FILE_BYTES": "8192"}),
		Overrides: Overrides{IndexMaxFiles: &files},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Config.Context.Index; got.MaxFileBytes != 8192 || got.MaxFiles != 250 {
		t.Fatalf("index = %+v", got)
	}
	if loaded.Provenance[fieldIndexEnabled] != SourceFile ||
		loaded.Provenance[fieldIndexMaxBytes] != SourceEnv ||
		loaded.Provenance[fieldIndexMaxFiles] != SourceStartup {
		t.Fatalf("provenance = %+v", loaded.Provenance)
	}

	// A ceiling of zero would index nothing while still reporting itself ready,
	// so it has to be refused rather than clamped.
	for env, field := range map[string]string{
		"CODEHELPER_INDEX_MAX_FILE_BYTES": fieldIndexMaxBytes,
		"CODEHELPER_INDEX_MAX_FILES":      fieldIndexMaxFiles,
	} {
		_, err := Load(LoadOptions{LookupEnv: envLookup(map[string]string{env: "0"})})
		var fieldErr *FieldError
		if !errors.As(err, &fieldErr) || fieldErr.Field != field {
			t.Fatalf("Load(%s=0) error = %v, want a %s field error", env, err, field)
		}
	}
	// With the index off the ceilings are moot and must not block a load.
	off, err := Load(LoadOptions{LookupEnv: envLookup(map[string]string{
		"CODEHELPER_INDEX_ENABLED":   "false",
		"CODEHELPER_INDEX_MAX_FILES": "0",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if off.Config.Context.Index.Enabled {
		t.Fatalf("index = %+v, want it disabled", off.Config.Context.Index)
	}
}

func TestRepoMapAndWorkingSetResolveAcrossSourcesAndBoundTheirCeilings(t *testing.T) {
	defaults, err := Load(LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantMap := RepoMap{Enabled: true, MaxBytes: 8 << 10, MaxDirectories: 24}
	wantSet := WorkingSet{Enabled: true, MaxEntries: 16, MaxBytes: 8 << 10}
	if defaults.Config.Context.RepoMap != wantMap {
		t.Fatalf("default repo map = %+v, want %+v", defaults.Config.Context.RepoMap, wantMap)
	}
	if defaults.Config.Context.WorkingSet != wantSet {
		t.Fatalf("default working set = %+v, want %+v", defaults.Config.Context.WorkingSet, wantSet)
	}

	path := writeConfig(t, `
[context.repo_map]
enabled = true
max_bytes = 4096
max_directories = 8

[context.working_set]
enabled = true
max_entries = 4
max_bytes = 2048
`)
	entries := 9
	loaded, err := Load(LoadOptions{
		Path:      path,
		LookupEnv: envLookup(map[string]string{"CODEHELPER_REPO_MAP_MAX_BYTES": "1024"}),
		Overrides: Overrides{WorkingSetMaxEntries: &entries},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Config.Context.RepoMap; got.MaxBytes != 1024 || got.MaxDirectories != 8 {
		t.Fatalf("repo map = %+v", got)
	}
	if got := loaded.Config.Context.WorkingSet; got.MaxEntries != 9 || got.MaxBytes != 2048 {
		t.Fatalf("working set = %+v", got)
	}
	if loaded.Provenance[fieldRepoMapMaxDirectories] != SourceFile ||
		loaded.Provenance[fieldRepoMapMaxBytes] != SourceEnv ||
		loaded.Provenance[fieldWorkingSetMaxEntries] != SourceStartup {
		t.Fatalf("provenance = %+v", loaded.Provenance)
	}

	// A ceiling too small to hold a section would leave a request paying for a
	// header that only reports its own truncation.
	for env, field := range map[string]string{
		"CODEHELPER_REPO_MAP_MAX_BYTES":       fieldRepoMapMaxBytes,
		"CODEHELPER_REPO_MAP_MAX_DIRECTORIES": fieldRepoMapMaxDirectories,
		"CODEHELPER_WORKING_SET_MAX_ENTRIES":  fieldWorkingSetMaxEntries,
		"CODEHELPER_WORKING_SET_MAX_BYTES":    fieldWorkingSetMaxBytes,
	} {
		_, err := Load(LoadOptions{LookupEnv: envLookup(map[string]string{env: "0"})})
		var fieldErr *FieldError
		if !errors.As(err, &fieldErr) || fieldErr.Field != field {
			t.Fatalf("Load(%s=0) error = %v, want a %s field error", env, err, field)
		}
	}

	// With a section off its ceilings are moot and must not block a load.
	off, err := Load(LoadOptions{LookupEnv: envLookup(map[string]string{
		"CODEHELPER_REPO_MAP_ENABLED":        "false",
		"CODEHELPER_REPO_MAP_MAX_BYTES":      "0",
		"CODEHELPER_WORKING_SET_ENABLED":     "false",
		"CODEHELPER_WORKING_SET_MAX_ENTRIES": "0",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if off.Config.Context.RepoMap.Enabled || off.Config.Context.WorkingSet.Enabled {
		t.Fatalf("context = %+v, want both sections disabled", off.Config.Context)
	}
}

func TestEvidenceAndCodingPolicyResolveAcrossSourcesAndBoundTheirCeilings(t *testing.T) {
	defaults, err := Load(LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	wantEvidence := Evidence{Enabled: true, MaxEntries: 24, MaxBytes: 4 << 10}
	if defaults.Config.Context.Evidence != wantEvidence {
		t.Fatalf("default evidence = %+v, want %+v", defaults.Config.Context.Evidence, wantEvidence)
	}
	if !defaults.Config.Context.CodingPolicy.Enabled {
		t.Fatal("the coding method is off by default")
	}

	path := writeConfig(t, `
[context.evidence]
enabled = true
max_entries = 8
max_bytes = 2048

[context.coding_policy]
enabled = false
`)
	entries := 12
	loaded, err := Load(LoadOptions{
		Path:      path,
		LookupEnv: envLookup(map[string]string{"CODEHELPER_EVIDENCE_MAX_BYTES": "1024"}),
		Overrides: Overrides{EvidenceMaxEntries: &entries},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Config.Context.Evidence; got.MaxEntries != 12 || got.MaxBytes != 1024 {
		t.Fatalf("evidence = %+v", got)
	}
	if loaded.Config.Context.CodingPolicy.Enabled {
		t.Fatal("the file did not turn the coding method off")
	}
	if loaded.Provenance[fieldEvidenceMaxBytes] != SourceEnv ||
		loaded.Provenance[fieldEvidenceMaxEntries] != SourceStartup ||
		loaded.Provenance[fieldCodingPolicyEnabled] != SourceFile {
		t.Fatalf("provenance = %+v", loaded.Provenance)
	}

	for env, field := range map[string]string{
		"CODEHELPER_EVIDENCE_MAX_ENTRIES": fieldEvidenceMaxEntries,
		"CODEHELPER_EVIDENCE_MAX_BYTES":   fieldEvidenceMaxBytes,
	} {
		_, err := Load(LoadOptions{LookupEnv: envLookup(map[string]string{env: "0"})})
		var fieldErr *FieldError
		if !errors.As(err, &fieldErr) || fieldErr.Field != field {
			t.Fatalf("Load(%s=0) error = %v, want a %s field error", env, err, field)
		}
	}

	off, err := Load(LoadOptions{LookupEnv: envLookup(map[string]string{
		"CODEHELPER_EVIDENCE_ENABLED":      "false",
		"CODEHELPER_EVIDENCE_MAX_ENTRIES":  "0",
		"CODEHELPER_CODING_POLICY_ENABLED": "false",
	})})
	if err != nil {
		t.Fatal(err)
	}
	if off.Config.Context.Evidence.Enabled || off.Config.Context.CodingPolicy.Enabled {
		t.Fatalf("context = %+v, want both off", off.Config.Context)
	}
}

func TestCompactTokenWindowResolvesAcrossSources(t *testing.T) {
	path := writeConfig(t, `
[context.compact]
auto_compact_tokens = 300
scope = "body_after_prefix"
`)
	scope := "total"
	snapshot, err := Load(LoadOptions{
		Path: path,
		LookupEnv: envLookup(map[string]string{
			"CODEHELPER_COMPACT_AUTO_TOKENS": "400",
		}),
		Overrides: Overrides{CompactScope: &scope},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := snapshot.Config.Context.Compact; got.AutoCompactTokens != 400 ||
		got.Scope != "total" {
		t.Fatalf("compact = %+v", got)
	}
	if snapshot.Provenance[fieldCompactAutoTokens] != SourceEnv ||
		snapshot.Provenance[fieldCompactScope] != SourceStartup {
		t.Fatalf("provenance = %+v", snapshot.Provenance)
	}
}

// The affected scope now has a repo index behind it, and it accepts a command so
// an operator can point it at their own suite.
func TestVerifyGateConfigAcceptsTheAffectedScope(t *testing.T) {
	loaded, err := Load(LoadOptions{LookupEnv: envLookup(map[string]string{
		"CODEHELPER_VERIFY_SCOPE":   "affected",
		"CODEHELPER_VERIFY_COMMAND": "go test {packages}",
	})})
	if err != nil {
		t.Fatal(err)
	}
	verify := loaded.Config.Execution.Verify
	if verify.Scope != "affected" || verify.Command != "go test {packages}" {
		t.Fatalf("verify = %+v", verify)
	}
}

// The unimplemented values must not load: silently degrading them into
// something that runs would hide the gap.
func TestVerifyGateConfigRejectsUnimplementedValues(t *testing.T) {
	tests := map[string]struct {
		env       map[string]string
		wantField string
	}{
		"unknown scope": {
			env:       map[string]string{"CODEHELPER_VERIFY_SCOPE": "packages"},
			wantField: fieldVerifyScope,
		},
		"ask on failure": {
			env:       map[string]string{"CODEHELPER_VERIFY_ON_FAILURE": "ask"},
			wantField: fieldVerifyOnFailure,
		},
		"unknown mode": {
			env:       map[string]string{"CODEHELPER_VERIFY_MODE": "always"},
			wantField: fieldVerifyMode,
		},
		"negative repair budget": {
			env:       map[string]string{"CODEHELPER_VERIFY_MAX_REPAIR_STEPS": "-1"},
			wantField: fieldVerifyRepair,
		},
		"zero timeout": {
			env:       map[string]string{"CODEHELPER_VERIFY_TIMEOUT": "0s"},
			wantField: fieldVerifyTimeout,
		},
		// A command under the diagnostics scope would silently never run.
		"command without a command scope": {
			env:       map[string]string{"CODEHELPER_VERIFY_COMMAND": "make verify"},
			wantField: fieldVerifyCommand,
		},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := Load(LoadOptions{LookupEnv: envLookup(test.env)})
			var fieldErr *FieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("Load() error = %v, want a field error", err)
			}
			if fieldErr.Field != test.wantField {
				t.Fatalf("field = %q, want %q", fieldErr.Field, test.wantField)
			}
			if fieldErr.Source != SourceEnv {
				t.Fatalf("source = %q, want env", fieldErr.Source)
			}
		})
	}
}

// The scheduler is off unless a host asks for it, because a one-shot process
// that claimed background work would have to finish it before exiting.
func TestWorkerConfigIsOffByDefaultAndResolvesAcrossSources(t *testing.T) {
	defaults, err := Load(LoadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	want := Worker{
		MaxParallel: 2, MaxAttempts: 1, Lease: 30 * time.Second,
		ClaimInterval: time.Second, AutomationInterval: 30 * time.Second,
		RetryBackoff: 15 * time.Second, RetryBackoffMax: 10 * time.Minute,
	}
	if defaults.Config.Execution.Worker != want {
		t.Fatalf("default worker = %+v, want %+v", defaults.Config.Execution.Worker, want)
	}

	path := writeConfig(t, `
[execution.worker]
enabled = true
max_parallel = 4
max_attempts = 3
lease = "45s"
claim_interval = "2s"
automation_interval = "1m"
retry_backoff = "5s"
retry_backoff_max = "1m"
max_tokens = 1000
max_cost_usd = 2.5
`)
	fromFile, err := Load(LoadOptions{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	worker := fromFile.Config.Execution.Worker
	if !worker.Enabled || worker.MaxParallel != 4 || worker.MaxAttempts != 3 ||
		worker.Lease != 45*time.Second || worker.ClaimInterval != 2*time.Second ||
		worker.AutomationInterval != time.Minute || worker.RetryBackoff != 5*time.Second ||
		worker.RetryBackoffMax != time.Minute || worker.MaxTokens != 1000 || worker.MaxCostUSD != 2.5 {
		t.Fatalf("worker from file = %+v", worker)
	}
	if fromFile.Provenance[fieldWorkerEnabled] != SourceFile ||
		fromFile.Provenance[fieldWorkerLease] != SourceFile {
		t.Fatalf("provenance = %+v", fromFile.Provenance)
	}

	parallel := 1
	disabled := false
	fromStartup, err := Load(LoadOptions{
		Path:      path,
		Overrides: Overrides{WorkerEnabled: &disabled, WorkerMaxParallel: &parallel},
	})
	if err != nil {
		t.Fatal(err)
	}
	worker = fromStartup.Config.Execution.Worker
	if worker.Enabled || worker.MaxParallel != 1 || worker.MaxAttempts != 3 {
		t.Fatalf("worker from startup = %+v", worker)
	}
	if fromStartup.Provenance[fieldWorkerMaxParallel] != SourceStartup {
		t.Fatalf("provenance = %+v", fromStartup.Provenance)
	}
}

// A lease that cannot outlive the interval at which it is renewed expires under
// a healthy worker, which hands a running task to a second one.
func TestWorkerConfigRejectsSettingsThatWouldLoseWork(t *testing.T) {
	tests := map[string]struct {
		file      string
		wantField string
	}{
		"lease shorter than the claim interval": {
			file:      "lease = \"1s\"\nclaim_interval = \"5s\"",
			wantField: fieldWorkerLease,
		},
		"lease equal to the claim interval": {
			file:      "lease = \"1s\"\nclaim_interval = \"1s\"",
			wantField: fieldWorkerLease,
		},
		"no parallelism": {file: "max_parallel = 0", wantField: fieldWorkerMaxParallel},
		"no attempts":    {file: "max_attempts = 0", wantField: fieldWorkerMaxAttempts},
		"spinning claim loop": {
			file:      "claim_interval = \"0s\"",
			wantField: fieldWorkerClaimInterval,
		},
		"spinning automation loop": {
			file:      "automation_interval = \"0s\"",
			wantField: fieldWorkerAutomationTick,
		},
		"backoff ceiling below its floor": {
			file:      "retry_backoff = \"1m\"\nretry_backoff_max = \"1s\"",
			wantField: fieldWorkerRetryBackoffMax,
		},
		"negative cost ceiling": {file: "max_cost_usd = -1.0", wantField: fieldWorkerMaxCostUSD},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			path := writeConfig(t, "[execution.worker]\n"+test.file+"\n")
			_, err := Load(LoadOptions{Path: path})
			var fieldErr *FieldError
			if !errors.As(err, &fieldErr) {
				t.Fatalf("Load() error = %v, want a field error", err)
			}
			if fieldErr.Field != test.wantField {
				t.Fatalf("field = %q, want %q", fieldErr.Field, test.wantField)
			}
		})
	}
}

func envLookup(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, exists := values[name]
		return value, exists
	}
}

func writeConfig(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}
