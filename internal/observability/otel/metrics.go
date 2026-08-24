package otel

import (
	"errors"
	"sort"
	"strings"

	"go.opentelemetry.io/otel/attribute"
)

var allowedLabelKeys = map[string]bool{
	"status":            true,
	"phase":             true,
	"provider":          true,
	"model_family":      true,
	"tool_class":        true,
	"error_category":    true,
	"sandbox_strength":  true,
	"observation_class": true,
}

type Labels map[string]string

func ValidateLabels(labels Labels) error {
	if len(labels) > len(allowedLabelKeys) {
		return errors.New("metric label set exceeds allowed dimensions")
	}
	for key, value := range labels {
		if !allowedLabelKeys[key] {
			return errors.New("metric label key is not allowed")
		}
		if len(value) > 64 ||
			strings.ContainsAny(value, "/\\\n\r\t") {
			return errors.New("metric label value is unbounded")
		}
	}
	return nil
}

func metricAttributes(labels Labels) []attribute.KeyValue {
	if ValidateLabels(labels) != nil {
		return nil
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]attribute.KeyValue, 0, len(keys))
	for _, key := range keys {
		result = append(result, attribute.String(key, labels[key]))
	}
	return result
}
