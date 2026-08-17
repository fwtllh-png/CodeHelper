package otel

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	otelmetric "go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Health struct {
	Submitted  uint64 `json:"submitted"`
	Projected  uint64 `json:"projected"`
	Dropped    uint64 `json:"dropped"`
	Failures   uint64 `json:"failures"`
	QueueDepth int64  `json:"queue_depth"`
}

type spanEntry struct {
	span             oteltrace.Span
	started          time.Time
	terminalPrepared time.Time
	class            string
}

type Service struct {
	queue chan observation.Envelope
	stop  chan struct{}
	done  chan struct{}

	traceProvider *sdktrace.TracerProvider
	meterProvider *sdkmetric.MeterProvider
	tracer        oteltrace.Tracer
	memory        *MemoryExporter
	metrics       *MetricRegistry

	observationCount otelmetric.Int64Counter
	observationDrop  otelmetric.Int64Counter
	queueDepth       otelmetric.Int64UpDownCounter
	projectionLag    otelmetric.Float64Histogram
	payloadBytes     otelmetric.Int64Histogram
	inconsistency    otelmetric.Int64Counter
	operationCount   otelmetric.Int64Counter
	providerCount    otelmetric.Int64Counter
	providerDuration otelmetric.Float64Histogram
	toolCount        otelmetric.Int64Counter
	toolDuration     otelmetric.Float64Histogram
	approvalDuration otelmetric.Float64Histogram
	turnDuration     otelmetric.Float64Histogram
	terminalDuration otelmetric.Float64Histogram

	mu    sync.Mutex
	spans map[string]spanEntry

	projectMu sync.RWMutex
	accepting atomic.Bool
	submitted atomic.Uint64
	projected atomic.Uint64
	dropped   atomic.Uint64
	failures  atomic.Uint64
	depth     atomic.Int64
	inFlight  atomic.Int64
	pending   atomic.Int64
	closeOnce sync.Once
}

func NewFromEnvironment(ctx context.Context) (*Service, error) {
	return New(ctx, environmentOptions())
}

func New(ctx context.Context, options Options) (*Service, error) {
	options = defaultOptions(options)
	if options.Protocol == ExportOff {
		return nil, nil
	}
	resourceValue, err := newResource(ctx, options.ServiceName)
	if err != nil {
		return nil, err
	}
	var memory *MemoryExporter
	traceExporter := options.TraceExporter
	metricReader := options.MetricReader
	if options.Protocol == ExportMemory && metricReader == nil {
		metricReader = sdkmetric.NewManualReader()
	}
	if traceExporter == nil {
		switch options.Protocol {
		case ExportMemory:
			memory = &MemoryExporter{}
			traceExporter = memory
		case ExportHTTP, ExportGRPC:
			traceExporter, metricReader, err = remoteExporters(
				ctx,
				options.Protocol,
			)
			if err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf(
				"unsupported OTEL exporter %q",
				options.Protocol,
			)
		}
	}
	service := &Service{
		queue: make(chan observation.Envelope, options.QueueCapacity),
		stop:  make(chan struct{}), done: make(chan struct{}),
		memory: memory, metrics: NewMetricRegistry(512),
		spans: make(map[string]spanEntry),
	}
	observed := &observedExporter{
		SpanExporter: traceExporter,
		failures:     &service.failures,
	}
	var processor sdktrace.SpanProcessor
	if options.Protocol == ExportMemory {
		processor = sdktrace.NewSimpleSpanProcessor(observed)
	} else {
		processor = sdktrace.NewBatchSpanProcessor(observed)
	}
	service.traceProvider = sdktrace.NewTracerProvider(
		sdktrace.WithResource(resourceValue),
		sdktrace.WithIDGenerator(envelopeIDGenerator{}),
		sdktrace.WithSpanProcessor(processor),
	)
	meterOptions := []sdkmetric.Option{
		sdkmetric.WithResource(resourceValue),
	}
	if metricReader != nil {
		meterOptions = append(
			meterOptions,
			sdkmetric.WithReader(metricReader),
		)
	}
	service.meterProvider = sdkmetric.NewMeterProvider(meterOptions...)
	service.tracer = service.traceProvider.Tracer(
		"github.com/fwtllh-png/CodeHelper/observation",
	)
	if err := service.initMetrics(); err != nil {
		_ = closeProviders(ctx, service.traceProvider, service.meterProvider)
		return nil, err
	}
	service.accepting.Store(true)
	go service.run()
	return service, nil
}

