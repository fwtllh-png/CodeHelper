package observation

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const (
	SchemaVersion   = 1
	maxSummarySize  = 16 << 10
	maxIdentitySize = 256
)

type ObservationID string

var (
	observationIDPattern = regexp.MustCompile(`^obs_[0-9a-f]{32}$`)
	lowerHexPattern      = regexp.MustCompile(`^[0-9a-f]+$`)
	payloadDigestPattern = regexp.MustCompile(`^sha256:[0-9a-f]{64}$`)
)

func NewID() (ObservationID, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate observation id: %w", err)
	}
	return ObservationID("obs_" + hex.EncodeToString(value[:])), nil
}

type Envelope struct {
	SchemaVersion    uint32          `json:"schema_version"`
	ID               ObservationID   `json:"id"`
	Kind             Kind            `json:"kind"`
	ObservedSequence uint64          `json:"observed_sequence"`
	Sequence         uint64          `json:"sequence"`
	RecordedAt       time.Time       `json:"recorded_at"`
	MonotonicNS      uint64          `json:"monotonic_ns,omitempty"`
	Identity         Identity        `json:"identity"`
	Trace            *TraceContext   `json:"trace,omitempty"`
	Causality        *Causality      `json:"causality,omitempty"`
	Policy           DataPolicy      `json:"policy"`
	Payload          *PayloadRef     `json:"payload,omitempty"`
	Summary          json.RawMessage `json:"summary,omitempty"`
}

type Identity struct {
	RuntimeID            string               `json:"runtime_id"`
	SessionID            string               `json:"session_id,omitempty"`
	ThreadID             protocol.ThreadID    `json:"thread_id,omitempty"`
	TurnID               protocol.TurnID      `json:"turn_id,omitempty"`
	OperationID          protocol.OperationID `json:"operation_id,omitempty"`
	RunID                protocol.RunID       `json:"run_id,omitempty"`
	NodeID               protocol.NodeID      `json:"node_id,omitempty"`
	AttemptID            protocol.AttemptID   `json:"attempt_id,omitempty"`
	EffectID             protocol.EffectID    `json:"effect_id,omitempty"`
	EventID              protocol.EventID     `json:"event_id,omitempty"`
	EventCursor          protocol.Cursor      `json:"event_cursor,omitempty"`
	FactSequence         uint64               `json:"fact_sequence,omitempty"`
	SampleID             string               `json:"sample_id,omitempty"`
	CallID               string               `json:"call_id,omitempty"`
	Attempt              uint32               `json:"attempt,omitempty"`
	AgentID              string               `json:"agent_id,omitempty"`
	ExtensionOperationID string               `json:"extension_operation_id,omitempty"`
}

type TraceContext struct {
	TraceID    string `json:"trace_id"`
	SpanID     string `json:"span_id"`
	ParentSpan string `json:"parent_span_id,omitempty"`
	TraceFlags byte   `json:"trace_flags,omitempty"`
	TraceState string `json:"trace_state,omitempty"`
}

type Causality struct {
	ParentObservationID ObservationID `json:"parent_observation_id,omitempty"`
	Links               []Link        `json:"links,omitempty"`
}

type Link struct {
	Relation string        `json:"relation"`
	Target   ObservationID `json:"target"`
}

type DataClass string
type RedactionStatus string

const (
	DataPublicMetadata DataClass = "public_metadata"
	DataOperational    DataClass = "operational"
	DataWorkspace      DataClass = "workspace_content"
	DataConversation   DataClass = "conversation_content"
	DataCredential     DataClass = "credential"
	DataRestricted     DataClass = "restricted"

	RedactionNotRequired RedactionStatus = "not_required"
	RedactionApplied     RedactionStatus = "applied"
	RedactionUnavailable RedactionStatus = "unavailable"
)

type DataPolicy struct {
	Class     DataClass       `json:"class"`
	Redaction RedactionStatus `json:"redaction"`
}

type PayloadRef struct {
	Digest        string          `json:"digest"`
	MediaType     string          `json:"media_type"`
	Encoding      string          `json:"encoding,omitempty"`
	OriginalBytes uint64          `json:"original_bytes"`
	StoredBytes   uint64          `json:"stored_bytes"`
	Truncated     bool            `json:"truncated,omitempty"`
	DataClass     DataClass       `json:"data_class"`
	Redaction     RedactionStatus `json:"redaction"`
}

var allowedRelations = map[string]bool{
	"caused_by":      true,
	"produced":       true,
	"observed_by":    true,
	"delivered_to":   true,
	"retried_from":   true,
	"recovered_from": true,
	"projected_as":   true,
	"verified_by":    true,
	"committed_with": true,
}

func (e Envelope) Validate() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("observation schema version must be %d", SchemaVersion)
	}
	if !observationIDPattern.MatchString(string(e.ID)) {
		return errors.New("observation id is invalid")
	}
	traits, ok := traitsFor(e.Kind)
	if !ok {
		return fmt.Errorf("observation kind %q has no traits", e.Kind)
	}
	if e.ObservedSequence == 0 || e.Sequence == 0 || e.RecordedAt.IsZero() {
		return errors.New(
			"observation observed_sequence, sequence, and recorded_at are required",
		)
	}
	if err := e.Identity.validate(traits); err != nil {
		return err
	}
	if e.Trace != nil {
		if err := e.Trace.Validate(); err != nil {
			return err
		}
	}
	if e.Causality != nil {
		if err := e.Causality.Validate(e.ID); err != nil {
			return err
		}
	}
	if err := e.Policy.Validate(); err != nil {
		return err
	}
	if err := validatePayloadPolicy(traits.Payload, e.Policy, e.Payload); err != nil {
		return err
	}
	if e.Payload != nil {
		if err := e.Payload.Validate(); err != nil {
			return err
		}
	}
	if len(e.Summary) > maxSummarySize {
		return fmt.Errorf("observation summary exceeds %d bytes", maxSummarySize)
	}
	if len(e.Summary) != 0 && !json.Valid(e.Summary) {
		return errors.New("observation summary must be valid JSON")
	}
	return nil
}

