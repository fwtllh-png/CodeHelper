// Command extensionecosystembaseline records and checks the extension ecosystem surface.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/hooks"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/mcp"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/plugin"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/skill"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	plugintool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/plugin"
	skilltool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/skill"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/promptcontext"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const (
	schemaVersion = 1
	stageEE0      = "EE0"
	initialRuns   = 5
	extendedRuns  = 10
	maxMADBP      = 1500
)

type report struct {
	SchemaVersion    int                 `json:"schema_version"`
	Stage            string              `json:"stage"`
	Status           string              `json:"status"`
	BaseCommit       string              `json:"base_commit"`
	Reference        reference           `json:"reference"`
	SamplePolicy     samplePolicy        `json:"sample_policy"`
	SkillScales      []skillScale        `json:"skill_scales"`
	PluginScales     []pluginScale       `json:"plugin_scales"`
	CombinedGolden   combinedGolden      `json:"combined_golden"`
	LifecycleGoldens []lifecycleGolden   `json:"lifecycle_goldens"`
	HostParity       map[string][]string `json:"host_parity"`
	Contracts        contractMetrics     `json:"contracts"`
	KnownGaps        []string            `json:"known_gaps"`
	Commands         map[string]string   `json:"commands"`
}

type reference struct {
	Repository string `json:"repository"`
	Commit     string `json:"commit"`
}

type samplePolicy struct {
	InitialRuns       int `json:"initial_runs"`
	ExtendedRuns      int `json:"extended_runs"`
	MaxMADBasisPoints int `json:"max_mad_basis_points"`
}

type durationStats struct {
	Runs           int   `json:"runs"`
	MedianMicros   int64 `json:"median_micros"`
	P95Micros      int64 `json:"p95_micros"`
	MADMicros      int64 `json:"mad_micros"`
	MADBasisPoints int64 `json:"mad_basis_points"`
}

type skillScale struct {
	Count               int           `json:"count"`
	FixtureCreateMicros int64         `json:"fixture_create_micros"`
	ColdDiscoveryMicros int64         `json:"cold_discovery_micros"`
	Discovery           durationStats `json:"discovery"`
	Refresh             durationStats `json:"refresh"`
	Discovered          int           `json:"discovered"`
	Issues              int           `json:"issues"`
	RawCatalogBytes     int           `json:"raw_catalog_bytes"`
	RawCatalogTokens    uint64        `json:"raw_catalog_tokens"`
	PromptCatalogBytes  int           `json:"prompt_catalog_bytes"`
	PromptCatalogTokens uint64        `json:"prompt_catalog_tokens"`
	PromptTruncated     bool          `json:"prompt_truncated"`
	CatalogDigest       string        `json:"catalog_digest"`
	Selection           durationStats `json:"selection"`
	CandidateCount      int           `json:"candidate_count"`
	CandidateBytes      int           `json:"candidate_bytes"`
	CandidateTokens     uint64        `json:"candidate_tokens"`
	TokenSavingsBP      int64         `json:"token_savings_basis_points"`
	CriticalSkillRecall bool          `json:"critical_skill_recall"`
	SelectedBodyCount   int           `json:"selected_body_count"`
}

type pluginScale struct {
	Count               int           `json:"count"`
	FixtureCreateMicros int64         `json:"fixture_create_micros"`
	ColdDiscoveryMicros int64         `json:"cold_discovery_micros"`
	Discovery           durationStats `json:"discovery"`
	Refresh             durationStats `json:"refresh"`
	Discovered          int           `json:"discovered"`
	ManifestBytes       int           `json:"manifest_bytes"`
	CatalogDigest       string        `json:"catalog_digest"`
}

type combinedGolden struct {
	Digest          string   `json:"digest"`
	Plugin          string   `json:"plugin"`
	Skill           string   `json:"skill"`
	MCPServer       string   `json:"mcp_server"`
	MCPTool         string   `json:"mcp_tool"`
	HookEvent       string   `json:"hook_event"`
	HookID          string   `json:"hook_id"`
	ModelTools      []string `json:"model_tools"`
	ToolSchemaBytes int      `json:"tool_schema_bytes"`
	FixtureHermetic bool     `json:"fixture_hermetic"`
}

type lifecycleGolden struct {
	Scenario        string `json:"scenario"`
	Test            string `json:"test"`
	RequiredOutcome string `json:"required_outcome"`
	Measured        bool   `json:"measured"`
}

// Every field is monotonic: false may become true, while true may not regress.
type contractMetrics struct {
	SharedToolRegistry             bool `json:"shared_tool_registry"`
	ConsequentialToolsGuarded      bool `json:"consequential_tools_guarded"`
	PluginSupplyChainVerified      bool `json:"plugin_supply_chain_verified"`
	SkillAuthorityAndLockfile      bool `json:"skill_authority_and_lockfile"`
	MCPSourceReconcile             bool `json:"mcp_source_reconcile"`
	HookSandboxAndBoundedOutput    bool `json:"hook_sandbox_and_bounded_output"`
	TurnFrozenExtensionSnapshot    bool `json:"turn_frozen_extension_snapshot"`
	TypedExtensionAPI              bool `json:"typed_extension_api"`
	PluginCapabilityBundle         bool `json:"plugin_capability_bundle"`
	UnifiedExtensionLifecycle      bool `json:"unified_extension_lifecycle"`
	RuntimeExtensionControlPlane   bool `json:"runtime_extension_control_plane"`
	OnDemandSkillCatalog           bool `json:"on_demand_skill_catalog"`
	PluginSkillProductionReachable bool `json:"plugin_skill_production_reachable"`
}

