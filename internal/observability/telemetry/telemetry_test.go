package telemetry

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
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
		got.Errors != want {
		t.Fatalf("metric snapshot = %+v, want every counter %d", got, want)
	}
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	return 0, w.err
}

var _ io.Writer = failingWriter{}
