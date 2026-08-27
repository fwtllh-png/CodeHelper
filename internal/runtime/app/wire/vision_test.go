package wire

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/config"
	observationjournal "github.com/fwtllh-png/CodeHelper/internal/observability/journal"
	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	"github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	apppersistence "github.com/fwtllh-png/CodeHelper/internal/runtime/app/persistence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// TestAVisionCallIsOnTheBooksEndToEnd is the T2 acceptance, whole-session: the
// model asks for image_analyze, the tool samples the vision route, and the
// tokens, the cost and the span all exist. Before this shard the same turn made
// the same HTTP request and left no trace of it anywhere.
func TestAVisionCallIsOnTheBooksEndToEnd(t *testing.T) {
	workspace := t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "shot.png"), []byte("PNGBYTES"), 0o600); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(workspace, "codehelper.toml")
	// The fixture answers for one model name only, so the vision slot names the
	// same model. What distinguishes the call is its purpose, which is the thing
	// under test: the account has to know a vision sample from an act sample even
	// when both models are the same.
	if err := os.WriteFile(configPath, []byte(`
[route.vision]
provider = "fixture"
model = "fixture-model"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := state.Open(t.Context(), state.Options{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseAll(context.Background()) })
	if err := apppersistence.EnsureThread(t.Context(), store, "thread", "session-vision", workspace); err != nil {
		t.Fatal(err)
	}
	tools := true
	session, err := NewExec(context.Background(), ExecOptions{
		ConfigPath:      configPath,
		FixturePath:     subagentFixture(t, "vision"),
		Permission:      "bypass",
		PersistentStore: store,
		ConfigOverrides: config.Overrides{Workspace: &workspace, Tools: &tools},
	})
	if err != nil {
		t.Fatalf("NewExec: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = session.Close(ctx)
	})

	events, err := session.Runtime.Events(t.Context(), 0)
	if err != nil {
		t.Fatal(err)
	}
	start, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: "thread", TurnID: "turn", ItemID: "prompt",
		Prompt: "look at the screenshot",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := session.Runtime.Submit(t.Context(), start); err != nil {
		t.Fatal(err)
	}

	var receipt *protocol.ExecutionReceiptData
	var usageEvents []*protocol.UsageData
	completed := false
	deadline := time.After(30 * time.Second)
	// The spans are written after the terminal event, so the read below has to
	// wait for the turn to finish rather than for its receipt.
	for receipt == nil || !completed {
		select {
		case event := <-events:
			switch data := event.Data.(type) {
			case *protocol.UsageData:
				usageEvents = append(usageEvents, data)
			case *protocol.ExecutionReceiptData:
				receipt = data
			case *protocol.TurnCompletedData:
				completed = true
			case *protocol.TurnFailedData:
				t.Fatalf("the turn failed: %+v", data)
			}
		case <-deadline:
			t.Fatal("the turn did not finish")
		}
	}

	// 11 for image_analyze plus 30 for the one-step turn_complete declaration,
	// plus 1500 for the image. The declaration summary is the final output, so
	// there is no separate final-answer sample.
	if receipt.InputTokens != 1541 {
		t.Fatalf("receipt input tokens = %d, want the image included", receipt.InputTokens)
	}
	var vision *protocol.ReceiptRoute
	for index, route := range receipt.Routes {
		if route.Purpose == string(model.PurposeVision) {
			vision = &receipt.Routes[index]
		}
	}
	if vision == nil {
		t.Fatalf("receipt routes = %+v, want the vision purpose named", receipt.Routes)
	}
	if len(usageEvents) == 0 {
		t.Fatal("no usage events reached the host")
	}
	// The rollup is what the Web usage panel reads. The image
	// has to be in there too, or the turn's own report and the ledger disagree.
	rollup, err := usage.NewSQLiteRepository(store.SQLite()).
		QueryRollup(t.Context(), usage.Query{TurnID: "turn"})
	if err != nil {
		t.Fatal(err)
	}
	if rollup.InputTokens != 1541 {
		t.Fatalf("usage rollup = %+v, want the image's 1500 input tokens counted", rollup)
	}

	// SO4 projects the frozen provider aggregate instead of rewriting the
	// Engine's mutable span tree after the Receipt was frozen.
	repository := trace.NewSQLiteRepository(store.SQLite())
	var spans []trace.Record
	modelSpans := 0
	for attempt := 0; attempt < 100 && modelSpans == 0; attempt++ {
		if attempt > 0 {
			time.Sleep(50 * time.Millisecond)
		}
		spans, err = repository.QueryByTurn(t.Context(), "turn")
		if err != nil {
			t.Fatal(err)
		}
		modelSpans = 0
		for _, span := range spans {
			if span.Name == trace.NameModelCall &&
				span.Attributes["aggregate"] == true &&
				span.Attributes["measurement_digest"] ==
					receipt.MeasurementDigest {
				modelSpans++
			}
		}
	}
	if modelSpans != 1 {
		t.Fatalf("measurement model spans = %d, want 1: %+v", modelSpans, spans)
	}

	if err := session.observability.router.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	observations, err := observationjournal.ReadAll(filepath.Join(
		store.Root(), "observability", "journal-v1",
	))
	if err != nil {
		t.Fatal(err)
	}
	kinds := make(map[observation.Kind]int)
	for _, record := range observations {
		kinds[record.Envelope.Kind]++
		if record.Envelope.Kind ==
			observation.KindTurnTerminalPrepared ||
			record.Envelope.Kind ==
				observation.KindTurnTerminalCommitted {
			var summary struct {
				MeasurementDigest string `json:"measurement_digest"`
			}
			if err := json.Unmarshal(
				record.Envelope.Summary,
				&summary,
			); err != nil ||
				summary.MeasurementDigest !=
					receipt.MeasurementDigest {
				t.Fatalf(
					"terminal observation summary=%s error=%v",
					record.Envelope.Summary,
					err,
				)
			}
		}
	}
	for _, kind := range []observation.Kind{
		observation.KindTurnStarted,
		observation.KindTurnTransitionCommitted,
		observation.KindModelRequestSent,
		observation.KindModelResponseCompleted,
		observation.KindToolStarted,
		observation.KindToolFinished,
		observation.KindTurnTerminalPrepared,
		observation.KindTurnTerminalCommitted,
	} {
		if kinds[kind] == 0 {
			t.Fatalf("observation %q is missing: %+v", kind, kinds)
		}
	}
	exported, err := session.Runtime.TraceExport.Export(
		t.Context(),
		trace.ExportRequest{
			SessionID: "session-vision", ProducerVersion: "test",
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if exported.Manifest.EventCount == 0 ||
		exported.Manifest.UsageCount == 0 ||
		exported.Manifest.ThroughSequence == 0 ||
		!strings.HasPrefix(
			string(exported.Content),
			`{"record_type":"manifest","format":"codehelper.observation-jsonl"`,
		) ||
		strings.Contains(string(exported.Content), workspace) {
		t.Fatalf(
			"trace export manifest=%+v content=%s",
			exported.Manifest,
			exported.Content,
		)
	}
}
