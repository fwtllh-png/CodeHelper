package cli_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
	usagestate "github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// TestMetricsReportsWhatTheDatabaseHolds is the T5 acceptance for the detailed
// view: no --file, real tokens, real cost, real phase timings.
func TestMetricsReportsWhatTheDatabaseHolds(t *testing.T) {
	dataDir := seedAccounting(t, true)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"metrics", "--data-dir", dataDir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var payload struct {
		Scope string `json:"scope"`
		Usage struct {
			Turns        uint64  `json:"turns"`
			Calls        uint64  `json:"calls"`
			InputTokens  uint64  `json:"input_tokens"`
			OutputTokens uint64  `json:"output_tokens"`
			CachedTokens uint64  `json:"cached_tokens"`
			CachedShare  float64 `json:"cached_share"`
			CostKnown    bool    `json:"cost_known"`
			Cost         string  `json:"cost"`
		} `json:"usage"`
		Models []struct {
			Model string `json:"model"`
			Cost  string `json:"cost"`
		} `json:"models"`
		Latency struct {
			Recorded bool   `json:"recorded"`
			Turns    uint64 `json:"turns"`
			P50MS    int64  `json:"turn_p50_ms"`
			Phases   []struct {
				Name    string `json:"name"`
				Calls   uint64 `json:"calls"`
				TotalMS int64  `json:"total_ms"`
			} `json:"phases"`
		} `json:"latency"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatalf("%v in %q", err, stdout.String())
	}
	if payload.Scope != "all" {
		t.Fatalf("scope = %q", payload.Scope)
	}
	if payload.Usage.Turns != 2 || payload.Usage.Calls != 2 {
		t.Fatalf("usage = %+v, want one call in each of two turns", payload.Usage)
	}
	if payload.Usage.InputTokens != 160 || payload.Usage.OutputTokens != 50 {
		t.Fatalf("tokens = %+v", payload.Usage)
	}
	if payload.Usage.CachedTokens != 20 || payload.Usage.CachedShare != 0.125 {
		t.Fatalf("cache = %+v, want 20 of 160 input tokens", payload.Usage)
	}
	if payload.Usage.CostKnown {
		t.Fatal("the unpriced call must keep the total a floor")
	}
	if !strings.Contains(payload.Usage.Cost, "unpriced") {
		t.Fatalf("cost = %q, want it to admit the unpriced call", payload.Usage.Cost)
	}
	if len(payload.Models) != 2 {
		t.Fatalf("models = %+v, want one row per model", payload.Models)
	}
	var unknown bool
	for _, model := range payload.Models {
		if model.Cost == "unknown" {
			unknown = true
		}
	}
	if !unknown {
		t.Fatalf("models = %+v, want the unpriced model reported as unknown", payload.Models)
	}
	if !payload.Latency.Recorded || payload.Latency.Turns != 1 || payload.Latency.P50MS != 5000 {
		t.Fatalf("latency = %+v", payload.Latency)
	}
	var phases []string
	for _, phase := range payload.Latency.Phases {
		phases = append(phases, phase.Name)
	}
	if strings.Join(phases, ",") != "turn,model_call,tool" {
		t.Fatalf("phases = %v", phases)
	}
}

func TestScorecardRollsUpWithoutClaimingToBeThin(t *testing.T) {
	dataDir := seedAccounting(t, true)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{"scorecard", "--data-dir", dataDir, "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if _, present := payload["thin"]; present {
		t.Fatalf("scorecard still reports thin: %#v", payload)
	}
	usageSection, ok := payload["usage"].(map[string]any)
	if !ok {
		t.Fatalf("payload = %#v", payload)
	}
	if usageSection["cost_known"] != false {
		t.Fatalf("cost_known = %#v", usageSection["cost_known"])
	}
	stdout.Reset()
	stderr.Reset()
	if code := cli.Run(
		[]string{"scorecard", "--data-dir", dataDir}, &stdout, &stderr,
	); code != 0 {
		t.Fatalf("text code=%d stderr=%q", code, stderr.String())
	}
	text := stdout.String()
	for _, want := range []string{"turns 2", "calls 2", "cost ", "cost_known false", "latency"} {
		if !strings.Contains(text, want) {
			t.Fatalf("scorecard text %q missing %q", text, want)
		}
	}
	if strings.Contains(text, "thin") {
		t.Fatalf("scorecard text still says thin: %q", text)
	}
}

// TestAccountingNarrowedToATurnRefusesToNameAnAmount ensures a turn whose model
// had no price reads as unknown, never as $0.00.
func TestAccountingNarrowedToATurnRefusesToNameAnAmount(t *testing.T) {
	dataDir := seedAccounting(t, true)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"scorecard", "--data-dir", dataDir, "--turn", "turn-2",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	text := stdout.String()
	if !strings.Contains(text, "scope turn:turn-2") || !strings.Contains(text, "cost unknown") {
		t.Fatalf("turn scorecard = %q", text)
	}
	if strings.Contains(text, "$0.00") {
		t.Fatalf("an unpriced turn was priced at zero: %q", text)
	}
	// The priced turn does name an amount, so the two are distinguishable.
	stdout.Reset()
	if code := cli.Run([]string{
		"scorecard", "--data-dir", dataDir, "--turn", "turn-1",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if priced := stdout.String(); !strings.Contains(priced, "cost $0.00015\n") ||
		!strings.Contains(priced, "cost_known true") {
		t.Fatalf("priced turn scorecard = %q", priced)
	}
}

// TestAccountingSaysLatencyWasNotRecorded covers usage without a corresponding
// trace. A row of zeros would incorrectly claim those turns were instant.
func TestAccountingSaysLatencyWasNotRecorded(t *testing.T) {
	dataDir := seedAccounting(t, false)
	for _, command := range []string{"metrics", "scorecard"} {
		var stdout, stderr bytes.Buffer
		if code := cli.Run(
			[]string{command, "--data-dir", dataDir}, &stdout, &stderr,
		); code != 0 {
			t.Fatalf("%s code=%d stderr=%q", command, code, stderr.String())
		}
		text := stdout.String()
		if !strings.Contains(text, "latency: not recorded") {
			t.Fatalf("%s = %q, want it to admit nothing was traced", command, text)
		}
		if strings.Contains(text, "p50=0ms") {
			t.Fatalf("%s reported a zero percentile: %q", command, text)
		}
	}
}

func TestAccountingOnAnEmptyDatabaseSaysNothingWasBilled(t *testing.T) {
	dataDir := t.TempDir()
	var stdout, stderr bytes.Buffer
	if code := cli.Run(
		[]string{"metrics", "--data-dir", dataDir}, &stdout, &stderr,
	); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	text := stdout.String()
	if !strings.Contains(text, "nothing billed") || !strings.Contains(text, "not recorded") {
		t.Fatalf("empty report = %q", text)
	}
}

func TestAccountingCommandsNameBothWaysToGetData(t *testing.T) {
	for _, command := range []string{"metrics", "scorecard"} {
		var stdout, stderr bytes.Buffer
		code := cli.Run([]string{command}, &stdout, &stderr)
		if code != 2 {
			t.Fatalf("%s code=%d, want a usage error", command, code)
		}
		message := stderr.String()
		if !strings.Contains(message, "--data-dir") || !strings.Contains(message, "--file") {
			t.Fatalf("%s error = %q, want both paths named", command, message)
		}
	}
}

func TestAccountingRejectsAnUnreadableWindow(t *testing.T) {
	dataDir := seedAccounting(t, true)
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"metrics", "--data-dir", dataDir, "--since", "yesterday",
	}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "--since") {
		t.Fatalf("error = %q", stderr.String())
	}
}

func TestAccountingWindowAcceptsADuration(t *testing.T) {
	dataDir := seedAccounting(t, true)
	var stdout, stderr bytes.Buffer
	if code := cli.Run([]string{
		"scorecard", "--data-dir", dataDir, "--since", "1h", "--json",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var recent map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &recent); err != nil {
		t.Fatal(err)
	}
	if section := recent["usage"].(map[string]any); section["calls"].(float64) != 2 {
		t.Fatalf("last hour = %#v", section)
	}
	stdout.Reset()
	if code := cli.Run([]string{
		"scorecard", "--data-dir", dataDir, "--until", "1h", "--json",
	}, &stdout, &stderr); code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var old map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &old); err != nil {
		t.Fatal(err)
	}
	if section := old["usage"].(map[string]any); section["calls"].(float64) != 0 {
		t.Fatalf("before the last hour = %#v", section)
	}
}

// seedAccounting builds a database with two turns: turn-1 billed against a priced
// model and traced, turn-2 billed against a model with no price. withTrace leaves
// the spans out so a caller can exercise the untraced case.
func seedAccounting(t *testing.T, withTrace bool) string {
	t.Helper()
	dataDir := t.TempDir()
	ctx := t.Context()
	store, err := state.Open(ctx, state.Options{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := store.Close(ctx); err != nil {
			t.Fatal(err)
		}
	}()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, statement := range []string{
		`INSERT INTO workspaces(id, root_path, created_at, updated_at)
		 VALUES ('workspace-1', '/workspace', ?, ?)`,
		`INSERT INTO sessions(id, workspace_id, status, created_at, updated_at)
		 VALUES ('session-1', 'workspace-1', 'open', ?, ?)`,
		`INSERT INTO threads(id, session_id, status, created_at, updated_at)
		 VALUES ('thread-1', 'session-1', 'open', ?, ?)`,
		`INSERT INTO turns(id, thread_id, ordinal, status, created_at, updated_at)
		 VALUES ('turn-1', 'thread-1', 0, 'completed', ?, ?)`,
		`INSERT INTO turns(id, thread_id, ordinal, status, created_at, updated_at)
		 VALUES ('turn-2', 'thread-1', 1, 'completed', ?, ?)`,
	} {
		if _, err := store.SQLite().DB().ExecContext(ctx, statement, now, now); err != nil {
			t.Fatal(err)
		}
	}
	usageRepository := usagestate.NewSQLiteRepository(store.SQLite())
	events := []struct {
		sequence protocol.Cursor
		turn     protocol.TurnID
		data     protocol.EventData
	}{
		{1, "turn-1", &protocol.TurnStartedData{Provider: "anthropic", Model: "claude"}},
		{2, "turn-1", &protocol.UsageData{
			Sample: 1, Provider: "anthropic", Model: "claude",
			InputTokens: 100, OutputTokens: 40, CachedTokens: 20,
			CostMicrounits: 150, CostKnown: true,
		}},
		{3, "turn-2", &protocol.TurnStartedData{Provider: "local", Model: "mystery"}},
		{4, "turn-2", &protocol.UsageData{
			Sample: 1, Provider: "local", Model: "mystery",
			InputTokens: 60, OutputTokens: 10,
		}},
	}
	for _, seed := range events {
		event, err := protocol.NewEvent(protocol.EventMeta{
			Sequence: seed.sequence, OperationID: "operation-1",
			ThreadID: "thread-1", TurnID: seed.turn, ItemID: "item-1",
		}, seed.data)
		if err != nil {
			t.Fatal(err)
		}
		if err := usageRepository.Project(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	if !withTrace {
		return dataDir
	}
	base := time.Now().UTC().Add(-time.Minute)
	envelope, err := json.Marshal(map[string]any{
		"measurement": map[string]any{
			"version":   1,
			"frozen_at": base.Add(5 * time.Second),
			"latency": map[string]any{
				"turn": map[string]any{
					"recorded": true, "milliseconds": 5000,
				},
				"provider": map[string]any{
					"recorded": true, "milliseconds": 1200,
				},
				"tool": map[string]any{
					"recorded": true, "milliseconds": 2000,
				},
			},
			"usage": map[string]any{"frozen": true},
		},
		"frozen_state": map[string]any{
			"terminal": map[string]any{"kind": "completed"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SQLite().DB().ExecContext(
		ctx,
		`INSERT INTO turn_terminal_envelopes(
			turn_id, effect_id, digest, envelope_json, marker_json
		) VALUES (?, 'terminal:turn-1', 'digest', ?, '{}')`,
		"turn-1",
		string(envelope),
	); err != nil {
		t.Fatal(err)
	}
	return dataDir
}
