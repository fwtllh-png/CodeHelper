package file

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/process"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
	pdf "github.com/ledongthuc/pdf"
)

const (
	defaultReadLines = 200
	maxReadLines     = 2000
	defaultListLimit = 200
	maxListLimit     = 2000
)

type Tools struct {
	root      string
	workspace *sandbox.Workspace
	backend   sandbox.Backend
}

func NewWithBackend(root string, backend sandbox.Backend) (*Tools, error) {
	if backend == nil {
		return nil, errors.New("file tools require an injected sandbox backend")
	}
	backend, err := sandbox.BindPolicy(backend, sandbox.Options{WorkspaceRoot: root})
	if err != nil {
		return nil, err
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return nil, err
	}
	return &Tools{root: workspace.Root(), workspace: workspace, backend: backend}, nil
}

func (t *Tools) Register(registry *tool.Registry) error {
	registry.SetSandboxBackend(t.backend)
	for _, executor := range []tool.Executor{
		&operation{tools: t, kind: "file_read"},
		&operation{tools: t, kind: "file_write"},
		&operation{tools: t, kind: "file_edit"},
		&operation{tools: t, kind: "file_apply"},
		&operation{tools: t, kind: "file_patch"},
		&operation{tools: t, kind: "file_list"},
	} {
		if err := registry.Register(executor, nil); err != nil {
			return err
		}
	}
	return nil
}

type operation struct {
	tools *Tools
	kind  string
}

