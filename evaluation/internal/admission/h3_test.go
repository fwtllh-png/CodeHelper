package admission

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type h3TestWriteCloser struct {
	strings.Builder
}

func (*h3TestWriteCloser) Close() error {
	return nil
}

func TestRepositoryH3CatalogPinsFourHourReleaseInventory(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	catalog, err := LoadH3(root, "evaluation/spec/h3-execution.json")
	if err != nil {
		t.Fatal(err)
	}
	if catalog.Endurance.DurationSeconds != 14_400 ||
		catalog.Endurance.MinCompletedTurns != 400 ||
		len(catalog.Release.RequiredLanes) != 8 {
		t.Fatalf("H3 catalog = %+v", catalog)
	}
}

func TestH3SchemasValidateRepositoryAssets(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", "..", ".."))
	for _, pair := range []struct {
		schema string
		value  string
	}{
		{
			"evaluation/schema/h3-execution.schema.json",
			"evaluation/spec/h3-execution.json",
		},
	} {
		validateH3SchemaAsset(t, root, pair.schema, pair.value)
	}
	release := H3ReleaseEvidence{
		SchemaVersion:   H3ReleaseSchemaVersion,
		QualificationID: "h3-test",
		SourceDigest:    digestH2("source"), LockIdentity: digestH2("lock"),
		FoundationDigest: digestH2("foundation"),
		RuntimeDigest:    digestH2("runtime"), VSIXDigest: digestH2("vsix"),
		HostDigest: digestH2("host"), ProviderDigest: digestH2("provider"),
		ModelDigest: digestH2("model"), ConfigDigest: digestH2("config"),
		Decision: "admit",
		Package: H3ReleasePackage{
			ManifestDigest:  digestH2("manifest"),
			ChecksumsDigest: digestH2("checksums"),
			SBOMDigest:      digestH2("sbom"),
			Targets: []string{
				"linux/amd64", "linux/arm64", "darwin/amd64",
				"darwin/arm64", "windows/amd64",
			},
		},
	}
	for _, id := range h3RequiredLanes {
		release.RequiredLanes = append(release.RequiredLanes, H3LaneEvidence{
			ID: id, Status: "passed", EvidenceDigest: digestH2(id),
		})
	}
	path := filepath.Join(t.TempDir(), "release-evidence.json")
	if err := writePrivateJSON(path, release); err != nil {
		t.Fatal(err)
	}
	validateH3SchemaAsset(
		t,
		root,
		"evaluation/schema/release-evidence.schema.json",
		path,
	)
}

func TestH3EndurancePolicyMeasuresSlopesAndRejectsGrowth(t *testing.T) {
	policy := testH3EndurancePolicy()
	evidence := H3EnduranceEvidence{
		ConfiguredDuration: 14_400, EffectiveDurationMS: 10_000,
		TurnIntervalMS: 1_000, DevelopmentOverride: true,
		TurnsScheduled: 10, TurnsCompleted: 10, TerminalCompleted: 10,
	}
	for turn := 0; turn <= 10; turn++ {
		evidence.Samples = append(evidence.Samples, H3ResourceSample{
			Turn:             turn,
			RSSBytes:         int64(1000 + turn*10),
			FDs:              10,
			PersistenceBytes: int64(1000 + turn*100),
			LatencyMS:        int64(100 + turn),
		})
	}
	evaluateH3Endurance(&evidence, policy)
	if evidence.Status != "passed" ||
		evidence.Slopes.RSSBytesPerTurn != 10 ||
		evidence.Slopes.PersistenceBytesPerTurn != 100 {
		t.Fatalf("H3 evidence = %+v", evidence)
	}
	evidence.Status = "failed"
	for index := range evidence.Samples {
		evidence.Samples[index].RSSBytes = int64(index) * 1_000_000
	}
	evaluateH3Endurance(&evidence, policy)
	if evidence.Status == "passed" {
		t.Fatal("H3 Endurance accepted excessive RSS slope")
	}
}

