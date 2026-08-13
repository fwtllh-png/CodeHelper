package file

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/persist/workspacejournal"
	"github.com/fwtllh-png/CodeHelper/internal/platform/textdiff"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

// Operations a file_apply transaction can carry.
const (
	opWrite  = "write"
	opEdit   = "edit"
	opMove   = "move"
	opDelete = "delete"
)

// maxTransactionChanges bounds one call so a runaway generation cannot pin an
// unbounded amount of file content in memory during validation.
const maxTransactionChanges = 64

const maxEditPlanContentBytes = 1 << 20

// changeRequest is one operation of a transaction. Fields belonging to other
// operations must be absent: silently ignoring a stray "old" on a write would
// hide the model's confusion behind a successful call.
type changeRequest struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	Content string `json:"content"`
	Old     string `json:"old"`
	New     string `json:"new"`
	To      string `json:"to"`
}

func (r changeRequest) validate() error {
	if strings.TrimSpace(r.Path) == "" {
		return errors.New("path is required")
	}
	var unexpected []string
	switch r.Op {
	case opWrite:
		unexpected = named(r, "old", "new", "to")
	case opEdit:
		unexpected = named(r, "content", "to")
		if r.Old == "" {
			return errors.New(`op "edit" requires a non-empty "old"`)
		}
	case opMove:
		unexpected = named(r, "content", "old", "new")
		if strings.TrimSpace(r.To) == "" {
			return errors.New(`op "move" requires "to"`)
		}
	case opDelete:
		unexpected = named(r, "content", "old", "new", "to")
	case "":
		return errors.New("op is required")
	default:
		return fmt.Errorf("unknown op %q, want write, edit, move or delete", r.Op)
	}
	if len(unexpected) != 0 {
		return fmt.Errorf("op %q does not take %s", r.Op, strings.Join(unexpected, ", "))
	}
	return nil
}

func named(request changeRequest, fields ...string) []string {
	values := map[string]string{
		"content": request.Content, "old": request.Old,
		"new": request.New, "to": request.To,
	}
	var present []string
	for _, field := range fields {
		if values[field] != "" {
			present = append(present, field)
		}
	}
	return present
}

// plannedFile is one path inside a transaction: what validation read from disk,
// and what the composed operations leave behind.
type plannedFile struct {
	name      string
	relative  string
	canonical string
	mode      fs.FileMode

	existed bool
	before  []byte

	exists bool
	after  []byte
}

// kind classifies the net effect on the path, empty when the transaction leaves
// its content as it found it.
func (p *plannedFile) kind() string {
	switch {
	case !p.existed && p.exists:
		return workspacejournal.ChangeCreated
	case p.existed && !p.exists:
		return workspacejournal.ChangeDeleted
	case p.existed && p.exists && !bytes.Equal(p.before, p.after):
		return workspacejournal.ChangeModified
	default:
		return ""
	}
}

// transaction composes a set of changes against an in-memory view of the
// workspace, so every failure mode of validation costs zero writes.
type transaction struct {
	tools *Tools
	files map[string]*plannedFile
	order []string
}

// Change is one operation of an Apply transaction (write / edit / move / delete).
type Change struct {
	Op      string `json:"op"`
	Path    string `json:"path"`
	Content string `json:"content,omitempty"`
	Old     string `json:"old,omitempty"`
	New     string `json:"new,omitempty"`
	To      string `json:"to,omitempty"`
}