func (s *Service) Project(envelope observation.Envelope) {
	if s == nil {
		return
	}
	s.projectMu.RLock()
	defer s.projectMu.RUnlock()
	if !s.accepting.Load() {
		return
	}
	s.pending.Add(1)
	s.depth.Add(1)
	s.queueDepth.Add(context.Background(), 1)
	select {
	case s.queue <- envelope:
		s.submitted.Add(1)
	default:
		s.depth.Add(-1)
		s.pending.Add(-1)
		s.dropped.Add(1)
		labels := Labels{"status": "queue_full"}
		s.queueDepth.Add(context.Background(), -1)
		s.observationDrop.Add(
			context.Background(),
			1,
			otelmetric.WithAttributes(metricAttributes(labels)...),
		)
		s.metrics.Add(
			"codehelper.observation.dropped",
			labels,
			1,
		)
	}
}

func (s *Service) ForceFlush(ctx context.Context) error {
	if s == nil {
		return nil
	}
	ticker := time.NewTicker(time.Millisecond)
	defer ticker.Stop()
	for s.pending.Load() != 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
	var traceErr, metricErr error
	if s.traceProvider != nil {
		traceErr = s.traceProvider.ForceFlush(ctx)
	}
	if s.meterProvider != nil {
		metricErr = s.meterProvider.ForceFlush(ctx)
	}
	if err := errors.Join(traceErr, metricErr); err != nil {
		s.failures.Add(1)
		return err
	}
	return nil
}

func (s *Service) Shutdown(ctx context.Context) error {
	if s == nil {
		return nil
	}
	var result error
	s.closeOnce.Do(func() {
		s.projectMu.Lock()
		s.accepting.Store(false)
		s.projectMu.Unlock()
		flushErr := s.ForceFlush(ctx)
		close(s.stop)
		select {
		case <-s.done:
		case <-ctx.Done():
			result = errors.Join(flushErr, ctx.Err())
			return
		}
		result = errors.Join(
			flushErr,
			closeProviders(ctx, s.traceProvider, s.meterProvider),
		)
	})
	return result
}

func (s *Service) Health() Health {
	if s == nil {
		return Health{}
	}
	return Health{
		Submitted:  s.submitted.Load(),
		Projected:  s.projected.Load(),
		Dropped:    s.dropped.Load(),
		Failures:   s.failures.Load(),
		QueueDepth: s.pending.Load(),
	}
}

func (s *Service) MemorySpans() []MemorySpan {
	if s == nil {
		return nil
	}
	return s.memory.Snapshot()
}

func (s *Service) MetricSnapshot() ([]MetricPoint, uint64) {
	if s == nil {
		return nil, 0
	}
	return s.metrics.Snapshot()
}

func (s *Service) run() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case envelope := <-s.queue:
			s.inFlight.Add(1)
			s.depth.Add(-1)
			s.project(envelope)
			s.inFlight.Add(-1)
			s.pending.Add(-1)
			s.queueDepth.Add(context.Background(), -1)
			s.projected.Add(1)
		}
	}
}

func (s *Service) project(envelope observation.Envelope) {
	traits, ok := observation.TraitsFor(envelope.Kind)
	if !ok {
		s.failures.Add(1)
		return
	}
	class := observationClass(envelope.Kind)
	labels := Labels{
		"status":            "recorded",
		"observation_class": class,
	}
	s.metrics.Add("codehelper.observation.count", labels, 1)
	s.observationCount.Add(
		context.Background(),
		1,
		otelmetric.WithAttributes(metricAttributes(labels)...),
	)
	s.recordMetric(envelope, class)
	lagMS := max(
		0,
		float64(time.Since(envelope.RecordedAt).Microseconds())/1000,
	)
	lagLabels := Labels{"observation_class": class}
	s.projectionLag.Record(
		context.Background(),
		lagMS,
		otelmetric.WithAttributes(metricAttributes(lagLabels)...),
	)
	s.metrics.Add("codehelper.projection.lag", lagLabels, lagMS)
	if envelope.Payload != nil {
		payloadLabels := Labels{"observation_class": class}
		s.payloadBytes.Record(
			context.Background(),
			int64(envelope.Payload.StoredBytes),
			otelmetric.WithAttributes(metricAttributes(payloadLabels)...),
		)
		s.metrics.Add(
			"codehelper.payload.bytes",
			payloadLabels,
			float64(envelope.Payload.StoredBytes),
		)
	}
	if traits.OTEL == observation.OTELMetric {
		s.recordMappedMetric(envelope, class)
	}
	if envelope.Trace == nil {
		return
	}
	switch traits.OTEL {
	case observation.OTELSpanStart:
		s.startSpan(envelope, class)
	case observation.OTELSpanEnd:
		s.endSpan(envelope)
	case observation.OTELEvent:
		s.addEvent(envelope)
	}
}