var gapNames = map[string]string{
	"TypedExtensionAPI":              "extension_contract_is_private_to_wire_and_tool_contribution",
	"PluginCapabilityBundle":         "plugin_manifest_only_models_one_executable_tool",
	"UnifiedExtensionLifecycle":      "extension_lifecycle_is_split_across_subsystems",
	"RuntimeExtensionControlPlane":   "hosts_execute_extension_management_outside_runtime_protocol",
	"OnDemandSkillCatalog":           "all_enabled_skill_metadata_is_projected_by_default",
	"PluginSkillProductionReachable": "plugin_skill_snapshot_is_not_connected_to_production_wiring",
}

func main() {
	root := flag.String("root", ".", "repository root")
	baselinePath := flag.String(
		"baseline",
		"docs/extension-ecosystem-ee0-baseline.json",
		"EE0 baseline JSON",
	)
	reportPath := flag.String("report", "", "optional measured report path")
	writeBaseline := flag.Bool("write-baseline", false, "replace the baseline with current metrics")
	baseCommit := flag.String("base-commit", "", "baseline source commit")
	flag.Parse()

	measured, err := measure(*root, *baseCommit)
	if err == nil && *writeBaseline {
		err = writeJSON(filepath.Join(*root, *baselinePath), measured)
	} else if err == nil {
		var baseline report
		baseline, err = readReport(filepath.Join(*root, *baselinePath))
		if err == nil {
			err = validateCandidate(baseline, measured)
		}
	}
	if reportErr := writeOptionalReport(*root, *reportPath, measured); err == nil {
		err = reportErr
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf(
		"extension ecosystem baseline passed: skills=%d plugins=%d golden=%s\n",
		measured.SkillScales[len(measured.SkillScales)-1].Discovered,
		measured.PluginScales[len(measured.PluginScales)-1].Discovered,
		measured.CombinedGolden.Digest,
	)
}

func measure(root, baseCommit string) (report, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return report{}, err
	}
	if baseCommit == "" {
		baseCommit = gitCommit(absolute)
	}
	temporary, err := os.MkdirTemp("", "codehelper-extension-ee0-")
	if err != nil {
		return report{}, err
	}
	defer os.RemoveAll(temporary)

	var skillScales []skillScale
	for _, count := range []int{0, 10, 100, 1000} {
		value, measureErr := measureSkillScale(filepath.Join(temporary, "skills-"+strconv.Itoa(count)), count)
		if measureErr != nil {
			return report{}, measureErr
		}
		skillScales = append(skillScales, value)
	}
	var pluginScales []pluginScale
	for _, count := range []int{0, 10, 100} {
		value, measureErr := measurePluginScale(filepath.Join(temporary, "plugins-"+strconv.Itoa(count)), count)
		if measureErr != nil {
			return report{}, measureErr
		}
		pluginScales = append(pluginScales, value)
	}
	combined, err := measureCombinedGolden(filepath.Join(temporary, "combined"))
	if err != nil {
		return report{}, err
	}
	restartMeasured, revokeMeasured, err := measureLocalLifecycle(filepath.Join(temporary, "lifecycle"))
	if err != nil {
		return report{}, err
	}
	if lifecycleErr := runLifecycleContractTests(absolute); lifecycleErr != nil {
		return report{}, lifecycleErr
	}
	contracts, err := measureContracts(absolute)
	if err != nil {
		return report{}, err
	}
	lifecycle := []lifecycleGolden{
		{
			Scenario:        "update_and_drain",
			Test:            "internal/adapter/plugin:TestSignedRegistryInstallUpdateDrainRollbackAndSecurityRevoke",
			RequiredOutcome: "old_generation_drains_while_new_calls_use_new_generation",
			Measured:        true,
		},
		{
			Scenario:        "rollback",
			Test:            "internal/adapter/plugin:TestSignedRegistryInstallUpdateDrainRollbackAndSecurityRevoke",
			RequiredOutcome: "rollback_selects_verified_predecessor",
			Measured:        true,
		},
		{
			Scenario:        "security_revoke",
			Test:            "internal/adapter/plugin:TestSecurityRevokeCancelsInflightPluginCall",
			RequiredOutcome: "new_and_inflight_calls_are_revoked",
			Measured:        revokeMeasured,
		},
		{
			Scenario:        "mcp_quarantine",
			Test:            "internal/adapter/tool/mcp:TestAsyncSyncQuarantinesStaleCatalogAndRecovers",
			RequiredOutcome: "stale_catalog_is_removed_and_recovery_is_source_scoped",
			Measured:        true,
		},
		{
			Scenario:        "restart",
			Test:            "scripts/extensionecosystembaseline:TestLocalLifecycleRestartAndRevoke",
			RequiredOutcome: "durable_enabled_state_reloads_without_duplicate_authority",
			Measured:        restartMeasured,
		},
	}
	return report{
		SchemaVersion: schemaVersion,
		Stage:         stageEE0,
		Status:        "baseline_frozen",
		BaseCommit:    baseCommit,
		Reference: reference{
			Repository: "../codex",
			Commit:     "3bbf1fe75701c97fb190e0867002ba2d9dbda5db",
		},
		SamplePolicy: samplePolicy{
			InitialRuns: initialRuns, ExtendedRuns: extendedRuns,
			MaxMADBasisPoints: maxMADBP,
		},
		SkillScales: skillScales, PluginScales: pluginScales,
		CombinedGolden:   combined,
		LifecycleGoldens: lifecycle,
		HostParity: map[string][]string{
			"runtime_protocol": {
				"extension.control", "extension.detail", "extension.health",
				"extension.list", "extension.permissions", "extension.receipts",
			},
			"cli": {
				"mcp.add", "mcp.disable", "mcp.enable", "mcp.list", "mcp.remove",
				"plugin.disable", "plugin.enable", "plugin.install", "plugin.list",
				"plugin.revoke", "plugin.rollback", "plugin.security_revoke",
				"plugin.trust", "plugin.update", "skill.disable", "skill.enable",
				"skill.list", "skill.lock", "skill.revoke", "skill.verify",
			},
			"tui":    {"extension.list"},
			"vscode": {"extension.control", "extension.list"},
			"acp":    {"extension.control", "extension.list"},
		},
		Contracts: contracts,
		KnownGaps: knownGaps(contracts),
		Commands: map[string]string{
			"baseline": "make extension-ecosystem-ee0",
			"update":   "make extension-ecosystem-ee0-update",
		},
	}, nil
}

