package file

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strconv"
	"strings"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/persist/workspacejournal"
	"github.com/fwtllh-png/QCode/internal/security/sandbox"
	sourcediff "github.com/sourcegraph/go-diff/diff"
)

func (t *Tools) preparePatchTransaction(
	ctx context.Context,
	patch string,
) (*transaction, []AppliedChange, string, int, error) {
	if strings.TrimSpace(patch) == "" {
		return nil, nil, "", 0, tool.Precondition(errors.New("patch is required"))
	}
	files, err := sourcediff.ParseMultiFileDiff([]byte(patch))
	if err != nil {
		return nil, nil, "", 0, tool.Precondition(fmt.Errorf("parse unified patch: %w", err))
	}
	renameOnly := parseRenameOnlyDiffs(patch)
	filtered := make([]*sourcediff.FileDiff, 0, len(renameOnly)+len(files))
	filtered = append(filtered, renameOnly...)
	for _, file := range files {
		if file.OrigName == "" && file.NewName == "" &&
			extendedPatchValue(file.Extended, "rename from ") != "" {
			continue
		}
		filtered = append(filtered, file)
	}
	files = filtered
	if len(files) == 0 {
		return nil, nil, "", 0, tool.Precondition(errors.New("patch contains no file changes"))
	}
	if len(files) > maxTransactionChanges {
		return nil, nil, "", 0, tool.Precondition(fmt.Errorf(
			"patch has %d file changes, at most %d are allowed",
			len(files), maxTransactionChanges,
		))
	}
	transaction := &transaction{tools: t, files: make(map[string]*plannedFile)}
	touched := make(map[string]bool)
	for index, file := range files {
		if err := rejectUnsupportedPatch(file.Extended); err != nil {
			return nil, nil, "", 0, tool.Precondition(
				fmt.Errorf("patch file %d: %w", index, err),
			)
		}
		original, originalExists, err := patchPath(file.OrigName)
		if err != nil {
			return nil, nil, "", 0, err
		}
		next, nextExists, err := patchPath(file.NewName)
		if err != nil {
			return nil, nil, "", 0, err
		}
		if !originalExists {
			if value := extendedPatchValue(file.Extended, "rename from "); value != "" {
				original, originalExists = value, true
			}
		}
		if !nextExists {
			if value := extendedPatchValue(file.Extended, "rename to "); value != "" {
				next, nextExists = value, true
			}
		}
		for _, path := range []string{original, next} {
			if path == "" || touched[path] {
				continue
			}
			canonical, err := t.resolve(path, sandbox.AllowMissing)
			if err != nil {
				return nil, nil, "", 0, fmt.Errorf("unsafe patch path: %w", err)
			}
			if err := workspacejournal.ValidateExpectedWrite(ctx, canonical); err != nil {
				return nil, nil, "", 0, fmt.Errorf("patch freshness %q: %w", path, err)
			}
			touched[path] = true
		}
		if err := transaction.applyPatchFile(
			file, original, originalExists, next, nextExists,
		); err != nil {
			return nil, nil, "", 0, tool.Precondition(
				fmt.Errorf("patch file %d: %w", index, err),
			)
		}
	}
	changes, rendered, err := transaction.summarize()
	if err != nil {
		return nil, nil, "", 0, err
	}
	return transaction, changes, rendered, len(files), nil
}

func parseRenameOnlyDiffs(patch string) []*sourcediff.FileDiff {
	var result []*sourcediff.FileDiff
	for _, section := range strings.Split(patch, "diff --git ")[1:] {
		if strings.Contains(section, "\n--- ") {
			continue
		}
		lines := strings.Split(section, "\n")
		var original, next string
		var extended []string
		for _, line := range lines[1:] {
			switch {
			case strings.HasPrefix(line, "rename from "):
				original = strings.TrimSpace(strings.TrimPrefix(line, "rename from "))
				extended = append(extended, line)
			case strings.HasPrefix(line, "rename to "):
				next = strings.TrimSpace(strings.TrimPrefix(line, "rename to "))
				extended = append(extended, line)
			case strings.HasPrefix(line, "old mode "),
				strings.HasPrefix(line, "new mode "),
				strings.HasPrefix(line, "similarity index "):
				extended = append(extended, line)
			}
		}
		if original != "" && next != "" {
			result = append(result, &sourcediff.FileDiff{
				OrigName: original, NewName: next, Extended: extended,
			})
		}
	}
	return result
}

