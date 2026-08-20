package admission

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/qualification"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestRepositoryH4CatalogPinsControlledInventory(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	catalog, err := LoadH4(root, "evaluation/spec/h4-execution.json")
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Canary.PhaseSlots) != 3 ||
		catalog.Canary.PhaseSlots[0] != 1 ||
		catalog.Canary.PhaseSlots[2] != 3 ||
		catalog.Canary.TurnsPerSlot != 20 ||
		len(catalog.Rollback.Command) == 0 {
		t.Fatalf("H4 catalog = %+v", catalog)
	}
}

func TestH4SchemasValidateRepositoryAssets(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	validateH4SchemaAsset(
		t,
		root,
		"evaluation/schema/h4-execution.schema.json",
		"evaluation/spec/h4-execution.json",
	)
	value := H4AdmissionEvidence{
		SchemaVersion:     H4EvidenceSchemaVersion,
		QualificationID:   "h4-test",
		SourceDigest:      digestH2("source"),
		LockIdentity:      digestH2("lock"),
		FoundationDigest:  digestH2("foundation"),
		RuntimeDigest:     digestH2("runtime"),
		VSIXDigest:        digestH2("vsix"),
		H3QualificationID: "h3-test",
		H3ReleaseDigest:   digestH2("release"),
		PackageDigest:     digestH2("package"),
		Scope:             "controlled_local_inventory",
		PublicRelease:     false,
		Decision:          "admit",
	}
	for _, id := range h4RequiredLanes {
		value.RequiredLanes = append(value.RequiredLanes, H4LaneEvidence{
			ID:             id,
			Status:         "passed",
			EvidenceDigest: digestH2(id),
		})
	}
	path := filepath.Join(t.TempDir(), "h4-admission-evidence.json")
	if err := writePrivateJSON(path, value); err != nil {
		t.Fatal(err)
	}
	validateH4SchemaAsset(
		t,
		root,
		"evaluation/schema/h4-admission-evidence.schema.json",
		path,
	)
}

func TestH4HealthGateStopsOnAnyFailedTurn(t *testing.T) {
	policy := testH4CanaryPolicy()
	if got := h4ExpansionDecision(10, 10, 0, 100, policy); got != "expand" {
		t.Fatalf("healthy decision = %q", got)
	}
	for _, test := range []struct {
		scheduled int
		completed int
		failed    int
		p95       int64
	}{
		{10, 9, 1, 100},
		{10, 9, 0, 100},
		{10, 10, 0, 1001},
	} {
		if got := h4ExpansionDecision(
			test.scheduled,
			test.completed,
			test.failed,
			test.p95,
			policy,
		); got != "stop" {
			t.Fatalf("unhealthy decision = %q for %+v", got, test)
		}
	}
}

