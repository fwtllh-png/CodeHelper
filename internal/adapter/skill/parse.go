package skill

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

const maxDescriptionBytes = 1024

type document struct {
	metadata Metadata
	body     string
	raw      []byte
}

func parseDocument(data []byte) (document, error) {
	if len(data) == 0 || !utf8.Valid(data) || strings.IndexByte(string(data), 0) >= 0 {
		return document{}, errors.New("skill must be non-empty UTF-8 text")
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	lines := strings.Split(text, "\n")
	if len(lines) < 3 || lines[0] != "---" {
		return document{}, errors.New("skill must begin with YAML frontmatter")
	}
	end := -1
	for index := 1; index < len(lines); index++ {
		if lines[index] == "---" {
			end = index
			break
		}
	}
	if end < 0 {
		return document{}, errors.New("skill frontmatter is not terminated")
	}
	metadata, err := parseMetadata(lines[1:end])
	if err != nil {
		return document{}, err
	}
	body := strings.TrimSpace(strings.Join(lines[end+1:], "\n"))
	if body == "" {
		return document{}, errors.New("skill body is required")
	}
	return document{metadata: metadata, body: body, raw: append([]byte(nil), data...)}, nil
}

func parseMetadata(lines []string) (Metadata, error) {
	values := make(map[string]string)
	localized := make(map[string]string)
	for index := 0; index < len(lines); {
		line := lines[index]
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			index++
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			return Metadata{}, fmt.Errorf("unexpected indentation on frontmatter line %d", index+2)
		}
		key, raw, found := strings.Cut(line, ":")
		key, raw = strings.TrimSpace(key), strings.TrimSpace(raw)
		if !found || key == "" {
			return Metadata{}, fmt.Errorf("invalid frontmatter line %d", index+2)
		}
		if key == "descriptions" {
			if raw != "" {
				return Metadata{}, errors.New("descriptions must be an indented locale map")
			}
			index++
			for index < len(lines) {
				nested := lines[index]
				if strings.TrimSpace(nested) == "" {
					index++
					continue
				}
				if nested[0] != ' ' {
					break
				}
				locale, value, ok := strings.Cut(strings.TrimSpace(nested), ":")
				locale = normalizeLocale(locale)
				if !ok || locale == "" {
					return Metadata{}, fmt.Errorf("invalid descriptions locale on frontmatter line %d", index+2)
				}
				parsed, next, err := parseScalar(lines, index, strings.TrimSpace(value))
				if err != nil {
					return Metadata{}, err
				}
				if _, duplicate := localized[locale]; duplicate {
					return Metadata{}, fmt.Errorf("duplicate description locale %q", locale)
				}
				localized[locale] = parsed
				index = next
			}
			continue
		}
		if !allowedMetadataKey(key) {
			return Metadata{}, fmt.Errorf("unknown skill metadata field %q", key)
		}
		if _, duplicate := values[key]; duplicate {
			return Metadata{}, fmt.Errorf("duplicate skill metadata field %q", key)
		}
		value, next, err := parseScalar(lines, index, raw)
		if err != nil {
			return Metadata{}, err
		}
		if locale, ok := descriptionLocale(key); ok {
			if _, duplicate := localized[locale]; duplicate {
				return Metadata{}, fmt.Errorf("duplicate description locale %q", locale)
			}
			localized[locale] = value
		} else {
			values[key] = value
		}
		index = next
	}

	metadata := Metadata{
		Name: values["name"], Description: values["description"],
		LocalizedDescriptions: localized,
	}
	if !namePattern.MatchString(metadata.Name) {
		return Metadata{}, errors.New("skill name must be 1-64 lowercase letters, numbers, or hyphens")
	}
	if err := validateDescription("description", metadata.Description); err != nil {
		return Metadata{}, err
	}
	for locale, value := range localized {
		if err := validateDescription("description_"+locale, value); err != nil {
			return Metadata{}, err
		}
	}
	if value, exists := values["disable-model-invocation"]; exists {
		parsed, err := parseBool("disable-model-invocation", value)
		if err != nil {
			return Metadata{}, err
		}
		metadata.DisableModelInvocation = parsed
	}
	if value, exists := values["user-invocable"]; exists {
		parsed, err := parseBool("user-invocable", value)
		if err != nil {
			return Metadata{}, err
		}
		metadata.UserInvocable = &parsed
	}
	return metadata, nil
}

func allowedMetadataKey(key string) bool {
	if key == "name" || key == "description" ||
		key == "disable-model-invocation" || key == "user-invocable" {
		return true
	}
	_, ok := descriptionLocale(key)
	return ok
}

func descriptionLocale(key string) (string, bool) {
	value, found := strings.CutPrefix(key, "description_")
	if !found {
		return "", false
	}
	value = normalizeLocale(value)
	return value, value != ""
}

func parseScalar(lines []string, index int, raw string) (string, int, error) {
	if raw == "|" || raw == "|-" || raw == ">" || raw == ">-" {
		var parts []string
		for next := index + 1; next < len(lines); next++ {
			line := lines[next]
			if line != "" && line[0] != ' ' && line[0] != '\t' {
				break
			}
			if line == "" {
				parts = append(parts, "")
				index = next
				continue
			}
			parts = append(parts, strings.TrimPrefix(strings.TrimPrefix(line, "  "), "\t"))
			index = next
		}
		if len(parts) == 0 {
			return "", index + 1, errors.New("frontmatter block scalar is empty")
		}
		separator := "\n"
		if strings.HasPrefix(raw, ">") {
			separator = " "
		}
		return strings.TrimSpace(strings.Join(parts, separator)), index + 1, nil
	}
	value, err := unquoteScalar(raw)
	return value, index + 1, err
}

func unquoteScalar(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if strings.HasPrefix(value, `"`) {
		if !strings.HasSuffix(value, `"`) {
			return "", errors.New("unterminated double-quoted frontmatter value")
		}
		var decoded string
		if err := json.Unmarshal([]byte(value), &decoded); err != nil {
			return "", fmt.Errorf("invalid double-quoted frontmatter value: %w", err)
		}
		return decoded, nil
	}
	if strings.HasPrefix(value, "'") {
		if !strings.HasSuffix(value, "'") {
			return "", errors.New("unterminated single-quoted frontmatter value")
		}
		return strings.ReplaceAll(value[1:len(value)-1], "''", "'"), nil
	}
	if strings.ContainsAny(value, "[]{}") {
		return "", errors.New("frontmatter collections are not allowed")
	}
	return value, nil
}

func validateDescription(field, value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return fmt.Errorf("%s is required", field)
	}
	if len(value) > maxDescriptionBytes || strings.ContainsAny(value, "\x00\r\n") {
		return fmt.Errorf("%s must be a single line of at most %d bytes", field, maxDescriptionBytes)
	}
	return nil
}

func parseBool(field, value string) (bool, error) {
	switch value {
	case "true":
		return true, nil
	case "false":
		return false, nil
	default:
		return false, fmt.Errorf("%s must be true or false", field)
	}
}
