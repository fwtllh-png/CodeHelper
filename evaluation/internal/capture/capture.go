package capture

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
)

const maxCaptureLineBytes = 8 << 20

type Format string

const (
	FormatVSCodeRuntime Format = "vscode_runtime_capture_v1"
	FormatProvider      Format = "provider_stream_v1"
	FormatObservation   Format = "observation_journal_v1"
)

type SanitizerOptions struct {
	Secrets         []string
	RestrictedPaths []string
}

type Slice struct {
	Kind        string
	Index       int
	Events      []evidence.RawEnvelope
	Signature   string
	SourceCount int
}

type vscodeEnvelope struct {
	Version         int             `json:"version"`
	CaptureID       string          `json:"capture_id"`
	CaptureSequence uint64          `json:"capture_sequence"`
	CapturedAt      string          `json:"captured_at"`
	Kind            string          `json:"kind"`
	Data            json.RawMessage `json:"data"`
}

func Read(path string, format Format) ([]evidence.RawEnvelope, error) {
	events, _, err := ReadWithDigest(path, format)
	return events, err
}

func ReadWithDigest(
	path string,
	format Format,
) ([]evidence.RawEnvelope, string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, "", err
	}
	defer file.Close()
	digest := sha256.New()
	reader := io.TeeReader(file, digest)
	var events []evidence.RawEnvelope
	switch format {
	case FormatVSCodeRuntime:
		events, err = decodeVSCodeRuntime(reader)
	case FormatProvider:
		events, err = decodeProvider(reader)
	case FormatObservation:
		events, err = decodeObservation(reader)
	default:
		return nil, "", fmt.Errorf("unsupported capture format %q", format)
	}
	if err != nil {
		return nil, "", err
	}
	return events, "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func decodeVSCodeRuntime(reader io.Reader) ([]evidence.RawEnvelope, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxCaptureLineBytes)
	var result []evidence.RawEnvelope
	var captureID string
	var lastSequence uint64
	line := 0
	for scanner.Scan() {
		line++
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		var envelope vscodeEnvelope
		if err := decodeStrict(raw, &envelope); err != nil {
			return nil, fmt.Errorf("decode VS Code capture line %d: %w", line, err)
		}
		if envelope.Version != 1 || envelope.CaptureID == "" ||
			envelope.CaptureSequence != lastSequence+1 ||
			evidence.NormalizeKind(envelope.Kind) == "" {
			return nil, fmt.Errorf("VS Code capture line %d has an invalid envelope", line)
		}
		if _, err := time.Parse(time.RFC3339Nano, envelope.CapturedAt); err != nil {
			return nil, fmt.Errorf("VS Code capture line %d timestamp: %w", line, err)
		}
		if captureID == "" {
			captureID = envelope.CaptureID
		} else if envelope.CaptureID != captureID {
			return nil, fmt.Errorf("VS Code capture line %d changes capture_id", line)
		}
		lastSequence = envelope.CaptureSequence
		converted, err := convertVSCode(envelope)
		if err != nil {
			return nil, fmt.Errorf("convert VS Code capture line %d: %w", line, err)
		}
		result = append(result, converted)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("VS Code capture is empty")
	}
	return result, nil
}

