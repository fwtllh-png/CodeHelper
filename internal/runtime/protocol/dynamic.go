package protocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

const DynamicToolSpecVersion = 1

var dynamicToolNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

// DynamicToolSpec is the versioned trusted-host registration surface for tools
// supplied by a trusted host client. Capability and sandbox policy are never
// taken from this payload; hosts attach those through registration policy.
type DynamicToolSpec struct {
	Version      int            `json:"version"`
	Namespace    string         `json:"namespace,omitempty"`
	Name         string         `json:"name"`
	Description  string         `json:"description"`
	InputSchema  map[string]any `json:"input_schema"`
	DeferLoading bool           `json:"defer_loading,omitempty"`
}

func (s DynamicToolSpec) Validate() error {
	if s.Version != DynamicToolSpecVersion {
		return fmt.Errorf("dynamic tool spec version %d is unsupported", s.Version)
	}
	if s.Namespace != "" && !dynamicToolNamePattern.MatchString(s.Namespace) {
		return errors.New("dynamic tool namespace is invalid")
	}
	if !dynamicToolNamePattern.MatchString(s.Name) {
		return errors.New("dynamic tool name is invalid")
	}
	if strings.TrimSpace(s.Description) == "" {
		return errors.New("dynamic tool description is required")
	}
	if s.InputSchema == nil {
		return errors.New("dynamic tool input_schema is required")
	}
	if s.InputSchema["type"] != "object" {
		return errors.New("dynamic tool input_schema type must be object")
	}
	return nil
}

func (s DynamicToolSpec) ToolName() string {
	if s.Namespace == "" {
		return s.Name
	}
	return s.Namespace + "__" + s.Name
}

type DynamicToolItemStatus string

const (
	DynamicToolInProgress DynamicToolItemStatus = "in_progress"
	DynamicToolCompleted  DynamicToolItemStatus = "completed"
	DynamicToolFailed     DynamicToolItemStatus = "failed"
)

type DynamicToolCallParams struct {
	Version   int             `json:"version"`
	ThreadID  ThreadID        `json:"thread_id"`
	TurnID    TurnID          `json:"turn_id"`
	CallID    string          `json:"call_id"`
	Namespace string          `json:"namespace,omitempty"`
	Tool      string          `json:"tool"`
	Arguments json.RawMessage `json:"arguments"`
}

func (p DynamicToolCallParams) Validate() error {
	if p.Version != DynamicToolSpecVersion {
		return fmt.Errorf("dynamic tool call version %d is unsupported", p.Version)
	}
	if p.ThreadID == "" || p.TurnID == "" || p.CallID == "" || p.Tool == "" {
		return errors.New("dynamic tool call identity fields are required")
	}
	if len(p.Arguments) == 0 {
		return errors.New("dynamic tool call arguments are required")
	}
	return nil
}

type DynamicToolCallContent struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

func (c DynamicToolCallContent) Validate() error {
	switch c.Type {
	case "input_text":
		if c.Text == "" {
			return errors.New("dynamic tool text content is required")
		}
	case "input_image":
		if c.ImageURL == "" {
			return errors.New("dynamic tool image_url is required")
		}
	default:
		return fmt.Errorf("unknown dynamic tool content type %q", c.Type)
	}
	return nil
}

type DynamicToolCallResult struct {
	Version int                      `json:"version"`
	Success bool                     `json:"success"`
	Content []DynamicToolCallContent `json:"content,omitempty"`
}

func (r DynamicToolCallResult) Validate() error {
	if r.Version != DynamicToolSpecVersion {
		return fmt.Errorf("dynamic tool result version %d is unsupported", r.Version)
	}
	for _, content := range r.Content {
		if err := content.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// DecodeDynamicToolSpec rejects unknown fields and unsupported versions.
func DecodeDynamicToolSpec(data []byte) (DynamicToolSpec, error) {
	var spec DynamicToolSpec
	if err := decodeStrictDynamic(data, &spec); err != nil {
		return DynamicToolSpec{}, err
	}
	if err := spec.Validate(); err != nil {
		return DynamicToolSpec{}, err
	}
	return spec, nil
}

func DecodeDynamicToolCallResult(data []byte) (DynamicToolCallResult, error) {
	var result DynamicToolCallResult
	if err := decodeStrictDynamic(data, &result); err != nil {
		return DynamicToolCallResult{}, err
	}
	if err := result.Validate(); err != nil {
		return DynamicToolCallResult{}, err
	}
	return result, nil
}

func decodeStrictDynamic(data []byte, value any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values")
		}
		return err
	}
	return nil
}