func (s *Service) startSpan(
	envelope observation.Envelope,
	class string,
) {
	current, parent, err := spanContexts(envelope)
	if err != nil {
		s.failures.Add(1)
		return
	}
	ctx := context.Background()
	if parent.IsValid() {
		ctx = oteltrace.ContextWithRemoteSpanContext(ctx, parent)
	}
	ctx = context.WithValue(ctx, desiredIDKey{}, current)
	_, span := s.tracer.Start(
		ctx,
		"codehelper."+string(envelope.Kind),
		oteltrace.WithTimestamp(envelope.RecordedAt),
		oteltrace.WithAttributes(
			attribute.String("codehelper.observation.class", class),
			attribute.String(
				"codehelper.observation.owner",
				string(ownerFor(envelope.Kind)),
			),
		),
	)
	key := spanKey(current)
	s.mu.Lock()
	if _, exists := s.spans[key]; exists {
		s.mu.Unlock()
		span.End()
		s.failures.Add(1)
		return
	}
	s.spans[key] = spanEntry{
		span: span, started: envelope.RecordedAt, class: class,
	}
	s.mu.Unlock()
	s.recordCount(class, "started")
}

func (s *Service) endSpan(
	envelope observation.Envelope,
) {
	current, _, err := spanContexts(envelope)
	if err != nil {
		s.failures.Add(1)
		return
	}
	key := spanKey(current)
	s.mu.Lock()
	entry, ok := s.spans[key]
	if ok {
		delete(s.spans, key)
	}
	s.mu.Unlock()
	if !ok {
		labels := Labels{"status": "missing_start"}
		attributes := otelmetric.WithAttributes(
			metricAttributes(labels)...,
		)
		s.inconsistency.Add(context.Background(), 1, attributes)
		s.metrics.Add(
			"codehelper.reducer.inconsistency",
			labels,
			1,
		)
		return
	}
	status := "completed"
	switch envelope.Kind {
	case observation.KindModelRequestFailed,
		observation.KindRuntimeCrashed,
		observation.KindSandboxDenied,
		observation.KindOperationRejected:
		status = "failed"
		entry.span.SetStatus(codes.Error, status)
	default:
		entry.span.SetStatus(codes.Ok, status)
	}
	entry.span.End(oteltrace.WithTimestamp(envelope.RecordedAt))
	duration := max(
		0,
		float64(envelope.RecordedAt.Sub(entry.started).Microseconds())/1000,
	)
	s.recordDuration(entry.class, status, duration)
	if envelope.Kind == observation.KindTurnTerminalCommitted &&
		!entry.terminalPrepared.IsZero() {
		terminalDuration := max(
			0,
			float64(
				envelope.RecordedAt.Sub(
					entry.terminalPrepared,
				).Microseconds(),
			)/1000,
		)
		labels := Labels{"status": status}
		attributes := otelmetric.WithAttributes(
			metricAttributes(labels)...,
		)
		s.terminalDuration.Record(
			context.Background(),
			terminalDuration,
			attributes,
		)
		s.metrics.Add(
			"codehelper.terminal.commit.duration",
			labels,
			terminalDuration,
		)
	}
}