func convertVSCode(envelope vscodeEnvelope) (evidence.RawEnvelope, error) {
	var data map[string]any
	if err := json.Unmarshal(envelope.Data, &data); err != nil {
		return evidence.RawEnvelope{}, fmt.Errorf("data must be a JSON object: %w", err)
	}
	result := evidence.RawEnvelope{
		ObservedSequence: envelope.CaptureSequence,
		ObservedAt:       envelope.CapturedAt,
		Source:           evidence.SourceHost,
		Kind:             evidence.NormalizeKind(envelope.Kind),
		Redacted:         true,
		Identity: evidence.Identity{
			Capture: envelope.CaptureID,
		},
	}
	switch {
	case envelope.Kind == "runtime.event":
		event, ok := object(data["event"])
		if !ok {
			return evidence.RawEnvelope{}, errors.New("runtime.event has no event object")
		}
		result.Source = evidence.SourceRuntime
		result.Kind = stringValue(event, "kind")
		if result.Kind == "" {
			return evidence.RawEnvelope{}, errors.New("runtime.event kind is required")
		}
		result.Kind = evidence.NormalizeKind(result.Kind)
		result.Identity = mergeIdentity(result.Identity, identityFrom(event))
		result.Identity.Session = stringValue(data, "session_id")
		result.Data = map[string]any{
			"event_sequence": integerValue(event, "sequence"),
			"event_version":  integerValue(event, "version"),
			"replayed":       booleanValue(data, "replayed"),
			"data_shape":     shapeOf(event["data"], 0),
		}
	case strings.HasPrefix(envelope.Kind, "acp.request."):
		result.Source = evidence.SourceACP
		result.Identity = mergeIdentity(result.Identity, identityFrom(data))
		if request := integerValue(data, "request_id"); request != nil {
			result.Identity.Request = "request:" + fmt.Sprint(request)
		}
		result.Data = compactMap(map[string]any{
			"method":         safeProtocolValue(stringValue(data, "method")),
			"duration_ms":    integerValue(data, "duration_ms"),
			"operation_kind": safeProtocolValue(stringValue(data, "operation_kind")),
			"error_dropped":  envelope.Kind == "acp.request.failed",
		})
	case envelope.Kind == "runtime.state":
		result.Data = compactMap(map[string]any{
			"state":                   safeProtocolValue(stringValue(data, "state")),
			"restart_attempt":         firstInteger(data, "restartAttempt", "restart_attempt"),
			"consecutive_failures":    firstInteger(data, "consecutiveFailures", "consecutive_failures"),
			"error_dropped":           hasNonEmpty(data, "error"),
			"next_restart_ms_present": hasNonEmpty(data, "nextRestartAt"),
		})
	case envelope.Kind == "runtime.exit":
		result.Data = compactMap(map[string]any{
			"code":   integerValue(data, "code"),
			"signal": safeProtocolValue(stringValue(data, "signal")),
		})
	case envelope.Kind == "runtime.stderr":
		result.Data = map[string]any{
			"content_dropped": true,
			"original_bytes":  len(stringValue(data, "text")),
		}
	case envelope.Kind == "session.error":
		result.Data = map[string]any{
			"content_dropped": true,
			"original_bytes":  len(stringValue(data, "message")),
		}
	default:
		result.Identity = mergeIdentity(result.Identity, identityFrom(data))
		result.Data = map[string]any{"data_shape": shapeOf(data, 0)}
	}
	return result, nil
}

func decodeProvider(reader io.Reader) ([]evidence.RawEnvelope, error) {
	values, err := decodeGenericJSONL(reader)
	if err != nil {
		return nil, err
	}
	result := make([]evidence.RawEnvelope, 0, len(values))
	for index, value := range values {
		kind := safeProtocolValue(stringValue(value, "type"))
		if kind == "" {
			return nil, fmt.Errorf("provider event line %d has no type", index+1)
		}
		identity := identityFrom(value)
		if identity.Event == "" {
			identity.Event = stringValue(value, "event_id")
		}
		toolCall, _ := object(value["tool_call"])
		usage, _ := object(value["usage"])
		result = append(result, evidence.RawEnvelope{
			ObservedSequence: uint64(index + 1),
			Source:           evidence.SourceProvider,
			Kind:             evidence.NormalizeKind(kind),
			Redacted:         true,
			Identity:         identity,
			Data: compactMap(map[string]any{
				"sequenced":         booleanValue(value, "sequenced"),
				"wire_sequence":     integerValue(value, "sequence"),
				"ordinal":           integerValue(value, "ordinal"),
				"stop_reason":       safeProtocolValue(stringValue(value, "stop_reason")),
				"index":             integerValue(value, "index"),
				"text_bytes":        len(stringValue(value, "text")),
				"signature_bytes":   len(stringValue(value, "signature")),
				"tool_name_present": stringValue(toolCall, "name") != "",
				"arguments_bytes":   len(stringValue(toolCall, "arguments")),
				"input_tokens":      integerValue(usage, "input_tokens"),
				"output_tokens":     integerValue(usage, "output_tokens"),
			}),
		})
	}
	return result, nil
}

