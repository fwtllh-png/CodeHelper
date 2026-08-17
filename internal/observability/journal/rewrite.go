package journal

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
)

type RewriteReport struct {
	Records             uint64
	PayloadsRemoved     uint64
	PayloadBytesRemoved uint64
	ReleasedDigests     []string
}

// RewritePayloads rebuilds the canonical digest chain in a sibling directory
// and swaps it into place only after every replacement record is durable.
// Callers must ensure no Writer is open for root.
func RewritePayloads(
	ctx context.Context,
	root string,
	remove func(observation.Envelope) bool,
	options Options,
) (RewriteReport, error) {
	if remove == nil {
		return RewriteReport{}, errors.New("payload retention predicate is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return RewriteReport{}, err
	}
	records, err := ReadAll(absolute)
	if err != nil {
		return RewriteReport{}, err
	}
	report := RewriteReport{Records: uint64(len(records))}
	removeSequences := make(map[uint64]bool)
	for _, record := range records {
		if record.Envelope.Payload != nil && remove(record.Envelope) {
			removeSequences[record.Sequence] = true
		}
	}
	if len(removeSequences) == 0 {
		return report, nil
	}
	parent := filepath.Dir(absolute)
	staging, err := os.MkdirTemp(parent, ".observation-journal-rewrite-")
	if err != nil {
		return RewriteReport{}, err
	}
	keepStaging := false
	defer func() {
		if !keepStaging {
			_ = os.RemoveAll(staging)
		}
	}()
	writer, err := Open(staging, options)
	if err != nil {
		return RewriteReport{}, err
	}
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			_ = writer.Close(context.Background())
			return RewriteReport{}, err
		}
		envelope := record.Envelope
		if envelope.Payload != nil && removeSequences[record.Sequence] {
			report.PayloadsRemoved++
			report.PayloadBytesRemoved += envelope.Payload.StoredBytes
			report.ReleasedDigests = append(
				report.ReleasedDigests,
				envelope.Payload.Digest[7:],
			)
			envelope.Payload = nil
		}
		if _, err := writer.Append(ctx, envelope); err != nil {
			_ = writer.Close(context.Background())
			return RewriteReport{}, err
		}
	}
	if err := writer.Close(ctx); err != nil {
		return RewriteReport{}, err
	}
	backup := staging + "-backup"
	if err := os.Rename(absolute, backup); err != nil {
		return RewriteReport{}, fmt.Errorf("stage journal replacement: %w", err)
	}
	if err := os.Rename(staging, absolute); err != nil {
		rollbackErr := os.Rename(backup, absolute)
		return RewriteReport{}, errors.Join(
			fmt.Errorf("install journal replacement: %w", err),
			rollbackErr,
		)
	}
	keepStaging = true
	if err := syncDirectory(parent); err != nil {
		return report, err
	}
	if err := os.RemoveAll(backup); err != nil {
		return report, fmt.Errorf("remove replaced journal: %w", err)
	}
	return report, syncDirectory(parent)
}

func syncDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}
