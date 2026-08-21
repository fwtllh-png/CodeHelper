package memory

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const RecordSchemaVersion = 1

type Scope string

const (
	ScopeUser       Scope = "user"
	ScopeWorkspace  Scope = "workspace"
	ScopeRepository Scope = "repository"
)

type Category string

const (
	CategoryPreference Category = "preference"
	CategoryConvention Category = "convention"
	CategoryFact       Category = "fact"
)

type MemoryRecord struct {
	ID        string     `json:"id"`
	Scope     Scope      `json:"scope"`
	ScopeID   string     `json:"scope_id,omitempty"`
	Category  Category   `json:"category"`
	Text      string     `json:"text"`
	Source    string     `json:"source"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Digest    string     `json:"digest"`
}

type Metadata struct {
	ID        string     `json:"id"`
	Scope     Scope      `json:"scope"`
	ScopeID   string     `json:"scope_id,omitempty"`
	Category  Category   `json:"category"`
	Source    string     `json:"source"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Digest    string     `json:"digest"`
}

type CreateRequest struct {
	Scope     Scope
	ScopeID   string
	Category  Category
	Text      string
	Source    string
	ExpiresAt *time.Time
}

type UpdateRequest struct {
	ID        string
	Text      string
	Category  Category
	ExpiresAt *time.Time
	SetExpiry bool
}

type Query struct {
	Text          string
	Scope         Scope
	Category      Category
	WorkspaceID   string
	RepositoryID  string
	PinnedIDs     []string
	MaxCandidates int
	MaxBytes      int
	Now           time.Time
}

type Selection struct {
	Generation     uint64   `json:"generation"`
	CandidateCount int      `json:"candidate_count"`
	SelectedIDs    []string `json:"selected_ids"`
	Truncated      bool     `json:"truncated"`
}

type recordFile struct {
	Version    int            `json:"version"`
	Generation uint64         `json:"generation"`
	Records    []MemoryRecord `json:"records"`
	Digest     string         `json:"digest"`
}

