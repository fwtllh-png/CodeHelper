package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	webhost "github.com/fwtllh-png/CodeHelper/internal/host/runtimeapi/web"
)

const inventoryVersion = 1

type inventory struct {
	Version    int             `json:"version"`
	SourceHash string          `json:"source_hash"`
	Items      []inventoryItem `json:"items"`
}

type inventoryItem struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	SourceHash string `json:"source_hash"`
}

type ledger struct {
	Version  int       `json:"version"`
	Features []feature `json:"features"`
}

type feature struct {
	ID                     string   `json:"id"`
	Disposition            string   `json:"disposition"`
	LegacyInventoryIDs     []string `json:"legacy_inventory_ids"`
	LegacyEvidence         []string `json:"legacy_evidence,omitempty"`
	RuntimeOwner           string   `json:"runtime_owner,omitempty"`
	WebAPI                 []string `json:"web_api,omitempty"`
	WebSurface             []string `json:"web_surface,omitempty"`
	RequiredQualifications []string `json:"required_qualifications,omitempty"`
	SecondarySurface       string   `json:"secondary_surface,omitempty"`
	DropRationale          string   `json:"drop_rationale,omitempty"`
	Replacement            string   `json:"replacement,omitempty"`
}

type parityReport struct {
	Version        int                   `json:"version"`
	CommitSHA      string                `json:"commit_sha"`
	Dirty          bool                  `json:"dirty"`
	InputDigest    string                `json:"input_digest"`
	Commands       []string              `json:"commands"`
	TestResult     string                `json:"test_result"`
	ArtifactDigest string                `json:"artifact_digest"`
	GeneratedAt    time.Time             `json:"generated_at"`
	Features       []parityFeatureResult `json:"features"`
}

type parityFeatureResult struct {
	ID             string   `json:"id"`
	Status         string   `json:"status"`
	Qualifications []string `json:"qualifications,omitempty"`
}

type vscodePackage struct {
	Contributes struct {
		Commands []struct {
			Command string `json:"command"`
		} `json:"commands"`
		Views map[string][]struct {
			ID string `json:"id"`
		} `json:"views"`
		Menus map[string][]struct {
			Command string `json:"command"`
		} `json:"menus"`
		Configuration struct {
			Properties map[string]json.RawMessage `json:"properties"`
		} `json:"configuration"`
		ViewsContainers map[string][]struct {
			ID string `json:"id"`
		} `json:"viewsContainers"`
	} `json:"contributes"`
	Scripts map[string]string `json:"scripts"`
}

type hostJourney struct {
	Journey []struct {
		ID string `json:"id"`
	} `json:"journey"`
}

func main() {
	var root, mode, inventoryPath, ledgerPath, reportPath string
	flag.StringVar(&root, "root", ".", "repository root")
	flag.StringVar(&mode, "mode", "check", "capture or check")
	flag.StringVar(
		&inventoryPath,
		"inventory",
		"testdata/contracts/legacy-capability-inventory.json",
		"inventory path relative to root",
	)
	flag.StringVar(
		&ledgerPath,
		"ledger",
		"testdata/contracts/web-feature-parity.json",
		"ledger path relative to root",
	)
	flag.StringVar(
		&reportPath,
		"report",
		".tmp/web-feature-parity-report.json",
		"generated report path relative to root",
	)
	flag.Parse()

	var err error
	switch mode {
	case "capture":
		err = capture(root, inventoryPath, ledgerPath)
	case "check":
		err = check(root, inventoryPath, ledgerPath)
	case "report":
		err = generateReport(
			root,
			inventoryPath,
			ledgerPath,
			reportPath,
		)
	default:
		err = fmt.Errorf("unsupported mode %q", mode)
	}
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "web parity check: %v\n", err)
		os.Exit(1)
	}
}

func capture(root, inventoryPath, ledgerPath string) error {
	items, err := collect(root)
	if err != nil {
		return err
	}
	value := inventory{Version: inventoryVersion, Items: items}
	value.SourceHash = inventoryDigest(items)
	if err := writeJSON(filepath.Join(root, inventoryPath), value); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(root, ledgerPath), buildLedger(items)); err != nil {
		return err
	}
	return nil
}

