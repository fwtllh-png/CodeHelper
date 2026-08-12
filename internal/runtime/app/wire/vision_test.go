package wire

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/config"
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

	// 11 + 30 + 40 + 50 for the turn's own samples, 1500 for the image. A
	// receipt that omitted the image would report 131 input tokens for a turn
	// that bought 1631.
	if receipt.InputTokens != 1631 {
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
	// The rollup is what `codehelper usage` and the TUI cost panel read. The image
	// has to be in there too, or the turn's own report and the ledger disagree.
	rollup, err := usage.NewSQLiteRepository(store.SQLite()).
		QueryRollup(t.Context(), usage.Query{TurnID: "turn"})
	if err != nil {
		t.Fatal(err)
	}
	if rollup.InputTokens != 1631 {
		t.Fatalf("usage rollup = %+v, want the image's 1500 input tokens counted", rollup)
	}

	// The span table is where "how long did the image take" is answered, and the
	// vision call has to be in it as a model call rather than as an opaque tool.
	// The write follows the terminal event, so this polls rather than assuming
	// the two have already been ordered.
	repository := trace.NewSQLiteRepository(store.SQLite())
	var spans []trace.Record
	visionSpans := 0
	for attempt := 0; attempt < 100 && visionSpans == 0; attempt++ {
		if attempt > 0 {
			time.Sleep(50 * time.Millisecond)
		}
		spans, err = repository.QueryByTurn(t.Context(), "turn")
		if err != nil {
			t.Fatal(err)
		}
		visionSpans = 0
		for _, span := range spans {
			if span.Name == trace.NameModelCall &&
				span.Attributes["purpose"] == string(model.PurposeVision) {
				visionSpans++
			}
		}
	}
	if visionSpans != 1 {
		t.Fatalf("vision model call spans = %d, want 1: %+v", visionSpans, spans)
	}
}