func (o *operation) PlanEdit(
	ctx context.Context, raw json.RawMessage,
) (tool.EditPlan, error) {
	var input struct {
		Path    string          `json:"path"`
		Content string          `json:"content"`
		Old     string          `json:"old"`
		New     string          `json:"new"`
		Changes []changeRequest `json:"changes"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.EditPlan{}, err
	}
	var requests []changeRequest
	switch o.kind {
	case "file_write":
		requests = []changeRequest{{Op: opWrite, Path: input.Path, Content: input.Content}}
	case "file_edit":
		requests = []changeRequest{{Op: opEdit, Path: input.Path, Old: input.Old, New: input.New}}
	case "file_apply":
		requests = input.Changes
	default:
		return tool.EditPlan{}, fmt.Errorf("tool %s does not support edit plans", o.kind)
	}
	return o.tools.plan(ctx, requests)
}

func (o *operation) Descriptor() tool.Descriptor {
	properties := map[string]any{
		"path": map[string]any{"type": "string", "minLength": float64(1)},
	}
	required := []string{"path"}
	description := ""
	switch o.kind {
	case "file_list":
		description = "List structured directory entries with bounded pagination. path must be workspace-relative (use \".\" for workspace root); absolute paths inside the workspace are accepted and rewritten."
		properties["path"] = map[string]any{
			"type":        "string",
			"minLength":   float64(1),
			"description": "Workspace-relative directory path, or \".\" for the workspace root",
		}
		properties["offset"] = map[string]any{"type": "integer"}
		properties["limit"] = map[string]any{"type": "integer"}
	case "file_read":
		description = "Read a bounded UTF-8 line range or extract selected PDF pages. path is workspace-relative (absolute paths inside workspace are rewritten)."
		properties["path"] = map[string]any{
			"type":        "string",
			"minLength":   float64(1),
			"description": "Workspace-relative file path",
		}
		properties["start_line"] = map[string]any{"type": "integer"}
		properties["max_lines"] = map[string]any{"type": "integer"}
		properties["pages"] = map[string]any{"type": "string"}
	case "file_write":
		description = "Atomically write a UTF-8 text file. path is workspace-relative. " +
			"If the path already exists, call file_read for that exact path first; " +
			"new paths do not require a prior read."
		properties["content"] = map[string]any{"type": "string"}
		required = append(required, "content")
	case "file_edit":
		description = "Atomically replace one exact text occurrence. path is workspace-relative. " +
			"Call file_read for that exact path before editing it."
		properties["old"] = map[string]any{"type": "string"}
		properties["new"] = map[string]any{"type": "string"}
		required = append(required, "old", "new")
	case "file_apply":
		description = "Apply a set of file changes as one transaction: write, edit, " +
			"move and delete across several workspace-relative paths. Every change is " +
			"validated first, so nothing is written unless all of them can be applied. " +
			"Later changes see earlier ones, so the same file can be edited twice in " +
			"one call. Before calling, use file_read on every existing source or " +
			"destination path named by the transaction; new paths need no prior read. " +
			"Set dry_run to get the unified diff without writing."
		delete(properties, "path")
		properties["changes"] = map[string]any{
			"type": "array", "minItems": float64(1),
			"maxItems": float64(maxTransactionChanges),
			"items": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"op": map[string]any{
						"type": "string",
						"enum": []any{opWrite, opEdit, opMove, opDelete},
					},
					"path": map[string]any{
						"type": "string", "minLength": float64(1),
						"description": "Workspace-relative path the change applies to",
					},
					"content": map[string]any{
						"type": "string", "description": `Full file content for op "write"`,
					},
					"old": map[string]any{
						"type": "string", "description": `Exact text to replace for op "edit"`,
					},
					"new": map[string]any{
						"type": "string", "description": `Replacement text for op "edit"`,
					},
					"to": map[string]any{
						"type":        "string",
						"description": `Destination path for op "move"; it must not exist yet`,
					},
				},
				"required": []string{"op", "path"}, "additionalProperties": false,
			},
		}
		properties["dry_run"] = map[string]any{
			"type":        "boolean",
			"description": "Validate and return the unified diff without writing anything",
		}
		required = []string{"changes"}
	case "file_patch":
		description = "Atomically apply a standard unified diff across workspace files. " +
			"Call file_read for every existing file changed or deleted by the patch first."
		delete(properties, "path")
		properties["patch"] = map[string]any{"type": "string", "minLength": float64(1)}
		required = []string{"patch"}
	}
	capability, access := tool.CapabilityRead, tool.AccessRead
	repeat := tool.RepeatExecute
	requirement := tool.SandboxNone
	resolver := tool.ResourceResolver{Templates: []tool.ResourceTemplate{{
		Kind: "file", Field: "path", Access: tool.AccessRead,
	}}}
	var aliases []tool.Alias
	switch o.kind {
	case "file_read":
		repeat = tool.RepeatReplaySameTurn
		aliases = []tool.Alias{{Name: "read_file", Hidden: true}}
	case "file_list":
		repeat = tool.RepeatReplaySameTurn
		resolver.Templates[0].Kind = "directory"
		resolver.Templates[0].Tree = true
		aliases = []tool.Alias{{Name: "list_files", Hidden: true}}
	case "file_write":
		capability, access = tool.CapabilityWrite, tool.AccessWrite
		resolver.Templates[0].Access = tool.AccessWrite
		aliases = []tool.Alias{{Name: "write_file", Hidden: true}}
	case "file_edit":
		capability, access = tool.CapabilityWrite, tool.AccessWrite
		resolver.Templates[0].Access = tool.AccessWrite
		aliases = []tool.Alias{{Name: "edit_file", Hidden: true}}
	case "file_apply":
		capability, access = tool.CapabilityWrite, tool.AccessTree
		resolver = tool.ResourceResolver{ChangesField: "changes"}
	case "file_patch":
		capability, access = tool.CapabilityWrite, tool.AccessTree
		requirement = tool.SandboxStrong
		resolver = tool.ResourceResolver{PatchField: "patch"}
		aliases = []tool.Alias{{Name: "apply_patch", Hidden: true}}
	}
	return tool.Descriptor{
		Name: o.kind, Description: description, Visibility: tool.VisibleModel,
		Capability: capability, AccessMode: access,
		ResourceResolver: resolver, Aliases: aliases,
		ParallelPolicy: tool.ParallelConcurrent, RepeatPolicy: repeat,
		SandboxRequirement: requirement, Availability: tool.AvailabilityAvailable,
		InputSchema: map[string]any{
			"type": "object", "properties": properties, "required": required, "additionalProperties": false,
		},
	}
}

func (o *operation) Execute(ctx context.Context, raw json.RawMessage) (tool.Result, error) {
	var input struct {
		Path      string          `json:"path"`
		Content   string          `json:"content"`
		Old       string          `json:"old"`
		New       string          `json:"new"`
		Patch     string          `json:"patch"`
		Changes   []changeRequest `json:"changes"`
		DryRun    bool            `json:"dry_run"`
		StartLine int             `json:"start_line"`
		MaxLines  int             `json:"max_lines"`
		Pages     string          `json:"pages"`
		Offset    int             `json:"offset"`
		Limit     int             `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return tool.Result{}, err
	}
	if o.kind == "file_patch" {
		return o.tools.applyUnifiedPatch(ctx, input.Patch)
	}
	switch o.kind {
	case "file_read":
		file, err := o.tools.workspace.OpenFile(input.Path)
		if err != nil {
			return tool.Result{}, err
		}
		defer file.Close()
		if strings.EqualFold(filepath.Ext(input.Path), ".pdf") || input.Pages != "" {
			if input.StartLine != 0 || input.MaxLines != 0 {
				return tool.Result{}, errors.New("PDF pages cannot be combined with text line ranges")
			}
			return readPDF(file, input.Pages)
		}
		return readTextRange(file, input.StartLine, input.MaxLines)
	case "file_write":
		if _, _, err := o.tools.transact(ctx, []changeRequest{{
			Op: opWrite, Path: input.Path, Content: input.Content,
		}}, false); err != nil {
			return tool.Result{}, err
		}
		return tool.Result{Content: "written", Metadata: map[string]any{"bytes": len(input.Content)}}, nil
	case "file_edit":
		if _, _, err := o.tools.transact(ctx, []changeRequest{{
			Op: opEdit, Path: input.Path, Old: input.Old, New: input.New,
		}}, false); err != nil {
			return tool.Result{}, err
		}
		return tool.Result{Content: "edited", Metadata: map[string]any{"replacements": 1}}, nil
	case "file_apply":
		changes, diff, err := o.tools.transact(ctx, input.Changes, input.DryRun)
		if err != nil {
			return tool.Result{}, err
		}
		return applyResult(changes, diff, len(input.Changes), input.DryRun), nil
	case "file_list":
		directory, err := o.tools.workspace.OpenDirectory(input.Path)
		if err != nil {
			return tool.Result{}, err
		}
		defer directory.Close()
		return listDirectory(directory, input.Offset, input.Limit)
	default:
		return tool.Result{}, errors.New("unknown file operation")
	}
}