func (s *Service) addEvent(envelope observation.Envelope) {
	current, _, err := spanContexts(envelope)
	if err != nil {
		return
	}
	s.mu.Lock()
	entry, ok := s.spans[spanKey(current)]
	if ok && envelope.Kind == observation.KindTurnTerminalPrepared {
		entry.terminalPrepared = envelope.RecordedAt
		s.spans[spanKey(current)] = entry
	}
	s.mu.Unlock()
	if ok {
		entry.span.AddEvent(
			string(envelope.Kind),
			oteltrace.WithTimestamp(envelope.RecordedAt),
		)
	}
}

func (s *Service) initMetrics() error {
	meter := s.meterProvider.Meter(
		"github.com/fwtllh-png/CodeHelper/observation",
	)
	var err error
	if s.observationCount, err = meter.Int64Counter(
		"codehelper.observation.count",
	); err != nil {
		return err
	}
	if s.observationDrop, err = meter.Int64Counter(
		"codehelper.observation.dropped",
	); err != nil {
		return err
	}
	if s.queueDepth, err = meter.Int64UpDownCounter(
		"codehelper.observation.queue.depth",
	); err != nil {
		return err
	}
	if s.projectionLag, err = meter.Float64Histogram(
		"codehelper.projection.lag",
	); err != nil {
		return err
	}
	if s.payloadBytes, err = meter.Int64Histogram(
		"codehelper.payload.bytes",
	); err != nil {
		return err
	}
	if s.inconsistency, err = meter.Int64Counter(
		"codehelper.reducer.inconsistency",
	); err != nil {
		return err
	}
	if s.operationCount, err = meter.Int64Counter(
		"codehelper.operation.count",
	); err != nil {
		return err
	}
	if s.providerCount, err = meter.Int64Counter(
		"codehelper.provider.request.count",
	); err != nil {
		return err
	}
	if s.providerDuration, err = meter.Float64Histogram(
		"codehelper.provider.request.duration",
	); err != nil {
		return err
	}
	if s.toolCount, err = meter.Int64Counter(
		"codehelper.tool.count",
	); err != nil {
		return err
	}
	if s.toolDuration, err = meter.Float64Histogram(
		"codehelper.tool.duration",
	); err != nil {
		return err
	}
	if s.approvalDuration, err = meter.Float64Histogram(
		"codehelper.approval.wait.duration",
	); err != nil {
		return err
	}
	s.turnDuration, err = meter.Float64Histogram(
		"codehelper.turn.duration",
	)
	if err != nil {
		return err
	}
	s.terminalDuration, err = meter.Float64Histogram(
		"codehelper.terminal.commit.duration",
	)
	return err
}

func (s *Service) recordMetric(
	envelope observation.Envelope,
	class string,
) {
	if class == "operation" &&
		envelope.Kind == observation.KindOperationAccepted {
		s.operationCount.Add(context.Background(), 1)
		s.metrics.Add(
			"codehelper.operation.count",
			Labels{"status": "accepted"},
			1,
		)
	}
}

func (s *Service) recordMappedMetric(
	envelope observation.Envelope,
	class string,
) {
	if envelope.Kind != observation.KindExtensionSubscriberDropped {
		return
	}
	labels := Labels{
		"status":            "subscriber_dropped",
		"observation_class": class,
	}
	attributes := otelmetric.WithAttributes(metricAttributes(labels)...)
	s.observationDrop.Add(context.Background(), 1, attributes)
	s.metrics.Add("codehelper.observation.dropped", labels, 1)
}

func (s *Service) recordCount(class, status string) {
	labels := Labels{"status": status}
	attributes := otelmetric.WithAttributes(metricAttributes(labels)...)
	switch class {
	case "provider":
		s.providerCount.Add(context.Background(), 1, attributes)
		s.metrics.Add("codehelper.provider.request.count", labels, 1)
	case "tool":
		s.toolCount.Add(context.Background(), 1, attributes)
		s.metrics.Add("codehelper.tool.count", labels, 1)
	}
}

func (s *Service) recordDuration(
	class, status string,
	durationMS float64,
) {
	labels := Labels{"status": status}
	attributes := otelmetric.WithAttributes(metricAttributes(labels)...)
	switch class {
	case "turn":
		s.turnDuration.Record(context.Background(), durationMS, attributes)
		s.metrics.Add("codehelper.turn.duration", labels, durationMS)
	case "provider":
		s.providerDuration.Record(context.Background(), durationMS, attributes)
		s.metrics.Add(
			"codehelper.provider.request.duration",
			labels,
			durationMS,
		)
	case "tool":
		s.toolDuration.Record(context.Background(), durationMS, attributes)
		s.metrics.Add("codehelper.tool.duration", labels, durationMS)
	case "approval":
		s.approvalDuration.Record(context.Background(), durationMS, attributes)
		s.metrics.Add(
			"codehelper.approval.wait.duration",
			labels,
			durationMS,
		)
	}
}

