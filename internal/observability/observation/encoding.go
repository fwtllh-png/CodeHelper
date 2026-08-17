package observation

import (
	"encoding/json"
	"strconv"
	"time"
)

// AppendJSON appends the canonical V1 JSON representation of an Envelope.
// Journal uses this typed encoder on its serialized hot path; readers still use
// encoding/json and verify the digest over the original RawMessage.
func AppendJSON(dst []byte, envelope Envelope) ([]byte, error) {
	if err := envelope.Validate(); err != nil {
		return dst, err
	}
	dst = append(dst, '{')
	dst = appendUintField(dst, "schema_version", uint64(envelope.SchemaVersion), false)
	dst = appendStringField(dst, "id", string(envelope.ID), false)
	dst = appendStringField(dst, "kind", string(envelope.Kind), false)
	dst = appendUintField(
		dst,
		"observed_sequence",
		envelope.ObservedSequence,
		false,
	)
	dst = appendUintField(dst, "sequence", envelope.Sequence, false)
	dst = append(dst, `,"recorded_at":"`...)
	dst = envelope.RecordedAt.AppendFormat(dst, time.RFC3339Nano)
	dst = append(dst, '"')
	if envelope.MonotonicNS != 0 {
		dst = appendUintField(dst, "monotonic_ns", envelope.MonotonicNS, false)
	}
	dst = append(dst, `,"identity":`...)
	dst = appendIdentityJSON(dst, envelope.Identity)
	if envelope.Trace != nil {
		dst = append(dst, `,"trace":`...)
		dst = appendTraceJSON(dst, *envelope.Trace)
	}
	if envelope.Causality != nil {
		dst = append(dst, `,"causality":`...)
		dst = appendCausalityJSON(dst, *envelope.Causality)
	}
	dst = append(dst, `,"policy":`...)
	dst = appendPolicyJSON(dst, envelope.Policy)
	if envelope.Payload != nil {
		dst = append(dst, `,"payload":`...)
		dst = appendPayloadJSON(dst, *envelope.Payload)
	}
	if len(envelope.Summary) != 0 {
		dst = append(dst, `,"summary":`...)
		dst = append(dst, envelope.Summary...)
	}
	dst = append(dst, '}')
	return dst, nil
}

func appendIdentityJSON(dst []byte, identity Identity) []byte {
	dst = append(dst, '{')
	dst = appendStringField(dst, "runtime_id", identity.RuntimeID, false)
	dst = appendStringField(dst, "session_id", identity.SessionID, true)
	dst = appendStringField(dst, "thread_id", string(identity.ThreadID), true)
	dst = appendStringField(dst, "turn_id", string(identity.TurnID), true)
	dst = appendStringField(dst, "operation_id", string(identity.OperationID), true)
	dst = appendStringField(dst, "run_id", string(identity.RunID), true)
	dst = appendStringField(dst, "node_id", string(identity.NodeID), true)
	dst = appendStringField(dst, "attempt_id", string(identity.AttemptID), true)
	dst = appendStringField(dst, "effect_id", string(identity.EffectID), true)
	dst = appendStringField(dst, "event_id", string(identity.EventID), true)
	if identity.EventCursor != 0 {
		dst = appendUintField(dst, "event_cursor", uint64(identity.EventCursor), false)
	}
	if identity.FactSequence != 0 {
		dst = appendUintField(dst, "fact_sequence", identity.FactSequence, false)
	}
	dst = appendStringField(dst, "sample_id", identity.SampleID, true)
	dst = appendStringField(dst, "call_id", identity.CallID, true)
	if identity.Attempt != 0 {
		dst = appendUintField(dst, "attempt", uint64(identity.Attempt), false)
	}
	dst = appendStringField(dst, "agent_id", identity.AgentID, true)
	dst = appendStringField(
		dst,
		"extension_operation_id",
		identity.ExtensionOperationID,
		true,
	)
	dst = append(dst, '}')
	return dst
}