func measureSkillScale(root string, count int) (skillScale, error) {
	workspace := filepath.Join(root, "workspace")
	home := filepath.Join(root, "home")
	started := time.Now()
	if err := os.MkdirAll(filepath.Join(workspace, ".agents", "skills"), 0o755); err != nil {
		return skillScale{}, err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return skillScale{}, err
	}
	for index := range count {
		name := fmt.Sprintf("skill-%04d", index)
		if err := writeSkill(
			filepath.Join(workspace, ".agents", "skills"),
			name,
			"Hermetic extension ecosystem skill used to measure catalog projection and discovery cost",
			"Use deterministic fixture instructions only.",
		); err != nil {
			return skillScale{}, err
		}
	}
	created := time.Since(started).Microseconds()
	options := skill.DiscoveryOptions{Workspace: workspace, UserHome: home}
	coldStarted := time.Now()
	catalog, err := skill.Discover(options)
	if err != nil {
		return skillScale{}, err
	}
	summaries, issues := catalog.List(context.Background())
	cold := time.Since(coldStarted).Microseconds()
	discovery, err := stableDuration(func() error {
		value, discoverErr := skill.Discover(options)
		if discoverErr != nil {
			return discoverErr
		}
		values, _ := value.List(context.Background())
		if len(values) != count {
			return fmt.Errorf("skill discovery count=%d, want %d", len(values), count)
		}
		return nil
	})
	if err != nil {
		return skillScale{}, err
	}
	if count > 0 {
		if writeErr := writeSkill(
			filepath.Join(workspace, ".agents", "skills"),
			"skill-0000",
			"Refreshed hermetic extension ecosystem skill used to measure source reconciliation cost",
			"Use refreshed deterministic fixture instructions only.",
		); writeErr != nil {
			return skillScale{}, writeErr
		}
	}
	refresh, err := stableDuration(func() error {
		value, discoverErr := skill.Discover(options)
		if discoverErr != nil {
			return discoverErr
		}
		values, _ := value.List(context.Background())
		if len(values) != count {
			return fmt.Errorf("skill refresh count=%d, want %d", len(values), count)
		}
		return nil
	})
	if err != nil {
		return skillScale{}, err
	}
	rendered := renderSkillCatalog(summaries)
	query := ""
	critical := ""
	if count > 0 {
		critical = fmt.Sprintf("skill-%04d", count-1)
		query = "use @" + critical
	}
	selection, err := catalog.Select(
		context.Background(),
		skill.SelectionRequest{
			Query: query, Mode: skill.SelectionCandidate,
		},
	)
	if err != nil {
		return skillScale{}, err
	}
	selectionTiming, err := stableDuration(func() error {
		_, selectErr := catalog.Select(
			context.Background(),
			skill.SelectionRequest{
				Query: query, Mode: skill.SelectionCandidate,
			},
		)
		return selectErr
	})
	if err != nil {
		return skillScale{}, err
	}
	candidateRendered := renderSelectedSkillCatalog(selection.Visible)
	criticalRecall := count == 0
	for _, summary := range selection.Candidates {
		criticalRecall = criticalRecall || summary.Name == critical
	}
	messages, receipt := promptcontext.AssembleWorldText(
		promptcontext.PartitionSkills,
		"skill://catalog",
		rendered,
		promptcontext.Budget{
			MaxBytes:  promptcontext.MaxSkillsPromptBytes,
			MaxTokens: promptcontext.MaxFragmentTokens,
		},
	)
	retained := ""
	if len(messages) != 0 {
		retained = messages[0].Text()
	}
	return skillScale{
		Count: count, FixtureCreateMicros: created, ColdDiscoveryMicros: cold,
		Discovery: discovery, Refresh: refresh, Discovered: len(summaries),
		Issues: len(issues), RawCatalogBytes: len(rendered),
		RawCatalogTokens:   promptcontext.HeuristicTokenCounter{}.Count(rendered),
		PromptCatalogBytes: len(retained), PromptCatalogTokens: receipt.RetainedTokens,
		PromptTruncated: receipt.Truncated, CatalogDigest: digestString(rendered),
		Selection: selectionTiming, CandidateCount: len(selection.Visible),
		CandidateBytes:      len(candidateRendered),
		CandidateTokens:     promptcontext.HeuristicTokenCounter{}.Count(candidateRendered),
		TokenSavingsBP:      int64(selection.Metrics.TokenSavings * 10_000),
		CriticalSkillRecall: criticalRecall, SelectedBodyCount: 0,
	}, nil
}