func readTextRange(file *os.File, startLine, maxLines int) (tool.Result, error) {
	if startLine < 0 || maxLines < 0 {
		return tool.Result{}, errors.New("start_line and max_lines must not be negative")
	}
	if startLine == 0 {
		startLine = 1
	}
	if maxLines == 0 {
		maxLines = defaultReadLines
	}
	maxLines = min(maxLines, maxReadLines)

	info, err := file.Stat()
	if err != nil {
		return tool.Result{}, err
	}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 1<<20)
	var lines []string
	lineNumber := 0
	hasMore := false
	for scanner.Scan() {
		lineNumber++
		if strings.IndexByte(scanner.Text(), 0) >= 0 || !utf8.Valid(scanner.Bytes()) {
			return tool.Result{}, errors.New("binary or non-UTF-8 file cannot be read as text")
		}
		if lineNumber < startLine {
			continue
		}
		if len(lines) == maxLines {
			hasMore = true
			break
		}
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return tool.Result{}, fmt.Errorf("read text lines: %w", err)
	}
	metadata := map[string]any{
		"bytes": info.Size(), "start_line": startLine, "returned_lines": len(lines),
		"max_lines": maxLines, "has_more": hasMore,
	}
	if hasMore {
		metadata["next_start_line"] = startLine + len(lines)
	}
	return tool.Result{
		Content: strings.Join(lines, "\n"), Truncated: hasMore, Metadata: metadata,
	}, nil
}