func (i Identity) validate(traits Traits) error {
	if strings.TrimSpace(i.RuntimeID) == "" {
		return errors.New("observation runtime_id is required")
	}
	values := []string{
		i.RuntimeID, i.SessionID, string(i.ThreadID), string(i.TurnID),
		string(i.OperationID), string(i.RunID), string(i.NodeID),
		string(i.AttemptID), string(i.EffectID), string(i.EventID),
		i.SampleID, i.CallID, i.AgentID, i.ExtensionOperationID,
	}
	for _, value := range values {
		if len(value) > maxIdentitySize {
			return fmt.Errorf("observation identity exceeds %d bytes", maxIdentitySize)
		}
	}
	for _, required := range traits.Correlations {
		if !i.has(required) {
			return fmt.Errorf(
				"observation kind requires %s correlation",
				required,
			)
		}
	}
	return nil
}

func (i Identity) has(name string) bool {
	switch name {
	case "runtime":
		return i.RuntimeID != ""
	case "session":
		return i.SessionID != ""
	case "thread":
		return i.ThreadID != ""
	case "turn":
		return i.TurnID != ""
	case "operation":
		return i.OperationID != ""
	case "run":
		return i.RunID != ""
	case "node":
		return i.NodeID != ""
	case "attempt":
		return i.AttemptID != ""
	case "effect":
		return i.EffectID != ""
	case "event":
		return i.EventID != "" && i.EventCursor != 0
	case "fact":
		return i.FactSequence != 0
	case "sample":
		return i.SampleID != ""
	case "call":
		return i.CallID != ""
	case "tool_attempt":
		return i.Attempt != 0
	case "agent":
		return i.AgentID != ""
	case "extension":
		return i.ExtensionOperationID != ""
	default:
		return false
	}
}

func (t TraceContext) Validate() error {
	if !validHex(t.TraceID, 32) || t.TraceID == strings.Repeat("0", 32) {
		return errors.New("observation trace_id is invalid")
	}
	if !validHex(t.SpanID, 16) || t.SpanID == strings.Repeat("0", 16) {
		return errors.New("observation span_id is invalid")
	}
	if t.ParentSpan != "" &&
		(!validHex(t.ParentSpan, 16) || t.ParentSpan == strings.Repeat("0", 16)) {
		return errors.New("observation parent_span_id is invalid")
	}
	if len(t.TraceState) > 512 {
		return errors.New("observation trace_state exceeds 512 bytes")
	}
	return nil
}

func validHex(value string, length int) bool {
	return len(value) == length && lowerHexPattern.MatchString(value)
}

func (c Causality) Validate(self ObservationID) error {
	if c.ParentObservationID != "" {
		if !observationIDPattern.MatchString(string(c.ParentObservationID)) ||
			c.ParentObservationID == self {
			return errors.New("observation parent identity is invalid")
		}
	}
	seen := make(map[string]bool, len(c.Links))
	for _, link := range c.Links {
		if !allowedRelations[link.Relation] ||
			!observationIDPattern.MatchString(string(link.Target)) ||
			link.Target == self {
			return errors.New("observation causality link is invalid")
		}
		key := link.Relation + "\x00" + string(link.Target)
		if seen[key] {
			return errors.New("observation causality link is duplicated")
		}
		seen[key] = true
	}
	return nil
}

func (p DataPolicy) Validate() error {
	switch p.Class {
	case DataPublicMetadata, DataOperational, DataWorkspace, DataConversation,
		DataCredential, DataRestricted:
	default:
		return errors.New("observation data class is invalid")
	}
	switch p.Redaction {
	case RedactionNotRequired, RedactionApplied, RedactionUnavailable:
	default:
		return errors.New("observation redaction status is invalid")
	}
	if (p.Class == DataCredential || p.Class == DataRestricted) &&
		p.Redaction == RedactionNotRequired {
		return errors.New("sensitive observation data requires redaction status")
	}
	return nil
}

func (p PayloadRef) Validate() error {
	if !payloadDigestPattern.MatchString(p.Digest) ||
		strings.TrimSpace(p.MediaType) == "" {
		return errors.New("observation payload reference is invalid")
	}
	policy := DataPolicy{Class: p.DataClass, Redaction: p.Redaction}
	return policy.Validate()
}

func validatePayloadPolicy(
	policy PayloadPolicy,
	data DataPolicy,
	payload *PayloadRef,
) error {
	switch policy {
	case PayloadForbidden:
		if payload != nil {
			return errors.New("observation kind forbids payload")
		}
	case PayloadRequired:
		if payload == nil {
			return errors.New("observation kind requires payload")
		}
	case PayloadOptionalSensitive:
		if payload != nil && data.Redaction == RedactionUnavailable {
			return errors.New("sensitive observation payload must be redacted")
		}
	case PayloadOptional:
	default:
		return errors.New("observation payload policy is invalid")
	}
	if payload != nil {
		if data.Class == DataCredential || data.Class == DataRestricted {
			return errors.New("credential or restricted payload must not be persisted")
		}
		if payload.DataClass != data.Class || payload.Redaction != data.Redaction {
			return errors.New("observation payload policy does not match payload reference")
		}
	}
	return nil
}
