package otel

import (
	"context"
	"fmt"

	metricgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	metrichttp "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	tracegrpc "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	tracehttp "go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func remoteExporters(
	ctx context.Context,
	protocol ExportProtocol,
) (
	sdktrace.SpanExporter,
	sdkmetric.Reader,
	error,
) {
	switch protocol {
	case ExportHTTP:
		traceExporter, err := tracehttp.New(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("create OTLP HTTP trace exporter: %w", err)
		}
		metricExporter, err := metrichttp.New(ctx)
		if err != nil {
			_ = traceExporter.Shutdown(ctx)
			return nil, nil, fmt.Errorf("create OTLP HTTP metric exporter: %w", err)
		}
		return traceExporter, sdkmetric.NewPeriodicReader(metricExporter), nil
	case ExportGRPC:
		traceExporter, err := tracegrpc.New(ctx)
		if err != nil {
			return nil, nil, fmt.Errorf("create OTLP gRPC trace exporter: %w", err)
		}
		metricExporter, err := metricgrpc.New(ctx)
		if err != nil {
			_ = traceExporter.Shutdown(ctx)
			return nil, nil, fmt.Errorf("create OTLP gRPC metric exporter: %w", err)
		}
		return traceExporter, sdkmetric.NewPeriodicReader(metricExporter), nil
	default:
		return nil, nil, fmt.Errorf("unsupported OTEL export protocol %q", protocol)
	}
}
