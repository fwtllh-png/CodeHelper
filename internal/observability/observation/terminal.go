package observation

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type TerminalStatus string

const (
	TerminalCompleted TerminalStatus = "completed"
	TerminalFailed    TerminalStatus = "failed"
	TerminalCanceled  TerminalStatus = "canceled"
)

// TerminalOutcome is the bounded, non-prose failure evidence retained in an
// observation summary. Raw error messages remain outside the observation
// journal because they may contain workspace content or credentials.
type TerminalOutcome struct {
	Status TerminalStatus          `json:"status"`
	Code   string                  `json:"code,omitempty"`
	Fault  *protocol.FaultMetadata `json:"fault,omitempty"`
}

type TerminalSummary struct {
	MeasurementDigest string           `json:"measurement_digest,omitempty"`
	Outcome           *TerminalOutcome `json:"outcome,omitempty"`
}

func EncodeTerminalSummary(
	measurementDigest string,
	outcome TerminalOutcome,
) (json.RawMessage, error) {
	summary := TerminalSummary{
		MeasurementDigest: measurementDigest,
		Outcome:           &outcome,
	}
	if err := summary.Validate(); err != nil {
		return nil, err
	}
	return json.Marshal(summary)
}

func DecodeTerminalSummary(raw json.RawMessage) (TerminalSummary, error) {
	var summary TerminalSummary
	if len(raw) == 0 {
		return summary, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&summary); err != nil {
		return TerminalSummary{}, fmt.Errorf("decode terminal summary: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return TerminalSummary{}, errors.New(
			"decode terminal summary: trailing JSON value",
		)
	}
	if err := summary.Validate(); err != nil {
		return TerminalSummary{}, err
	}
	return summary, nil
}

func (s TerminalSummary) Validate() error {
	if len(s.MeasurementDigest) > maxIdentitySize {
		return errors.New("terminal measurement digest is too large")
	}
	if s.Outcome == nil {
		return nil
	}
	switch s.Outcome.Status {
	case TerminalCompleted:
		if s.Outcome.Code != "" || s.Outcome.Fault != nil {
			return errors.New("completed terminal outcome contains failure metadata")
		}
	case TerminalFailed, TerminalCanceled:
		if strings.TrimSpace(s.Outcome.Code) == "" ||
			len(s.Outcome.Code) > 64 {
			return errors.New("failed terminal outcome code is invalid")
		}
		switch protocol.ErrorCode(s.Outcome.Code) {
		case protocol.CodeInvalidArgument,
			protocol.CodeConflict,
			protocol.CodeResourceExhausted,
			protocol.CodeUnavailable,
			protocol.CodeCanceled,
			protocol.CodeDeadlineExceeded,
			protocol.CodeInternal:
		default:
			return errors.New("failed terminal outcome code is unsupported")
		}
	default:
		return errors.New("terminal outcome status is invalid")
	}
	if s.Outcome.Fault != nil {
		if len(s.Outcome.Fault.OperationID) > maxIdentitySize ||
			len(s.Outcome.Fault.RecoveryAction) > maxIdentitySize {
			return errors.New("terminal fault metadata is too large")
		}
	}
	return nil
}
