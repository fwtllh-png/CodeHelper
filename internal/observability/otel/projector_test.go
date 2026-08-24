package otel

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func TestObservationProjectionPreservesW3CIdentity(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	service, err := New(t.Context(), Options{
		Protocol: ExportMemory, MetricReader: reader,
	})
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
	metrics := collectMetrics(t, reader)
	if !hasMetric(metrics, "codehelper.provider.request.duration") {
		t.Fatalf("metrics=%+v", metrics)
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

func TestMetricLabelPolicyRejectsIdentityAndPaths(t *testing.T) {
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
}

func TestTerminalCommitMetricUsesPreparedObservation(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	service, err := New(t.Context(), Options{
		Protocol: ExportMemory, MetricReader: reader,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	start := otelEnvelope(observation.KindTurnStarted, 1)
	prepared := otelEnvelope(observation.KindTurnTerminalPrepared, 2)
	committed := otelEnvelope(observation.KindTurnTerminalCommitted, 3)
	summary, err := observation.EncodeTerminalSummary(
		"measurement-1",
		observation.TerminalOutcome{
			Status: observation.TerminalFailed,
			Code:   string(protocol.CodeResourceExhausted),
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Summary, committed.Summary = summary, summary
	prepared.RecordedAt = start.RecordedAt.Add(time.Second)
	committed.RecordedAt = prepared.RecordedAt.Add(250 * time.Millisecond)
	service.Project(start)
	service.Project(prepared)
	service.Project(committed)
	if err := service.ForceFlush(t.Context()); err != nil {
		t.Fatal(err)
	}
	metrics := collectMetrics(t, reader)
	duration, ok := floatHistogramPoint(
		metrics,
		"codehelper.terminal.commit.duration",
	)
	if !ok || duration.Count != 1 || duration.Sum != 250 {
		t.Fatalf("terminal metric = %+v, all=%+v", duration, metrics)
	}
	outcome, ok := intSumPoint(metrics, "codehelper.turn.terminal.count")
	if !ok || outcome.Value != 1 ||
		attributeValue(outcome.Attributes, "status") != "failed" ||
		attributeValue(outcome.Attributes, "error_category") != "resource_exhausted" {
		t.Fatalf("terminal outcome metric = %+v, all=%+v", outcome, metrics)
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

func collectMetrics(t *testing.T, reader *sdkmetric.ManualReader) []metricdata.Metrics {
	t.Helper()
	var resource metricdata.ResourceMetrics
	if err := reader.Collect(t.Context(), &resource); err != nil {
		t.Fatal(err)
	}
	var values []metricdata.Metrics
	for _, scope := range resource.ScopeMetrics {
		values = append(values, scope.Metrics...)
	}
	return values
}

func hasMetric(values []metricdata.Metrics, name string) bool {
	_, ok := metricNamed(values, name)
	return ok
}

func metricNamed(
	values []metricdata.Metrics,
	name string,
) (metricdata.Metrics, bool) {
	for _, value := range values {
		if value.Name == name {
			return value, true
		}
	}
	return metricdata.Metrics{}, false
}

func floatHistogramPoint(
	values []metricdata.Metrics,
	name string,
) (metricdata.HistogramDataPoint[float64], bool) {
	value, ok := metricNamed(values, name)
	if !ok {
		return metricdata.HistogramDataPoint[float64]{}, false
	}
	histogram, ok := value.Data.(metricdata.Histogram[float64])
	if !ok || len(histogram.DataPoints) != 1 {
		return metricdata.HistogramDataPoint[float64]{}, false
	}
	return histogram.DataPoints[0], true
}

func intSumPoint(
	values []metricdata.Metrics,
	name string,
) (metricdata.DataPoint[int64], bool) {
	value, ok := metricNamed(values, name)
	if !ok {
		return metricdata.DataPoint[int64]{}, false
	}
	sum, ok := value.Data.(metricdata.Sum[int64])
	if !ok || len(sum.DataPoints) != 1 {
		return metricdata.DataPoint[int64]{}, false
	}
	return sum.DataPoints[0], true
}

func attributeValue(values attribute.Set, name string) string {
	value, ok := values.Value(attribute.Key(name))
	if !ok {
		return ""
	}
	return value.AsString()
}

type failingExporter struct{}

func (failingExporter) ExportSpans(
	context.Context,
	[]sdktrace.ReadOnlySpan,
) error {
	return errors.New("export unavailable")
}

func (failingExporter) Shutdown(context.Context) error { return nil }
