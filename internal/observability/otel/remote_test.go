package otel

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	collectormetric "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	collectortrace "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
)

func TestOTLPHTTPExporterReachesLocalCollector(t *testing.T) {
	var traces atomic.Uint64
	server := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		defer request.Body.Close()
		if request.URL.Path == "/v1/traces" {
			traces.Add(1)
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", server.URL)
	service, err := New(t.Context(), Options{Protocol: ExportHTTP})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	projectCompletedProviderSpan(service)
	if err := service.ForceFlush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if traces.Load() == 0 {
		t.Fatal("OTLP HTTP collector received no trace export")
	}
}

func TestOTLPGRPCExporterReachesLocalCollector(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	traceCollector := &testTraceCollector{}
	collectortrace.RegisterTraceServiceServer(server, traceCollector)
	collectormetric.RegisterMetricsServiceServer(
		server,
		&testMetricCollector{},
	)
	go func() {
		_ = server.Serve(listener)
	}()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	t.Setenv(
		"OTEL_EXPORTER_OTLP_ENDPOINT",
		"http://"+listener.Addr().String(),
	)
	t.Setenv("OTEL_EXPORTER_OTLP_INSECURE", "true")
	service, err := New(t.Context(), Options{Protocol: ExportGRPC})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Shutdown(context.Background()) })
	projectCompletedProviderSpan(service)
	flushContext, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := service.ForceFlush(flushContext); err != nil {
		t.Fatal(err)
	}
	if traceCollector.exports.Load() == 0 {
		t.Fatal("OTLP gRPC collector received no trace export")
	}
}

func projectCompletedProviderSpan(service *Service) {
	start := otelEnvelope(observation.KindModelRequestSent, 1)
	end := otelEnvelope(observation.KindModelResponseCompleted, 2)
	end.RecordedAt = start.RecordedAt.Add(1)
	service.Project(start)
	service.Project(end)
}

type testTraceCollector struct {
	collectortrace.UnimplementedTraceServiceServer
	exports atomic.Uint64
}

func (c *testTraceCollector) Export(
	context.Context,
	*collectortrace.ExportTraceServiceRequest,
) (*collectortrace.ExportTraceServiceResponse, error) {
	c.exports.Add(1)
	return &collectortrace.ExportTraceServiceResponse{}, nil
}

type testMetricCollector struct {
	collectormetric.UnimplementedMetricsServiceServer
}

func (*testMetricCollector) Export(
	context.Context,
	*collectormetric.ExportMetricsServiceRequest,
) (*collectormetric.ExportMetricsServiceResponse, error) {
	return &collectormetric.ExportMetricsServiceResponse{}, nil
}