func measurePluginScale(root string, count int) (pluginScale, error) {
	discoveryRoot := filepath.Join(root, "discovery")
	started := time.Now()
	if err := os.MkdirAll(discoveryRoot, 0o755); err != nil {
		return pluginScale{}, err
	}
	manifestBytes := 0
	for index := range count {
		data, err := writePluginBundle(
			discoveryRoot,
			fmt.Sprintf("plugin-%03d", index),
			uint64(index+1),
			"fixture-"+strconv.Itoa(index),
		)
		if err != nil {
			return pluginScale{}, err
		}
		manifestBytes += len(data)
	}
	created := time.Since(started).Microseconds()
	options := plugin.DiscoveryOptions{WorkspaceRoot: discoveryRoot}
	coldStarted := time.Now()
	candidates, err := plugin.Discover(options)
	if err != nil {
		return pluginScale{}, err
	}
	cold := time.Since(coldStarted).Microseconds()
	discovery, err := stableDuration(func() error {
		values, discoverErr := plugin.Discover(options)
		if discoverErr != nil {
			return discoverErr
		}
		if len(values) != count {
			return fmt.Errorf("plugin discovery count=%d, want %d", len(values), count)
		}
		return nil
	})
	if err != nil {
		return pluginScale{}, err
	}
	if count > 0 {
		_, writeErr := writePluginBundle(discoveryRoot, "plugin-000", 1001, "refreshed")
		if writeErr != nil {
			return pluginScale{}, writeErr
		}
	}
	refresh, err := stableDuration(func() error {
		values, discoverErr := plugin.Discover(options)
		if discoverErr != nil {
			return discoverErr
		}
		if len(values) != count {
			return fmt.Errorf("plugin refresh count=%d, want %d", len(values), count)
		}
		return nil
	})
	if err != nil {
		return pluginScale{}, err
	}
	identities := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		identities = append(
			identities,
			fmt.Sprintf("%s:%d:%s", candidate.Name, candidate.Manifest.Generation, candidate.Root),
		)
	}
	return pluginScale{
		Count: count, FixtureCreateMicros: created, ColdDiscoveryMicros: cold,
		Discovery: discovery, Refresh: refresh, Discovered: len(candidates),
		ManifestBytes: manifestBytes, CatalogDigest: digestString(strings.Join(identities, "\n")),
	}, nil
}

