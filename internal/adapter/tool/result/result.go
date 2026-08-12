// Package result builds structured tool results without making execution or
// policy decisions.
package result

import (
	"encoding/json"
	"fmt"
	"maps"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type Failure struct {
	Category  string
	Message   string
	Retryable bool
	Metadata  map[string]any
}

func Success[T any](value T, metadata map[string]any) (tool.Result, error) {
	content, err := json.Marshal(value)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: string(content), Metadata: clone(metadata)}, nil
}

func Text(content string, metadata map[string]any) tool.Result {
	return tool.Result{Content: content, Metadata: clone(metadata)}
}

func Fail(failure Failure) tool.Result {
	metadata := clone(failure.Metadata)
	if metadata == nil {
		metadata = make(map[string]any, 2)
	}
	if failure.Category != "" {
		metadata["error_category"] = failure.Category
	}
	if failure.Retryable {
		metadata["retryable"] = true
	}
	return tool.Result{
		Content:  failure.Message,
		IsError:  true,
		Metadata: metadata,
	}
}

func Unavailable(reason string) tool.Result {
	if reason == "" {
		reason = tool.ErrToolUnavailable.Error()
	}
	return Fail(Failure{Category: "unavailable", Message: reason})
}

func Validate(value tool.Result) error {
	if _, err := json.Marshal(value.Metadata); err != nil {
		return fmt.Errorf("tool result metadata is not JSON-compatible: %w", err)
	}
	return nil
}

func clone(source map[string]any) map[string]any {
	if source == nil {
		return nil
	}
	target := make(map[string]any, len(source))
	maps.Copy(target, source)
	return target
}
