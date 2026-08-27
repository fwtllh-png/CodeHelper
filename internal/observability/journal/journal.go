// Package journal owns the append-only canonical observation log.
package journal

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
)

var (
	ErrClosed     = errors.New("observation journal is closed")
	ErrCorrupt    = errors.New("observation journal is corrupt")
	ErrOutOfOrder = errors.New("observation sequence is out of order")
)

const (
	manifestName          = "manifest-v1.json"
	defaultMaxSegmentSize = 16 << 20
	maxRecordBytes        = 1 << 20
)

type Options struct {
	MaxSegmentBytes int64
}

type Record struct {
	Sequence       uint64               `json:"sequence"`
	Envelope       observation.Envelope `json:"envelope"`
	SHA256         string               `json:"sha256"`
	PreviousSHA256 string               `json:"previous_sha256,omitempty"`
}

type Writer struct {
	mu           sync.Mutex
	root         string
	maxSegment   int64
	file         *os.File
	openPath     string
	segmentFrom  uint64
	size         int64
	last         uint64
	digest       [sha256.Size]byte
	hasDigest    bool
	envelopeJSON []byte
	lineBuf      bytes.Buffer
	poisoned     error
	closed       bool
}

func Open(root string, options Options) (_ *Writer, resultErr error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("observation journal root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create observation journal root: %w", err)
	}
	if err := os.Chmod(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("secure observation journal root: %w", err)
	}
	if err := ensureManifest(absolute); err != nil {
		return nil, err
	}
	maxSegment := options.MaxSegmentBytes
	if maxSegment <= 0 {
		maxSegment = defaultMaxSegmentSize
	}
	writer := &Writer{root: absolute, maxSegment: maxSegment}
	openPath, last, digest, size, segmentFrom, err := recoverSegments(absolute)
	if err != nil {
		return nil, err
	}
	writer.last = last
	if digest != "" {
		if _, err := hex.Decode(writer.digest[:], []byte(digest)); err != nil {
			return nil, corruptf("decode recovered journal digest: %v", err)
		}
		writer.hasDigest = true
	}
	writer.size = size
	writer.segmentFrom = segmentFrom
	if openPath == "" {
		if err := writer.openSegment(last + 1); err != nil {
			return nil, err
		}
	} else {
		writer.openPath = openPath
		writer.file, err = os.OpenFile(openPath, os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, err
		}
	}
	return writer, nil
}

func (w *Writer) Append(
	ctx context.Context,
	envelope observation.Envelope,
) (observation.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return observation.Envelope{}, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.file == nil {
		return observation.Envelope{}, ErrClosed
	}
	if w.poisoned != nil {
		return observation.Envelope{}, errors.Join(ErrCorrupt, w.poisoned)
	}
	envelope.Sequence = w.last + 1
	w.envelopeJSON = w.envelopeJSON[:0]
	var err error
	w.envelopeJSON, err = observation.AppendJSON(w.envelopeJSON, envelope)
	if err != nil {
		return observation.Envelope{}, err
	}
	envelopeJSON := w.envelopeJSON
	digest := sha256.Sum256(envelopeJSON)
	w.lineBuf.Reset()
	encodeRecord(
		&w.lineBuf,
		envelope.Sequence,
		envelopeJSON,
		digest,
		w.digest,
		w.hasDigest,
	)
	line := w.lineBuf.Bytes()
	if int64(len(line)) > w.maxSegment {
		return observation.Envelope{}, fmt.Errorf(
			"observation record is %d bytes; segment limit is %d",
			len(line),
			w.maxSegment,
		)
	}
	if w.size != 0 && w.size+int64(len(line)) > w.maxSegment {
		if err := w.rotate(); err != nil {
			return observation.Envelope{}, err
		}
	}
	written, err := w.file.Write(line)
	if err != nil || written != len(line) {
		if err == nil {
			err = io.ErrShortWrite
		}
		w.poisoned = err
		return observation.Envelope{}, err
	}
	w.size += int64(written)
	w.last = envelope.Sequence
	w.digest = digest
	w.hasDigest = true
	return envelope, nil
}

func (w *Writer) LastSequence() uint64 {
	if w == nil {
		return 0
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.last
}

func (w *Writer) Sync(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.file == nil {
		return ErrClosed
	}
	if w.poisoned != nil {
		return errors.Join(ErrCorrupt, w.poisoned)
	}
	return w.file.Sync()
}

func (w *Writer) Close(ctx context.Context) error {
	if w == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return nil
	}
	w.closed = true
	if w.file == nil {
		return nil
	}
	syncErr := w.file.Sync()
	closeErr := w.file.Close()
	w.file = nil
	var renameErr error
	if w.last >= w.segmentFrom && w.size != 0 {
		closedPath := filepath.Join(
			w.root,
			closedSegmentName(w.segmentFrom, w.last),
		)
		renameErr = os.Rename(w.openPath, closedPath)
	}
	return errors.Join(syncErr, closeErr, renameErr, w.poisoned)
}