func decodeObservation(reader io.Reader) ([]evidence.RawEnvelope, error) {
	values, err := decodeGenericJSONL(reader)
	if err != nil {
		return nil, err
	}
	result := make([]evidence.RawEnvelope, 0, len(values))
	for index, record := range values {
		envelope, ok := object(record["envelope"])
		if !ok {
			envelope = record
		}
		identity, _ := object(envelope["identity"])
		kind := safeProtocolValue(stringValue(envelope, "kind"))
		if kind == "" {
			return nil, fmt.Errorf("observation line %d has no kind", index+1)
		}
		result = append(result, evidence.RawEnvelope{
			ObservedSequence: uint64Value(envelope, "sequence", uint64(index+1)),
			ObservedAt:       stringValue(envelope, "recorded_at"),
			Source:           evidence.SourceObservation,
			Kind:             evidence.NormalizeKind(kind),
			Redacted:         true,
			Identity:         identityFrom(identity),
			Data: map[string]any{
				"summary_shape":   shapeOf(envelope["summary"], 0),
				"payload_present": envelope["payload"] != nil,
			},
		})
	}
	return result, nil
}

func Slices(events []evidence.RawEnvelope) []Slice {
	if len(events) == 0 {
		return nil
	}
	result := []Slice{{
		Kind:        "full",
		Index:       1,
		Events:      append([]evidence.RawEnvelope(nil), events...),
		Signature:   FailureSignature(events),
		SourceCount: len(events),
	}}
	var operations []string
	for _, event := range events {
		if event.Identity.Operation != "" &&
			!slices.Contains(operations, event.Identity.Operation) {
			operations = append(operations, event.Identity.Operation)
		}
	}
	for index, operation := range operations {
		requests := make(map[string]bool)
		for _, event := range events {
			if event.Identity.Operation == operation && event.Identity.Request != "" {
				requests[event.Identity.Request] = true
			}
		}
		var selected []evidence.RawEnvelope
		for _, event := range events {
			if event.Identity.Operation == operation ||
				(event.Identity.Request != "" && requests[event.Identity.Request]) {
				selected = append(selected, event)
			}
		}
		if len(selected) != 0 {
			result = append(result, Slice{
				Kind: "operation", Index: index + 1, Events: selected,
				Signature: FailureSignature(selected), SourceCount: len(selected),
			})
		}
	}
	requestState := make(map[string]int)
	requestEvents := make(map[string][]evidence.RawEnvelope)
	var requestOrder []string
	for _, event := range events {
		if event.Source != evidence.SourceACP || event.Identity.Request == "" {
			continue
		}
		request := event.Identity.Request
		if _, exists := requestState[request]; !exists {
			requestOrder = append(requestOrder, request)
		}
		requestEvents[request] = append(requestEvents[request], event)
		switch event.Kind {
		case "acp.request.started":
			requestState[request]++
		case "acp.request.completed", "acp.request.failed":
			requestState[request]--
		}
	}
	orphanIndex := 0
	for _, request := range requestOrder {
		if requestState[request] <= 0 {
			continue
		}
		orphanIndex++
		selected := requestEvents[request]
		result = append(result, Slice{
			Kind: "orphan_request", Index: orphanIndex, Events: selected,
			Signature: "acp_request_incomplete", SourceCount: len(selected),
		})
	}
	return result
}

func FailureSignature(events []evidence.RawEnvelope) string {
	for index := len(events) - 1; index >= 0; index-- {
		switch events[index].Kind {
		case "turn.failed":
			return "turn_failed"
		case "turn.cancelled":
			return "turn_cancelled"
		case "turn.completed":
			return "turn_completed"
		case "acp.request.failed":
			return "acp_request_failed"
		}
	}
	return "partial_trace"
}

