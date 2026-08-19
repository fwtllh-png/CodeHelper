package corpus

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/capture"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/replay"
)

const SchemaVersion = 1

type SourceClass string

const (
	SourceRealPrivate SourceClass = "real_private_capture"
	SourceSynthetic   SourceClass = "synthetic_fixture"
)

type Selector struct {
	Kind  string `json:"kind"`
	Index int    `json:"index"`
}

type Manifest struct {
	SchemaVersion    int            `json:"schema_version"`
	ID               string         `json:"id"`
	SourceFormat     capture.Format `json:"source_format"`
	SourceClass      SourceClass    `json:"source_class"`
	SourceDigest     string         `json:"source_digest"`
	Selector         Selector       `json:"selector"`
	SourceEventCount int            `json:"source_event_count"`
	EventCount       int            `json:"event_count"`
	TraceDigest      string         `json:"trace_digest"`
	FirstDigest      string         `json:"first_digest"`
	LastDigest       string         `json:"last_digest"`
	FailureSignature string         `json:"failure_signature"`
	ContentMode      string         `json:"content_mode"`
	SecretScan       string         `json:"secret_scan"`
}

type Promotion struct {
	ID           string
	OutputRoot   string
	SourceFormat capture.Format
	SourceClass  SourceClass
	SourceDigest string
	Slice        capture.Slice
	Sanitizer    capture.SanitizerOptions
}

type Verified struct {
	Directory string
	Manifest  Manifest
	Events    []evidence.Envelope
	Outcome   replay.Outcome
}

var corpusIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

