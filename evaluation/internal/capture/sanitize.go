package capture

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
)

const redactedValue = "[REDACTED]"

var (
	sensitiveKey = regexp.MustCompile(
		`(?i)^(authorization|api[_-]?key|credential|password|secret|` +
			`access[_-]?token|refresh[_-]?token|cookie)$`,
	)
	emailPattern = regexp.MustCompile(
		`(?i)^[a-z0-9.!#$%&'*+/=?^_{}|~-]+@[a-z0-9.-]+\.[a-z]{2,}$`,
	)
	credentialPattern = regexp.MustCompile(
		`(?i)(bearer\s+[a-z0-9._~+/-]+=*|` +
			`(?:sk|rk|pk|api)[_-][a-z0-9]{8,}|` +
			`AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----)`,
	)
	absolutePathPattern = regexp.MustCompile(
		`(?:^|["\s])(?:/Users/|/home/|[A-Za-z]:\\Users\\)`,
	)
	longOpaquePattern = regexp.MustCompile(`^[A-Za-z0-9+/=_-]{40,}$`)
)

func sanitizeData(
	value any,
	options SanitizerOptions,
) (json.RawMessage, evidence.Redaction, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, "", fmt.Errorf("encode captured data: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var normalized any
	if decodeErr := decoder.Decode(&normalized); decodeErr != nil {
		return nil, "", fmt.Errorf("decode captured data: %w", decodeErr)
	}
	sanitized, changed := sanitizeValue("", normalized, options)
	encoded, err = json.Marshal(sanitized)
	if err != nil {
		return nil, "", fmt.Errorf("encode sanitized data: %w", err)
	}
	redaction := evidence.RedactionNotRequired
	if changed {
		redaction = evidence.RedactionApplied
	}
	return encoded, redaction, nil
}

func sanitizeValue(
	key string,
	value any,
	options SanitizerOptions,
) (any, bool) {
	if sensitiveKey.MatchString(key) {
		return nil, true
	}
	switch typed := value.(type) {
	case nil, bool, json.Number:
		return typed, false
	case string:
		sanitized, changed := sanitizeString(key, typed, options)
		return sanitized, changed
	case []any:
		changed := false
		result := make([]any, 0, len(typed))
		for _, item := range typed {
			sanitized, itemChanged := sanitizeValue("", item, options)
			result = append(result, sanitized)
			changed = changed || itemChanged
		}
		return result, changed
	case map[string]any:
		changed := false
		result := make(map[string]any, len(typed))
		for childKey, item := range typed {
			if sensitiveKey.MatchString(childKey) {
				changed = true
				continue
			}
			sanitized, itemChanged := sanitizeValue(childKey, item, options)
			result[childKey] = sanitized
			changed = changed || itemChanged
		}
		return result, changed
	default:
		return nil, true
	}
}

func sanitizeString(
	key string,
	value string,
	options SanitizerOptions,
) (string, bool) {
	for _, secret := range options.Secrets {
		if secret != "" && strings.Contains(value, secret) {
			return redactedValue, true
		}
	}
	for _, path := range options.RestrictedPaths {
		if path != "" && strings.Contains(value, path) {
			return redactedValue, true
		}
	}
	if emailPattern.MatchString(value) ||
		credentialPattern.MatchString(value) ||
		absolutePathPattern.MatchString(value) ||
		strings.Contains(value, "://") ||
		strings.ContainsRune(value, '\n') ||
		strings.ContainsRune(value, '\r') {
		return redactedValue, true
	}
	if value == redactedValue || protocolValuePattern.MatchString(value) {
		if value == redactedValue || safeMetadataString(key, value) {
			return value, false
		}
	}
	return redactedValue, true
}

func safeMetadataString(key, value string) bool {
	if sensitiveKey.MatchString(key) || credentialPattern.MatchString(value) ||
		longOpaquePattern.MatchString(value) {
		return false
	}
	switch key {
	case "type":
		switch value {
		case "null", "boolean", "number", "string", "array", "object",
			"depth_limited", "unknown":
			return true
		}
	case "state":
		switch value {
		case "starting", "ready", "stopping", "stopped", "restarting", "failed":
			return true
		}
	case "signal":
		return strings.HasPrefix(value, "SIG") && len(value) <= 16
	case "method":
		return strings.Contains(value, "/")
	case "operation_kind":
		return strings.ContainsAny(value, "._-")
	case "stop_reason":
		switch value {
		case "end_turn", "tool_use", "max_tokens", "stop", "length":
			return true
		}
	case "shape":
		return value == "metadata"
	}
	return false
}

func Scan(content []byte, options SanitizerOptions) error {
	if err := scanBytes(content, options); err != nil {
		return err
	}
	scanner := bytes.Split(content, []byte{'\n'})
	for index, line := range scanner {
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}
		var value any
		if err := json.Unmarshal(line, &value); err != nil {
			return fmt.Errorf("secret scan line %d is not JSON: %w", index+1, err)
		}
		if err := scanValue("", value); err != nil {
			return fmt.Errorf("secret scan line %d: %w", index+1, err)
		}
	}
	return nil
}

func ScanDocument(content []byte, options SanitizerOptions) error {
	if err := scanBytes(content, options); err != nil {
		return err
	}
	var value any
	if err := json.Unmarshal(content, &value); err != nil {
		return fmt.Errorf("secret scan document is not JSON: %w", err)
	}
	return scanValue("", value)
}

func scanBytes(content []byte, options SanitizerOptions) error {
	for _, secret := range options.Secrets {
		if secret != "" && bytes.Contains(content, []byte(secret)) {
			return errors.New("sanitized evidence contains a configured secret")
		}
	}
	for _, path := range options.RestrictedPaths {
		if path != "" && bytes.Contains(content, []byte(path)) {
			return errors.New("sanitized evidence contains a restricted path")
		}
	}
	if credentialPattern.Match(content) {
		return errors.New("sanitized evidence contains a credential pattern")
	}
	if absolutePathPattern.Match(content) {
		return errors.New("sanitized evidence contains an absolute user path")
	}
	return nil
}

func scanValue(key string, value any) error {
	if sensitiveKey.MatchString(key) {
		return fmt.Errorf("sensitive key %q remains", key)
	}
	switch typed := value.(type) {
	case string:
		if emailPattern.MatchString(typed) ||
			credentialPattern.MatchString(typed) ||
			absolutePathPattern.MatchString(typed) ||
			strings.Contains(typed, "://") {
			return errors.New("sensitive string remains")
		}
		if longOpaquePattern.MatchString(typed) &&
			!strings.HasPrefix(typed, "sha256:") &&
			opaqueEntropy(typed) {
			return errors.New("high-entropy opaque string remains")
		}
	case []any:
		for _, item := range typed {
			if err := scanValue("", item); err != nil {
				return err
			}
		}
	case map[string]any:
		for childKey, item := range typed {
			if err := scanValue(childKey, item); err != nil {
				return err
			}
		}
	}
	return nil
}

func opaqueEntropy(value string) bool {
	classes := 0
	var lower, upper, number, symbol bool
	for _, character := range value {
		switch {
		case unicode.IsLower(character):
			lower = true
		case unicode.IsUpper(character):
			upper = true
		case unicode.IsDigit(character):
			number = true
		default:
			symbol = true
		}
	}
	for _, present := range []bool{lower, upper, number, symbol} {
		if present {
			classes++
		}
	}
	return classes >= 3
}