func Canonicalize(
	raw []evidence.RawEnvelope,
	options SanitizerOptions,
) ([]evidence.Envelope, error) {
	if len(raw) == 0 {
		return nil, errors.New("cannot canonicalize an empty capture")
	}
	aliases := newAliases()
	var firstTime time.Time
	var previousRuntimeBySession = make(map[string]uint64)
	var startedRequest = make(map[string]uint64)
	var previousByOperation = make(map[string]uint64)
	events := make([]evidence.Envelope, 0, len(raw))
	var lastObserved uint64
	for _, item := range raw {
		if item.ObservedSequence <= lastObserved {
			return nil, errors.New("capture observed sequence is not increasing")
		}
		lastObserved = item.ObservedSequence
		offset, observed, err := relativeOffset(item.ObservedAt, firstTime, len(events))
		if err != nil {
			return nil, err
		}
		if firstTime.IsZero() && !observed.IsZero() {
			firstTime = observed
		}
		sanitized, redaction, err := sanitizeData(item.Data, options)
		if err != nil {
			return nil, err
		}
		if item.Redacted {
			redaction = evidence.RedactionApplied
		}
		identity := aliases.Identity(item.Identity)
		event := evidence.Envelope{
			OffsetMS: offset,
			Source:   item.Source,
			Kind:     item.Kind,
			Identity: identity,
			Policy: evidence.Policy{
				Class: evidence.DataOperational, Redaction: redaction,
			},
			Data: sanitized,
		}
		sequence := uint64(len(events) + 1)
		if identity.Request != "" {
			if item.Kind == "acp.request.started" {
				startedRequest[identity.Request] = sequence
			} else if parent := startedRequest[identity.Request]; parent != 0 {
				event.Causality.ParentSequence = parent
			}
		}
		if item.Source == evidence.SourceRuntime && identity.Session != "" {
			if parent := previousRuntimeBySession[identity.Session]; parent != 0 {
				event.Causality.ParentSequence = parent
			}
			previousRuntimeBySession[identity.Session] = sequence
		}
		if identity.Operation != "" {
			if previous := previousByOperation[identity.Operation]; previous != 0 &&
				previous != event.Causality.ParentSequence {
				event.Causality.Links = append(
					event.Causality.Links,
					previous,
				)
			}
			previousByOperation[identity.Operation] = sequence
		}
		events = append(events, event)
	}
	sealed, err := evidence.Seal(events)
	if err != nil {
		return nil, err
	}
	content, err := evidence.EncodeJSONL(sealed)
	if err != nil {
		return nil, err
	}
	if err := Scan(content, options); err != nil {
		return nil, err
	}
	return sealed, nil
}

func relativeOffset(value string, first time.Time, index int) (int64, time.Time, error) {
	if value == "" {
		return int64(index), time.Time{}, nil
	}
	observed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("parse capture timestamp: %w", err)
	}
	if first.IsZero() {
		return 0, observed, nil
	}
	offset := observed.Sub(first).Milliseconds()
	if offset < 0 {
		return 0, time.Time{}, errors.New("capture timestamp is out of order")
	}
	return offset, observed, nil
}

type aliasSet struct {
	values map[string]map[string]string
	counts map[string]int
}

func newAliases() *aliasSet {
	return &aliasSet{
		values: make(map[string]map[string]string),
		counts: make(map[string]int),
	}
}

func (a *aliasSet) alias(kind, value string) string {
	if value == "" {
		return ""
	}
	values := a.values[kind]
	if values == nil {
		values = make(map[string]string)
		a.values[kind] = values
	}
	if alias := values[value]; alias != "" {
		return alias
	}
	a.counts[kind]++
	alias := fmt.Sprintf("%s-%03d", kind, a.counts[kind])
	values[value] = alias
	return alias
}

func (a *aliasSet) Identity(raw evidence.Identity) evidence.Identity {
	return evidence.Identity{
		Capture:   a.alias("capture", raw.Capture),
		Session:   a.alias("session", raw.Session),
		Thread:    a.alias("thread", raw.Thread),
		Turn:      a.alias("turn", raw.Turn),
		Operation: a.alias("operation", raw.Operation),
		Item:      a.alias("item", raw.Item),
		Event:     a.alias("event", raw.Event),
		Effect:    a.alias("effect", raw.Effect),
		Attempt:   a.alias("attempt", raw.Attempt),
		Request:   a.alias("request", raw.Request),
		Run:       a.alias("run", raw.Run),
		Node:      a.alias("node", raw.Node),
		Resume:    a.alias("resume", raw.Resume),
		Call:      a.alias("call", raw.Call),
		Sample:    a.alias("sample", raw.Sample),
	}
}

func decodeGenericJSONL(reader io.Reader) ([]map[string]any, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxCaptureLineBytes)
	var result []map[string]any
	line := 0
	for scanner.Scan() {
		line++
		var value map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &value); err != nil {
			return nil, fmt.Errorf("decode capture line %d: %w", line, err)
		}
		result = append(result, value)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, errors.New("capture is empty")
	}
	return result, nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("line contains multiple JSON values")
	}
	return nil
}

func identityFrom(value map[string]any) evidence.Identity {
	return evidence.Identity{
		Session:   firstString(value, "session_id", "sessionId"),
		Thread:    firstString(value, "thread_id", "threadId"),
		Turn:      firstString(value, "turn_id", "turnId"),
		Operation: firstString(value, "operation_id", "operationId"),
		Item:      firstString(value, "item_id", "itemId"),
		Event:     firstString(value, "event_id", "eventId", "id"),
		Effect:    firstString(value, "effect_id", "effectId"),
		Attempt:   firstString(value, "attempt_id", "attemptId"),
		Run:       firstString(value, "run_id", "runId"),
		Node:      firstString(value, "node_id", "nodeId"),
		Resume:    firstString(value, "resume_id", "resumeId"),
		Call:      firstString(value, "call_id", "callId"),
		Sample:    firstString(value, "sample_id", "sampleId"),
	}
}

