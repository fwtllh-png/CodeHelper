package corpus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/capture"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/replay"
)

const QualifiedSchemaVersion = 2

type PromotionReview struct {
	SchemaVersion int    `json:"schema_version"`
	ID            string `json:"id"`
	BatchID       string `json:"batch_id"`
	Reviewer      string `json:"reviewer"`
	Decision      string `json:"decision"`
	SourceDigest  string `json:"source_digest"`
	ReviewedOn    string `json:"reviewed_on"`
}

type BatchManifest struct {
	SchemaVersion int      `json:"schema_version"`
	ID            string   `json:"id"`
	SourceDigest  string   `json:"source_digest"`
	ReviewDigest  string   `json:"review_digest"`
	Entries       []string `json:"entries"`
}

type QualifiedManifest struct {
	SchemaVersion    int            `json:"schema_version"`
	ID               string         `json:"id"`
	BatchID          string         `json:"batch_id"`
	SourceFormat     capture.Format `json:"source_format"`
	SourceClass      SourceClass    `json:"source_class"`
	SourceDigest     string         `json:"source_digest"`
	ReviewDigest     string         `json:"review_digest"`
	Selector         Selector       `json:"selector"`
	SourceEventCount int            `json:"source_event_count"`
	EventCount       int            `json:"event_count"`
	TraceDigest      string         `json:"trace_digest"`
	FirstDigest      string         `json:"first_digest"`
	LastDigest       string         `json:"last_digest"`
	FailureSignature string         `json:"failure_signature"`
	ReplayLevel      replay.Level   `json:"replay_level"`
	ContentMode      string         `json:"content_mode"`
	SecretScan       string         `json:"secret_scan"`
}

type BatchPromotion struct {
	BatchID      string
	OutputRoot   string
	EntryPrefix  string
	SourceFormat capture.Format
	SourceClass  SourceClass
	SourceDigest string
	Slices       []capture.Slice
	Sanitizer    capture.SanitizerOptions
	Review       PromotionReview
}

type VerifiedBatch struct {
	Directory string
	Manifest  BatchManifest
	Review    PromotionReview
	Entries   []VerifiedQualified
}

type VerifiedQualified struct {
	Directory string
	Manifest  QualifiedManifest
	Events    []evidence.Envelope
	Outcome   replay.Outcome
}