func measureCombinedGolden(root string) (combinedGolden, error) {
	workspace := filepath.Join(root, "workspace")
	pluginRoot := filepath.Join(root, "plugins")
	skillRoot := filepath.Join(workspace, ".agents", "skills")
	for _, directory := range []string{workspace, pluginRoot, skillRoot} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			return combinedGolden{}, err
		}
	}
	if _, err := writePluginBundle(pluginRoot, "fixture-plugin", 7, "combined"); err != nil {
		return combinedGolden{}, err
	}
	bundle := filepath.Join(pluginRoot, "fixture-plugin")
	manifest, err := plugin.ValidateBundle(bundle)
	if err != nil {
		return combinedGolden{}, err
	}
	if writeErr := writeSkill(
		skillRoot, "fixture-skill", "Combined golden skill", "Combined golden instructions.",
	); writeErr != nil {
		return combinedGolden{}, writeErr
	}
	catalog, err := skill.Discover(skill.DiscoveryOptions{
		Workspace: workspace, UserHome: filepath.Join(root, "home"),
	})
	if err != nil {
		return combinedGolden{}, err
	}
	summaries, issues := catalog.List(context.Background())
	if len(summaries) != 1 || len(issues) != 0 {
		return combinedGolden{}, fmt.Errorf(
			"combined skill catalog summaries=%d issues=%d", len(summaries), len(issues),
		)
	}
	mcpConfig := mcp.Config{
		Version: mcp.ConfigVersion,
		Servers: map[string]mcp.ServerConfig{
			"fixture": {
				Transport: "stdio", Command: "fixture-mcp",
				Tools: map[string]mcp.ToolBinding{
					"fixture.echo": {
						Capability: "read", AccessMode: "read",
						ParallelPolicy: "concurrent", SandboxRequirement: "none",
					},
				},
			},
		},
	}
	if validateErr := mcpConfig.Validate(); validateErr != nil {
		return combinedGolden{}, validateErr
	}
	hookConfig, err := hooks.DecodeConfig([]byte(`{
		"version": 1,
		"hooks": {
			"ToolCallBefore": [{
				"id": "fixture-gate",
				"command": "/bin/true",
				"timeout": "1s",
				"max_output_bytes": 1024
			}]
		}
	}`))
	if err != nil {
		return combinedGolden{}, err
	}
	registry := tool.NewRegistry(nil, nil)
	if registerErr := skilltool.Register(registry, catalog); registerErr != nil {
		return combinedGolden{}, registerErr
	}
	receipt, err := plugin.Review(
		bundle, manifest.Capabilities, manifest.Generation, time.Unix(1_700_000_000, 0),
	)
	if err != nil {
		return combinedGolden{}, err
	}
	loader, err := plugin.NewLoader(pluginRoot, baselineBackend{})
	if err != nil {
		return combinedGolden{}, err
	}
	loaded, err := loader.Load("fixture-plugin", receipt)
	if err != nil {
		return combinedGolden{}, err
	}
	defer loaded.Close()
	if registerErr := plugintool.Register(registry, loaded); registerErr != nil {
		return combinedGolden{}, registerErr
	}
	descriptors := registry.Descriptors(tool.VisibleModel)
	names := make([]string, 0, len(descriptors))
	schemaBytes := 0
	for _, descriptor := range descriptors {
		names = append(names, descriptor.Name)
		data, marshalErr := json.Marshal(descriptor.InputSchema)
		if marshalErr != nil {
			return combinedGolden{}, marshalErr
		}
		schemaBytes += len(data)
	}
	sort.Strings(names)
	canonical := struct {
		Plugin      string   `json:"plugin"`
		Generation  uint64   `json:"generation"`
		Skill       string   `json:"skill"`
		MCPServer   string   `json:"mcp_server"`
		MCPTool     string   `json:"mcp_tool"`
		HookEvent   string   `json:"hook_event"`
		HookID      string   `json:"hook_id"`
		ModelTools  []string `json:"model_tools"`
		SchemaBytes int      `json:"schema_bytes"`
	}{
		Plugin: manifest.Name, Generation: manifest.Generation,
		Skill: summaries[0].Name, MCPServer: "fixture", MCPTool: "fixture.echo",
		HookEvent:  string(hooks.ToolCallBefore),
		HookID:     hookConfig.Hooks[hooks.ToolCallBefore][0].ID,
		ModelTools: names, SchemaBytes: schemaBytes,
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return combinedGolden{}, err
	}
	return combinedGolden{
		Digest: "sha256:" + digestBytes(data), Plugin: canonical.Plugin,
		Skill: canonical.Skill, MCPServer: canonical.MCPServer, MCPTool: canonical.MCPTool,
		HookEvent: canonical.HookEvent, HookID: canonical.HookID,
		ModelTools: names, ToolSchemaBytes: schemaBytes, FixtureHermetic: true,
	}, nil
}

func measureLocalLifecycle(root string) (restart bool, revoke bool, err error) {
	discovery := filepath.Join(root, "plugins")
	workspace := filepath.Join(root, "workspace")
	stateDirectory := filepath.Join(root, "state")
	for _, directory := range []string{discovery, workspace, stateDirectory} {
		if mkdirErr := os.MkdirAll(directory, 0o755); mkdirErr != nil {
			return false, false, mkdirErr
		}
	}
	if _, err = writePluginBundle(discovery, "fixture", 1, "lifecycle"); err != nil {
		return false, false, err
	}
	config := plugin.RegistryConfig{
		Discovery:     plugin.DiscoveryOptions{WorkspaceRoot: discovery},
		StagingRoot:   filepath.Join(root, "staging"),
		StatePath:     filepath.Join(stateDirectory, "plugins.json"),
		WorkspaceRoot: workspace, Backend: baselineBackend{},
		Now:           func() time.Time { return time.Unix(1_700_000_000, 0).UTC() },
		WatchInterval: time.Hour,
	}
	first, err := plugin.NewRegistry(config)
	if err != nil {
		return false, false, err
	}
	if _, err = first.Trust("fixture"); err == nil {
		err = first.Enable("fixture")
	}
	if closeErr := first.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return false, false, err
	}
	second, err := plugin.NewRegistry(config)
	if err != nil {
		return false, false, err
	}
	defer second.Close()
	if err = second.Reload(); err != nil {
		return false, false, err
	}
	snapshots, err := second.LifecycleSnapshots()
	if err != nil {
		return false, false, err
	}
	restart = len(snapshots) == 1 && snapshots[0].Enabled && snapshots[0].Generation == 1
	loaded, err := second.Load("fixture")
	if err != nil {
		return restart, false, err
	}
	defer loaded.Close()
	if err = second.SecurityRevoke("fixture"); err != nil {
		return restart, false, err
	}
	_, runErr := loaded.Run(context.Background(), nil)
	revoke = runErr != nil && strings.Contains(runErr.Error(), "authority revoked")
	if !restart || !revoke {
		return restart, revoke, errors.New("local lifecycle golden did not reach required state")
	}
	return restart, revoke, nil
}

