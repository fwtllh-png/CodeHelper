package deepseek

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
)

const (
	dsmlToolCallsOpen  = "<｜｜DSML｜｜tool_calls>"
	dsmlToolCallsClose = "</｜｜DSML｜｜tool_calls>"
	dsmlInvokeClose    = "</｜｜DSML｜｜invoke>"
	dsmlParameterClose = "</｜｜DSML｜｜parameter>"
)

type dsmlStream struct {
	provider.Stream
	queue   []provider.StreamEvent
	pending string
	inDSML  bool
}

func (s *dsmlStream) Recv() (provider.StreamEvent, error) {
	for {
		if len(s.queue) != 0 {
			event := s.queue[0]
			s.queue = s.queue[1:]
			return event, nil
		}
		event, err := s.Stream.Recv()
		if err != nil {
			return provider.StreamEvent{}, err
		}
		if event.Type == provider.EventTextDelta {
			s.acceptText(event)
			continue
		}
		if event.Type == provider.EventMessageStop {
			if err := s.finish(event); err != nil {
				return provider.StreamEvent{}, err
			}
			continue
		}
		return event, nil
	}
}

func (s *dsmlStream) acceptText(event provider.StreamEvent) {
	if s.inDSML {
		s.pending += event.Text
		return
	}
	text := s.pending + event.Text
	s.pending = ""
	if index := strings.Index(text, dsmlToolCallsOpen); index >= 0 {
		s.queueText(event, text[:index])
		s.pending = text[index:]
		s.inDSML = true
		return
	}
	hold := markerPrefixSuffix(text, dsmlToolCallsOpen)
	s.queueText(event, text[:len(text)-hold])
	s.pending = text[len(text)-hold:]
}

func (s *dsmlStream) finish(stop provider.StreamEvent) error {
	if !s.inDSML {
		s.queueText(provider.StreamEvent{Index: -1}, s.pending)
		s.pending = ""
		s.queue = append(s.queue, stop)
		return nil
	}
	calls, err := parseDSMLToolCalls(s.pending)
	if err != nil {
		return streamFailure(
			provider.FailureMalformedResponse,
			"DeepSeek returned malformed DSML tool calls: "+err.Error(),
		)
	}
	for index := range calls {
		call := calls[index]
		s.queue = append(s.queue, provider.StreamEvent{
			Type: provider.EventToolCallDelta,
			ToolCall: &provider.ToolCallFragment{
				Index: index, ID: call.ID, Name: call.Name,
				Arguments: call.Arguments,
			},
		})
	}
	stop.StopReason = provider.StopReasonToolUse
	s.pending = ""
	s.inDSML = false
	s.queue = append(s.queue, stop)
	return nil
}

func (s *dsmlStream) queueText(source provider.StreamEvent, text string) {
	if text == "" {
		return
	}
	block := provider.ContentBlock{Type: provider.ContentText, Text: text}
	if source.Block != nil {
		block = *source.Block
		block.Text = text
	}
	s.queue = append(s.queue, provider.StreamEvent{
		Type: provider.EventTextDelta, Index: source.Index,
		Block: &block, Text: text,
	})
}

func markerPrefixSuffix(text, marker string) int {
	limit := min(len(text), len(marker)-1)
	for size := limit; size > 0; size-- {
		if strings.HasSuffix(text, marker[:size]) {
			return size
		}
	}
	return 0
}

type dsmlEnvelope struct {
	XMLName xml.Name     `xml:"dsml_tool_calls"`
	Invokes []dsmlInvoke `xml:"dsml_invoke"`
}

type dsmlInvoke struct {
	Name       string          `xml:"name,attr"`
	Parameters []dsmlParameter `xml:"dsml_parameter"`
}

type dsmlParameter struct {
	Name   string `xml:"name,attr"`
	String string `xml:"string,attr"`
	Value  string `xml:",chardata"`
}

func parseDSMLToolCalls(value string) ([]provider.ToolCall, error) {
	body := strings.TrimSpace(value)
	if !strings.HasPrefix(body, dsmlToolCallsOpen) ||
		!strings.HasSuffix(body, dsmlToolCallsClose) {
		return nil, errors.New("tool_calls envelope is incomplete")
	}
	normalized := strings.NewReplacer(
		"<｜｜DSML｜｜", "<dsml_",
		"</｜｜DSML｜｜", "</dsml_",
	).Replace(body)
	var envelope dsmlEnvelope
	if err := xml.Unmarshal([]byte(normalized), &envelope); err != nil {
		return nil, fmt.Errorf("decode envelope: %w", err)
	}
	calls := make([]provider.ToolCall, 0, len(envelope.Invokes))
	for _, invoke := range envelope.Invokes {
		name := strings.TrimSpace(invoke.Name)
		if name == "" {
			return nil, errors.New("invoke name is required")
		}
		arguments, err := parseDSMLParameters(invoke.Parameters)
		if err != nil {
			return nil, fmt.Errorf("invoke %q: %w", name, err)
		}
		encoded, err := json.Marshal(arguments)
		if err != nil {
			return nil, fmt.Errorf("invoke %q arguments: %w", name, err)
		}
		sum := sha256.Sum256(append([]byte(name+"\x00"), encoded...))
		calls = append(calls, provider.ToolCall{
			ID:   "call_dsml_" + hex.EncodeToString(sum[:8]),
			Name: name, Arguments: string(encoded),
		})
	}
	if len(calls) == 0 {
		return nil, errors.New("tool_calls envelope is empty")
	}
	return calls, nil
}

func parseDSMLParameters(parameters []dsmlParameter) (map[string]any, error) {
	arguments := make(map[string]any, len(parameters))
	for _, parameter := range parameters {
		name := strings.TrimSpace(parameter.Name)
		if name == "" {
			return nil, errors.New("parameter name is required")
		}
		if _, duplicate := arguments[name]; duplicate {
			return nil, fmt.Errorf("parameter %q is duplicated", name)
		}
		switch parameter.String {
		case "true":
			arguments[name] = parameter.Value
		case "false":
			var parsed any
			if err := json.Unmarshal([]byte(parameter.Value), &parsed); err != nil {
				return nil, fmt.Errorf(
					"parameter %q is not valid JSON: %w", name, err,
				)
			}
			arguments[name] = parsed
		default:
			return nil, fmt.Errorf(
				"parameter %q string must be true or false", name,
			)
		}
	}
	return arguments, nil
}

var _ provider.Stream = (*dsmlStream)(nil)
