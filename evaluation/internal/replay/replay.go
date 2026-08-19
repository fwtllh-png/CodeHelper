package replay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
)

type Level string

const (
	LevelStructural Level = "structural"
	LevelProvider   Level = "provider"
	LevelRuntime    Level = "runtime"
	LevelHost       Level = "host"
	LevelCrash      Level = "crash"
)

type LevelExecutor interface {
	Execute(context.Context, []evidence.Envelope) (Outcome, error)
}

type Coverage struct {
	Mutation MutationKind `json:"mutation"`
	Eligible int          `json:"eligible"`
	Executed int          `json:"executed"`
	Detected int          `json:"detected"`
}

type MutationKind string

const (
	MutationSplit     MutationKind = "split"
	MutationDelay     MutationKind = "delay"
	MutationDuplicate MutationKind = "duplicate"
	MutationTruncate  MutationKind = "truncate"
	MutationInterrupt MutationKind = "interrupt"
	MutationUnknown   MutationKind = "unknown_event"
	MutationMalformed MutationKind = "malformed_event"
)

type Mutation struct {
	Kind       MutationKind
	Sequence   uint64
	DelayMS    int64
	SplitBytes int
}

type Outcome struct {
	Level              Level
	Events             []evidence.Envelope
	Terminal           string
	FailureSignature   string
	DuplicateEvents    int
	IncompleteRequests int
	OrphanResponses    int
	Interrupted        bool
}

func ExecuteAt(
	ctx context.Context,
	level Level,
	events []evidence.Envelope,
	executors map[Level]LevelExecutor,
) (Outcome, error) {
	if level == LevelStructural {
		outcome, err := Execute(events)
		outcome.Level = level
		return outcome, err
	}
	executor := executors[level]
	if executor == nil {
		return Outcome{}, fmt.Errorf("Replay level %q has no production executor", level)
	}
	outcome, err := executor.Execute(ctx, events)
	if err != nil {
		return Outcome{}, err
	}
	outcome.Level = level
	return outcome, nil
}

func Execute(events []evidence.Envelope) (Outcome, error) {
	if err := evidence.ValidateAll(events); err != nil {
		return Outcome{}, err
	}
	outcome := Outcome{
		Level:  LevelStructural,
		Events: append([]evidence.Envelope(nil), events...),
	}
	seenDigests := make(map[string]struct{}, len(events))
	requests := make(map[string]int)
	var providerSequence uint64
	for _, event := range events {
		if _, exists := seenDigests[event.Digest]; exists {
			outcome.DuplicateEvents++
		}
		seenDigests[event.Digest] = struct{}{}
		if strings.HasSuffix(event.Kind, ".duplicate") {
			outcome.DuplicateEvents++
		}
		if event.Source == evidence.SourceACP && event.Identity.Request != "" {
			switch event.Kind {
			case "acp.request.started":
				requests[event.Identity.Request]++
			case "acp.request.completed", "acp.request.failed":
				if requests[event.Identity.Request] == 0 {
					outcome.OrphanResponses++
					continue
				}
				requests[event.Identity.Request]--
			}
		}
		if event.Source == evidence.SourceProvider {
			var data map[string]any
			if err := json.Unmarshal(event.Data, &data); err != nil {
				return Outcome{}, fmt.Errorf("decode provider evidence: %w", err)
			}
			if sequence, ok := number(data["wire_sequence"]); ok && sequence != 0 {
				if sequence <= providerSequence {
					return Outcome{}, errors.New("provider wire sequence is not increasing")
				}
				providerSequence = sequence
			}
		}
		switch event.Kind {
		case "turn.completed", "turn.failed", "turn.cancelled":
			if outcome.Terminal != "" {
				return Outcome{}, errors.New("replay contains multiple terminal events")
			}
			outcome.Terminal = event.Kind
		case "transport.interrupted":
			outcome.Interrupted = true
		}
	}
	for _, pending := range requests {
		if pending > 0 {
			outcome.IncompleteRequests += pending
		}
	}
	switch {
	case outcome.Interrupted:
		outcome.FailureSignature = "transport_interrupted"
	case outcome.Terminal == "turn.failed":
		outcome.FailureSignature = "turn_failed"
	case outcome.Terminal == "turn.cancelled":
		outcome.FailureSignature = "turn_cancelled"
	case outcome.Terminal == "turn.completed":
		outcome.FailureSignature = "turn_completed"
	case outcome.IncompleteRequests != 0:
		outcome.FailureSignature = "acp_request_incomplete"
	default:
		for index := len(events) - 1; index >= 0; index-- {
			if events[index].Kind == "acp.request.failed" {
				outcome.FailureSignature = "acp_request_failed"
				break
			}
		}
		if outcome.FailureSignature == "" {
			outcome.FailureSignature = "partial_trace"
		}
	}
	return outcome, nil
}