func PromoteBatch(options BatchPromotion) (BatchManifest, error) {
	if !corpusIDPattern.MatchString(options.BatchID) ||
		!corpusIDPattern.MatchString(options.EntryPrefix) ||
		strings.TrimSpace(options.OutputRoot) == "" ||
		!strictDigestValid(options.SourceDigest) ||
		len(options.Slices) == 0 {
		return BatchManifest{}, errors.New("qualified promotion identity is invalid")
	}
	if err := options.Review.Validate(); err != nil {
		return BatchManifest{}, err
	}
	if options.Review.BatchID != options.BatchID ||
		options.Review.SourceDigest != options.SourceDigest {
		return BatchManifest{}, errors.New("promotion review is not bound to the batch")
	}
	reviewJSON, err := marshalPrivate(options.Review)
	if err != nil {
		return BatchManifest{}, err
	}
	if err := capture.ScanDocument(reviewJSON, options.Sanitizer); err != nil {
		return BatchManifest{}, fmt.Errorf("scan promotion review: %w", err)
	}
	reviewDigest := evidence.DigestBytes(reviewJSON)

	if err := os.MkdirAll(options.OutputRoot, 0o700); err != nil {
		return BatchManifest{}, err
	}
	destination := filepath.Join(options.OutputRoot, options.BatchID)
	if _, err := os.Stat(destination); err == nil {
		return BatchManifest{}, fmt.Errorf("promotion batch %q already exists", options.BatchID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return BatchManifest{}, err
	}
	temporary, err := os.MkdirTemp(
		options.OutputRoot,
		"."+options.BatchID+".tmp-",
	)
	if err != nil {
		return BatchManifest{}, err
	}
	defer os.RemoveAll(temporary)
	if err := os.Chmod(temporary, 0o700); err != nil {
		return BatchManifest{}, err
	}
	if err := os.WriteFile(
		filepath.Join(temporary, "promotion-review.json"),
		reviewJSON,
		0o600,
	); err != nil {
		return BatchManifest{}, err
	}

	entriesRoot := filepath.Join(temporary, "entries")
	if err := os.Mkdir(entriesRoot, 0o700); err != nil {
		return BatchManifest{}, err
	}
	var entryIDs []string
	for index, slice := range options.Slices {
		entryID := fmt.Sprintf("%s-%02d", options.EntryPrefix, index+1)
		if err := writeQualifiedEntry(
			entriesRoot,
			entryID,
			options,
			slice,
			reviewDigest,
		); err != nil {
			return BatchManifest{}, err
		}
		entryIDs = append(entryIDs, entryID)
	}
	slices.Sort(entryIDs)
	batch := BatchManifest{
		SchemaVersion: QualifiedSchemaVersion,
		ID:            options.BatchID,
		SourceDigest:  options.SourceDigest,
		ReviewDigest:  reviewDigest,
		Entries:       entryIDs,
	}
	batchJSON, err := marshalPrivate(batch)
	if err != nil {
		return BatchManifest{}, err
	}
	if err := capture.ScanDocument(batchJSON, options.Sanitizer); err != nil {
		return BatchManifest{}, fmt.Errorf("scan batch manifest: %w", err)
	}
	if err := os.WriteFile(
		filepath.Join(temporary, "batch.json"),
		batchJSON,
		0o600,
	); err != nil {
		return BatchManifest{}, err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return BatchManifest{}, err
	}
	return batch, nil
}

func VerifyBatch(
	directory string,
	sanitizer capture.SanitizerOptions,
) (VerifiedBatch, error) {
	batchRaw, err := os.ReadFile(filepath.Join(directory, "batch.json"))
	if err != nil {
		return VerifiedBatch{}, err
	}
	reviewRaw, err := os.ReadFile(filepath.Join(directory, "promotion-review.json"))
	if err != nil {
		return VerifiedBatch{}, err
	}
	if err := capture.ScanDocument(batchRaw, sanitizer); err != nil {
		return VerifiedBatch{}, err
	}
	if err := capture.ScanDocument(reviewRaw, sanitizer); err != nil {
		return VerifiedBatch{}, err
	}
	var batch BatchManifest
	if err := decodeStrict(batchRaw, &batch); err != nil {
		return VerifiedBatch{}, err
	}
	var review PromotionReview
	if err := decodeStrict(reviewRaw, &review); err != nil {
		return VerifiedBatch{}, err
	}
	if err := batch.Validate(); err != nil {
		return VerifiedBatch{}, err
	}
	if err := review.Validate(); err != nil {
		return VerifiedBatch{}, err
	}
	if evidence.DigestBytes(reviewRaw) != batch.ReviewDigest ||
		review.BatchID != batch.ID ||
		review.SourceDigest != batch.SourceDigest {
		return VerifiedBatch{}, errors.New("promotion review binding does not match batch")
	}
	allowed := map[string]bool{
		"batch.json":            true,
		"promotion-review.json": true,
		"entries":               true,
	}
	rootEntries, err := os.ReadDir(directory)
	if err != nil {
		return VerifiedBatch{}, err
	}
	for _, entry := range rootEntries {
		if !allowed[entry.Name()] {
			return VerifiedBatch{}, fmt.Errorf("unscanned batch file %q", entry.Name())
		}
	}
	result := VerifiedBatch{
		Directory: directory,
		Manifest:  batch,
		Review:    review,
	}
	for _, entryID := range batch.Entries {
		entry, err := verifyQualifiedEntry(
			filepath.Join(directory, "entries", entryID),
			batch,
			sanitizer,
		)
		if err != nil {
			return VerifiedBatch{}, fmt.Errorf("verify qualified corpus %s: %w", entryID, err)
		}
		result.Entries = append(result.Entries, entry)
	}
	return result, nil
}

func writeQualifiedEntry(
	root string,
	id string,
	options BatchPromotion,
	slice capture.Slice,
	reviewDigest string,
) error {
	events, err := capture.Canonicalize(slice.Events, options.Sanitizer)
	if err != nil {
		return fmt.Errorf("canonicalize %s: %w", id, err)
	}
	outcome, err := replay.ExecuteAt(
		context.Background(),
		replay.LevelStructural,
		events,
		nil,
	)
	if err != nil {
		return err
	}
	if outcome.FailureSignature != slice.Signature {
		return fmt.Errorf("qualified corpus %s changed failure signature", id)
	}
	content, err := evidence.EncodeJSONL(events)
	if err != nil {
		return err
	}
	if err := capture.Scan(content, options.Sanitizer); err != nil {
		return err
	}
	manifest := QualifiedManifest{
		SchemaVersion: QualifiedSchemaVersion,
		ID:            id, BatchID: options.BatchID,
		SourceFormat: options.SourceFormat, SourceClass: options.SourceClass,
		SourceDigest: options.SourceDigest, ReviewDigest: reviewDigest,
		Selector:         Selector{Kind: slice.Kind, Index: slice.Index},
		SourceEventCount: slice.SourceCount, EventCount: len(events),
		TraceDigest: evidence.DigestBytes(content),
		FirstDigest: events[0].Digest, LastDigest: events[len(events)-1].Digest,
		FailureSignature: outcome.FailureSignature,
		ReplayLevel:      replay.LevelStructural,
		ContentMode:      "metadata_only", SecretScan: "passed",
	}
	if err := manifest.Validate(); err != nil {
		return err
	}
	manifestJSON, err := marshalPrivate(manifest)
	if err != nil {
		return err
	}
	if err := capture.ScanDocument(manifestJSON, options.Sanitizer); err != nil {
		return err
	}
	directory := filepath.Join(root, id)
	if err := os.Mkdir(directory, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, "events.jsonl"), content, 0o600); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(directory, "manifest.json"), manifestJSON, 0o600)
}