type observedExporter struct {
	sdktrace.SpanExporter
	failures *atomic.Uint64
}

func (e *observedExporter) ExportSpans(
	ctx context.Context,
	spans []sdktrace.ReadOnlySpan,
) error {
	err := e.SpanExporter.ExportSpans(ctx, spans)
	if err != nil {
		e.failures.Add(1)
	}
	return err
}

type desiredIDKey struct{}

type envelopeIDGenerator struct{}

func (envelopeIDGenerator) NewIDs(
	ctx context.Context,
) (oteltrace.TraceID, oteltrace.SpanID) {
	if desired, ok := ctx.Value(desiredIDKey{}).(oteltrace.SpanContext); ok {
		return desired.TraceID(), desired.SpanID()
	}
	return randomIDs()
}

func (envelopeIDGenerator) NewSpanID(
	ctx context.Context,
	_ oteltrace.TraceID,
) oteltrace.SpanID {
	if desired, ok := ctx.Value(desiredIDKey{}).(oteltrace.SpanContext); ok {
		return desired.SpanID()
	}
	_, spanID := randomIDs()
	return spanID
}

func randomIDs() (oteltrace.TraceID, oteltrace.SpanID) {
	var traceID oteltrace.TraceID
	var spanID oteltrace.SpanID
	_, _ = rand.Read(traceID[:])
	_, _ = rand.Read(spanID[:])
	return traceID, spanID
}

func spanContexts(
	envelope observation.Envelope,
) (oteltrace.SpanContext, oteltrace.SpanContext, error) {
	traceID, err := oteltrace.TraceIDFromHex(envelope.Trace.TraceID)
	if err != nil {
		return oteltrace.SpanContext{}, oteltrace.SpanContext{}, err
	}
	spanID, err := oteltrace.SpanIDFromHex(envelope.Trace.SpanID)
	if err != nil {
		return oteltrace.SpanContext{}, oteltrace.SpanContext{}, err
	}
	state, err := oteltrace.ParseTraceState(envelope.Trace.TraceState)
	if err != nil {
		return oteltrace.SpanContext{}, oteltrace.SpanContext{}, err
	}
	current := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID,
		TraceFlags: oteltrace.TraceFlags(envelope.Trace.TraceFlags),
		TraceState: state,
	})
	var parent oteltrace.SpanContext
	if envelope.Trace.ParentSpan != "" {
		parentID, parentErr := oteltrace.SpanIDFromHex(
			envelope.Trace.ParentSpan,
		)
		if parentErr != nil {
			return current, parent, parentErr
		}
		parent = oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
			TraceID: traceID, SpanID: parentID,
			TraceFlags: current.TraceFlags(),
			TraceState: state, Remote: true,
		})
	}
	return current, parent, nil
}

func spanKey(context oteltrace.SpanContext) string {
	return context.TraceID().String() + "/" + context.SpanID().String()
}

func ownerFor(kind observation.Kind) observation.Owner {
	traits, _ := observation.TraitsFor(kind)
	return traits.Owner
}

func observationClass(kind observation.Kind) string {
	value := string(kind)
	switch {
	case hasPrefix(value, "model."):
		return "provider"
	case hasPrefix(value, "tool."):
		return "tool"
	case hasPrefix(value, "approval."):
		return "approval"
	case hasPrefix(value, "turn."):
		return "turn"
	case hasPrefix(value, "operation."):
		return "operation"
	case hasPrefix(value, "process."):
		return "process"
	case hasPrefix(value, "agent."):
		return "agent"
	case hasPrefix(value, "extension."):
		return "extension"
	case hasPrefix(value, "workflow."):
		return "workflow"
	default:
		return "runtime"
	}
}

func hasPrefix(value, prefix string) bool {
	return len(value) >= len(prefix) && value[:len(prefix)] == prefix
}
