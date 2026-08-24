package otel

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.37.0"
)

type ExportProtocol string

const (
	ExportMemory ExportProtocol = "memory"
	ExportHTTP   ExportProtocol = "http/protobuf"
	ExportGRPC   ExportProtocol = "grpc"
	ExportOff    ExportProtocol = "off"
)

type Options struct {
	Protocol      ExportProtocol
	QueueCapacity int
	ServiceName   string
	TraceExporter sdktrace.SpanExporter
	MetricReader  sdkmetric.Reader
}

type MemorySpan struct {
	Name       string
	TraceID    string
	SpanID     string
	ParentSpan string
	Start      time.Time
	End        time.Time
	Attributes map[string]string
}

type MemoryExporter struct {
	mu    sync.Mutex
	spans []MemorySpan
}

func (e *MemoryExporter) ExportSpans(
	_ context.Context,
	values []sdktrace.ReadOnlySpan,
) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, value := range values {
		span := MemorySpan{
			Name:       value.Name(),
			TraceID:    value.SpanContext().TraceID().String(),
			SpanID:     value.SpanContext().SpanID().String(),
			ParentSpan: value.Parent().SpanID().String(),
			Start:      value.StartTime(), End: value.EndTime(),
			Attributes: make(map[string]string),
		}
		for _, item := range value.Attributes() {
			span.Attributes[string(item.Key)] = item.Value.String()
		}
		e.spans = append(e.spans, span)
	}
	return nil
}

func (*MemoryExporter) Shutdown(context.Context) error { return nil }

func (e *MemoryExporter) Snapshot() []MemorySpan {
	if e == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	result := make([]MemorySpan, len(e.spans))
	for index, span := range e.spans {
		result[index] = span
		result[index].Attributes = cloneStringMap(span.Attributes)
	}
	return result
}

func defaultOptions(options Options) Options {
	if options.Protocol == "" {
		options.Protocol = ExportOff
	}
	if options.QueueCapacity <= 0 {
		options.QueueCapacity = 4096
	}
	if options.ServiceName == "" {
		options.ServiceName = "codehelper"
	}
	return options
}

func environmentOptions() Options {
	protocol := ExportProtocol(strings.TrimSpace(
		os.Getenv("CODEHELPER_OTEL_EXPORTER"),
	))
	if protocol == "" {
		if strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT")) != "" {
			protocol = ExportProtocol(strings.TrimSpace(
				os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"),
			))
			if protocol == "" {
				protocol = ExportGRPC
			}
		} else {
			protocol = ExportOff
		}
	}
	switch strings.ToLower(string(protocol)) {
	case "none", "disabled":
		protocol = ExportOff
	case "http":
		protocol = ExportHTTP
	}
	return defaultOptions(Options{Protocol: protocol})
}

func newResource(
	ctx context.Context,
	serviceName string,
) (*resource.Resource, error) {
	return resource.New(
		ctx,
		resource.WithAttributes(
			semconv.ServiceName(serviceName),
			attribute.String("codehelper.observation.schema", "v1"),
		),
	)
}

func closeProviders(
	ctx context.Context,
	traceProvider *sdktrace.TracerProvider,
	meterProvider *sdkmetric.MeterProvider,
) error {
	var traceErr, metricErr error
	if traceProvider != nil {
		traceErr = traceProvider.Shutdown(ctx)
	}
	if meterProvider != nil {
		metricErr = meterProvider.Shutdown(ctx)
	}
	return errors.Join(traceErr, metricErr)
}

func cloneStringMap(value map[string]string) map[string]string {
	if len(value) == 0 {
		return nil
	}
	result := make(map[string]string, len(value))
	for key, item := range value {
		result[key] = item
	}
	return result
}