func Promote(options Promotion) (Manifest, error) {
	if !corpusIDPattern.MatchString(options.ID) {
		return Manifest{}, fmt.Errorf("corpus id %q is invalid", options.ID)
	}
	if strings.TrimSpace(options.OutputRoot) == "" ||
		!strictDigestValid(options.SourceDigest) {
		return Manifest{}, errors.New("corpus output root and source digest are required")
	}
	switch options.SourceClass {
	case SourceRealPrivate, SourceSynthetic:
	default:
		return Manifest{}, fmt.Errorf("corpus source class %q is invalid", options.SourceClass)
	}
	if options.Slice.Kind == "" || options.Slice.Index < 1 ||
		len(options.Slice.Events) == 0 {
		return Manifest{}, errors.New("corpus promotion requires a causal slice")
	}
	events, err := capture.Canonicalize(options.Slice.Events, options.Sanitizer)
	if err != nil {
		return Manifest{}, fmt.Errorf("canonicalize corpus: %w", err)
	}
	outcome, err := replay.Execute(events)
	if err != nil {
		return Manifest{}, fmt.Errorf("replay promoted corpus: %w", err)
	}
	if outcome.FailureSignature != options.Slice.Signature {
		return Manifest{}, fmt.Errorf(
			"failure signature changed from %q to %q",
			options.Slice.Signature,
			outcome.FailureSignature,
		)
	}
	content, err := evidence.EncodeJSONL(events)
	if err != nil {
		return Manifest{}, err
	}
	if err := capture.Scan(content, options.Sanitizer); err != nil {
		return Manifest{}, fmt.Errorf("secret scan promoted corpus: %w", err)
	}
	manifest := Manifest{
		SchemaVersion:    SchemaVersion,
		ID:               options.ID,
		SourceFormat:     options.SourceFormat,
		SourceClass:      options.SourceClass,
		SourceDigest:     options.SourceDigest,
		Selector:         Selector{Kind: options.Slice.Kind, Index: options.Slice.Index},
		SourceEventCount: options.Slice.SourceCount,
		EventCount:       len(events),
		TraceDigest:      evidence.DigestBytes(content),
		FirstDigest:      events[0].Digest,
		LastDigest:       events[len(events)-1].Digest,
		FailureSignature: outcome.FailureSignature,
		ContentMode:      "metadata_only",
		SecretScan:       "passed",
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	destination := filepath.Join(options.OutputRoot, options.ID)
	if _, err := os.Stat(destination); err == nil {
		return Manifest{}, fmt.Errorf("corpus %q already exists", options.ID)
	} else if !errors.Is(err, os.ErrNotExist) {
		return Manifest{}, err
	}
	if err := os.MkdirAll(options.OutputRoot, 0o755); err != nil {
		return Manifest{}, err
	}
	temporary, err := os.MkdirTemp(options.OutputRoot, "."+options.ID+".tmp-")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(temporary)
	if err := os.WriteFile(filepath.Join(temporary, "events.jsonl"), content, 0o600); err != nil {
		return Manifest{}, err
	}
	manifestJSON, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return Manifest{}, err
	}
	manifestJSON = append(manifestJSON, '\n')
	if err := os.WriteFile(filepath.Join(temporary, "manifest.json"), manifestJSON, 0o600); err != nil {
		return Manifest{}, err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func Verify(directory string, sanitizer capture.SanitizerOptions) (Verified, error) {
	manifestRaw, err := os.ReadFile(filepath.Join(directory, "manifest.json"))
	if err != nil {
		return Verified{}, err
	}
	var manifest Manifest
	if err := decodeStrict(manifestRaw, &manifest); err != nil {
		return Verified{}, fmt.Errorf("decode corpus manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Verified{}, err
	}
	if err := capture.ScanDocument(manifestRaw, sanitizer); err != nil {
		return Verified{}, fmt.Errorf("corpus manifest secret scan: %w", err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return Verified{}, err
	}
	if len(entries) != 2 {
		return Verified{}, errors.New("corpus entry has extra or missing files")
	}
	content, err := os.ReadFile(filepath.Join(directory, "events.jsonl"))
	if err != nil {
		return Verified{}, err
	}
	if evidence.DigestBytes(content) != manifest.TraceDigest {
		return Verified{}, errors.New("corpus trace digest does not match")
	}
	if err := capture.Scan(content, sanitizer); err != nil {
		return Verified{}, fmt.Errorf("corpus secret scan: %w", err)
	}
	events, err := evidence.DecodeJSONL(bytes.NewReader(content))
	if err != nil {
		return Verified{}, err
	}
	if len(events) != manifest.EventCount ||
		events[0].Digest != manifest.FirstDigest ||
		events[len(events)-1].Digest != manifest.LastDigest {
		return Verified{}, errors.New("corpus event count or boundary digest does not match")
	}
	outcome, err := replay.Execute(events)
	if err != nil {
		return Verified{}, err
	}
	if outcome.FailureSignature != manifest.FailureSignature {
		return Verified{}, fmt.Errorf(
			"corpus failure signature = %q, want %q",
			outcome.FailureSignature,
			manifest.FailureSignature,
		)
	}
	return Verified{
		Directory: directory, Manifest: manifest, Events: events, Outcome: outcome,
	}, nil
}

func VerifyAll(root string, sanitizer capture.SanitizerOptions) ([]Verified, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var directories []string
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".") {
			continue
		}
		if !entry.IsDir() {
			return nil, fmt.Errorf("replay corpus root contains unscanned file %q", entry.Name())
		}
		if entry.IsDir() {
			directories = append(directories, filepath.Join(root, entry.Name()))
		}
	}
	slices.Sort(directories)
	if len(directories) == 0 {
		return nil, errors.New("replay corpus has an empty denominator")
	}
	result := make([]Verified, 0, len(directories))
	seen := make(map[string]struct{}, len(directories))
	for _, directory := range directories {
		verified, err := Verify(directory, sanitizer)
		if err != nil {
			return nil, fmt.Errorf("verify corpus %s: %w", filepath.Base(directory), err)
		}
		if _, exists := seen[verified.Manifest.ID]; exists {
			return nil, fmt.Errorf("duplicate corpus id %q", verified.Manifest.ID)
		}
		seen[verified.Manifest.ID] = struct{}{}
		result = append(result, verified)
	}
	return result, nil
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != SchemaVersion || !corpusIDPattern.MatchString(m.ID) ||
		!strictDigestValid(m.SourceDigest) || !strictDigestValid(m.TraceDigest) ||
		!strictDigestValid(m.FirstDigest) || !strictDigestValid(m.LastDigest) {
		return errors.New("corpus manifest identity or digest is invalid")
	}
	switch m.SourceFormat {
	case capture.FormatVSCodeRuntime, capture.FormatProvider,
		capture.FormatObservation:
	default:
		return fmt.Errorf("corpus source format %q is invalid", m.SourceFormat)
	}
	switch m.SourceClass {
	case SourceRealPrivate, SourceSynthetic:
	default:
		return fmt.Errorf("corpus source class %q is invalid", m.SourceClass)
	}
	switch m.Selector.Kind {
	case "full", "operation", "orphan_request":
	default:
		return errors.New("corpus manifest selector is invalid")
	}
	switch m.FailureSignature {
	case "turn_failed", "turn_cancelled", "turn_completed",
		"acp_request_failed", "acp_request_incomplete",
		"partial_trace", "transport_interrupted":
	default:
		return errors.New("corpus manifest failure signature is invalid")
	}
	if m.Selector.Index < 1 ||
		m.SourceEventCount < 1 || m.EventCount < 1 ||
		m.FailureSignature == "" ||
		m.ContentMode != "metadata_only" || m.SecretScan != "passed" {
		return errors.New("corpus manifest metadata is incomplete")
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("manifest contains multiple JSON values")
	}
	return nil
}