func collect(root string) ([]inventoryItem, error) {
	var items []inventoryItem
	add := func(kind, name, source string) error {
		digest, err := fileDigest(filepath.Join(root, source))
		if err != nil {
			return err
		}
		items = append(items, inventoryItem{
			ID: kind + "." + stableID(name), Kind: kind, Name: name,
			Source: source, SourceHash: digest,
		})
		return nil
	}

	var extension vscodePackage
	if err := readJSONLoose(filepath.Join(root, "extensions/vscode/package.json"), &extension); err != nil {
		return nil, err
	}
	for _, command := range extension.Contributes.Commands {
		if err := add("vscode_command", command.Command, "extensions/vscode/package.json"); err != nil {
			return nil, err
		}
	}
	for _, views := range extension.Contributes.Views {
		for _, view := range views {
			if err := add("vscode_view", view.ID, "extensions/vscode/package.json"); err != nil {
				return nil, err
			}
		}
	}
	for location, entries := range extension.Contributes.Menus {
		for _, entry := range entries {
			if err := add(
				"vscode_menu",
				location+":"+entry.Command,
				"extensions/vscode/package.json",
			); err != nil {
				return nil, err
			}
		}
	}
	for name := range extension.Contributes.Configuration.Properties {
		if err := add(
			"vscode_configuration",
			name,
			"extensions/vscode/package.json",
		); err != nil {
			return nil, err
		}
	}
	for location, entries := range extension.Contributes.ViewsContainers {
		for _, entry := range entries {
			if err := add(
				"vscode_view_container",
				location+":"+entry.ID,
				"extensions/vscode/package.json",
			); err != nil {
				return nil, err
			}
		}
	}
	for name := range extension.Scripts {
		if !legacyCapabilityScript(name) {
			continue
		}
		if err := add(
			"vscode_package_script",
			name,
			"extensions/vscode/package.json",
		); err != nil {
			return nil, err
		}
	}

	acpSource := "internal/host/runtimeapi/acp/server.go"
	acpData, err := os.ReadFile(filepath.Join(root, acpSource))
	if err != nil {
		return nil, err
	}
	for _, name := range quotedValues(block(acpData, "var methods = []string{", "}\n\nvar dynamicMethods")) {
		if err := add("acp_method", name, acpSource); err != nil {
			return nil, err
		}
	}
	for _, name := range quotedValues(block(acpData, "var dynamicMethods = []string{", "}\n\ntype Dependencies")) {
		if err := add("acp_dynamic_method", name, acpSource); err != nil {
			return nil, err
		}
	}

	journeySource := "testdata/contracts/host-journey-contract.json"
	var journeys hostJourney
	if err := readJSONLoose(filepath.Join(root, journeySource), &journeys); err != nil {
		return nil, err
	}
	for _, journey := range journeys.Journey {
		if err := add("host_journey", journey.ID, journeySource); err != nil {
			return nil, err
		}
	}

	makeSource := "Makefile"
	makeData, err := os.ReadFile(filepath.Join(root, makeSource))
	if err != nil {
		return nil, err
	}
	targetPattern := regexp.MustCompile(`(?m)^([A-Za-z0-9_.-]+):`)
	for _, match := range targetPattern.FindAllSubmatch(makeData, -1) {
		name := string(match[1])
		if strings.Contains(name, "vscode") || strings.Contains(name, "acp") {
			if err := add("legacy_make_target", name, makeSource); err != nil {
				return nil, err
			}
		}
	}

	for _, secondary := range []string{
		"cli-host", "tui-host", "worker-execution",
		"automation-management", "mcp-management", "boot-repair",
	} {
		items = append(items, inventoryItem{
			ID:   "secondary_surface." + stableID(secondary),
			Kind: "secondary_surface", Name: secondary,
			Source: "docs/zh-CN/web-primary-entry-plan.md",
		})
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	for index := 1; index < len(items); index++ {
		if items[index-1].ID == items[index].ID {
			return nil, fmt.Errorf("duplicate inventory id %q", items[index].ID)
		}
	}
	return items, nil
}

func buildLedger(items []inventoryItem) ledger {
	features := make(map[string]*feature)
	for _, item := range items {
		id, disposition := classify(item)
		current := features[id]
		if current == nil {
			current = &feature{ID: id, Disposition: disposition}
			switch disposition {
			case "required":
				current.RuntimeOwner = "pending"
				current.WebAPI = []string{"pending"}
				current.WebSurface = []string{"pending"}
				current.RequiredQualifications = []string{"pending"}
			case "retained_secondary":
				current.SecondarySurface = item.Name
				current.RequiredQualifications = []string{"pending"}
			case "intentional_drop":
				current.DropRationale = "legacy host-specific surface"
				current.Replacement = "Web primary entry or retained CLI surface"
			}
			features[id] = current
		}
		current.LegacyInventoryIDs = append(current.LegacyInventoryIDs, item.ID)
		current.LegacyEvidence = appendUnique(current.LegacyEvidence, item.Source)
	}
	result := ledger{Version: inventoryVersion}
	for _, value := range features {
		sort.Strings(value.LegacyInventoryIDs)
		sort.Strings(value.LegacyEvidence)
		result.Features = append(result.Features, *value)
	}
	sort.Slice(result.Features, func(i, j int) bool {
		return result.Features[i].ID < result.Features[j].ID
	})
	return result
}

func classify(item inventoryItem) (string, string) {
	switch item.Kind {
	case "secondary_surface":
		return item.Name, "retained_secondary"
	case "legacy_make_target":
		return "legacy-vscode-acp-build-chain", "intentional_drop"
	case "acp_dynamic_method":
		return "acp-dynamic-tools", "intentional_drop"
	case "host_journey":
		return "journey-" + stableID(item.Name), "required"
	case "vscode_view":
		return "view-" + stableID(strings.TrimPrefix(item.Name, "codehelper.")), "required"
	case "vscode_command":
		return classifyVSCodeCommand(item.Name), "required"
	case "vscode_menu":
		_, command, _ := strings.Cut(item.Name, ":")
		return classifyVSCodeCommand(command), "required"
	case "vscode_configuration":
		switch {
		case strings.Contains(item.Name, "binary"),
			strings.Contains(item.Name, "update"):
			return "binary-update", "required"
		case strings.Contains(item.Name, "runtime"):
			return "runtime-readiness", "required"
		default:
			return "workspace-binding", "required"
		}
	case "vscode_view_container":
		return "view-chat", "required"
	case "vscode_package_script":
		if item.Name == "release:binary" {
			return "release-packaging", "retained_secondary"
		}
		if strings.Contains(item.Name, "binary") ||
			strings.Contains(item.Name, "update") {
			return "binary-update", "required"
		}
		return "legacy-vscode-acp-build-chain", "intentional_drop"
	case "acp_method":
		return classifyACPMethod(item.Name)
	default:
		return "unclassified", "required"
	}
}

func legacyCapabilityScript(name string) bool {
	for _, marker := range []string{
		"release", "package", "update", "security", "performance",
		"electron", "matrix",
	} {
		if strings.Contains(name, marker) {
			return true
		}
	}
	return false
}

func classifyVSCodeCommand(name string) string {
	switch {
	case strings.Contains(name, "Credential"):
		return "credential"
	case strings.Contains(name, "Extension"):
		return "extension"
	case strings.Contains(name, "Chat"):
		return "session-lifecycle"
	case strings.Contains(name, "Selection"):
		return "workspace-context"
	case strings.Contains(name, "Setup"), strings.Contains(name, "Runtime"),
		strings.Contains(name, "Quickstart"), strings.Contains(name, "Status"):
		return "runtime-readiness"
	case strings.Contains(name, "Update"):
		return "binary-update"
	default:
		return "workspace-binding"
	}
}

func classifyACPMethod(name string) (string, string) {
	switch {
	case name == "initialize" || name == "shutdown":
		return "web-transport-lifecycle", "required"
	case strings.HasPrefix(name, "provider/") || strings.HasPrefix(name, "model/"):
		return "profile", "required"
	case strings.HasPrefix(name, "checkpoint/"):
		return "checkpoint", "required"
	case strings.HasPrefix(name, "plan/") || strings.HasPrefix(name, "turn/recover"):
		return "plan-recovery", "required"
	case strings.HasPrefix(name, "extension/"):
		return "extension", "required"
	case strings.HasPrefix(name, "task/") || strings.HasPrefix(name, "agent/"):
		return "task-agent", "required"
	case strings.HasPrefix(name, "usage/"):
		return "usage-receipt", "required"
	case strings.HasPrefix(name, "thread/"):
		return "history-reconnect", "required"
	case strings.HasPrefix(name, "session/"):
		return "session-lifecycle", "required"
	default:
		return "web-api-" + stableID(name), "required"
	}
}

func check(root, inventoryPath, ledgerPath string) error {
	var captured inventory
	if err := readJSON(filepath.Join(root, inventoryPath), &captured); err != nil {
		return err
	}
	if captured.Version != inventoryVersion {
		return fmt.Errorf("inventory version = %d, want %d", captured.Version, inventoryVersion)
	}
	if captured.SourceHash != inventoryDigest(captured.Items) {
		return errors.New("inventory source hash does not match its items")
	}
	var requirements ledger
	if err := readJSON(filepath.Join(root, ledgerPath), &requirements); err != nil {
		return err
	}
	if requirements.Version != inventoryVersion {
		return fmt.Errorf("ledger version = %d, want %d", requirements.Version, inventoryVersion)
	}
	if err := verifyLegacyHostsRemoved(root); err != nil {
		return err
	}
	inventoryIDs := make(map[string]struct{}, len(captured.Items))
	for _, item := range captured.Items {
		if item.ID == "" || item.Kind == "" || item.Name == "" || item.Source == "" {
			return fmt.Errorf("inventory item is incomplete: %+v", item)
		}
		if _, exists := inventoryIDs[item.ID]; exists {
			return fmt.Errorf("duplicate inventory id %q", item.ID)
		}
		inventoryIDs[item.ID] = struct{}{}
	}
	mapped := make(map[string]string, len(captured.Items))
	webAPIs := publishedWebAPIs()
	for _, value := range requirements.Features {
		if value.ID == "" {
			return errors.New("ledger feature id is required")
		}
		switch value.Disposition {
		case "required":
			if value.RuntimeOwner == "" || len(value.WebAPI) == 0 ||
				len(value.WebSurface) == 0 || len(value.RequiredQualifications) == 0 {
				return fmt.Errorf("required feature %q is incomplete", value.ID)
			}
			if containsPending(value.RuntimeOwner, value.WebAPI, value.WebSurface, value.RequiredQualifications) {
				return fmt.Errorf("required feature %q still contains pending evidence", value.ID)
			}
			if len(value.WebSurface) != 0 &&
				!hasPrefix(value.RequiredQualifications, "web/src/ui/") &&
				!hasPrefix(
					value.RequiredQualifications,
					"web/tests/e2e/",
				) {
				return fmt.Errorf(
					"required feature %q has no Web UI qualification",
					value.ID,
				)
			}
			if err := requirePath(root, value.ID, "runtime owner", value.RuntimeOwner); err != nil {
				return err
			}
			if err := requireWebAPIs(value.ID, value.WebAPI, webAPIs); err != nil {
				return err
			}
			if err := requireQualifications(
				root,
				value.ID,
				value.RequiredQualifications,
			); err != nil {
				return err
			}
		case "retained_secondary":
			if value.SecondarySurface == "" || len(value.RequiredQualifications) == 0 {
				return fmt.Errorf("secondary feature %q is incomplete", value.ID)
			}
			if containsPending("", value.RequiredQualifications) {
				return fmt.Errorf("secondary feature %q still contains pending evidence", value.ID)
			}
			if err := requireQualifications(
				root,
				value.ID,
				value.RequiredQualifications,
			); err != nil {
				return err
			}
		case "intentional_drop":
			if value.DropRationale == "" || value.Replacement == "" {
				return fmt.Errorf("dropped feature %q is incomplete", value.ID)
			}
			if err := verifyDrop(root, value, captured.Items); err != nil {
				return err
			}
		default:
			return fmt.Errorf("feature %q has invalid disposition %q", value.ID, value.Disposition)
		}
		for _, id := range value.LegacyInventoryIDs {
			if _, exists := inventoryIDs[id]; !exists {
				return fmt.Errorf("feature %q references unknown inventory id %q", value.ID, id)
			}
			if owner, exists := mapped[id]; exists {
				return fmt.Errorf("inventory id %q belongs to both %q and %q", id, owner, value.ID)
			}
			mapped[id] = value.ID
		}
	}
	for id := range inventoryIDs {
		if _, exists := mapped[id]; !exists {
			return fmt.Errorf("inventory id %q is not mapped", id)
		}
	}
	fmt.Printf(
		"web parity capture valid: %d inventory items, %d features\n",
		len(captured.Items),
		len(requirements.Features),
	)
	return nil
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func hasPrefix(values []string, prefix string) bool {
	for _, value := range values {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func generateReport(
	root, inventoryPath, ledgerPath, reportPath string,
) error {
	if err := check(root, inventoryPath, ledgerPath); err != nil {
		return err
	}
	var requirements ledger
	if err := readJSON(filepath.Join(root, ledgerPath), &requirements); err != nil {
		return err
	}
	commands := qualificationCommands(requirements.Features)
	commandNames := make([]string, 0, len(commands))
	for _, arguments := range commands {
		commandNames = append(commandNames, strings.Join(arguments, " "))
		command := exec.Command(arguments[0], arguments[1:]...)
		command.Dir = root
		command.Stdout = os.Stdout
		command.Stderr = os.Stderr
		if err := command.Run(); err != nil {
			return fmt.Errorf("qualification %q failed: %w", strings.Join(arguments, " "), err)
		}
	}
	commit, dirty, err := gitIdentity(root)
	if err != nil {
		return err
	}
	inputs := []string{inventoryPath, ledgerPath, "web/src", "internal/host/runtimeapi/web"}
	for _, value := range requirements.Features {
		if value.RuntimeOwner != "" {
			inputs = append(inputs, value.RuntimeOwner)
		}
		for _, qualification := range value.RequiredQualifications {
			path, _ := splitQualification(qualification)
			inputs = append(inputs, path)
		}
	}
	inputDigest, err := pathsDigest(root, inputs)
	if err != nil {
		return err
	}
	artifactDigest, err := pathsDigest(root, []string{"web/dist"})
	if err != nil {
		return err
	}
	result := parityReport{
		Version:        1,
		CommitSHA:      commit,
		Dirty:          dirty,
		InputDigest:    inputDigest,
		Commands:       commandNames,
		TestResult:     "passed",
		ArtifactDigest: artifactDigest,
		GeneratedAt:    time.Now().UTC(),
		Features:       make([]parityFeatureResult, 0, len(requirements.Features)),
	}
	if dirty {
		result.TestResult = "passed_dirty"
	}
	for _, value := range requirements.Features {
		result.Features = append(result.Features, parityFeatureResult{
			ID:             value.ID,
			Status:         parityStatus(value.Disposition, dirty),
			Qualifications: append([]string(nil), value.RequiredQualifications...),
		})
	}
	return writeJSON(filepath.Join(root, reportPath), result)
}

func parityStatus(disposition string, dirty bool) string {
	if dirty {
		return "qualified_dirty"
	}
	if disposition == "intentional_drop" {
		return "verified_drop"
	}
	return "verified"
}

func publishedWebAPIs() map[string]struct{} {
	result := make(map[string]struct{})
	for _, route := range webhost.Contract().Routes {
		name := strings.TrimPrefix(route.Path, "/api/v1/")
		name = strings.TrimPrefix(name, "/")
		if route.Method == "GET+WEBSOCKET" {
			name += " WebSocket"
		}
		result[name] = struct{}{}
	}
	return result
}

func requireWebAPIs(
	featureID string,
	declared []string,
	published map[string]struct{},
) error {
	for _, api := range declared {
		if _, exists := published[api]; !exists {
			return fmt.Errorf(
				"required feature %q references unknown Web API %q",
				featureID,
				api,
			)
		}
	}
	return nil
}

func verifyDrop(root string, value feature, items []inventoryItem) error {
	byID := make(map[string]inventoryItem, len(items))
	for _, item := range items {
		byID[item.ID] = item
	}
	for _, id := range value.LegacyInventoryIDs {
		item, exists := byID[id]
		if !exists {
			continue
		}
		switch item.Kind {
		case "legacy_make_target":
			data, err := os.ReadFile(filepath.Join(root, "Makefile"))
			if err != nil {
				return err
			}
			pattern := regexp.MustCompile(
				`(?m)^` + regexp.QuoteMeta(item.Name) + `:`,
			)
			if pattern.Match(data) {
				return fmt.Errorf(
					"dropped feature %q still exposes Make target %q",
					value.ID,
					item.Name,
				)
			}
		case "acp_dynamic_method":
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(item.Source))); err == nil {
				return fmt.Errorf(
					"dropped feature %q still exposes ACP source %q",
					value.ID,
					item.Source,
				)
			} else if !errors.Is(err, os.ErrNotExist) {
				return err
			}
		}
	}
	return nil
}

func verifyLegacyHostsRemoved(root string) error {
	for _, name := range []string{
		"extensions/vscode",
		"internal/host/runtimeapi/acp",
		"internal/compatibility",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err == nil {
			return fmt.Errorf("legacy host path %q still exists", name)
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

func qualificationCommands(features []feature) [][]string {
	goSelectors := make(map[string]map[string]struct{})
	var webSelectors []string
	var webE2ESelectors []string
	needsHostJourney := false
	for _, value := range features {
		for _, qualification := range value.RequiredQualifications {
			path, selector := splitQualification(qualification)
			switch {
			case strings.HasPrefix(path, "web/tests/e2e/"):
				webE2ESelectors = appendUnique(webE2ESelectors, selector)
			case strings.HasPrefix(path, "web/") &&
				(strings.HasSuffix(path, ".ts") ||
					strings.HasSuffix(path, ".tsx")):
				webSelectors = appendUnique(webSelectors, selector)
			case path == "testdata/contracts/host-journey-contract.json":
				needsHostJourney = true
			case strings.HasSuffix(path, "_test.go") ||
				strings.HasPrefix(path, "internal/"):
				pkg := path
				if filepath.Ext(pkg) == ".go" {
					pkg = filepath.Dir(pkg)
				}
				pkg = "./" + filepath.ToSlash(pkg)
				if goSelectors[pkg] == nil {
					goSelectors[pkg] = make(map[string]struct{})
				}
				if selector != "" {
					goSelectors[pkg][selector] = struct{}{}
				}
			}
		}
	}
	packages := make([]string, 0, len(goSelectors))
	for pkg := range goSelectors {
		packages = append(packages, pkg)
	}
	sort.Strings(packages)
	commands := make([][]string, 0, len(packages)+6)
	for _, pkg := range packages {
		selectors := sortedKeys(goSelectors[pkg])
		arguments := []string{"go", "test", pkg}
		if len(selectors) != 0 {
			arguments = append(arguments, "-run", exactSelectorPattern(selectors))
		}
		commands = append(commands, arguments)
	}
	if needsHostJourney {
		commands = append(commands, []string{"make", "host-journey-contract"})
	}
	commands = append(commands, []string{"npm", "--prefix", "web", "run", "check"})
	if len(webSelectors) != 0 {
		sort.Strings(webSelectors)
		commands = append(commands, []string{
			"npm", "--prefix", "web", "test", "--",
			"--testNamePattern", selectorSuffixPattern(webSelectors),
		})
	}
	commands = append(commands, []string{"make", "web-build"})
	if len(webE2ESelectors) != 0 {
		sort.Strings(webE2ESelectors)
		commands = append(commands, []string{"make", "build"})
		commands = append(commands, []string{
			"npm", "--prefix", "web", "run", "test:e2e", "--",
			"--grep", selectorSuffixPattern(webE2ESelectors),
		})
	}
	return commands
}

func exactSelectorPattern(selectors []string) string {
	escaped := make([]string, 0, len(selectors))
	for _, selector := range selectors {
		escaped = append(escaped, regexp.QuoteMeta(selector))
	}
	return "^(?:" + strings.Join(escaped, "|") + ")$"
}

func selectorSuffixPattern(selectors []string) string {
	return strings.TrimPrefix(exactSelectorPattern(selectors), "^")
}

func sortedKeys(values map[string]struct{}) []string {
	result := make([]string, 0, len(values))
	for value := range values {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func splitQualification(value string) (string, string) {
	path, selector, found := strings.Cut(value, "#")
	if !found {
		return value, ""
	}
	return path, selector
}

func requireQualification(root, featureID, value string) error {
	path, selector := splitQualification(value)
	if err := requirePath(root, featureID, "qualification", path); err != nil {
		return err
	}
	if selector == "" {
		if strings.Contains(value, "#") {
			return fmt.Errorf("feature %q has an empty qualification selector", featureID)
		}
		return nil
	}
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		return err
	}
	source := string(data)
	if strings.HasSuffix(path, "_test.go") {
		source = strings.ReplaceAll(source, "\n", " ")
		if !strings.Contains(source, "func "+selector+"(") {
			return fmt.Errorf(
				"feature %q qualification selector %q is not declared in %q",
				featureID,
				selector,
				path,
			)
		}
		return nil
	}
	if !strings.Contains(source, strconv.Quote(selector)) &&
		!strings.Contains(source, "'"+selector+"'") {
		return fmt.Errorf(
			"feature %q qualification selector %q is not declared in %q",
			featureID,
			selector,
			path,
		)
	}
	return nil
}

func requireQualifications(root, featureID string, values []string) error {
	hasSelector := false
	for _, value := range values {
		path, selector := splitQualification(value)
		if isTestFile(path) && selector == "" {
			return fmt.Errorf(
				"feature %q test qualification %q requires an exact selector",
				featureID,
				path,
			)
		}
		if selector != "" {
			hasSelector = true
		}
		if err := requireQualification(root, featureID, value); err != nil {
			return err
		}
	}
	if !hasSelector {
		return fmt.Errorf("feature %q has no exact test selector", featureID)
	}
	return nil
}

func isTestFile(path string) bool {
	return strings.HasSuffix(path, "_test.go") ||
		strings.HasSuffix(path, ".test.ts") ||
		strings.HasSuffix(path, ".test.tsx") ||
		strings.HasSuffix(path, ".spec.ts")
}

func gitIdentity(root string) (string, bool, error) {
	commitCommand := exec.Command("git", "rev-parse", "HEAD")
	commitCommand.Dir = root
	commitOutput, err := commitCommand.Output()
	if err != nil {
		return "", false, fmt.Errorf("resolve parity commit: %w", err)
	}
	statusCommand := exec.Command("git", "status", "--porcelain", "--untracked-files=all")
	statusCommand.Dir = root
	statusOutput, err := statusCommand.Output()
	if err != nil {
		return "", false, fmt.Errorf("resolve parity worktree state: %w", err)
	}
	return strings.TrimSpace(string(commitOutput)), len(bytes.TrimSpace(statusOutput)) != 0, nil
}

func pathsDigest(root string, names []string) (string, error) {
	unique := make(map[string]struct{}, len(names))
	var files []string
	for _, name := range names {
		name = filepath.Clean(filepath.FromSlash(name))
		if _, exists := unique[name]; exists {
			continue
		}
		unique[name] = struct{}{}
		full := filepath.Join(root, name)
		info, err := os.Stat(full)
		if err != nil {
			return "", fmt.Errorf("digest input %q: %w", name, err)
		}
		if !info.IsDir() {
			files = append(files, name)
			continue
		}
		err = filepath.WalkDir(full, func(path string, entry os.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.IsDir() {
				if entry.Name() == "node_modules" {
					return filepath.SkipDir
				}
				return nil
			}
			relative, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			files = append(files, filepath.Clean(relative))
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	sort.Strings(files)
	hash := sha256.New()
	for _, name := range files {
		data, err := os.ReadFile(filepath.Join(root, name))
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", filepath.ToSlash(name), len(data))
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{'\n'})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func containsPending(owner string, groups ...[]string) bool {
	if owner == "pending" {
		return true
	}
	for _, group := range groups {
		for _, value := range group {
			if value == "" || value == "pending" {
				return true
			}
		}
	}
	return false
}

func requirePath(root, featureID, kind, value string) error {
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(value))); err != nil {
		return fmt.Errorf("feature %q %s %q is unavailable: %w", featureID, kind, value, err)
	}
	return nil
}

func block(data []byte, start, end string) []byte {
	from := bytes.Index(data, []byte(start))
	if from < 0 {
		return nil
	}
	from += len(start)
	to := bytes.Index(data[from:], []byte(end))
	if to < 0 {
		return nil
	}
	return data[from : from+to]
}

func quotedValues(data []byte) []string {
	pattern := regexp.MustCompile(`"([^"]+)"`)
	var values []string
	for _, match := range pattern.FindAllSubmatch(data, -1) {
		values = append(values, string(match[1]))
	}
	return values
}

func stableID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer("/", "-", ".", "-", "_", "-", " ", "-")
	return strings.Trim(replacer.Replace(value), "-")
}

func inventoryDigest(items []inventoryItem) string {
	hash := sha256.New()
	for _, item := range items {
		_, _ = fmt.Fprintf(
			hash,
			"%s\x00%s\x00%s\x00%s\x00%s\n",
			item.ID,
			item.Kind,
			item.Name,
			item.Source,
			item.SourceHash,
		)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func fileDigest(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func readJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func readJSONLoose(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	return nil
}

func writeJSON(path string, value any) error {
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

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}
