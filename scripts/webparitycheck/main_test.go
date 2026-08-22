package main

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCaptureProducesClosedInventoryAndLedger(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "extensions/vscode/package.json", `{
  "contributes": {
    "commands": [{"command": "codehelper.newChat"}],
    "views": {"codehelper": [{"id": "codehelper.chat"}]},
    "menus": {"editor/context": [{"command": "codehelper.newChat"}]},
    "configuration": {"properties": {"codehelper.runtime.autoStart": {"type": "boolean"}}},
    "viewsContainers": {"activitybar": [{"id": "codehelper"}]}
  },
  "scripts": {
    "release:binary": "node release.mjs",
    "lint": "eslint ."
  }
}`)
	writeFixture(t, root, "internal/host/runtimeapi/acp/server.go", `
var methods = []string{
	"initialize", "session/new",
}

var dynamicMethods = []string{
	"tool/register",
}

type Dependencies struct{}
`)
	writeFixture(t, root, "testdata/contracts/host-journey-contract.json", `{
  "journey": [{"id": "start"}]
}`)
	writeFixture(t, root, "Makefile", "vscode-check:\n\ttrue\n")
	writeFixture(t, root, "docs/zh-CN/web-primary-entry-plan.md", "# plan\n")

	if err := capture(
		root,
		"testdata/contracts/legacy-capability-inventory.json",
		"testdata/contracts/web-feature-parity.json",
	); err != nil {
		t.Fatal(err)
	}

	var got inventory
	if err := readJSON(
		filepath.Join(root, "testdata/contracts/legacy-capability-inventory.json"),
		&got,
	); err != nil {
		t.Fatal(err)
	}
	if len(got.Items) != 17 {
		t.Fatalf("inventory items = %d, want 17", len(got.Items))
	}
	if got.SourceHash == "" {
		t.Fatal("inventory source hash is empty")
	}
}

func TestClassifyRequiresExplicitDynamicToolDrop(t *testing.T) {
	id, disposition := classify(inventoryItem{
		Kind: "acp_dynamic_method",
		Name: "tool/register",
	})
	if id != "acp-dynamic-tools" || disposition != "intentional_drop" {
		t.Fatalf("classification = %q %q", id, disposition)
	}
}

func TestClassifyPreservesBinaryReleaseAsSecondarySurface(t *testing.T) {
	id, disposition := classify(inventoryItem{
		Kind: "vscode_package_script",
		Name: "release:binary",
	})
	if id != "release-packaging" || disposition != "retained_secondary" {
		t.Fatalf("classification = %q %q", id, disposition)
	}
}

func TestStableID(t *testing.T) {
	if got := stableID("Session/Profile.Get"); got != "session-profile-get" {
		t.Fatalf("stable id = %q", got)
	}
}

func TestParityStatusDoesNotVerifyDirtyInputs(t *testing.T) {
	if got := parityStatus("required", true); got != "qualified_dirty" {
		t.Fatalf("dirty required status = %q", got)
	}
	if got := parityStatus("required", false); got != "verified" {
		t.Fatalf("clean required status = %q", got)
	}
	if got := parityStatus("intentional_drop", true); got != "qualified_dirty" {
		t.Fatalf("dirty drop status = %q", got)
	}
}

func TestPublishedWebAPIsMatchLedgerSpelling(t *testing.T) {
	apis := publishedWebAPIs()
	for _, name := range []string{
		"bootstrap",
		"events WebSocket",
		"healthz",
		"operation/submit",
		"session/snapshot",
	} {
		if _, exists := apis[name]; !exists {
			t.Errorf("published Web API %q is missing", name)
		}
	}
}

func TestRequireWebAPIsRejectsUnknownRoute(t *testing.T) {
	if err := requireWebAPIs(
		"feature",
		[]string{"session/list", "missing/route"},
		publishedWebAPIs(),
	); err == nil {
		t.Fatal("unknown Web API was accepted")
	}
}

func TestVerifyDropRejectsRetainedLegacySurface(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "Makefile", "vscode-check:\n\ttrue\n")
	value := feature{
		ID:                 "legacy-build",
		LegacyInventoryIDs: []string{"legacy_make_target.vscode-check"},
	}
	items := []inventoryItem{{
		ID:     "legacy_make_target.vscode-check",
		Kind:   "legacy_make_target",
		Name:   "vscode-check",
		Source: "Makefile",
	}}
	if err := verifyDrop(root, value, items); err == nil {
		t.Fatal("retained legacy Make target was accepted as a verified drop")
	}
}

func TestVerifyLegacyHostsRemovedRejectsOldSourceTrees(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "extensions/vscode/package.json", "{}")
	if err := verifyLegacyHostsRemoved(root); err == nil {
		t.Fatal("legacy VS Code source tree was accepted")
	}
}

func TestQualificationCommandsExecuteDeclaredTestKinds(t *testing.T) {
	commands := qualificationCommands([]feature{{
		RequiredQualifications: []string{
			"internal/runtime/app/session_lifecycle_test.go#TestSessionControlCreatesActivatesAndSubmitsWithStableIdentity",
			"internal/runtime/app/chatmerge",
			"scripts/webprotocolgen/main_test.go#TestGenerateProducesDeterministicGuardedRoutes",
			"testdata/contracts/host-journey-contract.json",
			"web/src/runtime/client.test.ts#normalizes empty collections and submits act prompts as answers",
			"web/tests/e2e/web.spec.ts#creates a Session and completes a fixture-backed Turn",
		},
	}})
	got := make([]string, 0, len(commands))
	for _, command := range commands {
		got = append(got, strings.Join(command, " "))
	}
	for _, want := range []string{
		"go test ./internal/runtime/app -run ^(?:TestSessionControlCreatesActivatesAndSubmitsWithStableIdentity)$",
		"go test ./internal/runtime/app/chatmerge",
		"go test ./scripts/webprotocolgen -run ^(?:TestGenerateProducesDeterministicGuardedRoutes)$",
		"make host-journey-contract",
		"npm --prefix web run check",
		"npm --prefix web test -- --testNamePattern (?:normalizes empty collections and submits act prompts as answers)$",
		"make web-build",
		"make build",
		"npm --prefix web run test:e2e -- --grep (?:creates a Session and completes a fixture-backed Turn)$",
	} {
		if !slices.Contains(got, want) {
			t.Fatalf("commands = %v, missing %q", got, want)
		}
	}
}

func TestRequireQualificationValidatesExactSelectors(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, root, "internal/example/example_test.go", `
package example
func TestExactEvidence(t *testing.T) {}
`)
	writeFixture(t, root, "web/src/example.test.ts", `
it("exact browser evidence", () => {});
`)
	for _, value := range []string{
		"internal/example/example_test.go#TestExactEvidence",
		"web/src/example.test.ts#exact browser evidence",
	} {
		if err := requireQualification(root, "feature", value); err != nil {
			t.Fatal(err)
		}
	}
	if err := requireQualification(
		root,
		"feature",
		"internal/example/example_test.go#TestMissing",
	); err == nil {
		t.Fatal("missing selector was accepted")
	}
	if err := requireQualifications(
		root,
		"feature",
		[]string{"internal/example/example_test.go"},
	); err == nil {
		t.Fatal("test qualification without selector was accepted")
	}
}

func writeFixture(t *testing.T, root, name, data string) {
	t.Helper()
	path := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
}