func VerifyMutationCoverage(
	events []evidence.Envelope,
	required []MutationKind,
) ([]Coverage, error) {
	var result []Coverage
	for _, kind := range required {
		coverage := Coverage{Mutation: kind}
		sequence, eligible := mutationTarget(events, kind)
		if eligible {
			coverage.Eligible = 1
		}
		if !eligible {
			return nil, fmt.Errorf("required mutation %q has zero eligible events", kind)
		}
		mutation := Mutation{Kind: kind, Sequence: sequence, DelayMS: 1}
		mutated, err := Mutate(events, mutation)
		coverage.Executed = 1
		if err != nil {
			return nil, fmt.Errorf("execute mutation %q: %w", kind, err)
		}
		outcome, err := Execute(mutated)
		if err != nil {
			if kind == MutationDuplicate {
				coverage.Detected = 1
				result = append(result, coverage)
				continue
			}
			return nil, fmt.Errorf("observe mutation %q: %w", kind, err)
		}
		switch kind {
		case MutationDuplicate:
			if outcome.DuplicateEvents > 0 {
				coverage.Detected = 1
			}
		case MutationInterrupt:
			if outcome.Interrupted {
				coverage.Detected = 1
			}
		default:
			coverage.Detected = 1
		}
		if coverage.Detected == 0 {
			return nil, fmt.Errorf("mutation %q executed but was not detected", kind)
		}
		result = append(result, coverage)
	}
	return result, nil
}

func CausalSlice(
	events []evidence.Envelope,
	targetSequence uint64,
) ([]evidence.Envelope, error) {
	if err := evidence.ValidateAll(events); err != nil {
		return nil, err
	}
	target, err := eventIndex(events, targetSequence)
	if err != nil {
		return nil, err
	}
	required := map[uint64]bool{events[target].Sequence: true}
	var visit func(uint64)
	visit = func(sequence uint64) {
		index, indexErr := eventIndex(events, sequence)
		if indexErr != nil {
			return
		}
		event := events[index]
		parents := append([]uint64(nil), event.Causality.Links...)
		if event.Causality.ParentSequence != 0 {
			parents = append(parents, event.Causality.ParentSequence)
		}
		for _, parent := range parents {
			if required[parent] {
				continue
			}
			required[parent] = true
			visit(parent)
		}
	}
	visit(events[target].Sequence)
	var selected []evidence.Envelope
	for _, event := range events {
		if required[event.Sequence] {
			selected = append(selected, event)
		}
	}
	return reseal(selected)
}

func mutationTarget(events []evidence.Envelope, kind MutationKind) (uint64, bool) {
	switch kind {
	case MutationSplit:
		for _, event := range events {
			if event.Source == evidence.SourceProvider {
				return event.Sequence, true
			}
		}
	case MutationTruncate:
		if len(events) > 1 {
			return events[len(events)-1].Sequence, true
		}
	case MutationDelay, MutationDuplicate, MutationInterrupt,
		MutationUnknown, MutationMalformed:
		if len(events) > 0 {
			return events[0].Sequence, true
		}
	}
	return 0, false
}

func Mutate(
	events []evidence.Envelope,
	mutation Mutation,
) ([]evidence.Envelope, error) {
	if err := evidence.ValidateAll(events); err != nil {
		return nil, err
	}
	switch mutation.Kind {
	case MutationSplit:
		return split(events, mutation)
	case MutationDelay:
		return delay(events, mutation)
	case MutationDuplicate:
		return duplicate(events, mutation)
	case MutationTruncate:
		return truncate(events, mutation)
	case MutationInterrupt:
		return interrupt(events, mutation)
	case MutationUnknown:
		return unknown(events, mutation)
	case MutationMalformed:
		return malformed(events, mutation)
	default:
		return nil, fmt.Errorf("unsupported replay mutation %q", mutation.Kind)
	}
}

func split(events []evidence.Envelope, mutation Mutation) ([]evidence.Envelope, error) {
	index, err := eventIndex(events, mutation.Sequence)
	if err != nil {
		return nil, err
	}
	event := events[index]
	if event.Source != evidence.SourceProvider {
		return nil, errors.New("split mutation requires a provider event")
	}
	data := append([]byte(nil), event.Data...)
	splitAt := mutation.SplitBytes
	if splitAt <= 0 || splitAt >= len(data) {
		splitAt = len(data) / 2
	}
	if splitAt <= 0 || splitAt >= len(data) {
		return nil, errors.New("provider event is too small to split")
	}
	left := event
	right := event
	left.Kind = event.Kind + ".fragment"
	right.Kind = event.Kind + ".fragment"
	left.Data = []byte(fmt.Sprintf(
		`{"fragment_index":1,"fragment_bytes":%d}`,
		splitAt,
	))
	right.Data = []byte(fmt.Sprintf(
		`{"fragment_index":2,"fragment_bytes":%d}`,
		len(data)-splitAt,
	))
	result := append([]evidence.Envelope(nil), events[:index]...)
	result = append(result, left, right)
	result = append(result, events[index+1:]...)
	return reseal(result)
}

