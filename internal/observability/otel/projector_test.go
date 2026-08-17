package otel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestObservationProjectionPreservesW3CIdentity(t *testing.T) {
	service, err := New(t.Context(), Options{Protocol: ExportMemory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	start := otelEnvelope(observation.KindModelRequestSent, 1)
	end := otelEnvelope(observation.KindModelResponseCompleted, 2)
	end.RecordedAt = start.RecordedAt.Add(1250 * time.Millisecond)
	service.Project(start)
	service.Project(end)
	if err := service.ForceFlush(t.Context()); err != nil {
		t.Fatal(err)
	}
	spans := service.MemorySpans()
	if len(spans) != 1 ||
		spans[0].TraceID != start.Trace.TraceID ||
		spans[0].SpanID != start.Trace.SpanID ||
		spans[0].ParentSpan != start.Trace.ParentSpan ||
		spans[0].End.Sub(spans[0].Start) != 1250*time.Millisecond {
		t.Fatalf("spans = %+v", spans)
	}
	points, dropped := service.MetricSnapshot()
	if dropped != 0 || !hasMetric(
		points,
		"codehelper.provider.request.duration",
	) {
		t.Fatalf("metrics=%+v dropped=%d", points, dropped)
	}
}

func TestExporterFailureDoesNotEscapeProjection(t *testing.T) {
	service, err := New(t.Context(), Options{
		Protocol:      ExportMemory,
		TraceExporter: failingExporter{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	service.Project(otelEnvelope(observation.KindModelRequestSent, 1))
	service.Project(otelEnvelope(observation.KindModelRequestFailed, 2))
	_ = service.ForceFlush(t.Context())
	if health := service.Health(); health.Projected != 2 ||
		health.Failures == 0 {
		t.Fatalf("health = %+v", health)
	}
}

func TestMetricCardinalityPolicyRejectsIdentityAndPaths(t *testing.T) {
	for _, labels := range []Labels{
		{"turn_id": "turn-1"},
		{"status": "/workspace/private"},
		{"status": strings.Repeat("x", 65)},
	} {
		if err := ValidateLabels(labels); err == nil {
			t.Fatalf("labels accepted: %+v", labels)
		}
	}
	if err := ValidateLabels(Labels{
		"status":            "completed",
		"observation_class": "provider",
	}); err != nil {
		t.Fatal(err)
	}
	registry := NewMetricRegistry(2)
	if !registry.Add("metric", Labels{"status": "one"}, 1) ||
		!registry.Add("metric", Labels{"status": "two"}, 1) ||
		registry.Add("metric", Labels{"status": "three"}, 1) {
		t.Fatal("metric series bound was not enforced")
	}
	if _, dropped := registry.Snapshot(); dropped != 1 {
		t.Fatalf("dropped series = %d", dropped)
	}
}

func TestTerminalCommitMetricUsesPreparedObservation(t *testing.T) {
	service, err := New(t.Context(), Options{Protocol: ExportMemory})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	start := otelEnvelope(observation.KindTurnStarted, 1)
	prepared := otelEnvelope(observation.KindTurnTerminalPrepared, 2)
	committed := otelEnvelope(observation.KindTurnTerminalCommitted, 3)
	prepared.RecordedAt = start.RecordedAt.Add(time.Second)
	committed.RecordedAt = prepared.RecordedAt.Add(250 * time.Millisecond)
	service.Project(start)
	service.Project(prepared)
	service.Project(committed)
	if err := service.ForceFlush(t.Context()); err != nil {
		t.Fatal(err)
	}
	points, _ := service.MetricSnapshot()
	point, ok := metricNamed(
		points,
		"codehelper.terminal.commit.duration",
	)
	if !ok || point.Count != 1 || point.Sum != 250 {
		t.Fatalf("terminal metric = %+v, all=%+v", point, points)
	}
}

func TestShutdownDrainsEveryAcceptedProjection(t *testing.T) {
	service, err := New(t.Context(), Options{
		Protocol:      ExportMemory,
		QueueCapacity: 256,
	})
	if err != nil {
		t.Fatal(err)
	}
	for sequence := uint64(1); sequence <= 128; sequence++ {
		service.Project(otelEnvelope(observation.KindRuntimeReady, sequence))
	}
	if err := service.Shutdown(t.Context()); err != nil {
		t.Fatal(err)
	}
	health := service.Health()
	if health.Submitted != 128 ||
		health.Projected != health.Submitted ||
		health.QueueDepth != 0 {
		t.Fatalf("health = %+v", health)
	}
}

func BenchmarkSO5OTELProjection(b *testing.B) {
	service, err := New(b.Context(), Options{
		Protocol:      ExportMemory,
		QueueCapacity: max(4096, b.N),
	})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		_ = service.Shutdown(context.Background())
	})
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		service.Project(
			otelEnvelope(observation.KindRuntimeReady, uint64(index+1)),
		)
	}
	if err := service.ForceFlush(b.Context()); err != nil {
		b.Fatal(err)
	}
}

func otelEnvelope(
	kind observation.Kind,
	sequence uint64,
) observation.Envelope {
	return observation.Envelope{
		SchemaVersion: observation.SchemaVersion,
		ID: observation.ObservationID(
			"obs_" + strings.Repeat(
				string("0123456789abcdef"[sequence%16]),
				32,
			),
		),
		Kind: kind, Sequence: sequence, ObservedSequence: sequence,
		RecordedAt: time.Unix(int64(sequence), 0).UTC(),
		Identity: observation.Identity{
			RuntimeID: "runtime-1",
			TurnID:    protocol.TurnID("turn-1"),
			SampleID:  "sample-1",
		},
		Trace: &observation.TraceContext{
			TraceID:    strings.Repeat("1", 32),
			SpanID:     strings.Repeat("2", 16),
			ParentSpan: strings.Repeat("3", 16),
			TraceFlags: 1,
		},
		Policy: observation.DataPolicy{
			Class:     observation.DataOperational,
			Redaction: observation.RedactionNotRequired,
		},
	}
}

func hasMetric(values []MetricPoint, name string) bool {
	_, ok := metricNamed(values, name)
	return ok
}

func metricNamed(
	values []MetricPoint,
	name string,
) (MetricPoint, bool) {
	for _, value := range values {
		if value.Name == name {
			return value, true
		}
	}
	return MetricPoint{}, false
}

type failingExporter struct{}

func (failingExporter) ExportSpans(
	context.Context,
	[]sdktrace.ReadOnlySpan,
) error {
	return errors.New("export unavailable")
}

func (failingExporter) Shutdown(context.Context) error { return nil }