func readPDF(file *os.File, pagesExpression string) (tool.Result, error) {
	info, err := file.Stat()
	if err != nil {
		return tool.Result{}, err
	}
	reader, err := pdf.NewReader(file, info.Size())
	if err != nil {
		return tool.Result{}, fmt.Errorf("open PDF: %w", err)
	}
	pages, err := parsePageRange(pagesExpression, reader.NumPage())
	if err != nil {
		return tool.Result{}, err
	}
	text := make([]string, 0, len(pages))
	for _, pageNumber := range pages {
		value, err := reader.Page(pageNumber).GetPlainText(nil)
		if err != nil {
			return tool.Result{}, fmt.Errorf("extract PDF page %d: %w", pageNumber, err)
		}
		text = append(text, strings.TrimSpace(value))
	}
	return tool.Result{
		Content: strings.Join(text, "\n\f\n"),
		Metadata: map[string]any{
			"format": "pdf", "pages": pages, "page_count": len(pages),
			"total_pages": reader.NumPage(),
		},
	}, nil
}

func parsePageRange(expression string, total int) ([]int, error) {
	if total <= 0 {
		return nil, errors.New("PDF has no pages")
	}
	if strings.TrimSpace(expression) == "" {
		pages := make([]int, total)
		for index := range pages {
			pages[index] = index + 1
		}
		return pages, nil
	}
	seen := make(map[int]bool)
	var pages []int
	for part := range strings.SplitSeq(expression, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, errors.New("invalid empty PDF page range")
		}
		start, end := 0, 0
		if left, right, found := strings.Cut(part, "-"); found {
			var err error
			start, err = strconv.Atoi(strings.TrimSpace(left))
			if err != nil {
				return nil, fmt.Errorf("invalid PDF page range %q", part)
			}
			if strings.TrimSpace(right) == "" {
				end = total
			} else if end, err = strconv.Atoi(strings.TrimSpace(right)); err != nil {
				return nil, fmt.Errorf("invalid PDF page range %q", part)
			}
		} else {
			var err error
			start, err = strconv.Atoi(part)
			if err != nil {
				return nil, fmt.Errorf("invalid PDF page %q", part)
			}
			end = start
		}
		if start < 1 || end < start || end > total {
			return nil, fmt.Errorf("PDF page range %q is outside 1-%d", part, total)
		}
		for page := start; page <= end; page++ {
			if !seen[page] {
				seen[page] = true
				pages = append(pages, page)
			}
		}
	}
	sort.Ints(pages)
	return pages, nil
}

func listDirectory(directory *os.File, offset, limit int) (tool.Result, error) {
	if offset < 0 || limit < 0 {
		return tool.Result{}, errors.New("offset and limit must not be negative")
	}
	if limit == 0 {
		limit = defaultListLimit
	}
	limit = min(limit, maxListLimit)
	entries, err := directory.ReadDir(-1)
	if err != nil {
		return tool.Result{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	total := len(entries)
	offset = min(offset, total)
	end := min(total, offset+limit)
	items := make([]map[string]any, 0, end-offset)
	for _, entry := range entries[offset:end] {
		info, err := entry.Info()
		if err != nil {
			return tool.Result{}, err
		}
		entryType := "file"
		switch {
		case entry.Type()&os.ModeSymlink != 0:
			entryType = "symlink"
		case entry.IsDir():
			entryType = "directory"
		}
		items = append(items, map[string]any{
			"name": entry.Name(), "path": entry.Name(),
			"type": entryType, "size": info.Size(), "mode": info.Mode().Perm().String(),
		})
	}
	hasMore := end < total
	payload := map[string]any{"entries": items, "total": total, "offset": offset, "has_more": hasMore}
	if hasMore {
		payload["next_offset"] = end
	}
	content, err := json.Marshal(payload)
	if err != nil {
		return tool.Result{}, err
	}
	return tool.Result{
		Content: string(content), Truncated: hasMore,
		Metadata: map[string]any{
			"total": total, "returned": len(items), "offset": offset,
			"has_more": hasMore, "next_offset": end,
		},
	}, nil
}

func (t *Tools) applyUnifiedPatch(ctx context.Context, patch string) (tool.Result, error) {
	if strings.TrimSpace(patch) == "" {
		return tool.Result{}, errors.New("patch is required")
	}
	for _, name := range patchPaths(patch) {
		canonical, err := t.resolve(name, sandbox.AllowMissing)
		if err != nil {
			return tool.Result{}, fmt.Errorf("unsafe patch path: %w", err)
		}
		if err := workspacejournal.ValidateExpectedWrite(ctx, canonical); err != nil {
			return tool.Result{}, fmt.Errorf("patch freshness %q: %w", name, err)
		}
	}
	directory, err := process.OpenPinnedDirectory(t.backend, t.root)
	if err != nil {
		return tool.Result{}, err
	}
	defer directory.Close()
	sandboxBackend, requireStrong := guard.ProcessSandbox(ctx, t.backend)
	check, err := process.NewCommand(ctx, process.Options{
		Path: "git", Args: []string{"apply", "--check", "--whitespace=nowarn", "-"},
		Dir: t.root, DirFile: directory, Sandbox: sandboxBackend, RequireStrongSandbox: requireStrong,
	})
	if err != nil {
		if requireStrong && errors.Is(err, guard.ErrSandboxDenied) {
			return tool.Result{}, guard.MarkSandboxDenial(err, "file_patch check")
		}
		return tool.Result{}, err
	}
	check.Stdin = strings.NewReader(patch)
	if output, err := check.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(output))
		return tool.Result{}, fmt.Errorf("patch conflict: %s", msg)
	}
	apply, err := process.NewCommand(ctx, process.Options{
		Path: "git", Args: []string{"apply", "--whitespace=nowarn", "-"},
		Dir: t.root, DirFile: directory, Sandbox: sandboxBackend, RequireStrongSandbox: requireStrong,
	})
	if err != nil {
		if requireStrong && errors.Is(err, guard.ErrSandboxDenied) {
			return tool.Result{}, guard.MarkSandboxDenial(err, "file_patch apply")
		}
		return tool.Result{}, err
	}
	apply.Stdin = strings.NewReader(patch)
	if output, err := apply.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(output))
		return tool.Result{}, fmt.Errorf("apply patch: %s", msg)
	}
	return tool.Result{Content: "patched", Metadata: map[string]any{"format": "unified"}}, nil
}