func appendTraceJSON(dst []byte, trace TraceContext) []byte {
	dst = append(dst, '{')
	dst = appendStringField(dst, "trace_id", trace.TraceID, false)
	dst = appendStringField(dst, "span_id", trace.SpanID, false)
	dst = appendStringField(dst, "parent_span_id", trace.ParentSpan, true)
	if trace.TraceFlags != 0 {
		dst = appendUintField(dst, "trace_flags", uint64(trace.TraceFlags), false)
	}
	dst = appendStringField(dst, "trace_state", trace.TraceState, true)
	dst = append(dst, '}')
	return dst
}

func appendCausalityJSON(dst []byte, causality Causality) []byte {
	dst = append(dst, '{')
	first := true
	if causality.ParentObservationID != "" {
		dst = appendQuotedName(dst, "parent_observation_id", first)
		dst = strconv.AppendQuote(dst, string(causality.ParentObservationID))
		first = false
	}
	if len(causality.Links) != 0 {
		dst = appendQuotedName(dst, "links", first)
		dst = append(dst, '[')
		for index, link := range causality.Links {
			if index != 0 {
				dst = append(dst, ',')
			}
			dst = append(dst, '{')
			dst = appendStringField(dst, "relation", link.Relation, false)
			dst = appendStringField(dst, "target", string(link.Target), false)
			dst = append(dst, '}')
		}
		dst = append(dst, ']')
	}
	dst = append(dst, '}')
	return dst
}

func appendPolicyJSON(dst []byte, policy DataPolicy) []byte {
	dst = append(dst, '{')
	dst = appendStringField(dst, "class", string(policy.Class), false)
	dst = appendStringField(dst, "redaction", string(policy.Redaction), false)
	dst = append(dst, '}')
	return dst
}

func appendPayloadJSON(dst []byte, payload PayloadRef) []byte {
	dst = append(dst, '{')
	dst = appendStringField(dst, "digest", payload.Digest, false)
	dst = appendStringField(dst, "media_type", payload.MediaType, false)
	dst = appendStringField(dst, "encoding", payload.Encoding, true)
	dst = appendUintField(dst, "original_bytes", payload.OriginalBytes, false)
	dst = appendUintField(dst, "stored_bytes", payload.StoredBytes, false)
	if payload.Truncated {
		dst = appendBoolField(dst, "truncated", true)
	}
	dst = appendStringField(dst, "data_class", string(payload.DataClass), false)
	dst = appendStringField(dst, "redaction", string(payload.Redaction), false)
	dst = append(dst, '}')
	return dst
}

func appendStringField(
	dst []byte,
	name, value string,
	omitEmpty bool,
) []byte {
	if omitEmpty && value == "" {
		return dst
	}
	dst = appendQuotedName(dst, name, dst[len(dst)-1] == '{')
	return strconv.AppendQuote(dst, value)
}

func appendUintField(
	dst []byte,
	name string,
	value uint64,
	_ bool,
) []byte {
	dst = appendQuotedName(dst, name, dst[len(dst)-1] == '{')
	return strconv.AppendUint(dst, value, 10)
}

func appendBoolField(dst []byte, name string, value bool) []byte {
	dst = appendQuotedName(dst, name, dst[len(dst)-1] == '{')
	return strconv.AppendBool(dst, value)
}

func appendQuotedName(dst []byte, name string, first bool) []byte {
	if !first {
		dst = append(dst, ',')
	}
	dst = strconv.AppendQuote(dst, name)
	dst = append(dst, ':')
	return dst
}

// DecodeJSON is the cold-path counterpart used by Journal recovery tests and
// tools that need the same strict validation as the Writer.
func DecodeJSON(content []byte) (Envelope, error) {
	var envelope Envelope
	if err := json.Unmarshal(content, &envelope); err != nil {
		return Envelope{}, err
	}
	if err := envelope.Validate(); err != nil {
		return Envelope{}, err
	}
	return envelope, nil
}