func verifyQualifiedEntry(
	directory string,
	batch BatchManifest,
	sanitizer capture.SanitizerOptions,
) (VerifiedQualified, error) {
	entries, err := os.ReadDir(directory)
	if err != nil {
		return VerifiedQualified{}, err
	}
	if len(entries) != 2 {
		return VerifiedQualified{}, errors.New("qualified corpus entry has extra or missing files")
	}
	manifestRaw, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return VerifiedQualified{}, err
	}
	content, err := os.ReadFile(filepath.Join(directory, "events.jsonl"))
	if err != nil {
		return VerifiedQualified{}, err
	}
	if err := capture.ScanDocument(manifestRaw, sanitizer); err != nil {
		return VerifiedQualified{}, err
	}
	if err := capture.Scan(content, sanitizer); err != nil {
		return VerifiedQualified{}, err
	}
	var manifest QualifiedManifest
	if err := decodeStrict(manifestRaw, &manifest); err != nil {
		return VerifiedQualified{}, err
	}
	if err := manifest.Validate(); err != nil {
		return VerifiedQualified{}, err
	}
	if manifest.BatchID != batch.ID ||
		manifest.SourceDigest != batch.SourceDigest ||
		manifest.ReviewDigest != batch.ReviewDigest ||
		evidence.DigestBytes(content) != manifest.TraceDigest {
		return VerifiedQualified{}, errors.New("qualified corpus identity does not match batch")
	}
	events, err := evidence.DecodeJSONL(bytes.NewReader(content))
	if err != nil {
		return VerifiedQualified{}, err
	}
	if len(events) != manifest.EventCount ||
		events[0].Digest != manifest.FirstDigest ||
		events[len(events)-1].Digest != manifest.LastDigest {
		return VerifiedQualified{}, errors.New("qualified corpus event boundary does not match")
	}
	outcome, err := replay.ExecuteAt(
		context.Background(),
		manifest.ReplayLevel,
		events,
		nil,
	)
	if err != nil {
		return VerifiedQualified{}, err
	}
	if outcome.FailureSignature != manifest.FailureSignature {
		return VerifiedQualified{}, errors.New("qualified corpus failure signature changed")
	}
	return VerifiedQualified{
		Directory: directory,
		Manifest:  manifest,
		Events:    events,
		Outcome:   outcome,
	}, nil
}

