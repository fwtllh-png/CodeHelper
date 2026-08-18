package observation

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
)

type AdmissionStatus string

const (
	AdmissionAccepted       AdmissionStatus = "accepted"
	AdmissionPayloadDropped AdmissionStatus = "payload_dropped"
	AdmissionQueueFull      AdmissionStatus = "queue_full"
	AdmissionWriterFailed   AdmissionStatus = "writer_failed"
	AdmissionDisabled       AdmissionStatus = "disabled"
)

type AdmissionReceipt struct {
	Status AdmissionStatus `json:"status"`
	ID     ObservationID   `json:"id,omitempty"`
}

type Recorder interface {
	Record(context.Context, Record) AdmissionReceipt
}

type Projector interface {
	Project(Envelope)
	ForceFlush(context.Context) error
	Shutdown(context.Context) error
}

// Record is a pre-journal observation. The Router owns ID, sequence, payload
// admission, and persistence so producers cannot create dangling references.
type Record struct {
	Kind      Kind
	Identity  Identity
	Trace     *TraceContext
	Causality *Causality
	Policy    DataPolicy
	Payload   *Payload
	Summary   json.RawMessage
}

type Payload struct {
	Data      []byte
	MediaType string
	Encoding  string
	Truncated bool
	DataClass DataClass
	Redaction RedactionStatus
}

type IDGenerator struct {
	prefix  [8]byte
	counter atomic.Uint64
}

func NewIDGenerator() (*IDGenerator, error) {
	generator := &IDGenerator{}
	if _, err := rand.Read(generator.prefix[:]); err != nil {
		return nil, fmt.Errorf("generate observation id prefix: %w", err)
	}
	return generator, nil
}

func (g *IDGenerator) Next() ObservationID {
	if g == nil {
		return ""
	}
	var raw [16]byte
	copy(raw[:8], g.prefix[:])
	binary.BigEndian.PutUint64(raw[8:], g.counter.Add(1))
	var encoded [36]byte
	copy(encoded[:4], "obs_")
	hex.Encode(encoded[4:], raw[:])
	return ObservationID(string(encoded[:]))
}

func NewRuntimeID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate runtime id: %w", err)
	}
	return "runtime_" + hex.EncodeToString(value[:]), nil
}

func (r Record) Validate() error {
	traits, ok := traitsFor(r.Kind)
	if !ok {
		return fmt.Errorf("observation kind %q has no traits", r.Kind)
	}
	if err := r.Identity.validate(traits); err != nil {
		return err
	}
	if r.Trace != nil {
		if err := r.Trace.Validate(); err != nil {
			return err
		}
	}
	if r.Causality != nil {
		if err := r.Causality.Validate(""); err != nil {
			return err
		}
	}
	if err := r.Policy.Validate(); err != nil {
		return err
	}
	switch traits.Payload {
	case PayloadForbidden:
		if r.Payload != nil {
			return errors.New("observation kind forbids payload")
		}
	case PayloadRequired:
		if r.Payload == nil {
			return errors.New("observation kind requires payload")
		}
	case PayloadOptional, PayloadOptionalSensitive:
	default:
		return errors.New("observation payload policy is invalid")
	}
	if r.Payload != nil {
		if strings.TrimSpace(r.Payload.MediaType) == "" {
			return errors.New("observation payload media type is required")
		}
		if r.Payload.DataClass != r.Policy.Class ||
			r.Payload.Redaction != r.Policy.Redaction {
			return errors.New("observation payload policy does not match record")
		}
		if r.Payload.DataClass == DataCredential ||
			r.Payload.DataClass == DataRestricted {
			return errors.New("credential or restricted payload must not be persisted")
		}
	}
	if len(r.Summary) > maxSummarySize ||
		(len(r.Summary) != 0 && !json.Valid(r.Summary)) {
		return errors.New("observation summary is invalid")
	}
	if (r.Kind == KindTurnTerminalPrepared ||
		r.Kind == KindTurnTerminalCommitted) &&
		len(r.Summary) != 0 {
		if _, err := DecodeTerminalSummary(r.Summary); err != nil {
			return err
		}
	}
	return nil
}

func (r Record) Clone() Record {
	cloned := r
	if r.Trace != nil {
		trace := *r.Trace
		cloned.Trace = &trace
	}
	if r.Causality != nil {
		causality := *r.Causality
		causality.Links = append([]Link(nil), r.Causality.Links...)
		cloned.Causality = &causality
	}
	if r.Payload != nil {
		payload := *r.Payload
		payload.Data = append([]byte(nil), r.Payload.Data...)
		cloned.Payload = &payload
	}
	cloned.Summary = append(json.RawMessage(nil), r.Summary...)
	return cloned
}
