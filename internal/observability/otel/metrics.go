package otel

import (
	"errors"
	"sort"
	"strings"
	"sync"

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

type MetricPoint struct {
	Name   string            `json:"name"`
	Labels map[string]string `json:"labels,omitempty"`
	Count  uint64            `json:"count"`
	Sum    float64           `json:"sum,omitempty"`
}

type MetricRegistry struct {
	mu        sync.Mutex
	maxSeries int
	points    map[string]MetricPoint
	dropped   uint64
}

func NewMetricRegistry(maxSeries int) *MetricRegistry {
	if maxSeries <= 0 {
		maxSeries = 512
	}
	return &MetricRegistry{
		maxSeries: maxSeries,
		points:    make(map[string]MetricPoint),
	}
}

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

func (r *MetricRegistry) Add(
	name string,
	labels Labels,
	value float64,
) bool {
	if r == nil || name == "" || ValidateLabels(labels) != nil {
		return false
	}
	key := metricKey(name, labels)
	r.mu.Lock()
	defer r.mu.Unlock()
	point, exists := r.points[key]
	if !exists && len(r.points) >= r.maxSeries {
		r.dropped++
		return false
	}
	if !exists {
		point = MetricPoint{
			Name:   name,
			Labels: cloneStringMap(labels),
		}
	}
	point.Count++
	point.Sum += value
	r.points[key] = point
	return true
}

func (r *MetricRegistry) Snapshot() ([]MetricPoint, uint64) {
	if r == nil {
		return nil, 0
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	values := make([]MetricPoint, 0, len(r.points))
	for _, point := range r.points {
		point.Labels = cloneStringMap(point.Labels)
		values = append(values, point)
	}
	sort.Slice(values, func(left, right int) bool {
		return metricKey(values[left].Name, values[left].Labels) <
			metricKey(values[right].Name, values[right].Labels)
	})
	return values, r.dropped
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

func metricKey(name string, labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var builder strings.Builder
	builder.WriteString(name)
	for _, key := range keys {
		builder.WriteByte('|')
		builder.WriteString(key)
		builder.WriteByte('=')
		builder.WriteString(labels[key])
	}
	return builder.String()
}
