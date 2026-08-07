package config

import (
	"encoding/json"
	"fmt"
	"strings"
)

// FieldExplanation describes one resolved field without exposing secret values.
type FieldExplanation struct {
	Field   string `json:"field"`
	Current any    `json:"current"`
	Default any    `json:"default"`
	Source  Source `json:"source"`
	Risk    string `json:"risk"`
	Impact  string `json:"impact"`
}

func (s Snapshot) Explain(field string) (FieldExplanation, error) {
	field = strings.TrimSpace(field)
	source, ok := s.Provenance[field]
	if !ok {
		return FieldExplanation{}, fmt.Errorf("unknown config field %q", field)
	}
	current, err := flattenedConfig(s.Config)
	if err != nil {
		return FieldExplanation{}, err
	}
	defaults, err := flattenedConfig(Defaults())
	if err != nil {
		return FieldExplanation{}, err
	}
	currentValue, exists := current[field]
	if !exists {
		return FieldExplanation{}, fmt.Errorf(
			"config field %q has provenance but no resolved value",
			field,
		)
	}
	defaultValue := defaults[field]
	risk, impact := fieldRisk(field)
	return FieldExplanation{
		Field: field, Current: currentValue, Default: defaultValue,
		Source: source, Risk: risk, Impact: impact,
	}, nil
}

func flattenedConfig(config Config) (map[string]any, error) {
	data, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := json.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	result := make(map[string]any)
	var flatten func(string, map[string]any)
	flatten = func(prefix string, values map[string]any) {
		for key, value := range values {
			field := key
			if prefix != "" {
				field = prefix + "." + key
			}
			nested, ok := value.(map[string]any)
			if ok {
				if field == "route.slots" {
					flatten("route", nested)
					continue
				}
				flatten(field, nested)
				continue
			}
			result[field] = value
		}
	}
	flatten("", document)
	return result, nil
}

func fieldRisk(field string) (string, string) {
	switch {
	case strings.HasPrefix(field, "credential."):
		return "high", "must contain a non-secret reference; raw credentials are forbidden"
	case field == fieldTools:
		return "high", "enables the governed tool surface for agent turns"
	case field == fieldVerifyMode ||
		field == fieldVerifyOnFailure ||
		field == fieldVerifyCommand:
		return "high", "changes whether repository modifications pass, fail, or revert"
	case field == fieldJournalDurable || field == fieldJournalRecoverOnStart:
		return "high", "changes crash recovery and edit rollback behavior"
	case field == fieldWorkerEnabled ||
		strings.HasPrefix(field, "execution.worker."):
		return "high", "changes durable background execution or its resource bounds"
	case field == fieldProvider || field == fieldModel || field == fieldProtocol ||
		strings.HasPrefix(field, "route."):
		return "medium", "changes which provider route handles model work"
	case strings.HasPrefix(field, "execution.budget_") ||
		field == fieldMaxSteps ||
		field == fieldMaxConcurrent:
		return "medium", "changes execution resource limits"
	case strings.HasPrefix(field, "context."):
		return "medium", "changes context recall, truncation, or token use"
	case strings.HasPrefix(field, "runtime.") ||
		strings.HasPrefix(field, "state."):
		return "medium", "changes runtime capacity or durable state behavior"
	default:
		return "low", "changes a bounded runtime configuration value"
	}
}