func extendedPatchValue(lines []string, prefix string) string {
	for _, line := range lines {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func (x *transaction) applyPatchFile(
	file *sourcediff.FileDiff,
	original string,
	originalExists bool,
	next string,
	nextExists bool,
) error {
	if !originalExists && !nextExists {
		return errors.New("patch cannot delete and create /dev/null")
	}
	var source []byte
	var mode fs.FileMode = 0o644
	var originalFile *plannedFile
	if originalExists {
		var err error
		originalFile, err = x.load(original)
		if err != nil {
			return err
		}
		if !originalFile.exists {
			return fmt.Errorf("source %q does not exist", original)
		}
		source = originalFile.after
		mode = originalFile.mode
	}
	updated, err := applyPatchHunks(source, file.Hunks)
	if err != nil {
		return fmt.Errorf("patch conflict: %w", err)
	}
	if !nextExists {
		originalFile.exists, originalFile.after = false, nil
		return nil
	}
	target, err := x.load(next)
	if err != nil {
		return err
	}
	if originalExists && target.canonical != originalFile.canonical {
		if target.exists {
			return fmt.Errorf("rename target %q already exists", next)
		}
		originalFile.exists, originalFile.after = false, nil
		target.exists = true
	} else if !originalExists && target.exists {
		return fmt.Errorf("new file target %q already exists", next)
	} else {
		target.exists = true
	}
	target.after = updated
	target.mode = patchMode(file.Extended, mode)
	return nil
}

func applyPatchHunks(source []byte, hunks []*sourcediff.Hunk) ([]byte, error) {
	if len(hunks) == 0 {
		return append([]byte(nil), source...), nil
	}
	lines := splitPatchLines(source)
	var output bytes.Buffer
	cursor := 0
	for _, hunk := range hunks {
		start := int(hunk.OrigStartLine) - 1
		if hunk.OrigStartLine == 0 {
			start = 0
		}
		if start < cursor || start > len(lines) {
			return nil, errors.New("hunk starts outside source")
		}
		for _, line := range lines[cursor:start] {
			output.Write(line)
		}
		cursor = start
		originalCount, newCount := 0, 0
		for _, line := range splitPatchBody(hunk.Body) {
			if len(line) == 0 {
				continue
			}
			switch line[0] {
			case ' ':
				if cursor >= len(lines) || !bytes.Equal(lines[cursor], line[1:]) {
					return nil, errors.New("context does not match source")
				}
				output.Write(lines[cursor])
				cursor++
				originalCount++
				newCount++
			case '-':
				if cursor >= len(lines) || !bytes.Equal(lines[cursor], line[1:]) {
					return nil, errors.New("removed line does not match source")
				}
				cursor++
				originalCount++
			case '+':
				output.Write(line[1:])
				newCount++
			case '\\':
				// The marker describes the preceding line and carries no content.
			default:
				return nil, errors.New("hunk contains an invalid line prefix")
			}
		}
		if originalCount != int(hunk.OrigLines) || newCount != int(hunk.NewLines) {
			return nil, errors.New("hunk line counts do not match header")
		}
	}
	for _, line := range lines[cursor:] {
		output.Write(line)
	}
	return output.Bytes(), nil
}

func splitPatchLines(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	return bytes.SplitAfter(data, []byte("\n"))
}

func splitPatchBody(data []byte) [][]byte {
	if len(data) == 0 {
		return nil
	}
	lines := bytes.SplitAfter(data, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func patchPath(value string) (string, bool, error) {
	value = strings.TrimSpace(value)
	if value == "/dev/null" || value == "" {
		return "", false, nil
	}
	value = strings.TrimPrefix(strings.TrimPrefix(value, "a/"), "b/")
	value = strings.ReplaceAll(value, `\"`, `"`)
	if strings.ContainsRune(value, '\x00') {
		return "", false, errors.New("patch path contains NUL")
	}
	return value, true, nil
}

func patchMode(extended []string, fallback fs.FileMode) fs.FileMode {
	for _, line := range extended {
		for _, prefix := range []string{"new file mode ", "new mode "} {
			if !strings.HasPrefix(line, prefix) {
				continue
			}
			value, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, prefix)), 8, 32)
			if err == nil {
				return fs.FileMode(value).Perm()
			}
		}
	}
	return fallback.Perm()
}

func rejectUnsupportedPatch(extended []string) error {
	for _, line := range extended {
		if strings.HasPrefix(line, "Binary files ") ||
			strings.HasPrefix(line, "GIT binary patch") ||
			strings.HasPrefix(line, "copy from ") ||
			strings.HasPrefix(line, "copy to ") {
			return errors.New("binary and copy patches are not supported")
		}
	}
	return nil
}