func delay(events []evidence.Envelope, mutation Mutation) ([]evidence.Envelope, error) {
	index, err := eventIndex(events, mutation.Sequence)
	if err != nil {
		return nil, err
	}
	if mutation.DelayMS <= 0 {
		return nil, errors.New("delay mutation requires positive delay_ms")
	}
	result := append([]evidence.Envelope(nil), events...)
	for position := index; position < len(result); position++ {
		result[position].OffsetMS += mutation.DelayMS
	}
	return reseal(result)
}

func duplicate(events []evidence.Envelope, mutation Mutation) ([]evidence.Envelope, error) {
	index, err := eventIndex(events, mutation.Sequence)
	if err != nil {
		return nil, err
	}
	result := append([]evidence.Envelope(nil), events[:index+1]...)
	copy := events[index]
	copy.Kind += ".duplicate"
	result = append(result, copy)
	result = append(result, events[index+1:]...)
	return reseal(result)
}

func truncate(events []evidence.Envelope, mutation Mutation) ([]evidence.Envelope, error) {
	index, err := eventIndex(events, mutation.Sequence)
	if err != nil {
		return nil, err
	}
	if index == 0 {
		return nil, errors.New("truncate mutation cannot produce an empty trace")
	}
	return reseal(append([]evidence.Envelope(nil), events[:index]...))
}

func interrupt(events []evidence.Envelope, mutation Mutation) ([]evidence.Envelope, error) {
	index, err := eventIndex(events, mutation.Sequence)
	if err != nil {
		return nil, err
	}
	result := append([]evidence.Envelope(nil), events[:index]...)
	interrupted := evidence.Envelope{
		OffsetMS: events[index].OffsetMS,
		Source:   evidence.SourceHarness,
		Kind:     "transport.interrupted",
		Identity: events[index].Identity,
		Policy: evidence.Policy{
			Class: evidence.DataOperational, Redaction: evidence.RedactionNotRequired,
		},
		Data: []byte(`{"injected":true}`),
	}
	result = append(result, interrupted)
	return reseal(result)
}

func unknown(events []evidence.Envelope, mutation Mutation) ([]evidence.Envelope, error) {
	index, err := eventIndex(events, mutation.Sequence)
	if err != nil {
		return nil, err
	}
	result := append([]evidence.Envelope(nil), events[:index]...)
	unknown := evidence.Envelope{
		OffsetMS: events[index].OffsetMS,
		Source:   evidence.SourceProvider,
		Kind:     "provider.unknown_event",
		Identity: events[index].Identity,
		Policy: evidence.Policy{
			Class: evidence.DataOperational, Redaction: evidence.RedactionNotRequired,
		},
		Data: []byte(`{"future_version":true}`),
	}
	result = append(result, unknown)
	result = append(result, events[index:]...)
	return reseal(result)
}

func malformed(events []evidence.Envelope, mutation Mutation) ([]evidence.Envelope, error) {
	index, err := eventIndex(events, mutation.Sequence)
	if err != nil {
		return nil, err
	}
	result := append([]evidence.Envelope(nil), events[:index]...)
	malformed := evidence.Envelope{
		OffsetMS: events[index].OffsetMS,
		Source:   evidence.SourceProvider,
		Kind:     "provider.malformed_event",
		Identity: events[index].Identity,
		Policy: evidence.Policy{
			Class:     evidence.DataOperational,
			Redaction: evidence.RedactionNotRequired,
		},
		Data: []byte(`{"malformed":true}`),
	}
	result = append(result, malformed)
	result = append(result, events[index:]...)
	return reseal(result)
}

func eventIndex(events []evidence.Envelope, sequence uint64) (int, error) {
	index := slices.IndexFunc(events, func(event evidence.Envelope) bool {
		return event.Sequence == sequence
	})
	if index < 0 {
		return 0, fmt.Errorf("replay sequence %d does not exist", sequence)
	}
	return index, nil
}

func reseal(events []evidence.Envelope) ([]evidence.Envelope, error) {
	remap := make(map[uint64]uint64, len(events))
	for index, event := range events {
		if _, exists := remap[event.Sequence]; !exists {
			remap[event.Sequence] = uint64(index + 1)
		}
	}
	for index := range events {
		event := &events[index]
		if event.Causality.ParentSequence != 0 {
			event.Causality.ParentSequence = remap[event.Causality.ParentSequence]
		}
		var links []uint64
		for _, link := range event.Causality.Links {
			if mapped := remap[link]; mapped != 0 {
				links = append(links, mapped)
			}
		}
		event.Causality.Links = links
		event.Digest = ""
		event.PreviousDigest = ""
	}
	return evidence.Seal(events)
}

func number(value any) (uint64, bool) {
	switch typed := value.(type) {
	case float64:
		if typed > 0 {
			return uint64(typed), true
		}
	case json.Number:
		number, err := typed.Int64()
		if err == nil && number > 0 {
			return uint64(number), true
		}
	}
	return 0, false
}