func ReadAll(root string) ([]Record, error) {
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	return readAll(absolute)
}

// Snapshot returns a fully committed, digest-verified view while preventing
// concurrent Append calls from exposing a partial final record.
func (w *Writer) Snapshot(ctx context.Context) ([]observation.Envelope, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.file == nil {
		return nil, ErrClosed
	}
	if w.poisoned != nil {
		return nil, errors.Join(ErrCorrupt, w.poisoned)
	}
	if err := w.file.Sync(); err != nil {
		return nil, err
	}
	records, err := readAll(w.root)
	if err != nil {
		return nil, err
	}
	envelopes := make([]observation.Envelope, 0, len(records))
	for _, record := range records {
		envelopes = append(envelopes, record.Envelope)
	}
	return envelopes, nil
}

func readAll(root string) ([]Record, error) {
	files, err := segmentFiles(root)
	if err != nil {
		return nil, err
	}
	var records []Record
	var previous uint64
	var digest string
	for _, path := range files {
		segment, _, err := readSegment(path, false, previous, digest)
		if err != nil {
			return nil, err
		}
		records = append(records, segment...)
		if len(segment) != 0 {
			previous = segment[len(segment)-1].Sequence
			digest = segment[len(segment)-1].SHA256
		}
	}
	return records, nil
}

func (w *Writer) rotate() error {
	if err := w.file.Sync(); err != nil {
		return err
	}
	if err := w.file.Close(); err != nil {
		return err
	}
	closedPath := filepath.Join(
		w.root,
		closedSegmentName(w.segmentFrom, w.last),
	)
	if err := os.Rename(w.openPath, closedPath); err != nil {
		return err
	}
	w.file = nil
	return w.openSegment(w.last + 1)
}

func (w *Writer) openSegment(start uint64) error {
	path := filepath.Join(w.root, openSegmentName(start))
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w.file = file
	w.openPath = path
	w.segmentFrom = start
	w.size = 0
	return nil
}

func recoverSegments(
	root string,
) (openPath string, last uint64, digest string, size int64, start uint64, err error) {
	files, err := segmentFiles(root)
	if err != nil {
		return "", 0, "", 0, 0, err
	}
	var openCount int
	for index, path := range files {
		if chmodErr := os.Chmod(path, 0o600); chmodErr != nil {
			return "", 0, "", 0, 0, chmodErr
		}
		isLast := index == len(files)-1
		isOpen := strings.HasSuffix(path, "-open.jsonl")
		if isOpen {
			openCount++
			if !isLast {
				return "", 0, "", 0, 0, corruptf("open segment is not final")
			}
		}
		records, committedBytes, readErr := readSegment(
			path,
			isLast && isOpen,
			last,
			digest,
		)
		if readErr != nil {
			return "", 0, "", 0, 0, readErr
		}
		declaredStart, declaredEnd, boundsErr := parseSegmentBounds(path)
		if boundsErr != nil {
			return "", 0, "", 0, 0, boundsErr
		}
		if len(records) == 0 && !isOpen {
			return "", 0, "", 0, 0, corruptf("closed segment is empty")
		}
		if len(records) != 0 &&
			(records[0].Sequence != declaredStart ||
				(declaredEnd != 0 &&
					records[len(records)-1].Sequence != declaredEnd)) {
			return "", 0, "", 0, 0, corruptf(
				"segment name does not match record bounds",
			)
		}
		if len(records) != 0 {
			last = records[len(records)-1].Sequence
			digest = records[len(records)-1].SHA256
		}
		if isOpen {
			openPath = path
			size = committedBytes
			start, err = parseSegmentStart(path)
			if err != nil {
				return "", 0, "", 0, 0, err
			}
		}
	}
	if openCount > 1 {
		return "", 0, "", 0, 0, corruptf("multiple open segments")
	}
	return openPath, last, digest, size, start, nil
}