func TestH3DevelopmentEnduranceCannotBecomeFormal(t *testing.T) {
	path := filepath.Join(t.TempDir(), "endurance-evidence.json")
	evidence := H3EnduranceEvidence{
		SchemaVersion:   H3EnduranceSchemaVersion,
		QualificationID: "h3-test", Status: "passed",
		SourceDigest: digestH2("source"), LockIdentity: digestH2("lock"),
		RuntimeDigest:      digestH2("runtime"),
		ConfiguredDuration: 14_400, EffectiveDurationMS: 10_000,
		TurnIntervalMS: 1_000, ObservedDurationMS: 10_000,
		DevelopmentOverride: true,
		TurnsScheduled:      2, TurnsCompleted: 2, TerminalCompleted: 2,
		Samples: []H3ResourceSample{
			{Turn: 0, RSSBytes: 100, FDs: 5, PersistenceBytes: 10},
			{Turn: 1, RSSBytes: 100, FDs: 5, PersistenceBytes: 20, LatencyMS: 1},
			{Turn: 2, RSSBytes: 100, FDs: 5, PersistenceBytes: 30, LatencyMS: 1},
		},
	}
	evidence.EvidenceDigest = digestH3Endurance(evidence)
	if err := writePrivateJSON(path, evidence); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadH3EnduranceEvidence(path, false); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadH3EnduranceEvidence(path, true); err == nil {
		t.Fatal("development Endurance evidence was accepted as formal")
	}
}

func TestH3FixtureHasBoundedRepeatedPlainCompletion(t *testing.T) {
	root := t.TempDir()
	if err := writeH3Fixture(root, 3, "say hello"); err != nil {
		t.Fatal(err)
	}
	var config struct {
		Streams []string `json:"streams"`
	}
	raw, err := os.ReadFile(filepath.Join(root, "fixture.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	stream, err := os.ReadFile(filepath.Join(root, "stream.sse"))
	if err != nil {
		t.Fatal(err)
	}
	if len(config.Streams) != 3 ||
		strings.Contains(string(stream), `"tool_calls"`) ||
		!strings.Contains(string(stream), `"content":"hello"`) ||
		!strings.Contains(string(stream), `"finish_reason":"stop"`) ||
		!strings.Contains(string(stream), "{{request_input_tokens}}") {
		t.Fatalf("H3 fixture = %+v %s", config, stream)
	}
}

func TestH3DevelopmentEnduranceUsesPlannedTurnDenominator(t *testing.T) {
	policy := testH3EndurancePolicy()
	evidence := H3EnduranceEvidence{
		ConfiguredDuration: 14_400, EffectiveDurationMS: 60_000,
		TurnIntervalMS: 1_000, DevelopmentOverride: true,
		TurnsScheduled: 2, TurnsCompleted: 2, TerminalCompleted: 2,
	}
	for turn := 0; turn <= 2; turn++ {
		evidence.Samples = append(evidence.Samples, H3ResourceSample{
			Turn: turn, RSSBytes: 100, FDs: 5,
			PersistenceBytes: int64(turn * 10), LatencyMS: 1,
		})
	}
	evaluateH3Endurance(&evidence, policy)
	if evidence.Status == "passed" {
		t.Fatal("H3 Endurance lowered the denominator to scheduled Turns")
	}
}

func TestH3PromptSettlesErrorResponseWithoutTerminal(t *testing.T) {
	host := &h3ACPProcess{
		stdin:     &h3TestWriteCloser{},
		frames:    make(chan h3RPCFrame, 1),
		readErr:   make(chan error, 1),
		pending:   make(map[string]h3RPCFrame),
		terminals: make(map[protocol.TurnID]int),
	}
	var frame h3RPCFrame
	if err := json.Unmarshal([]byte(
		`{"jsonrpc":"2.0","id":"prompt","error":{"code":-32000,"message":"failed"}}`,
	), &frame); err != nil {
		t.Fatal(err)
	}
	host.frames <- frame
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	if _, err := host.prompt(ctx, "prompt", "session", "say hello"); err == nil {
		t.Fatal("H3 prompt accepted an ACP error response")
	} else if ctx.Err() != nil {
		t.Fatal("H3 prompt waited for a terminal after the ACP error response")
	}
}

func validateH3SchemaAsset(
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

func testH3EndurancePolicy() H3EnduranceSpec {
	return H3EnduranceSpec{
		DurationSeconds:     14_400,
		TurnIntervalSeconds: 30, TurnTimeoutSeconds: 20,
		WarmupTurns: 1, MinCompletedTurns: 400, Prompt: "say hello",
		MaxRSSSlopeBytesPerTurn:             100,
		MaxRSSGrowthBytes:                   1000,
		MaxFDSlopeMilliPerTurn:              10,
		MaxFDGrowth:                         1,
		MaxPersistenceSlopeBytesPerTurn:     1000,
		MaxLatencySlopeMilliMSPerTurn:       2000,
		MaxP95LatencyMS:                     1000,
		MaxLateEarlyLatencyRatioBasisPoints: 20_000,
	}
}
