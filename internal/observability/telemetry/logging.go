package telemetry

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"regexp"
	"slices"
	"strings"
)

const redacted = "[REDACTED]"

var (
	bearerPattern = regexp.MustCompile(`(?i)\bbearer\s+[A-Za-z0-9._~+/=-]+`)
	secretPattern = regexp.MustCompile(`(?i)\b(authorization|api[_-]?key|token|password|secret)(\s*[:=]\s*)([^\s,;"']+)`)
)

type Redactor struct {
	secrets []string
}

func NewRedactor(secrets ...string) Redactor {
	filtered := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret != "" && !slices.Contains(filtered, secret) {
			filtered = append(filtered, secret)
		}
	}
	slices.SortFunc(filtered, func(left, right string) int {
		return len(right) - len(left)
	})
	return Redactor{secrets: filtered}
}

func (r Redactor) Redact(value string) string {
	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, redacted)
	}
	value = bearerPattern.ReplaceAllString(value, "Bearer "+redacted)
	return secretPattern.ReplaceAllString(value, "${1}${2}"+redacted)
}

type redactingHandler struct {
	next     slog.Handler
	redactor Redactor
}

func NewJSONLogger(writer io.Writer, level slog.Level, redactor Redactor) *slog.Logger {
	handler := slog.NewJSONHandler(writer, &slog.HandlerOptions{Level: level})
	return slog.New(&redactingHandler{next: handler, redactor: redactor})
}

func (h *redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	sanitized := slog.NewRecord(record.Time, record.Level, h.redactor.Redact(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		sanitized.AddAttrs(h.redactAttr(attr))
		return true
	})
	return h.next.Handle(ctx, sanitized)
}

func (h *redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	sanitized := make([]slog.Attr, 0, len(attrs))
	for _, attr := range attrs {
		sanitized = append(sanitized, h.redactAttr(attr))
	}
	return &redactingHandler{next: h.next.WithAttrs(sanitized), redactor: h.redactor}
}

func (h *redactingHandler) WithGroup(name string) slog.Handler {
	return &redactingHandler{next: h.next.WithGroup(name), redactor: h.redactor}
}

func (h *redactingHandler) redactAttr(attr slog.Attr) slog.Attr {
	attr.Value = attr.Value.Resolve()
	switch attr.Value.Kind() {
	case slog.KindString:
		attr.Value = slog.StringValue(h.redactor.Redact(attr.Value.String()))
	case slog.KindGroup:
		group := attr.Value.Group()
		for index := range group {
			group[index] = h.redactAttr(group[index])
		}
		attr.Value = slog.GroupValue(group...)
	case slog.KindAny:
		attr.Value = slog.AnyValue(h.redactAny(attr.Value.Any()))
	}
	return attr
}

func (h *redactingHandler) redactAny(value any) any {
	switch typed := value.(type) {
	case nil:
		return nil
	case error:
		return h.redactor.Redact(typed.Error())
	case fmt.Stringer:
		return h.redactor.Redact(typed.String())
	case []byte:
		return h.redactor.Redact(string(typed))
	}

	data, err := json.Marshal(value)
	if err != nil {
		return redacted
	}
	var generic any
	if err := json.Unmarshal(data, &generic); err != nil {
		return redacted
	}
	return redactJSONValue(h.redactor, generic)
}

func redactJSONValue(redactor Redactor, value any) any {
	switch typed := value.(type) {
	case string:
		return redactor.Redact(typed)
	case []any:
		for index := range typed {
			typed[index] = redactJSONValue(redactor, typed[index])
		}
	case map[string]any:
		for key, item := range typed {
			typed[key] = redactJSONValue(redactor, item)
		}
	}
	return value
}