// AppliedChange is the per-path outcome of a transaction, reported to the model
// and to the receipt.
type AppliedChange struct {
	Path    string `json:"path"`
	Kind    string `json:"kind"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// Apply runs a validate-then-apply transaction. dryRun renders a unified diff
// and writes nothing. Used by file_apply and by integrate_agent.
func (t *Tools) Apply(
	ctx context.Context, changes []Change, dryRun bool,
) ([]AppliedChange, string, error) {
	requests := changeRequests(changes)
	return t.transact(ctx, requests, dryRun)
}

// PlanApply returns the exact edit plan Apply would execute without writing.
// Runtime hosts use it for trusted, plan-bound workspace operations such as
// merging an isolated Chat worktree back into the editor workspace.
func (t *Tools) PlanApply(
	ctx context.Context, changes []Change,
) (tool.EditPlan, error) {
	return t.plan(ctx, changeRequests(changes))
}

func changeRequests(changes []Change) []changeRequest {
	requests := make([]changeRequest, len(changes))
	for index, change := range changes {
		requests[index] = changeRequest{
			Op: change.Op, Path: change.Path, Content: change.Content,
			Old: change.Old, New: change.New, To: change.To,
		}
	}
	return requests
}

// ResultFromApply builds the model-visible tool result for an Apply call.
func ResultFromApply(changes []AppliedChange, diff string, dryRun bool) tool.Result {
	return applyResult(changes, diff, len(changes), dryRun)
}

// transact runs the two phases: validate everything (freshness, sandbox
// resolution, op preconditions, composition) and only then touch the disk. With
// dryRun the composed result is rendered as a unified diff and nothing is
// written.
func (t *Tools) transact(
	ctx context.Context, requests []changeRequest, dryRun bool,
) ([]AppliedChange, string, error) {
	if len(requests) == 0 {
		return nil, "", tool.Precondition(errors.New("changes must not be empty"))
	}
	if len(requests) > maxTransactionChanges {
		return nil, "", tool.Precondition(fmt.Errorf(
			"transaction has %d changes, at most %d are allowed",
			len(requests), maxTransactionChanges,
		))
	}
	for index, request := range requests {
		if err := request.validate(); err != nil {
			return nil, "", tool.Precondition(fmt.Errorf("change %d: %w", index, err))
		}
	}
	// Freshness first, before any op precondition: an edit whose "old" text no
	// longer matches because someone else rewrote the file must report the stale
	// read, not the failed match.
	if err := t.checkFreshness(ctx, requests); err != nil {
		return nil, "", err
	}
	transaction := &transaction{tools: t, files: make(map[string]*plannedFile)}
	// Composition only reads, so any failure here has changed nothing and the
	// caller can safely retry with different changes.
	for index, request := range requests {
		if err := transaction.compose(request); err != nil {
			return nil, "", changePrecondition(index, request, err)
		}
	}
	changes, diff, err := transaction.summarize()
	if err != nil {
		return nil, "", err
	}
	if dryRun {
		return changes, diff, nil
	}
	if err := transaction.commit(); err != nil {
		return nil, "", err
	}
	return changes, diff, nil
}

func (t *Tools) plan(
	ctx context.Context, requests []changeRequest,
) (tool.EditPlan, error) {
	if len(requests) == 0 {
		return tool.EditPlan{}, tool.Precondition(errors.New("changes must not be empty"))
	}
	if len(requests) > maxTransactionChanges {
		return tool.EditPlan{}, tool.Precondition(fmt.Errorf(
			"transaction has %d changes, at most %d are allowed",
			len(requests), maxTransactionChanges,
		))
	}
	for index, request := range requests {
		if err := request.validate(); err != nil {
			return tool.EditPlan{}, tool.Precondition(fmt.Errorf("change %d: %w", index, err))
		}
	}
	if err := t.checkFreshness(ctx, requests); err != nil {
		return tool.EditPlan{}, err
	}
	transaction := &transaction{tools: t, files: make(map[string]*plannedFile)}
	for index, request := range requests {
		if err := transaction.compose(request); err != nil {
			return tool.EditPlan{}, changePrecondition(index, request, err)
		}
	}
	_, diff, err := transaction.summarize()
	if err != nil {
		return tool.EditPlan{}, err
	}
	plan := tool.EditPlan{Diff: diff}
	total := len(diff)
	for _, planned := range transaction.changed() {
		if isBinary(planned.before) || isBinary(planned.after) {
			return tool.EditPlan{}, tool.Precondition(fmt.Errorf(
				"binary edit plan %q cannot be previewed", planned.relative,
			))
		}
		total += len(planned.before) + len(planned.after)
		if total > maxEditPlanContentBytes {
			return tool.EditPlan{}, tool.Precondition(fmt.Errorf(
				"edit plan exceeds %d bytes", maxEditPlanContentBytes,
			))
		}
		plan.Files = append(plan.Files, tool.EditPlanFile{
			Path: planned.relative, Kind: planned.kind(),
			Before: string(planned.before), After: string(planned.after),
			BeforeExists: planned.existed, AfterExists: planned.exists,
			BeforeDigest: digestContent(planned.before, planned.existed),
			AfterDigest:  digestContent(planned.after, planned.exists),
		})
	}
	if len(plan.Files) == 0 {
		return tool.EditPlan{}, tool.Precondition(errors.New("edit plan has no changes"))
	}
	encoded, err := json.Marshal(plan.Files)
	if err != nil {
		return tool.EditPlan{}, err
	}
	sum := sha256.Sum256(append(encoded, diff...))
	plan.ID = hex.EncodeToString(sum[:])
	return plan, nil
}

func digestContent(content []byte, exists bool) string {
	if !exists {
		return "missing"
	}
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// checkFreshness enforces the read-before-edit contract for every path the
// transaction names, including a move's source and target.
func (t *Tools) checkFreshness(ctx context.Context, requests []changeRequest) error {
	seen := make(map[string]bool)
	for _, request := range requests {
		for _, name := range []string{request.Path, request.To} {
			if strings.TrimSpace(name) == "" {
				continue
			}
			canonical, err := t.resolve(name, sandbox.AllowMissing)
			if err != nil {
				return err
			}
			if seen[canonical] {
				continue
			}
			seen[canonical] = true
			if err := workspacejournal.ValidateExpectedWrite(ctx, canonical); err != nil {
				return fmt.Errorf("write freshness %q: %w", name, err)
			}
		}
	}
	return nil
}

// load returns the transaction's view of a path, reading it from disk the first
// time it is touched. Later operations see earlier ones' results, so one
// transaction can edit the same file twice without an intervening re-read.
func (x *transaction) load(name string) (*plannedFile, error) {
	canonical, err := x.tools.resolve(name, sandbox.AllowMissing)
	if err != nil {
		return nil, err
	}
	if planned, ok := x.files[canonical]; ok {
		return planned, nil
	}
	relative, err := filepath.Rel(x.tools.root, canonical)
	if err != nil {
		return nil, err
	}
	planned := &plannedFile{
		name: name, relative: filepath.ToSlash(relative),
		canonical: canonical, mode: 0o644,
	}
	data, mode, err := x.tools.readFile(name)
	switch {
	case err == nil:
		planned.existed, planned.exists = true, true
		planned.before, planned.after = data, data
		planned.mode = mode
	case errors.Is(err, os.ErrNotExist):
	default:
		return nil, err
	}
	x.files[canonical] = planned
	x.order = append(x.order, canonical)
	return planned, nil
}

func (x *transaction) compose(request changeRequest) error {
	planned, err := x.load(request.Path)
	if err != nil {
		return err
	}
	switch request.Op {
	case opWrite:
		planned.exists, planned.after = true, []byte(request.Content)
	case opEdit:
		if !planned.exists {
			return errors.New("file does not exist")
		}
		next, err := replaceOnce(planned.after, request.Old, request.New)
		if err != nil {
			return err
		}
		planned.after = next
	case opDelete:
		if !planned.exists {
			return errors.New("file does not exist")
		}
		planned.exists, planned.after = false, nil
	case opMove:
		if !planned.exists {
			return errors.New("file does not exist")
		}
		target, err := x.load(request.To)
		if err != nil {
			return err
		}
		if target.canonical == planned.canonical {
			return errors.New(`"to" is the same file as "path"`)
		}
		if target.exists {
			return fmt.Errorf("move target %q already exists", request.To)
		}
		target.exists, target.after, target.mode = true, planned.after, planned.mode
		planned.exists, planned.after = false, nil
	}
	return nil
}

// changed lists the files whose content the transaction alters, in the order
// their paths were first touched.
func (x *transaction) changed() []*plannedFile {
	var files []*plannedFile
	for _, canonical := range x.order {
		if planned := x.files[canonical]; planned.kind() != "" {
			files = append(files, planned)
		}
	}
	return files
}

// summarize reports per-path line statistics and renders the unified diff used
// for previews. Binary content is counted as zero lines rather than failing the
// transaction: writing bytes is allowed, only diffing them is not.
func (x *transaction) summarize() ([]AppliedChange, string, error) {
	var changes []AppliedChange
	var diff strings.Builder
	for _, planned := range x.changed() {
		before := textdiff.Content{Data: planned.before, Missing: !planned.existed}
		after := textdiff.Content{Data: planned.after, Missing: !planned.exists}
		body, stats, err := textdiff.Unified(planned.relative, before, after, textdiff.DefaultContext)
		switch {
		case err == nil:
			diff.WriteString(body)
		case errors.Is(err, textdiff.ErrBinary):
			fmt.Fprintf(&diff, "Binary file %s differs\n", planned.relative)
		default:
			return nil, "", err
		}
		changes = append(changes, AppliedChange{
			Path: planned.relative, Kind: planned.kind(),
			Added: stats.Added, Removed: stats.Removed,
		})
	}
	return changes, diff.String(), nil
}

// commit writes the composed result to disk. A failure part way through undoes
// the writes already made, so the caller sees either the whole transaction or
// none of it.
func (x *transaction) commit() error {
	var done []*plannedFile
	fail := func(planned *plannedFile, cause error) error {
		cause = fmt.Errorf("apply %q: %w", planned.relative, cause)
		var failures []string
		for index := len(done) - 1; index >= 0; index-- {
			if err := x.restore(done[index]); err != nil {
				failures = append(failures, err.Error())
			}
		}
		if len(failures) != 0 {
			return fmt.Errorf(
				"%w; the workspace is partially changed because rollback failed: %s",
				cause, strings.Join(failures, "; "),
			)
		}
		return cause
	}
	// Writes come before removals: if the process dies between the two phases the
	// workspace holds an extra copy of the data rather than none.
	for _, planned := range x.changed() {
		if !planned.exists {
			continue
		}
		if err := x.write(planned); err != nil {
			return fail(planned, err)
		}
		done = append(done, planned)
	}
	for _, planned := range x.changed() {
		if planned.exists {
			continue
		}
		if err := x.tools.workspace.Remove(planned.name); err != nil {
			return fail(planned, err)
		}
		done = append(done, planned)
	}
	return nil
}

func (x *transaction) write(planned *plannedFile) error {
	if planned.existed {
		return x.tools.workspace.AtomicWrite(planned.name, planned.after, planned.mode)
	}
	return x.tools.workspace.AtomicCreate(planned.name, planned.after, planned.mode)
}

func (x *transaction) restore(planned *plannedFile) error {
	if planned.existed {
		return x.tools.workspace.AtomicWrite(planned.name, planned.before, planned.mode)
	}
	if err := x.tools.workspace.Remove(planned.name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func (t *Tools) readFile(name string) ([]byte, fs.FileMode, error) {
	file, err := t.workspace.OpenFile(name)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		return nil, 0, err
	}
	info, err := file.Stat()
	if err != nil {
		return nil, 0, err
	}
	return data, info.Mode().Perm(), nil
}

func changePrecondition(index int, request changeRequest, err error) error {
	wrapped := fmt.Errorf(
		"change %d (%s %s): %w",
		index,
		request.Op,
		request.Path,
		err,
	)
	var match *editMatchError
	if errors.As(err, &match) {
		requiredAction := "reread_exact_range"
		if match.excerpt != "" {
			requiredAction = "replace_failed_change"
		}
		wrapped = tool.WithRecoveryHint(wrapped, tool.RecoveryHint{
			ErrorCategory:  "edit_precondition_miss",
			RequiredAction: requiredAction,
			Path:           request.Path,
			RetryOriginal:  false,
			FailedChange:   index + 1,
			MatchCount:     match.count,
			StartLine:      match.startLine,
			EndLine:        match.endLine,
			CurrentExcerpt: match.excerpt,
		})
	}
	return tool.Precondition(wrapped)
}

type editMatchError struct {
	count     int
	startLine int
	endLine   int
	excerpt   string
}

func (e *editMatchError) Error() string {
	return fmt.Sprintf("old text matched %d times, want exactly once", e.count)
}

// replaceOnce replaces the single occurrence of old. Anything else is a failed
// precondition: zero matches means the model is editing text it never read, and
// several means it cannot know which one it hit.
func replaceOnce(data []byte, old, new string) ([]byte, error) {
	if isBinary(data) {
		return nil, errors.New("binary file cannot be edited")
	}
	content := string(data)
	if count := strings.Count(content, old); count != 1 {
		excerpt, startLine, endLine := closestEditExcerpt(content, old)
		return nil, &editMatchError{
			count: count, startLine: startLine, endLine: endLine, excerpt: excerpt,
		}
	}
	return []byte(strings.Replace(content, old, new, 1)), nil
}

func closestEditExcerpt(content, old string) (string, int, int) {
	lines := strings.Split(content, "\n")
	compactLines := make([]string, len(lines))
	starts := make([]int, len(lines))
	var compact strings.Builder
	for index, line := range lines {
		starts[index] = compact.Len()
		compactLines[index] = strings.Map(func(value rune) rune {
			if unicode.IsSpace(value) {
				return -1
			}
			return value
		}, line)
		compact.WriteString(compactLines[index])
	}
	compactContent := compact.String()
	for _, sourceLine := range strings.Split(old, "\n") {
		candidate := []rune(strings.Map(func(value rune) rune {
			if unicode.IsSpace(value) {
				return -1
			}
			return value
		}, sourceLine))
		for _, limit := range []int{48, 32, 24, 16, 12, 8} {
			if len(candidate) < limit {
				continue
			}
			anchor := string(candidate[:limit])
			if strings.Count(compactContent, anchor) != 1 {
				continue
			}
			offset := strings.Index(compactContent, anchor)
			lineIndex := 0
			for index := 1; index < len(starts) && starts[index] <= offset; index++ {
				lineIndex = index
			}
			first := max(0, lineIndex-2)
			last := min(len(lines), lineIndex+4)
			excerpt := strings.Join(lines[first:last], "\n")
			if len(excerpt) > 2000 {
				excerpt = excerpt[:2000]
			}
			return excerpt, first + 1, last
		}
	}
	return "", 0, 0
}

// formatApplied renders the model-visible summary of a committed transaction.
func formatApplied(changes []AppliedChange) string {
	if len(changes) == 0 {
		return "no changes"
	}
	var builder strings.Builder
	for _, change := range changes {
		fmt.Fprintf(
			&builder, "%s %s +%d -%d\n",
			change.Kind, change.Path, change.Added, change.Removed,
		)
	}
	return strings.TrimRight(builder.String(), "\n")
}

func applyMetadata(changes []AppliedChange, operations int, dryRun bool) map[string]any {
	added, removed := 0, 0
	files := make([]map[string]any, 0, len(changes))
	for _, change := range changes {
		added += change.Added
		removed += change.Removed
		files = append(files, map[string]any{
			"path": change.Path, "kind": change.Kind,
			"added": change.Added, "removed": change.Removed,
		})
	}
	return map[string]any{
		"operations": operations, "files": files,
		"added": added, "removed": removed, "dry_run": dryRun,
	}
}

func applyResult(changes []AppliedChange, diff string, operations int, dryRun bool) tool.Result {
	metadata := applyMetadata(changes, operations, dryRun)
	if !dryRun {
		return tool.Result{Content: formatApplied(changes), Metadata: metadata}
	}
	content := diff
	if strings.TrimSpace(content) == "" {
		content = "no changes"
	}
	return tool.Result{Content: content, Metadata: metadata}
}