func runLifecycleContractTests(root string) error {
	commands := [][]string{
		{
			"test", "-count=1", "./internal/adapter/plugin",
			"-run",
			"Test(SignedRegistryInstallUpdateDrainRollbackAndSecurityRevoke|SecurityRevokeCancelsInflightPluginCall)",
		},
		{
			"test", "-count=1", "./internal/adapter/tool/mcp",
			"-run", "TestAsyncSyncQuarantinesStaleCatalogAndRecovers",
		},
	}
	for _, arguments := range commands {
		command := exec.Command("go", arguments...)
		command.Dir = root
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf(
				"lifecycle contract %q: %w\n%s",
				strings.Join(arguments, " "),
				err,
				strings.TrimSpace(string(output)),
			)
		}
	}
	return nil
}

func measureContracts(root string) (contractMetrics, error) {
	pluginSkill, err := pluginSkillProductionReachable(
		filepath.Join(root, "internal/runtime/app/wire/contributors_capabilities.go"),
	)
	if err != nil {
		return contractMetrics{}, err
	}
	return contractMetrics{
		SharedToolRegistry: sourceContainsAll(
			filepath.Join(root, "internal/runtime/app/wire/modules_extensions.go"),
			"contributeExtensions(ctx, state.tools.registry", "tool.Registry",
		),
		ConsequentialToolsGuarded: sourceContainsAll(
			filepath.Join(root, "internal/adapter/tool/guard/guard.go"),
			"ExecuteBound", "policy", "approval",
		),
		PluginSupplyChainVerified: sourceContainsAll(
			filepath.Join(root, "internal/adapter/plugin/registry.go"),
			"Immutable Staging", "SecurityRevoke", "LifecycleSnapshots",
		) || sourceContainsAll(
			filepath.Join(root, "internal/adapter/plugin/registry.go"),
			"SecurityRevoke", "LifecycleSnapshots", "ResolveAndStage",
		),
		SkillAuthorityAndLockfile: sourceContainsAll(
			filepath.Join(root, "internal/adapter/skill/catalog.go"),
			"AuthorityVerifier", "lock", "verify",
		),
		MCPSourceReconcile: sourceContainsAll(
			filepath.Join(root, "internal/adapter/tool/mcp/mcp.go"),
			"Reconcile", "quarantineLocked", "SourceRegistrations",
		),
		HookSandboxAndBoundedOutput: sourceContainsAll(
			filepath.Join(root, "internal/adapter/hooks/executor.go"),
			"MaxOutputBytes", "Sandbox", "Timeout",
		),
		TurnFrozenExtensionSnapshot: sourceContainsAll(
			filepath.Join(root, "internal/runtime/agent/engine/turncontext.go"),
			"ExtensionPlan", "runtimeextension.Plan", "SnapshotTurnSpec",
		),
		TypedExtensionAPI: regularDirectory(filepath.Join(root, "internal/runtime/extension")),
		PluginCapabilityBundle: treeContainsAll(
			filepath.Join(root, "internal/adapter/plugin"),
			"ManifestSchemaV2", "CapabilityBundle",
			"SkillCapability", "HookCapability", "MCPCapability",
		),
		UnifiedExtensionLifecycle: treeContainsAll(
			filepath.Join(root, "internal/runtime/app/extension"),
			"StateDraining", "StateRevoked", "StateQuarantined",
		),
		RuntimeExtensionControlPlane: treeContainsAll(
			filepath.Join(root, "internal/runtime/protocol"),
			"ExtensionControlOperation", "ExtensionControlResult",
			"ExtensionControlEvent", "ReduceExtensionControlEvents",
		) && sourceContainsAll(
			filepath.Join(root, "internal/host/runtimeapi/acp/server.go"),
			"extension/list", "extension/control",
		) && sourceContainsAll(
			filepath.Join(root, "internal/host/cli/extensions.go"),
			"OpenExtensionControlPlane", "Submit",
		),
		OnDemandSkillCatalog: treeContainsAll(
			filepath.Join(root, "internal/adapter/tool"),
			"skills.list", "skills.read",
		),
		PluginSkillProductionReachable: pluginSkill,
	}, nil
}

