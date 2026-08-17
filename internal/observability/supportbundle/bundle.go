// Package supportbundle creates a minimal, re-redacted observation archive.
package supportbundle

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/journal"
	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/observability/privacy"
)

type PayloadReader interface {
	Get(context.Context, string) ([]byte, error)
}

type Options struct {
	JournalRoot    string
	OutputPath     string
	Payloads       PayloadReader
	Redactor       *privacy.Policy
	IncludePayload bool
	Now            func() time.Time
}

type Report struct {
	Observations uint64
	Payloads     uint64
	OutputPath   string
}

type bundleObservation struct {
	Envelope     observation.Envelope `json:"envelope"`
	PayloadEntry string               `json:"payload_entry,omitempty"`
	PayloadSHA   string               `json:"payload_sha256,omitempty"`
}

func Create(ctx context.Context, options Options) (Report, error) {
	if strings.TrimSpace(options.JournalRoot) == "" ||
		strings.TrimSpace(options.OutputPath) == "" {
		return Report{}, errors.New("support bundle paths are required")
	}
	if options.Redactor == nil {
		return Report{}, errors.New("support bundle redactor is required")
	}
	if options.IncludePayload && options.Payloads == nil {
		return Report{}, errors.New("support bundle payload reader is required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	records, err := journal.ReadAll(options.JournalRoot)
	if err != nil {
		return Report{}, err
	}
	output, err := filepath.Abs(options.OutputPath)
	if err != nil {
		return Report{}, err
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o700); err != nil {
		return Report{}, err
	}
	file, err := os.OpenFile(
		output,
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if err != nil {
		return Report{}, err
	}
	archive := zip.NewWriter(file)
	ok := false
	defer func() {
		if !ok {
			_ = archive.Close()
			_ = file.Close()
			_ = os.Remove(output)
		}
	}()
	manifest, err := archive.Create("manifest.json")
	if err != nil {
		return Report{}, err
	}
	if err := json.NewEncoder(manifest).Encode(map[string]any{
		"schema_version":  1,
		"created_at":      options.Now().UTC(),
		"redacted":        true,
		"payloads":        options.IncludePayload,
		"observation_log": "observations.jsonl",
	}); err != nil {
		return Report{}, err
	}
	var observationLog bytes.Buffer
	report := Report{
		Observations: uint64(len(records)),
		OutputPath:   output,
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return Report{}, err
		}
		value, payload, err := sanitizeRecord(ctx, options, record)
		if err != nil {
			return Report{}, err
		}
		if payload != nil {
			entry, err := archive.Create(value.PayloadEntry)
			if err != nil {
				return Report{}, err
			}
			if _, err := entry.Write(payload); err != nil {
				return Report{}, err
			}
			report.Payloads++
		}
		if err := json.NewEncoder(&observationLog).Encode(value); err != nil {
			return Report{}, err
		}
	}
	logEntry, err := archive.Create("observations.jsonl")
	if err != nil {
		return Report{}, err
	}
	if _, err := logEntry.Write(observationLog.Bytes()); err != nil {
		return Report{}, err
	}
	if err := archive.Close(); err != nil {
		return Report{}, err
	}
	if err := file.Sync(); err != nil {
		return Report{}, err
	}
	if err := file.Close(); err != nil {
		return Report{}, err
	}
	if err := os.Chmod(output, 0o600); err != nil {
		return Report{}, err
	}
	ok = true
	return report, nil
}

func sanitizeRecord(
	ctx context.Context,
	options Options,
	record journal.Record,
) (bundleObservation, []byte, error) {
	envelope := record.Envelope
	summary, err := options.Redactor.RedactBytes(
		envelope.Summary,
		"application/json",
	)
	if err != nil {
		return bundleObservation{}, nil, err
	}
	envelope.Summary = summary
	sourcePayload := envelope.Payload
	envelope.Payload = nil
	result := bundleObservation{Envelope: envelope}
	if !options.IncludePayload || sourcePayload == nil {
		return result, nil, nil
	}
	content, err := options.Payloads.Get(
		ctx,
		strings.TrimPrefix(sourcePayload.Digest, "sha256:"),
	)
	if err != nil {
		return bundleObservation{}, nil, fmt.Errorf(
			"read support payload for %s: %w",
			envelope.ID,
			err,
		)
	}
	content, err = options.Redactor.RedactBytes(
		content,
		sourcePayload.MediaType,
	)
	if err != nil {
		return bundleObservation{}, nil, err
	}
	digest := sha256.Sum256(content)
	result.PayloadEntry = "payloads/" + string(envelope.ID) + ".data"
	result.PayloadSHA = hex.EncodeToString(digest[:])
	return result, content, nil
}