func readSegment(
	path string,
	repairTail bool,
	previousSequence uint64,
	previousDigest string,
) ([]Record, int64, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		return nil, 0, err
	}
	defer file.Close()
	reader := bufio.NewReaderSize(file, 64<<10)
	var records []Record
	var committedBytes int64
	sequence := previousSequence
	digest := previousDigest
	for {
		line, readErr := reader.ReadBytes('\n')
		if errors.Is(readErr, io.EOF) && len(line) != 0 {
			if !repairTail {
				return nil, 0, corruptf("%s has a torn interior record", path)
			}
			if err := file.Truncate(committedBytes); err != nil {
				return nil, 0, err
			}
			break
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return nil, 0, readErr
		}
		if len(line) > maxRecordBytes {
			return nil, 0, corruptf("%s contains an oversized record", path)
		}
		var wire struct {
			Sequence       uint64          `json:"sequence"`
			Envelope       json.RawMessage `json:"envelope"`
			SHA256         string          `json:"sha256"`
			PreviousSHA256 string          `json:"previous_sha256,omitempty"`
		}
		if err := json.Unmarshal(bytes.TrimSuffix(line, []byte{'\n'}), &wire); err != nil {
			return nil, 0, corruptf("%s contains invalid JSON: %v", path, err)
		}
		envelope, err := observation.DecodeJSON(wire.Envelope)
		if err != nil {
			return nil, 0, corruptf("%s contains invalid envelope: %v", path, err)
		}
		record := Record{
			Sequence: wire.Sequence, Envelope: envelope,
			SHA256: wire.SHA256, PreviousSHA256: wire.PreviousSHA256,
		}
		if record.Sequence != sequence+1 ||
			record.Envelope.Sequence != record.Sequence {
			return nil, 0, errors.Join(
				ErrOutOfOrder,
				fmt.Errorf("record sequence %d follows %d", record.Sequence, sequence),
			)
		}
		if record.PreviousSHA256 != digest {
			return nil, 0, corruptf("record %d digest chain mismatch", record.Sequence)
		}
		if record.SHA256 != sha256Hex(wire.Envelope) {
			return nil, 0, corruptf("record %d digest mismatch", record.Sequence)
		}
		records = append(records, record)
		sequence = record.Sequence
		digest = record.SHA256
		committedBytes += int64(len(line))
	}
	return records, committedBytes, nil
}

func segmentFiles(root string) ([]string, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}
		if _, err := parseSegmentStart(entry.Name()); err != nil {
			return nil, corruptf("invalid segment name %q", entry.Name())
		}
		result = append(result, filepath.Join(root, entry.Name()))
	}
	sort.Strings(result)
	return result, nil
}

func parseSegmentStart(path string) (uint64, error) {
	start, _, err := parseSegmentBounds(path)
	return start, err
}

func parseSegmentBounds(path string) (uint64, uint64, error) {
	name := filepath.Base(path)
	before, after, ok := strings.Cut(strings.TrimSuffix(name, ".jsonl"), "-")
	if !ok || len(before) != 20 {
		return 0, 0, ErrCorrupt
	}
	start, err := strconv.ParseUint(before, 10, 64)
	if err != nil {
		return 0, 0, ErrCorrupt
	}
	if after == "open" {
		return start, 0, nil
	}
	if len(after) != 20 {
		return 0, 0, ErrCorrupt
	}
	end, err := strconv.ParseUint(after, 10, 64)
	if err != nil || end < start {
		return 0, 0, ErrCorrupt
	}
	return start, end, nil
}

func ensureManifest(root string) error {
	path := filepath.Join(root, manifestName)
	content := []byte("{\"schema_version\":1,\"format\":\"observation-jsonl-sha256-chain\"}\n")
	existing, err := os.ReadFile(path)
	if err == nil {
		if !bytes.Equal(existing, content) {
			return corruptf("journal manifest differs from v1")
		}
		return os.Chmod(path, 0o600)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, content, 0o600)
}

func openSegmentName(start uint64) string {
	return fmt.Sprintf("%020d-open.jsonl", start)
}

func closedSegmentName(start, end uint64) string {
	return fmt.Sprintf("%020d-%020d.jsonl", start, end)
}

func sha256Hex(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

func encodeRecord(
	buffer *bytes.Buffer,
	sequence uint64,
	envelopeJSON []byte,
	digest [sha256.Size]byte,
	previous [sha256.Size]byte,
	hasPrevious bool,
) {
	buffer.Grow(len(envelopeJSON) + 192)
	buffer.WriteString(`{"sequence":`)
	var number [20]byte
	buffer.Write(strconv.AppendUint(number[:0], sequence, 10))
	buffer.WriteString(`,"envelope":`)
	buffer.Write(envelopeJSON)
	buffer.WriteString(`,"sha256":"`)
	appendHex(buffer, digest[:])
	buffer.WriteByte('"')
	if hasPrevious {
		buffer.WriteString(`,"previous_sha256":"`)
		appendHex(buffer, previous[:])
		buffer.WriteByte('"')
	}
	buffer.WriteByte('}')
	buffer.WriteByte('\n')
}

func appendHex(buffer *bytes.Buffer, value []byte) {
	available := buffer.AvailableBuffer()
	available = hex.AppendEncode(available, value)
	buffer.Write(available)
}

func corruptf(format string, args ...any) error {
	return errors.Join(ErrCorrupt, fmt.Errorf(format, args...))
}
