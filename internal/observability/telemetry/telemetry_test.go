package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestJSONLoggerRedactsCredentialCorpus(t *testing.T) {
	secrets := []string{
		"literal-super-secret",
		"sk-1234567890abcdef",
		"password-value",
	}
	var output bytes.Buffer
	logger := NewJSONLogger(&output, slog.LevelDebug, NewRedactor(secrets...))

	logger.Error(
		"request failed with literal-super-secret",
		"authorization", "Bearer opaque-token-value",
		"nested", slog.GroupValue(
			slog.String("api_key", "sk-1234567890abcdef"),
			slog.Any("payload", map[string]any{"password": "password=password-value"}),
		),
	)

	text := output.String()
	for _, secret := range append(secrets, "opaque-token-value") {
		if strings.Contains(text, secret) {
			t.Fatalf("JSON log contains secret %q: %s", secret, text)
		}
	}
	if !strings.Contains(text, redacted) {
		t.Fatalf("JSON log does not contain redaction marker: %s", text)
	}
	if !strings.HasPrefix(text, "{") || !strings.HasSuffix(text, "}\n") {
		t.Fatalf("logger output is not NDJSON: %q", text)
	}
}

func TestRedactingHandlerPropagatesWriterFailure(t *testing.T) {
	want := errors.New("writer failed")
	handler := &redactingHandler{
		next:     slog.NewJSONHandler(failingWriter{err: want}, nil),
		redactor: NewRedactor(),
	}
	record := slog.NewRecord(time.Now(), slog.LevelError, "failure", 0)

	if err := handler.Handle(context.Background(), record); !errors.Is(err, want) {
		t.Fatalf("Handle() error = %v, want %v", err, want)
	}
}

func TestMetricsAreAtomic(t *testing.T) {
	metrics := NewMetrics()
	const workers = 20
	const iterations = 100
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			for range iterations {
				metrics.OperationSubmitted()
				metrics.OperationProcessed()
				metrics.EventPublished()
				metrics.SubscriberDropped()
				metrics.ProviderRequest()
				metrics.AgentTurn()
				metrics.ToolExecution()
				metrics.Error()
				metrics.TurnKernelObserver(true, true)
				metrics.Approval("evaluated", "network.read", "medium", "approval_required", 0)
				metrics.Approval("auto_allowed", "network.read", "medium", "auto_review_allowed", 2*time.Millisecond)
				metrics.Approval("human_required", "process.mutating", "high", "approval_required", 3*time.Millisecond)
				metrics.Approval("denied", "external.mutation", "critical", "permission_denied", 0)
				metrics.Approval("grant_hit", "network.read", "medium", "approval_required", 0)
				metrics.Approval("waited", "process.mutating", "high", "approval_required", 4*time.Millisecond)
			}
		}()
	}
	group.Wait()

	got := metrics.Snapshot()
	want := uint64(workers * iterations)
	if got.OperationsSubmitted != want ||
		got.OperationsProcessed != want ||
		got.EventsPublished != want ||
		got.SubscribersDropped != want ||
		got.ProviderRequests != want ||
		got.AgentTurns != want ||
		got.ToolExecutions != want ||
		got.Errors != want ||
		got.TurnKernelTransitions != want ||
		got.TurnKernelDrifts != want ||
		got.TurnKernelDigestErrors != want ||
		got.ApprovalEvaluatedTotal != want ||
		got.ApprovalAutoAllowedTotal != want ||
		got.ApprovalHumanRequiredTotal != want ||
		got.ApprovalDeniedTotal != want ||
		got.ApprovalGrantHitTotal != want ||
		got.ApprovalReviewerLatencyMS != want*5 ||
		got.ApprovalWaitLatencyMS != want*4 {
		t.Fatalf("metric snapshot = %+v, want every counter %d", got, want)
	}
	if got.HeapAllocBytes == 0 ||
		got.HeapInuseBytes == 0 ||
		got.HeapObjects == 0 ||
		got.ProcessSysBytes == 0 {
		t.Fatalf("runtime memory snapshot = %+v", got)
	}
}

func TestTurnKernelObserverMetricsSeparateHealthyTransitions(t *testing.T) {
	metrics := NewMetrics()
	metrics.TurnKernelObserver(false, false)
	metrics.TurnKernelObserver(true, false)
	metrics.TurnKernelObserver(true, true)

	snapshot := metrics.Snapshot()
	if snapshot.TurnKernelTransitions != 3 ||
		snapshot.TurnKernelDrifts != 2 ||
		snapshot.TurnKernelDigestErrors != 1 {
		t.Fatalf("turn kernel metrics = %+v", snapshot)
	}
}

func TestApprovalMetricsNeverStoreInvocationData(t *testing.T) {
	metrics := NewMetrics()
	metrics.Approval(
		"human_required", "network.read command=secret", "medium path=/private",
		"approval_required", 0,
	)
	encoded, err := json.Marshal(metrics.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"command", "path", "arguments", "prompt", "resource"} {
		if bytes.Contains(encoded, []byte(forbidden)) {
			t.Fatalf("approval metrics contain forbidden data: %s", encoded)
		}
	}
}

func TestAgentRolloutMetricsComeFromTerminalFacts(t *testing.T) {
	metrics := NewMetrics()
	started := time.Date(2026, 8, 13, 0, 0, 0, 0, time.UTC)
	metrics.ObserveAgentEvent(&protocol.AgentSpawnedData{AgentID: "agent-1"}, started)
	metrics.ObserveAgentEvent(
		&protocol.AgentStatusData{
			AgentID: "agent-1", Status: "completed",
			Detail: []byte(`{"result":{"usage":{"cost_microunits":42,"cost_known":true}}}`),
		},
		started.Add(1250*time.Millisecond),
	)
	metrics.ObserveAgentEvent(&protocol.AgentSpawnedData{AgentID: "agent-2"}, started)
	metrics.ObserveAgentEvent(
		&protocol.AgentStatusData{AgentID: "agent-2", Status: "interrupted"},
		started.Add(time.Second),
	)
	for _, status := range []string{"applied", "failed", "discarded"} {
		metrics.ObserveAgentEvent(
			&protocol.AgentIntegrationData{Status: status},
			started,
		)
	}

	snapshot := metrics.Snapshot()
	if snapshot.AgentSpawns != 2 ||
		snapshot.AgentCompleted != 1 ||
		snapshot.AgentInterrupted != 1 ||
		snapshot.AgentCompletionLatencyMS != 2250 ||
		snapshot.AgentCompletionLatencySamples != 2 ||
		snapshot.AgentIntegrationsApplied != 1 ||
		snapshot.AgentIntegrationsFailed != 1 ||
		snapshot.AgentIntegrationsDiscarded != 1 ||
		snapshot.AgentCostMicrounits != 42 ||
		snapshot.AgentCostKnownSamples != 1 ||
		snapshot.AgentCostUnknownSamples != 1 {
		t.Fatalf("agent rollout metrics = %+v", snapshot)
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

var _ io.Writer = failingWriter{}