func (r PromotionReview) Validate() error {
	if r.SchemaVersion != QualifiedSchemaVersion ||
		!corpusIDPattern.MatchString(r.ID) ||
		!corpusIDPattern.MatchString(r.BatchID) ||
		!corpusIDPattern.MatchString(r.Reviewer) ||
		r.Decision != "approved" ||
		!strictDigestValid(r.SourceDigest) ||
		r.ReviewedOn == "" {
		return errors.New("promotion review is incomplete or not approved")
	}
	return nil
}

func (m BatchManifest) Validate() error {
	if m.SchemaVersion != QualifiedSchemaVersion ||
		!corpusIDPattern.MatchString(m.ID) ||
		!strictDigestValid(m.SourceDigest) ||
		!strictDigestValid(m.ReviewDigest) ||
		len(m.Entries) == 0 {
		return errors.New("qualified batch manifest is invalid")
	}
	if !slices.IsSorted(m.Entries) {
		return errors.New("qualified batch entries are not sorted")
	}
	seen := make(map[string]struct{}, len(m.Entries))
	for _, id := range m.Entries {
		if !corpusIDPattern.MatchString(id) {
			return errors.New("qualified batch entry id is invalid")
		}
		if _, exists := seen[id]; exists {
			return errors.New("qualified batch entry id is duplicated")
		}
		seen[id] = struct{}{}
	}
	return nil
}

func (m QualifiedManifest) Validate() error {
	if m.SchemaVersion != QualifiedSchemaVersion ||
		!corpusIDPattern.MatchString(m.ID) ||
		!corpusIDPattern.MatchString(m.BatchID) ||
		!strictDigestValid(m.SourceDigest) ||
		!strictDigestValid(m.ReviewDigest) ||
		!strictDigestValid(m.TraceDigest) ||
		!strictDigestValid(m.FirstDigest) ||
		!strictDigestValid(m.LastDigest) {
		return errors.New("qualified corpus identity or digest is invalid")
	}
	switch m.SourceFormat {
	case capture.FormatVSCodeRuntime, capture.FormatProvider, capture.FormatObservation:
	default:
		return errors.New("qualified corpus source format is invalid")
	}
	switch m.SourceClass {
	case SourceRealPrivate, SourceSynthetic:
	default:
		return errors.New("qualified corpus source class is invalid")
	}
	switch m.Selector.Kind {
	case "full", "operation", "orphan_request":
	default:
		return errors.New("qualified corpus selector is invalid")
	}
	if m.Selector.Index < 1 || m.SourceEventCount < 1 || m.EventCount < 1 ||
		m.FailureSignature == "" ||
		m.ReplayLevel != replay.LevelStructural ||
		m.ContentMode != "metadata_only" || m.SecretScan != "passed" {
		return errors.New("qualified corpus metadata is incomplete")
	}
	return nil
}

func strictDigestValid(value string) bool {
	if len(value) != len("sha256:")+64 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		digit := character >= '0' && character <= '9'
		hex := character >= 'a' && character <= 'f'
		if !digit && !hex {
			return false
		}
	}
	return true
}

func marshalPrivate(value any) ([]byte, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}