func pluginSkillProductionReachable(path string) (bool, error) {
	file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
	if err != nil {
		return false, err
	}
	reachable := false
	ast.Inspect(file, func(node ast.Node) bool {
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		selector, ok := literal.Type.(*ast.SelectorExpr)
		if !ok || selector.Sel.Name != "DiscoveryOptions" {
			return true
		}
		for _, element := range literal.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if name, ok := field.Key.(*ast.Ident); ok && name.Name == "Plugins" {
				reachable = true
			}
		}
		return true
	})
	return reachable, nil
}

func renderSkillCatalog(values []skill.Summary) string {
	if len(values) == 0 {
		return ""
	}
	values = append([]skill.Summary(nil), values...)
	sort.Slice(values, func(i, j int) bool {
		if values[i].Name != values[j].Name {
			return values[i].Name < values[j].Name
		}
		if values[i].Source != values[j].Source {
			return values[i].Source < values[j].Source
		}
		return values[i].Path < values[j].Path
	})
	var builder strings.Builder
	builder.WriteString(
		"Available skills (metadata only). Call load_skill with a name before following its instructions.\n",
	)
	for _, value := range values {
		builder.WriteString("- name=")
		builder.WriteString(strconv.Quote(value.Name))
		builder.WriteString(" description=")
		description := strings.TrimSpace(value.Description)
		runes := []rune(description)
		if len(runes) > 160 {
			description = string(runes[:157]) + "..."
		}
		builder.WriteString(strconv.Quote(description))
		builder.WriteString(" source=")
		builder.WriteString(strconv.Quote(string(value.Source)))
		builder.WriteByte('\n')
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func renderSelectedSkillCatalog(values []skill.Summary) string {
	if len(values) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("Selected skills (metadata only).\n")
	for _, value := range values {
		builder.WriteString("- name=")
		builder.WriteString(strconv.Quote(value.Name))
		builder.WriteString(" description=")
		builder.WriteString(strconv.Quote(value.Description))
		builder.WriteString(" handle=")
		builder.WriteString(strconv.Quote(value.Handle))
		builder.WriteString(" package=")
		builder.WriteString(strconv.Quote(value.PackageHandle))
		builder.WriteString(" resource=")
		builder.WriteString(strconv.Quote(value.ResourceHandle))
		builder.WriteByte('\n')
	}
	return strings.TrimSuffix(builder.String(), "\n")
}

func stableDuration(operation func() error) (durationStats, error) {
	values := make([]int64, 0, extendedRuns)
	run := func() error {
		started := time.Now()
		err := operation()
		values = append(values, max(1, time.Since(started).Microseconds()))
		return err
	}
	for range initialRuns {
		if err := run(); err != nil {
			return durationStats{}, err
		}
	}
	stats := calculateDurationStats(values)
	if stats.MADBasisPoints > maxMADBP {
		for len(values) < extendedRuns {
			if err := run(); err != nil {
				return durationStats{}, err
			}
		}
		stats = calculateDurationStats(values)
	}
	return stats, nil
}

func calculateDurationStats(values []int64) durationStats {
	ordered := append([]int64(nil), values...)
	slices.Sort(ordered)
	median := percentile(ordered, 50)
	deviations := make([]int64, len(ordered))
	for index, value := range ordered {
		deviations[index] = abs64(value - median)
	}
	slices.Sort(deviations)
	mad := percentile(deviations, 50)
	basisPoints := int64(0)
	if median > 0 {
		basisPoints = mad * 10_000 / median
	}
	return durationStats{
		Runs: len(values), MedianMicros: median, P95Micros: percentile(ordered, 95),
		MADMicros: mad, MADBasisPoints: basisPoints,
	}
}

func percentile(ordered []int64, percent int) int64 {
	if len(ordered) == 0 {
		return 0
	}
	index := (percent*len(ordered) + 99) / 100
	index = max(1, index) - 1
	return ordered[min(index, len(ordered)-1)]
}

func writeSkill(root, name, description, body string) error {
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(
		"---\nname: %s\ndescription: %s\n---\n%s\n",
		name, description, body,
	)
	return os.WriteFile(filepath.Join(directory, "SKILL.md"), []byte(content), 0o600)
}

func writePluginBundle(root, name string, generation uint64, argument string) ([]byte, error) {
	directory := filepath.Join(root, name)
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return nil, err
	}
	executable := []byte("#!/bin/sh\nexit 0\n")
	if err := os.WriteFile(filepath.Join(directory, "run.sh"), executable, 0o700); err != nil {
		return nil, err
	}
	hash := sha256.Sum256(executable)
	manifest := plugin.Manifest{
		SchemaVersion: plugin.ManifestSchemaV1,
		Name:          name, Executable: "run.sh",
		ExecutableSHA256: hex.EncodeToString(hash[:]),
		Arguments:        []string{argument}, Generation: generation,
		Capabilities: plugin.CapabilityInventory{
			Tools: []string{"plugin_run"}, FilesystemRoots: []string{"workspace"},
			AllowProcess: true,
		},
	}
	data, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(directory, plugin.ManifestName), data, 0o600); err != nil {
		return nil, err
	}
	return data, nil
}

