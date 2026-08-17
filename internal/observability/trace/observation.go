package trace

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
)

var fallbackTraceID atomic.Uint64

func (r *Recorder) observeStart(span Record) {
	kind, ok := startObservationKind(span.Name)
	if r == nil || r.observer == nil || !ok {
		return
	}
	record := r.observationRecord(kind, span)
	receipt := r.observer.Record(context.Background(), record)
	if receipt.Status != observation.AdmissionAccepted &&
		receipt.Status != observation.AdmissionPayloadDropped {
		return
	}
	r.mu.Lock()
	r.observed[span.ID] = receipt.ID
	r.mu.Unlock()
}

func (r *Recorder) observeEnd(span Record) {
	kind, ok := endObservationKind(span.Name, span.Status)
	if r == nil || r.observer == nil || !ok {
		return
	}
	record := r.observationRecord(kind, span)
	r.mu.Lock()
	startID := r.observed[span.ID]
	r.mu.Unlock()
	if startID != "" {
		record.Causality = &observation.Causality{
			ParentObservationID: startID,
		}
	}
	_ = r.observer.Record(context.Background(), record)
}

func (r *Recorder) observationRecord(
	kind observation.Kind,
	span Record,
) observation.Record {
	identity := r.identity
	if callID, ok := span.Attributes["call_id"].(string); ok {
		identity.CallID = callID
	}
	if sample, ok := span.Attributes["sample"]; ok {
		identity.SampleID = fmt.Sprint(sample)
	}
	if attempt, ok := observationAttempt(span.Attributes["attempt"]); ok {
		identity.Attempt = attempt
	}
	r.mu.Lock()
	spanID := r.spanIDs[span.ID]
	parentSpanID := r.spanIDs[span.ParentID]
	if span.ParentID == 0 && parentSpanID == "" {
		parentSpanID = r.remoteParentSpan
	}
	parentObservationID := r.observed[span.ParentID]
	r.mu.Unlock()
	traceContext := &observation.TraceContext{
		TraceID:    r.traceID,
		SpanID:     spanID,
		ParentSpan: parentSpanID,
		TraceFlags: r.traceFlags,
		TraceState: r.traceState,
	}
	var causality *observation.Causality
	if parentObservationID != "" {
		causality = &observation.Causality{
			ParentObservationID: parentObservationID,
		}
	}
	return observation.Record{
		Kind: kind, Identity: identity, Trace: traceContext,
		Causality: causality,
		Policy: observation.DataPolicy{
			Class:     observation.DataOperational,
			Redaction: observation.RedactionNotRequired,
		},
	}
}

func startObservationKind(name string) (observation.Kind, bool) {
	switch name {
	case NameTurn:
		return observation.KindTurnStarted, true
	case NameModelCall:
		return observation.KindModelRequestSent, true
	case NameTool:
		return observation.KindToolStarted, true
	case NameApprovalWait:
		return observation.KindApprovalRequested, true
	case NameVerify:
		return observation.KindVerificationStarted, true
	default:
		return "", false
	}
}

func endObservationKind(
	name string,
	status Status,
) (observation.Kind, bool) {
	switch name {
	case NameModelCall:
		if status == StatusOK {
			return observation.KindModelResponseCompleted, true
		}
		return observation.KindModelRequestFailed, true
	case NameTool:
		return observation.KindToolFinished, true
	case NameApprovalWait:
		return observation.KindApprovalResolved, true
	case NameVerify:
		return observation.KindVerificationFinished, true
	default:
		return "", false
	}
}

func observationAttempt(value any) (uint32, bool) {
	switch typed := value.(type) {
	case int:
		if typed > 0 && uint64(typed) <= uint64(^uint32(0)) {
			return uint32(typed), true
		}
	case int32:
		if typed > 0 {
			return uint32(typed), true
		}
	case int64:
		if typed > 0 && uint64(typed) <= uint64(^uint32(0)) {
			return uint32(typed), true
		}
	case uint:
		if typed > 0 && uint64(typed) <= uint64(^uint32(0)) {
			return uint32(typed), true
		}
	case uint32:
		return typed, typed > 0
	case uint64:
		if typed > 0 && typed <= uint64(^uint32(0)) {
			return uint32(typed), true
		}
	}
	return 0, false
}

func newTraceHex(bytes int) string {
	value := make([]byte, bytes)
	if _, err := rand.Read(value); err == nil {
		return hex.EncodeToString(value)
	}
	seed := fmt.Sprintf(
		"%d:%d",
		time.Now().UnixNano(),
		fallbackTraceID.Add(1),
	)
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:bytes])
}