func (t *Tools) resolve(name string, mode sandbox.ResolveMode) (string, error) {
	return t.workspace.Resolve(name, mode)
}

type replacement struct {
	old string
	new string
}

func (t *Tools) edit(name string, replacements []replacement) (tool.Result, error) {
	file, err := t.workspace.OpenFile(name)
	if err != nil {
		return tool.Result{}, err
	}
	data, err := io.ReadAll(file)
	if err != nil {
		file.Close()
		return tool.Result{}, err
	}
	info, err := file.Stat()
	closeErr := file.Close()
	if err != nil {
		return tool.Result{}, err
	}
	if closeErr != nil {
		return tool.Result{}, closeErr
	}
	if isBinary(data) {
		return tool.Result{}, errors.New("binary file cannot be edited")
	}
	content := string(data)
	for index, item := range replacements {
		if item.old == "" {
			return tool.Result{}, fmt.Errorf("replacement %d has empty old text", index)
		}
		if count := strings.Count(content, item.old); count != 1 {
			return tool.Result{}, fmt.Errorf("replacement %d matched %d times, want exactly once", index, count)
		}
		content = strings.Replace(content, item.old, item.new, 1)
	}
	if err := t.atomicWrite(name, []byte(content), info.Mode().Perm()); err != nil {
		return tool.Result{}, err
	}
	return tool.Result{Content: "edited", Metadata: map[string]any{"replacements": len(replacements)}}, nil
}

func (t *Tools) atomicWrite(name string, data []byte, mode fs.FileMode) error {
	return t.workspace.AtomicWrite(name, data, mode)
}

func patchPaths(patch string) []string {
	seen := make(map[string]bool)
	var paths []string
	for line := range strings.SplitSeq(patch, "\n") {
		if !strings.HasPrefix(line, "+++ ") && !strings.HasPrefix(line, "--- ") {
			continue
		}
		name := strings.Fields(strings.TrimSpace(line[4:]))
		if len(name) == 0 || name[0] == "/dev/null" {
			continue
		}
		path := strings.TrimPrefix(strings.TrimPrefix(name[0], "a/"), "b/")
		if !seen[path] {
			seen[path] = true
			paths = append(paths, path)
		}
	}
	return paths
}

func isBinary(data []byte) bool {
	for _, value := range data {
		if value == 0 {
			return true
		}
	}
	return false
}
