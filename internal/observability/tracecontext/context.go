// Package tracecontext owns W3C Trace Context propagation without making
// tracing an execution authority.
package tracecontext

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"go.opentelemetry.io/otel/propagation"
	oteltrace "go.opentelemetry.io/otel/trace"
)

const (
	HeaderTraceParent      = "traceparent"
	HeaderTraceState       = "tracestate"
	EnvironmentTraceParent = "TRACEPARENT"
	EnvironmentTraceState  = "TRACESTATE"
)

type Link struct {
	TraceID    string `json:"trace_id"`
	SpanID     string `json:"span_id"`
	TraceState string `json:"trace_state,omitempty"`
	TraceFlags byte   `json:"trace_flags,omitempty"`
}

func NewRoot(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	parent := oteltrace.SpanContextFromContext(ctx)
	traceID := parent.TraceID()
	if !traceID.IsValid() {
		var err error
		traceID, err = newTraceID()
		if err != nil {
			return ctx, err
		}
	}
	spanID, err := newSpanID()
	if err != nil {
		return ctx, err
	}
	state := parent.TraceState()
	flags := parent.TraceFlags()
	if !parent.IsValid() {
		flags = oteltrace.FlagsSampled
	}
	current := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID,
		TraceFlags: flags, TraceState: state,
	})
	return oteltrace.ContextWithSpanContext(ctx, current), nil
}

func Child(ctx context.Context) (context.Context, error) {
	return NewRoot(ctx)
}

func Current(ctx context.Context) (Link, bool) {
	if ctx == nil {
		return Link{}, false
	}
	current := oteltrace.SpanContextFromContext(ctx)
	if !current.IsValid() {
		return Link{}, false
	}
	return Link{
		TraceID:    current.TraceID().String(),
		SpanID:     current.SpanID().String(),
		TraceState: current.TraceState().String(),
		TraceFlags: byte(current.TraceFlags()),
	}, true
}

func WithLink(
	ctx context.Context,
	link Link,
	remote bool,
) (context.Context, error) {
	traceID, err := oteltrace.TraceIDFromHex(link.TraceID)
	if err != nil || !traceID.IsValid() {
		return ctx, errors.New("trace context trace id is invalid")
	}
	spanID, err := oteltrace.SpanIDFromHex(link.SpanID)
	if err != nil || !spanID.IsValid() {
		return ctx, errors.New("trace context span id is invalid")
	}
	state, err := oteltrace.ParseTraceState(link.TraceState)
	if err != nil {
		return ctx, fmt.Errorf("trace context tracestate: %w", err)
	}
	current := oteltrace.NewSpanContext(oteltrace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID,
		TraceFlags: oteltrace.TraceFlags(link.TraceFlags),
		TraceState: state, Remote: remote,
	})
	return oteltrace.ContextWithSpanContext(ctx, current), nil
}

func InjectHTTP(ctx context.Context, header http.Header) bool {
	if header == nil {
		return false
	}
	if _, ok := Current(ctx); !ok {
		return false
	}
	propagation.TraceContext{}.Inject(
		ctx,
		propagation.HeaderCarrier(header),
	)
	return true
}

func ExtractHTTP(
	ctx context.Context,
	header http.Header,
) (context.Context, error) {
	traceParent := headerValue(header, HeaderTraceParent)
	if header == nil || strings.TrimSpace(traceParent) == "" {
		return ctx, nil
	}
	traceState := headerValue(header, HeaderTraceState)
	if err := validateTraceState(traceState); err != nil {
		return ctx, err
	}
	carrier := make(http.Header, 2)
	carrier.Set(HeaderTraceParent, traceParent)
	carrier.Set(HeaderTraceState, traceState)
	extracted := propagation.TraceContext{}.Extract(
		ctx,
		propagation.HeaderCarrier(carrier),
	)
	current := oteltrace.SpanContextFromContext(extracted)
	if !current.IsValid() {
		return ctx, errors.New("traceparent is invalid")
	}
	return extracted, nil
}

func headerValue(header http.Header, name string) string {
	if header == nil {
		return ""
	}
	if value := header.Get(name); value != "" {
		return value
	}
	values := header[name]
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func InjectMap(ctx context.Context, target map[string]string) bool {
	if target == nil {
		return false
	}
	if _, ok := Current(ctx); !ok {
		return false
	}
	propagation.TraceContext{}.Inject(
		ctx,
		propagation.MapCarrier(target),
	)
	return true
}

func ExtractMap(
	ctx context.Context,
	source map[string]string,
) (context.Context, error) {
	header := make(http.Header, 2)
	if source != nil {
		header.Set(HeaderTraceParent, source[HeaderTraceParent])
		header.Set(HeaderTraceState, source[HeaderTraceState])
	}
	return ExtractHTTP(ctx, header)
}

func Environment(ctx context.Context) []string {
	values := make(map[string]string, 2)
	if !InjectMap(ctx, values) {
		return nil
	}
	result := []string{
		EnvironmentTraceParent + "=" + values[HeaderTraceParent],
	}
	if values[HeaderTraceState] != "" {
		result = append(
			result,
			EnvironmentTraceState+"="+values[HeaderTraceState],
		)
	}
	return result
}

func ToObservation(ctx context.Context) *observation.TraceContext {
	link, ok := Current(ctx)
	if !ok {
		return nil
	}
	return &observation.TraceContext{
		TraceID: link.TraceID, SpanID: link.SpanID,
		TraceFlags: link.TraceFlags, TraceState: link.TraceState,
	}
}

func FromObservation(
	ctx context.Context,
	value observation.TraceContext,
) (context.Context, error) {
	return WithLink(ctx, Link{
		TraceID: value.TraceID, SpanID: value.SpanID,
		TraceState: value.TraceState, TraceFlags: value.TraceFlags,
	}, false)
}

func validateTraceState(value string) error {
	if len(value) > 512 {
		return errors.New("tracestate exceeds 512 bytes")
	}
	if value == "" {
		return nil
	}
	if _, err := oteltrace.ParseTraceState(value); err != nil {
		return fmt.Errorf("tracestate is invalid: %w", err)
	}
	return nil
}

func newTraceID() (oteltrace.TraceID, error) {
	var value oteltrace.TraceID
	if _, err := rand.Read(value[:]); err != nil {
		return value, fmt.Errorf("generate trace id: %w", err)
	}
	if !value.IsValid() {
		return newTraceID()
	}
	return value, nil
}

func newSpanID() (oteltrace.SpanID, error) {
	var value oteltrace.SpanID
	if _, err := rand.Read(value[:]); err != nil {
		return value, fmt.Errorf("generate span id: %w", err)
	}
	if !value.IsValid() {
		return newSpanID()
	}
	return value, nil
}