func (s *Store) Remember(request CreateRequest) (MemoryRecord, bool, error) {
	if s == nil {
		return MemoryRecord{}, false, ErrDisabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := acquireRecordsLock(s.lockFile)
	if err != nil {
		return MemoryRecord{}, false, err
	}
	defer lock.Close()
	record, err := s.normalizeCreate(request)
	if err != nil {
		return MemoryRecord{}, false, err
	}
	file, _, err := s.loadRecordFileLocked()
	if err != nil {
		return MemoryRecord{}, false, err
	}
	for _, existing := range file.Records {
		if existing.Digest == record.Digest {
			return existing, false, nil
		}
	}
	now := time.Now().UTC()
	record.CreatedAt = now
	record.UpdatedAt = now
	record.ID = stableRecordID(record, now)
	record.Digest = recordDigest(record)
	file.Records = append(file.Records, record)
	file.Generation++
	if err := s.saveRecordFileLocked(file); err != nil {
		return MemoryRecord{}, false, err
	}
	return record, true, nil
}

func (s *Store) List(query Query) ([]Metadata, uint64, error) {
	records, generation, err := s.Search(query)
	if err != nil {
		return nil, 0, err
	}
	result := make([]Metadata, len(records))
	for index, record := range records {
		result[index] = Metadata{
			ID: record.ID, Scope: record.Scope, ScopeID: record.ScopeID,
			Category: record.Category, Source: record.Source,
			CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
			ExpiresAt: record.ExpiresAt, Digest: record.Digest,
		}
	}
	return result, generation, nil
}

func (s *Store) Get(id string) (MemoryRecord, error) {
	if s == nil {
		return MemoryRecord{}, ErrDisabled
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return MemoryRecord{}, errors.New("memory record id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, _, err := s.loadRecordFileLocked()
	if err != nil {
		return MemoryRecord{}, err
	}
	for _, record := range file.Records {
		if record.ID == id && s.recordVisible(record) {
			return cloneRecord(record), nil
		}
	}
	return MemoryRecord{}, os.ErrNotExist
}

func (s *Store) Update(request UpdateRequest) (MemoryRecord, error) {
	if s == nil {
		return MemoryRecord{}, ErrDisabled
	}
	request.ID = strings.TrimSpace(request.ID)
	text, err := normalizeText(request.Text)
	if request.ID == "" || err != nil {
		if err != nil {
			return MemoryRecord{}, err
		}
		return MemoryRecord{}, errors.New("memory record id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := acquireRecordsLock(s.lockFile)
	if err != nil {
		return MemoryRecord{}, err
	}
	defer lock.Close()
	file, _, err := s.loadRecordFileLocked()
	if err != nil {
		return MemoryRecord{}, err
	}
	for index := range file.Records {
		if file.Records[index].ID != request.ID ||
			!s.recordVisible(file.Records[index]) {
			continue
		}
		record := file.Records[index]
		record.Text = text
		if request.Category != "" {
			record.Category = request.Category
		}
		if !record.Category.Valid() {
			return MemoryRecord{}, errors.New("memory category is invalid")
		}
		if request.SetExpiry {
			record.ExpiresAt = cloneTime(request.ExpiresAt)
		}
		record.UpdatedAt = time.Now().UTC()
		record.Digest = recordDigest(record)
		for otherIndex, other := range file.Records {
			if otherIndex != index && other.Digest == record.Digest {
				return MemoryRecord{}, errors.New("memory update duplicates an existing record")
			}
		}
		file.Records[index] = record
		file.Generation++
		if err := s.saveRecordFileLocked(file); err != nil {
			return MemoryRecord{}, err
		}
		return cloneRecord(record), nil
	}
	return MemoryRecord{}, os.ErrNotExist
}

func (s *Store) Forget(id string) (bool, uint64, error) {
	if s == nil {
		return false, 0, ErrDisabled
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return false, 0, errors.New("memory record id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := acquireRecordsLock(s.lockFile)
	if err != nil {
		return false, 0, err
	}
	defer lock.Close()
	file, _, err := s.loadRecordFileLocked()
	if err != nil {
		return false, 0, err
	}
	for index, record := range file.Records {
		if record.ID != id || !s.recordVisible(record) {
			continue
		}
		file.Records = append(file.Records[:index], file.Records[index+1:]...)
		file.Generation++
		if err := s.saveRecordFileLocked(file); err != nil {
			return false, 0, err
		}
		return true, file.Generation, nil
	}
	return false, file.Generation, nil
}

func (s *Store) Search(query Query) ([]MemoryRecord, uint64, error) {
	records, generation, _, _, err := s.search(query)
	return records, generation, err
}

func (s *Store) search(
	query Query,
) ([]MemoryRecord, uint64, int, bool, error) {
	if s == nil {
		return nil, 0, 0, false, ErrDisabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, _, err := s.loadRecordFileLocked()
	if err != nil {
		return nil, 0, 0, false, err
	}
	now := query.Now
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if query.WorkspaceID == "" {
		query.WorkspaceID = s.options.WorkspaceID
	}
	if query.RepositoryID == "" {
		query.RepositoryID = s.options.RepositoryID
	}
	if query.MaxCandidates <= 0 {
		query.MaxCandidates = s.options.MaxCandidates
	}
	if query.MaxBytes <= 0 {
		query.MaxBytes = s.options.MaxPromptBytes
	}
	if query.Scope != "" && !query.Scope.Valid() {
		return nil, 0, 0, false,
			errors.New("memory query scope is invalid")
	}
	if query.Category != "" && !query.Category.Valid() {
		return nil, 0, 0, false,
			errors.New("memory query category is invalid")
	}
	pinned := make(map[string]int, len(query.PinnedIDs))
	for index, id := range query.PinnedIDs {
		pinned[id] = index
	}
	terms := lexicalTerms(query.Text)
	type candidate struct {
		record MemoryRecord
		pinned int
		scope  int
		score  int
	}
	var candidates []candidate
	for _, record := range file.Records {
		if record.ExpiresAt != nil && !record.ExpiresAt.After(now) {
			continue
		}
		if query.Scope != "" && record.Scope != query.Scope ||
			query.Category != "" && record.Category != query.Category {
			continue
		}
		scopeRank, allowed := scopeMatch(record, query)
		if !allowed {
			continue
		}
		pinRank := len(pinned) + 1
		if index, ok := pinned[record.ID]; ok {
			pinRank = index
		}
		candidates = append(candidates, candidate{
			record: record, pinned: pinRank, scope: scopeRank,
			score: lexicalScore(record, terms),
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		left, right := candidates[i], candidates[j]
		if left.pinned != right.pinned {
			return left.pinned < right.pinned
		}
		if left.scope != right.scope {
			return left.scope < right.scope
		}
		if left.score != right.score {
			return left.score > right.score
		}
		if !left.record.UpdatedAt.Equal(right.record.UpdatedAt) {
			return left.record.UpdatedAt.After(right.record.UpdatedAt)
		}
		return left.record.ID < right.record.ID
	})
	var result []MemoryRecord
	bytesUsed := 0
	truncated := false
	for _, candidate := range candidates {
		if len(result) == query.MaxCandidates {
			truncated = true
			break
		}
		size := len(renderRecord(candidate.record))
		if len(result) != 0 {
			size++
		}
		if bytesUsed+size > query.MaxBytes {
			truncated = true
			continue
		}
		result = append(result, cloneRecord(candidate.record))
		bytesUsed += size
	}
	return result, file.Generation, len(candidates), truncated, nil
}

func (s *Store) Generation() (uint64, error) {
	if s == nil {
		return 0, ErrDisabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	file, _, err := s.loadRecordFileLocked()
	return file.Generation, err
}

func (s *Store) ComposeBlockFor(query Query) (string, bool, error) {
	block, selection, err := s.SelectBlock(query)
	if err != nil {
		return "", false, err
	}
	return block, len(selection.SelectedIDs) != 0, nil
}

func (s *Store) SelectBlock(query Query) (string, Selection, error) {
	records, generation, candidateCount, truncated, err := s.search(query)
	if err != nil {
		return "", Selection{}, err
	}
	source := fmt.Sprintf("%s#generation=%d", s.recordsFile, generation)
	limit := query.MaxBytes
	if limit <= 0 {
		limit = s.options.MaxPromptBytes
	}
	var block string
	for len(records) != 0 {
		body := renderRecords(records)
		block = AsSystemBlockBounded(body, source, limit)
		if block != "" && len(block) <= limit &&
			strings.Contains(block, escapeMemoryPartition(body)) {
			break
		}
		records = records[:len(records)-1]
		truncated = true
	}
	selection := Selection{
		Generation: generation, CandidateCount: candidateCount,
		SelectedIDs: make([]string, len(records)),
		Truncated:   truncated,
	}
	for index, record := range records {
		selection.SelectedIDs[index] = record.ID
	}
	if len(records) == 0 {
		return "", selection, nil
	}
	return block, selection, nil
}

func (s *Store) normalizeCreate(request CreateRequest) (MemoryRecord, error) {
	text, err := normalizeText(request.Text)
	if err != nil {
		return MemoryRecord{}, err
	}
	if request.Scope == "" {
		request.Scope = ScopeUser
	}
	if request.Category == "" {
		request.Category = CategoryFact
	}
	scopeID, err := s.resolveScopeID(request.Scope, request.ScopeID)
	if err != nil {
		return MemoryRecord{}, err
	}
	if !request.Category.Valid() {
		return MemoryRecord{}, errors.New("memory category is invalid")
	}
	source := strings.TrimSpace(request.Source)
	if source == "" {
		source = "user"
	}
	if len(source) > 256 || strings.ContainsRune(source, 0) {
		return MemoryRecord{}, errors.New("memory source is invalid")
	}
	record := MemoryRecord{
		Scope: request.Scope, ScopeID: scopeID, Category: request.Category,
		Text: text, Source: source, ExpiresAt: cloneTime(request.ExpiresAt),
	}
	record.Digest = recordDigest(record)
	return record, nil
}

func (s *Store) resolveScopeID(scope Scope, explicit string) (string, error) {
	explicit = strings.TrimSpace(explicit)
	switch scope {
	case ScopeUser:
		if explicit != "" {
			return "", errors.New("user memory cannot carry a scope id")
		}
		return "", nil
	case ScopeWorkspace:
		if explicit == "" {
			explicit = s.options.WorkspaceID
		}
	case ScopeRepository:
		if explicit == "" {
			explicit = s.options.RepositoryID
		}
	default:
		return "", errors.New("memory scope is invalid")
	}
	if explicit == "" || len(explicit) > 4096 || strings.ContainsRune(explicit, 0) {
		return "", errors.New("memory scope identity is unavailable")
	}
	return explicit, nil
}

func (s Scope) Valid() bool {
	return s == ScopeUser || s == ScopeWorkspace || s == ScopeRepository
}

func (c Category) Valid() bool {
	return c == CategoryPreference || c == CategoryConvention || c == CategoryFact
}

func normalizeText(value string) (string, error) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "#"))
	if value == "" || utf8.RuneCountInString(value) == 0 {
		return "", ErrEmptyNote
	}
	if len(value) > MaxNoteBytes {
		return "", ErrNoteTooLarge
	}
	if strings.ContainsRune(value, 0) || !utf8.ValidString(value) {
		return "", errors.New("memory text is invalid UTF-8")
	}
	if containsCredentialMaterial(value) {
		return "", errors.New("memory note must not contain secrets")
	}
	return value, nil
}

func (s *Store) loadRecordFileLocked() (recordFile, bool, error) {
	if err := s.ensureRecordsFile(); err != nil {
		return recordFile{}, false, err
	}
	raw, err := os.ReadFile(s.recordsFile)
	if errors.Is(err, os.ErrNotExist) {
		return newRecordFile(), false, nil
	}
	if err != nil {
		return recordFile{}, false, err
	}
	if len(raw) > MaxFileBytes {
		return recordFile{}, true, ErrFileTooLarge
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var file recordFile
	if err := decoder.Decode(&file); err != nil {
		return recordFile{}, true, fmt.Errorf("decode memory records: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return recordFile{}, true, errors.New("memory records contain trailing JSON")
		}
		return recordFile{}, true, fmt.Errorf("decode trailing memory records: %w", err)
	}
	if err := file.validate(); err != nil {
		return recordFile{}, true, err
	}
	return file, true, nil
}

func (s *Store) saveRecordFileLocked(file recordFile) error {
	if err := s.ensureRecordsFile(); err != nil {
		return err
	}
	sort.Slice(file.Records, func(i, j int) bool {
		return file.Records[i].ID < file.Records[j].ID
	})
	file.Version = RecordSchemaVersion
	file.Digest = file.digest()
	raw, err := json.Marshal(file)
	if err != nil {
		return err
	}
	if len(raw) > MaxFileBytes {
		return ErrFileTooLarge
	}
	tmp, err := os.CreateTemp(s.root, ".memory-records-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, s.recordsFile); err != nil {
		return err
	}
	dir, err := os.Open(s.root)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

func newRecordFile() recordFile {
	file := recordFile{Version: RecordSchemaVersion}
	file.Digest = file.digest()
	return file
}

func (f recordFile) validate() error {
	if f.Version != RecordSchemaVersion || f.Digest == "" ||
		f.Digest != f.digest() || f.Generation == 0 && len(f.Records) != 0 {
		return errors.New("memory record file identity is invalid")
	}
	seenIDs := make(map[string]struct{}, len(f.Records))
	seenDigests := make(map[string]struct{}, len(f.Records))
	for _, record := range f.Records {
		if err := record.validate(); err != nil {
			return err
		}
		if _, duplicate := seenIDs[record.ID]; duplicate {
			return errors.New("memory record id is duplicated")
		}
		if _, duplicate := seenDigests[record.Digest]; duplicate {
			return errors.New("memory record digest is duplicated")
		}
		seenIDs[record.ID] = struct{}{}
		seenDigests[record.Digest] = struct{}{}
	}
	return nil
}

func (f recordFile) digest() string {
	f.Digest = ""
	encoded, _ := json.Marshal(f)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (r MemoryRecord) validate() error {
	if !r.Scope.Valid() || !r.Category.Valid() || r.ID == "" ||
		r.Source == "" || r.Text == "" || r.CreatedAt.IsZero() ||
		r.UpdatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) ||
		r.Digest == "" || r.Digest != recordDigest(r) {
		return errors.New("memory record is invalid")
	}
	if r.Scope == ScopeUser && r.ScopeID != "" ||
		r.Scope != ScopeUser && r.ScopeID == "" {
		return errors.New("memory record scope identity is invalid")
	}
	return nil
}

func recordDigest(record MemoryRecord) string {
	record.ID = ""
	record.CreatedAt = time.Time{}
	record.UpdatedAt = time.Time{}
	record.Digest = ""
	encoded, _ := json.Marshal(record)
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func stableRecordID(record MemoryRecord, created time.Time) string {
	sum := sha256.Sum256([]byte(
		string(record.Scope) + "\x00" + record.ScopeID + "\x00" +
			string(record.Category) + "\x00" + record.Digest + "\x00" +
			created.Format(time.RFC3339Nano),
	))
	return "mem_" + hex.EncodeToString(sum[:16])
}

func scopeMatch(record MemoryRecord, query Query) (int, bool) {
	switch record.Scope {
	case ScopeWorkspace:
		return 0, query.WorkspaceID != "" && record.ScopeID == query.WorkspaceID
	case ScopeRepository:
		return 1, query.RepositoryID != "" && record.ScopeID == query.RepositoryID
	case ScopeUser:
		return 2, true
	default:
		return 0, false
	}
}

func lexicalScore(record MemoryRecord, terms map[string]struct{}) int {
	if len(terms) == 0 {
		return 0
	}
	score := 0
	recordTerms := lexicalTerms(
		string(record.Category) + " " + record.Text + " " + record.Source,
	)
	for term := range terms {
		if _, ok := recordTerms[term]; ok {
			score++
		}
	}
	return score
}

func lexicalTerms(value string) map[string]struct{} {
	result := make(map[string]struct{})
	var token strings.Builder
	flush := func() {
		if token.Len() < 2 {
			token.Reset()
			return
		}
		result[token.String()] = struct{}{}
		token.Reset()
	}
	for _, character := range strings.ToLower(value) {
		if unicode.IsLetter(character) || unicode.IsDigit(character) ||
			character == '_' || character >= utf8.RuneSelf {
			token.WriteRune(character)
			continue
		}
		flush()
	}
	flush()
	return result
}

func renderRecords(records []MemoryRecord) string {
	if len(records) == 0 {
		return ""
	}
	var builder strings.Builder
	for index, record := range records {
		if index != 0 {
			builder.WriteByte('\n')
		}
		builder.WriteString(renderRecord(record))
	}
	return builder.String()
}

func renderRecord(record MemoryRecord) string {
	return fmt.Sprintf(
		"- id=%s scope=%s category=%s: %s",
		record.ID,
		record.Scope,
		record.Category,
		record.Text,
	)
}

func AsSystemBlockBounded(content, source string, limit int) string {
	if limit <= 0 || limit > MaxPromptBytes {
		limit = MaxPromptBytes
	}
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	header := fmt.Sprintf(
		"<user_memory source=%q non_authoritative=%q>\n",
		escapeMemoryPartition(source),
		"true",
	)
	footer := "\n</user_memory>"
	available := limit - len(header) - len(footer)
	if available <= 0 {
		return ""
	}
	payload := escapeMemoryPartition(trimmed)
	if len(payload) > available {
		cutoff := previousUTF8Boundary(payload, available)
		payload = payload[:cutoff]
	}
	return header + payload + footer
}

func escapeMemoryPartition(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	return strings.ReplaceAll(value, ">", "&gt;")
}

func cloneRecord(record MemoryRecord) MemoryRecord {
	record.ExpiresAt = cloneTime(record.ExpiresAt)
	return record
}

func cloneTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func (s *Store) RepositoryPath() string {
	if s == nil {
		return ""
	}
	return filepath.Clean(s.recordsFile)
}

func (s *Store) BindScopes(workspaceID, repositoryID string) error {
	if s == nil {
		return ErrDisabled
	}
	workspaceID = strings.TrimSpace(workspaceID)
	repositoryID = strings.TrimSpace(repositoryID)
	if workspaceID == "" || strings.ContainsRune(workspaceID, 0) ||
		len(workspaceID) > 4096 ||
		strings.ContainsRune(repositoryID, 0) || len(repositoryID) > 4096 {
		return errors.New("memory scope identities are invalid")
	}
	s.mu.Lock()
	s.options.WorkspaceID = workspaceID
	s.options.RepositoryID = repositoryID
	s.mu.Unlock()
	return nil
}

func (s *Store) recordVisible(record MemoryRecord) bool {
	switch record.Scope {
	case ScopeUser:
		return true
	case ScopeWorkspace:
		return s.options.WorkspaceID != "" &&
			record.ScopeID == s.options.WorkspaceID
	case ScopeRepository:
		return s.options.RepositoryID != "" &&
			record.ScopeID == s.options.RepositoryID
	default:
		return false
	}
}

func CanonicalScopeIdentities(workspace string) (string, string, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(workspace))
	if err != nil {
		return "", "", fmt.Errorf("resolve memory workspace: %w", err)
	}
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", "", fmt.Errorf("resolve memory workspace links: %w", err)
	}
	workspaceID := pathIdentity("workspace", resolved)
	repositoryRoot := findRepositoryIdentityRoot(resolved)
	repositoryID := ""
	if repositoryRoot != "" {
		repositoryID = pathIdentity("repository", repositoryRoot)
	}
	return workspaceID, repositoryID, nil
}

func pathIdentity(kind, path string) string {
	sum := sha256.Sum256([]byte(filepath.ToSlash(filepath.Clean(path))))
	return kind + ":" + hex.EncodeToString(sum[:])
}

func findRepositoryIdentityRoot(workspace string) string {
	current := filepath.Clean(workspace)
	for {
		gitPath := filepath.Join(current, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if info.IsDir() {
				return gitPath
			}
			raw, readErr := os.ReadFile(gitPath)
			if readErr == nil {
				value := strings.TrimSpace(string(raw))
				if relative, ok := strings.CutPrefix(value, "gitdir:"); ok {
					target := strings.TrimSpace(relative)
					if !filepath.IsAbs(target) {
						target = filepath.Join(current, target)
					}
					if resolved, resolveErr := filepath.EvalSymlinks(target); resolveErr == nil {
						target = resolved
					}
					target = filepath.Clean(target)
					marker := string(filepath.Separator) + "worktrees" +
						string(filepath.Separator)
					if index := strings.LastIndex(target, marker); index >= 0 {
						return target[:index]
					}
					return target
				}
			}
		}
		parent := filepath.Dir(current)
		if parent == current {
			return ""
		}
		current = parent
	}
}