func TestH4StopDrillFeedsReviewedMetadataOnlyCorpus(t *testing.T) {
	root := t.TempDir()
	stop, err := RunH4StopDrill(root, testH4CanaryPolicy())
	if err != nil {
		t.Fatal(err)
	}
	incident, err := RunH4IncidentClosure(
		root,
		stop.EvidenceDigest,
		H4IncidentSpec{
			BatchID:     "h4-canary-incident",
			EntryPrefix: "canary-incident",
			Reviewer:    "h4-reviewer",
			ReviewedOn:  "2026-08-20",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if incident.Status != "passed" ||
		incident.FailureSignature != "turn_failed" ||
		incident.SecretScan != "passed" ||
		incident.Entries != 1 {
		t.Fatalf("incident evidence = %+v", incident)
	}
}

func TestH4PrerequisiteRejectsPackageTamper(t *testing.T) {
	root := t.TempDir()
	lock := freeze.Lock{
		FoundationDigest: digestH2("foundation"),
		SourceDigest:     digestH2("source"),
		RuntimeDigest:    digestH2("runtime"),
		VSIXDigest:       digestH2("vsix"),
		LockIdentity:     digestH2("lock"),
	}
	report, err := qualification.Run(
		t.Context(),
		qualification.Request{
			ID:               "h3-test",
			Kind:             "endurance",
			Root:             root,
			FoundationDigest: lock.FoundationDigest,
			SourceDigest:     lock.SourceDigest,
			RuntimeDigest:    lock.RuntimeDigest,
			VSIXDigest:       lock.VSIXDigest,
			LockIdentity:     lock.LockIdentity,
			Tasks: []qualification.Task{
				{
					ID: "passed",
					Check: func(context.Context) (string, error) {
						return digestH2("passed"), nil
					},
				},
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	reportRoot := filepath.Join(root, "report")
	if err := qualification.Write(reportRoot, report); err != nil {
		t.Fatal(err)
	}
	packageRoot := filepath.Join(root, "package")
	packageEvidence := writeH4TestPackage(t, packageRoot)
	release := H3ReleaseEvidence{
		SchemaVersion:    H3ReleaseSchemaVersion,
		QualificationID:  report.ID,
		SourceDigest:     lock.SourceDigest,
		LockIdentity:     lock.LockIdentity,
		FoundationDigest: lock.FoundationDigest,
		RuntimeDigest:    lock.RuntimeDigest,
		VSIXDigest:       lock.VSIXDigest,
		Decision:         "admit",
		Package:          packageEvidence,
	}
	for _, id := range h3RequiredLanes {
		release.RequiredLanes = append(release.RequiredLanes, H3LaneEvidence{
			ID:             id,
			Status:         "passed",
			EvidenceDigest: digestH2(id),
		})
	}
	releasePath := filepath.Join(root, "release-evidence.json")
	if err := writePrivateJSON(releasePath, release); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ValidateH4Prerequisite(
		filepath.Join(reportRoot, "qualification.json"),
		releasePath,
		packageRoot,
		lock,
	); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(packageRoot, "bin", "codehelper"),
		[]byte("tampered"),
		0o700,
	); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := ValidateH4Prerequisite(
		filepath.Join(reportRoot, "qualification.json"),
		releasePath,
		packageRoot,
		lock,
	); err == nil || !strings.Contains(err.Error(), "checksums") {
		t.Fatalf("tampered package prerequisite error = %v", err)
	}
}

func TestH4DevelopmentCanaryProductionHost(t *testing.T) {
	binary := os.Getenv("CODEHELPER_H4_CANARY_BINARY")
	if binary == "" {
		t.Skip("CODEHELPER_H4_CANARY_BINARY is not set")
	}
	digest, err := digestH3File(binary)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	development := os.Getenv("CODEHELPER_H4_CANARY_FORMAL") != "1"
	turnsOverride := 0
	intervalOverride := time.Duration(0)
	expectedTurns := 120
	if development {
		turnsOverride = 2
		intervalOverride = time.Millisecond
		expectedTurns = 12
	}
	evidence, err := RunH4Canary(
		context.Background(),
		H4CanaryRequest{
			Root:             root,
			Output:           t.TempDir(),
			QualificationID:  "h4-development",
			SourceDigest:     digestH2("source"),
			LockIdentity:     digestH2("lock"),
			PackageBinary:    binary,
			PackageDigest:    digest,
			Policy:           testH4CanaryPolicy(),
			Development:      development,
			TurnsOverride:    turnsOverride,
			IntervalOverride: intervalOverride,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if evidence.Status != "passed" ||
		evidence.DevelopmentOverride != development ||
		evidence.TurnsScheduled != expectedTurns ||
		evidence.TurnsCompleted != expectedTurns ||
		evidence.TurnsFailed != 0 ||
		len(evidence.Slots) != 3 {
		t.Fatalf("development Canary evidence = %+v", evidence)
	}
}

func validateH4SchemaAsset(
	t *testing.T,
	root, schemaPath, valuePath string,
) {
	t.Helper()
	schemaRaw, err := os.ReadFile(filepath.Join(root, schemaPath))
	if err != nil {
		t.Fatal(err)
	}
	var schemaValue any
	if err := json.Unmarshal(schemaRaw, &schemaValue); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("schema.json", schemaValue); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile("schema.json")
	if err != nil {
		t.Fatal(err)
	}
	absoluteValue := valuePath
	if !filepath.IsAbs(absoluteValue) {
		absoluteValue = filepath.Join(root, valuePath)
	}
	raw, err := os.ReadFile(absoluteValue)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if err := compiled.Validate(value); err != nil {
		t.Fatal(err)
	}
}

func testH4CanaryPolicy() H4CanarySpec {
	return H4CanarySpec{
		PhaseSlots:         []int{1, 2, 3},
		TurnsPerSlot:       20,
		TurnIntervalMS:     250,
		TurnTimeoutSeconds: 20,
		Prompt:             "say hello",
		MaxP95LatencyMS:    1000,
		MaxRSSGrowthBytes:  64 << 20,
		MaxFDGrowth:        8,
	}
}

func writeH4TestPackage(
	t *testing.T,
	root string,
) H3ReleasePackage {
	t.Helper()
	files := map[string]string{
		"bin/codehelper":        "candidate",
		"bin/linux-amd64":       "linux-amd64",
		"bin/linux-arm64":       "linux-arm64",
		"bin/darwin-amd64":      "darwin-amd64",
		"bin/darwin-arm64":      "darwin-arm64",
		"bin/windows-amd64.exe": "windows-amd64",
		"codehelper.tar.gz":     "archive",
	}
	var names []string
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o700); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, name := range names {
		digest, err := digestH3File(
			filepath.Join(root, filepath.FromSlash(name)),
		)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(
			&sums,
			"%s  ./%s\n",
			strings.TrimPrefix(digest, "sha256:"),
			name,
		)
	}
	checksumsPath := filepath.Join(root, "SHA256SUMS")
	if err := os.WriteFile(
		checksumsPath,
		[]byte(sums.String()),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	sbomPath := filepath.Join(root, "sbom", "codehelper.cdx.json")
	if err := os.MkdirAll(filepath.Dir(sbomPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		sbomPath,
		[]byte(`{"bomFormat":"CycloneDX"}`),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	manifest := h3PackageManifest{
		SchemaVersion: 1,
		Product:       "codehelper",
		Version:       "0.0.1",
		Commit:        "commit",
		BuiltAt:       "2026-08-20T00:00:00Z",
		Stage:         "candidate",
		StageSequence: []string{
			"experimental", "preview", "candidate", "default",
		},
		Tarball:   "codehelper.tar.gz",
		SBOM:      "sbom/codehelper.cdx.json",
		Checksums: "SHA256SUMS",
		SHA256SUMSDigest: strings.TrimPrefix(
			mustH4FileDigest(t, checksumsPath),
			"sha256:",
		),
		GeneratedAt: "2026-08-20T00:00:00Z",
	}
	manifestPath := filepath.Join(root, "package-manifest.json")
	if err := writePrivateJSON(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	return H3ReleasePackage{
		ManifestDigest:  mustH4FileDigest(t, manifestPath),
		ChecksumsDigest: mustH4FileDigest(t, checksumsPath),
		SBOMDigest:      mustH4FileDigest(t, sbomPath),
		Targets: []string{
			"linux/amd64",
			"linux/arm64",
			"darwin/amd64",
			"darwin/arm64",
			"windows/amd64",
		},
	}
}

func mustH4FileDigest(t *testing.T, path string) string {
	t.Helper()
	digest, err := digestH3File(path)
	if err != nil {
		t.Fatal(err)
	}
	return digest
}