func validateCandidate(baseline, candidate report) error {
	if baseline.SchemaVersion != schemaVersion || baseline.Stage != stageEE0 {
		return errors.New("extension ecosystem baseline has an unsupported schema or stage")
	}
	if candidate.SchemaVersion != schemaVersion || candidate.Stage != stageEE0 {
		return errors.New("extension ecosystem candidate has an unsupported schema or stage")
	}
	if !candidate.CombinedGolden.FixtureHermetic ||
		candidate.CombinedGolden.Digest == "" ||
		len(candidate.CombinedGolden.ModelTools) == 0 {
		return errors.New("combined extension golden is incomplete")
	}
	if !scalesMatch(candidate.SkillScales, []int{0, 10, 100, 1000}) {
		return errors.New("skill scale fixture set changed")
	}
	if !pluginScalesMatch(candidate.PluginScales, []int{0, 10, 100}) {
		return errors.New("plugin scale fixture set changed")
	}
	largest := candidate.SkillScales[len(candidate.SkillScales)-1]
	if largest.TokenSavingsBP < 8000 || !largest.CriticalSkillRecall ||
		largest.SelectedBodyCount != 0 ||
		largest.CandidateCount > skill.DefaultSelectionLimit {
		return errors.New("EE5 skill selection gate failed")
	}
	for _, golden := range candidate.LifecycleGoldens {
		if !golden.Measured {
			return fmt.Errorf("lifecycle golden %q was not measured", golden.Scenario)
		}
	}
	baselineValue := astFields(baseline.Contracts)
	candidateValue := astFields(candidate.Contracts)
	for name, enabled := range baselineValue {
		if enabled && !candidateValue[name] {
			return fmt.Errorf("extension contract %s regressed", name)
		}
	}
	return nil
}

func scalesMatch(values []skillScale, expected []int) bool {
	if len(values) != len(expected) {
		return false
	}
	for index, count := range expected {
		if values[index].Count != count || values[index].Discovered != count {
			return false
		}
	}
	return true
}

func pluginScalesMatch(values []pluginScale, expected []int) bool {
	if len(values) != len(expected) {
		return false
	}
	for index, count := range expected {
		if values[index].Count != count || values[index].Discovered != count {
			return false
		}
	}
	return true
}

func astFields(value contractMetrics) map[string]bool {
	return map[string]bool{
		"SharedToolRegistry":             value.SharedToolRegistry,
		"ConsequentialToolsGuarded":      value.ConsequentialToolsGuarded,
		"PluginSupplyChainVerified":      value.PluginSupplyChainVerified,
		"SkillAuthorityAndLockfile":      value.SkillAuthorityAndLockfile,
		"MCPSourceReconcile":             value.MCPSourceReconcile,
		"HookSandboxAndBoundedOutput":    value.HookSandboxAndBoundedOutput,
		"TurnFrozenExtensionSnapshot":    value.TurnFrozenExtensionSnapshot,
		"TypedExtensionAPI":              value.TypedExtensionAPI,
		"PluginCapabilityBundle":         value.PluginCapabilityBundle,
		"UnifiedExtensionLifecycle":      value.UnifiedExtensionLifecycle,
		"RuntimeExtensionControlPlane":   value.RuntimeExtensionControlPlane,
		"OnDemandSkillCatalog":           value.OnDemandSkillCatalog,
		"PluginSkillProductionReachable": value.PluginSkillProductionReachable,
	}
}

func knownGaps(value contractMetrics) []string {
	fields := astFields(value)
	var result []string
	for field, name := range gapNames {
		if !fields[field] {
			result = append(result, name)
		}
	}
	sort.Strings(result)
	return result
}

func sourceContainsAll(path string, fragments ...string) bool {
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, fragment := range fragments {
		if !bytes.Contains(data, []byte(fragment)) {
			return false
		}
	}
	return true
}

func treeContainsAll(root string, fragments ...string) bool {
	found := make([]bool, len(fragments))
	_ = filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".go" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		for index, fragment := range fragments {
			found[index] = found[index] || bytes.Contains(data, []byte(fragment))
		}
		return nil
	})
	for _, value := range found {
		if !value {
			return false
		}
	}
	return true
}

func regularDirectory(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func readReport(path string) (report, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return report{}, err
	}
	var value report
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return report{}, err
	}
	return value, nil
}

func writeOptionalReport(root, path string, value report) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	return writeJSON(filepath.Join(root, path), value)
}

func writeJSON(path string, value report) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func digestString(value string) string {
	return "sha256:" + digestBytes([]byte(value))
}

func digestBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func gitCommit(root string) string {
	command := exec.Command("git", "rev-parse", "HEAD")
	command.Dir = root
	output, err := command.Output()
	if err != nil {
		return "unknown"
	}
	return strings.TrimSpace(string(output))
}

func abs64(value int64) int64 {
	if value < 0 {
		return -value
	}
	return value
}

type baselineBackend struct{}

func (baselineBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "baseline", Backend: "baseline",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (baselineBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	command.PreparedReadOnly = command.WorkspaceReadOnly
	command.PreparedWritePaths = append([]string(nil), command.WorkspaceWritePaths...)
	command.PreparedNetworkDenied = command.DenyNetwork
	return command, nil
}
