// Package privacy owns capture admission and redaction before observation
// payloads can reach durable storage.
package privacy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/observability/telemetry"
)

type CaptureMode string

const (
	CaptureOff      CaptureMode = "off"
	CaptureMetadata CaptureMode = "metadata"
	CaptureFailure  CaptureMode = "failure"
	CaptureFull     CaptureMode = "full"
)

const redacted = "[REDACTED]"

var sensitiveKey = regexp.MustCompile(
	`(?i)(authorization|api[_-]?key|credential|password|secret|token|cookie)`,
)

type Options struct {
	Mode            CaptureMode
	Secrets         []string
	RestrictedPaths []string
}

type Result struct {
	Record         observation.Record
	Disabled       bool
	PayloadDropped bool
}

type Policy struct {
	mode            CaptureMode
	redactor        telemetry.Redactor
	restrictedPaths []string
}

func New(options Options) (*Policy, error) {
	mode := options.Mode
	if mode == "" {
		mode = CaptureMetadata
	}
	switch mode {
	case CaptureOff, CaptureMetadata, CaptureFailure, CaptureFull:
	default:
		return nil, fmt.Errorf("invalid observation capture mode %q", mode)
	}
	paths := make([]string, 0, len(options.RestrictedPaths))
	for _, path := range options.RestrictedPaths {
		path = strings.TrimSpace(path)
		if path != "" && !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}
	slices.SortFunc(paths, func(left, right string) int {
		return len(right) - len(left)
	})
	return &Policy{
		mode:            mode,
		redactor:        telemetry.NewRedactor(options.Secrets...),
		restrictedPaths: paths,
	}, nil
}

func ParseMode(value string) (CaptureMode, error) {
	mode := CaptureMode(strings.ToLower(strings.TrimSpace(value)))
	if mode == "" {
		return CaptureMetadata, nil
	}
	switch mode {
	case CaptureOff, CaptureMetadata, CaptureFailure, CaptureFull:
		return mode, nil
	default:
		return "", fmt.Errorf("invalid observation capture mode %q", value)
	}
}

func (p *Policy) Mode() CaptureMode {
	if p == nil {
		return CaptureMetadata
	}
	return p.mode
}

func (p *Policy) Apply(record observation.Record) (Result, error) {
	if p == nil {
		return Result{Record: record.Clone()}, nil
	}
	if p.mode == CaptureOff {
		return Result{Disabled: true}, nil
	}
	result := Result{Record: record.Clone()}
	summary, err := p.redactJSON(result.Record.Summary)
	if err != nil {
		return Result{}, fmt.Errorf("redact observation summary: %w", err)
	}
	result.Record.Summary = summary
	if result.Record.Payload == nil {
		result.Record.Policy.Redaction = p.redactionStatus(
			result.Record.Policy.Class,
		)
		return result, nil
	}
	payload := result.Record.Payload
	if !p.capturePayload(result.Record) {
		result.Record.Payload = nil
		result.Record.Policy.Redaction = p.redactionStatus(
			result.Record.Policy.Class,
		)
		result.PayloadDropped = true
		return result, nil
	}
	redactedPayload, err := p.redactPayload(
		payload.Data,
		payload.MediaType,
	)
	if err != nil {
		return Result{}, fmt.Errorf("redact observation payload: %w", err)
	}
	payload.Data = redactedPayload
	payload.Redaction = observation.RedactionApplied
	result.Record.Policy.Redaction = observation.RedactionApplied
	return result, nil
}

// Sanitize implements the Router admission boundary without making the Router
// depend on privacy implementation details.
func (p *Policy) Sanitize(
	record observation.Record,
) (observation.Record, bool, bool, error) {
	result, err := p.Apply(record)
	return result.Record, result.Disabled, result.PayloadDropped, err
}

func (p *Policy) RedactBytes(data []byte, mediaType string) ([]byte, error) {
	if p == nil {
		return append([]byte(nil), data...), nil
	}
	return p.redactPayload(data, mediaType)
}

func (p *Policy) capturePayload(record observation.Record) bool {
	class := record.Policy.Class
	if class == observation.DataCredential ||
		class == observation.DataRestricted {
		return false
	}
	switch p.mode {
	case CaptureFull:
		return true
	case CaptureFailure:
		return failureKind(record.Kind)
	default:
		return false
	}
}

func (p *Policy) redactionStatus(
	class observation.DataClass,
) observation.RedactionStatus {
	if class == observation.DataCredential ||
		class == observation.DataRestricted {
		return observation.RedactionApplied
	}
	return observation.RedactionNotRequired
}

func failureKind(kind observation.Kind) bool {
	value := string(kind)
	return strings.Contains(value, "failed") ||
		strings.Contains(value, "crashed") ||
		strings.Contains(value, "denied") ||
		strings.Contains(value, "rejected") ||
		strings.Contains(value, "rolled_back")
}

func (p *Policy) redactPayload(
	data []byte,
	mediaType string,
) ([]byte, error) {
	if len(data) == 0 {
		return nil, nil
	}
	if strings.Contains(strings.ToLower(mediaType), "json") &&
		json.Valid(data) {
		return p.redactJSON(data)
	}
	return []byte(p.redactString(string(data))), nil
}

func (p *Policy) redactJSON(data []byte) ([]byte, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, nil
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, errors.New("content is not valid JSON")
	}
	value = p.redactValue("", value)
	return json.Marshal(value)
}

func (p *Policy) redactValue(key string, value any) any {
	if sensitiveKey.MatchString(key) {
		return redacted
	}
	switch typed := value.(type) {
	case string:
		return p.redactString(typed)
	case []any:
		for index := range typed {
			typed[index] = p.redactValue("", typed[index])
		}
	case map[string]any:
		for childKey, child := range typed {
			typed[childKey] = p.redactValue(childKey, child)
		}
	}
	return value
}

func (p *Policy) redactString(value string) string {
	value = p.redactor.Redact(value)
	for _, path := range p.restrictedPaths {
		value = strings.ReplaceAll(value, path, redacted)
	}
	return value
}
