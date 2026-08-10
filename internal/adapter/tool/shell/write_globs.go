package shell

import (
	"encoding/json"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
)

const maxExpandedWritePaths = 512

func (t *Tool) expandWriteGlobs(raw json.RawMessage) (json.RawMessage, error) {
	var values map[string]any
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	items, exists := values["write_globs"]
	if !exists {
		return raw, nil
	}
	globs, err := stringArray(items, "write_globs")
	if err != nil {
		return nil, err
	}
	explicit, err := stringArray(values["write_paths"], "write_paths")
	if err != nil {
		return nil, err
	}
	matches := make(map[string]struct{}, len(explicit))
	for _, path := range explicit {
		matches[filepath.ToSlash(filepath.Clean(path))] = struct{}{}
	}
	for _, pattern := range globs {
		pattern = filepath.ToSlash(filepath.Clean(strings.TrimSpace(pattern)))
		if pattern == "." || filepath.IsAbs(pattern) ||
			pattern == ".." || strings.HasPrefix(pattern, "../") {
			return nil, errors.New("write_glob must be workspace-relative")
		}
		matched := 0
		err := filepath.WalkDir(t.workspace.Root(), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type()&fs.ModeSymlink != 0 {
				if entry.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if entry.IsDir() || !entry.Type().IsRegular() {
				return nil
			}
			relative, err := filepath.Rel(t.workspace.Root(), path)
			if err != nil {
				return err
			}
			relative = filepath.ToSlash(relative)
			if !matchPathGlob(pattern, relative) {
				return nil
			}
			matches[relative] = struct{}{}
			matched++
			if len(matches) > maxExpandedWritePaths {
				return errors.New("write_globs expanded beyond 512 files")
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if matched == 0 {
			return nil, errors.New("write_glob matched no existing files: " + pattern)
		}
	}
	expanded := make([]string, 0, len(matches))
	for path := range matches {
		expanded = append(expanded, path)
	}
	slices.Sort(expanded)
	values["write_paths"] = expanded
	delete(values, "write_globs")
	return json.Marshal(values)
}

func stringArray(value any, field string) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	items, ok := value.([]any)
	if !ok {
		return nil, errors.New(field + " must be an array")
	}
	result := make([]string, 0, len(items))
	for _, item := range items {
		text, ok := item.(string)
		if !ok || strings.TrimSpace(text) == "" {
			return nil, errors.New(field + " entries must be non-empty strings")
		}
		result = append(result, text)
	}
	return result, nil
}

func matchPathGlob(pattern, candidate string) bool {
	return matchGlobSegments(
		strings.Split(pattern, "/"),
		strings.Split(candidate, "/"),
	)
}

func matchGlobSegments(pattern, candidate []string) bool {
	if len(pattern) == 0 {
		return len(candidate) == 0
	}
	if pattern[0] == "**" {
		return matchGlobSegments(pattern[1:], candidate) ||
			(len(candidate) != 0 && matchGlobSegments(pattern, candidate[1:]))
	}
	if len(candidate) == 0 {
		return false
	}
	matched, err := filepath.Match(pattern[0], candidate[0])
	return err == nil && matched && matchGlobSegments(pattern[1:], candidate[1:])
}