func mergeIdentity(left, right evidence.Identity) evidence.Identity {
	if right.Session != "" {
		left.Session = right.Session
	}
	if right.Thread != "" {
		left.Thread = right.Thread
	}
	if right.Turn != "" {
		left.Turn = right.Turn
	}
	if right.Operation != "" {
		left.Operation = right.Operation
	}
	if right.Item != "" {
		left.Item = right.Item
	}
	if right.Event != "" {
		left.Event = right.Event
	}
	if right.Effect != "" {
		left.Effect = right.Effect
	}
	if right.Attempt != "" {
		left.Attempt = right.Attempt
	}
	if right.Run != "" {
		left.Run = right.Run
	}
	if right.Node != "" {
		left.Node = right.Node
	}
	if right.Resume != "" {
		left.Resume = right.Resume
	}
	if right.Call != "" {
		left.Call = right.Call
	}
	if right.Sample != "" {
		left.Sample = right.Sample
	}
	return left
}

func shapeOf(value any, depth int) any {
	if depth >= 4 {
		return "depth_limited"
	}
	switch typed := value.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case float64, json.Number:
		return "number"
	case string:
		return map[string]any{"type": "string", "bytes": len(typed)}
	case []any:
		items := make([]any, 0, min(len(typed), 3))
		for index := 0; index < len(typed) && index < 3; index++ {
			items = append(items, shapeOf(typed[index], depth+1))
		}
		return map[string]any{
			"type": "array", "length": len(typed), "sample": items,
		}
	case map[string]any:
		shapes := make([]string, 0, len(typed))
		for key, item := range typed {
			if sensitiveKey.MatchString(key) {
				continue
			}
			encoded, _ := json.Marshal(shapeOf(item, depth+1))
			shapes = append(shapes, string(encoded))
		}
		slices.Sort(shapes)
		samples := make([]json.RawMessage, 0, min(len(shapes), 8))
		for index := 0; index < len(shapes) && index < 8; index++ {
			samples = append(samples, json.RawMessage(shapes[index]))
		}
		return map[string]any{
			"type": "object", "field_count": len(typed), "value_shapes": samples,
		}
	default:
		return "unknown"
	}
}

func object(value any) (map[string]any, bool) {
	result, ok := value.(map[string]any)
	return result, ok
}

func stringValue(value map[string]any, key string) string {
	if value == nil {
		return ""
	}
	result, _ := value[key].(string)
	return result
}

func firstString(value map[string]any, keys ...string) string {
	for _, key := range keys {
		if result := stringValue(value, key); result != "" {
			return result
		}
	}
	return ""
}

func integerValue(value map[string]any, key string) any {
	if value == nil {
		return nil
	}
	switch typed := value[key].(type) {
	case float64:
		return int64(typed)
	case json.Number:
		result, _ := typed.Int64()
		return result
	case int:
		return int64(typed)
	case int64:
		return typed
	default:
		return nil
	}
}

func firstInteger(value map[string]any, keys ...string) any {
	for _, key := range keys {
		if result := integerValue(value, key); result != nil {
			return result
		}
	}
	return nil
}

func uint64Value(value map[string]any, key string, fallback uint64) uint64 {
	switch typed := integerValue(value, key).(type) {
	case int64:
		if typed > 0 {
			return uint64(typed)
		}
	}
	return fallback
}

func booleanValue(value map[string]any, key string) any {
	if value == nil {
		return nil
	}
	if result, ok := value[key].(bool); ok {
		return result
	}
	return nil
}

func hasNonEmpty(value map[string]any, key string) bool {
	item, exists := value[key]
	if !exists || item == nil {
		return false
	}
	if text, ok := item.(string); ok {
		return text != ""
	}
	return true
}

func compactMap(value map[string]any) map[string]any {
	for key, item := range value {
		if item == nil || item == "" || item == false || item == 0 {
			delete(value, key)
		}
	}
	return value
}

func safeProtocolValue(value string) string {
	value = strings.TrimSpace(value)
	if protocolValuePattern.MatchString(value) {
		return value
	}
	return ""
}

var protocolValuePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,95}$`)
