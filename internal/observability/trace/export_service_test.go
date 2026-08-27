package trace

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/observability/usage"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestExportServiceProducesSessionScopedVerifiableNDJSON(t *testing.T) {
	recordedAt := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	snapshotter := exportSnapshotter{envelopes: []observation.Envelope{
		exportEnvelope(1, "session-1", observation.DataOperational, recordedAt),
		exportEnvelope(2, "session-2", observation.DataOperational, recordedAt),
		exportEnvelope(3, "process", observation.DataConversation, recordedAt),
	}}
	snapshotter.envelopes[2].Identity.TurnID = "turn-session-1"
	snapshotter.envelopes[0].Payload = &observation.PayloadRef{
		Digest: "sha256:" + strings.Repeat("a", 64), MediaType: "application/json",
		OriginalBytes: 10, StoredBytes: 10,
		DataClass: observation.DataOperational,
		Redaction: observation.RedactionApplied,
	}
	snapshotter.envelopes[0].Summary =
		json.RawMessage(`{"path":"/workspace/secret.go"}`)
	snapshotter.envelopes[2].Summary = json.RawMessage(`{"prompt":"private"}`)
	service := NewExportService(
		sessionReader{
			sessionID: "session-1", workspaceRoot: "/workspace",
		},
		snapshotter,
		exportUsageReader{aggregates: []usage.Aggregate{{
			SessionID: "session-1", TurnID: "turn-session-1",
			Provider: "fixture", Model: "fixture",
			InputTokens: 100, OutputTokens: 20, CachedTokens: 80,
			UnpricedCalls: 1, Calls: 1,
			FirstAt: recordedAt, LastAt: recordedAt.Add(time.Second),
		}}},
		"/workspace",
	)
	service.now = func() time.Time { return recordedAt.Add(time.Hour) }

	result, err := service.Export(t.Context(), ExportRequest{
		SessionID: "session-1", ProducerVersion: "test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.MediaType != ExportMediaType ||
		!strings.HasSuffix(result.Filename, ".ndjson") ||
		result.Manifest.EventCount != 2 ||
		result.Manifest.UsageCount != 1 ||
		result.Manifest.ThroughSequence != 3 {
		t.Fatalf("export result = %+v", result)
	}

	scanner := bufio.NewScanner(bytes.NewReader(result.Content))
	var lines [][]byte
	for scanner.Scan() {
		lines = append(lines, append([]byte(nil), scanner.Bytes()...))
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(lines) != 4 {
		t.Fatalf("lines = %d, want 4", len(lines))
	}
	var manifest ExportManifest
	if err := json.Unmarshal(lines[0], &manifest); err != nil {
		t.Fatal(err)
	}
	var eventBytes []byte
	for _, line := range lines[1:] {
		eventBytes = append(eventBytes, line...)
		eventBytes = append(eventBytes, '\n')
	}
	digest := sha256.Sum256(eventBytes)
	if manifest.RecordsSHA256 != hex.EncodeToString(digest[:]) {
		t.Fatalf("records digest = %q", manifest.RecordsSHA256)
	}
	if bytes.Contains(result.Content, []byte("session-2")) ||
		bytes.Contains(result.Content, []byte(`"prompt":"private"`)) ||
		bytes.Contains(result.Content, []byte("/workspace")) ||
		bytes.Contains(result.Content, []byte(strings.Repeat("a", 64))) {
		t.Fatalf("export leaked excluded data: %s", result.Content)
	}
	if !bytes.Contains(result.Content, []byte(`"payload_omitted":true`)) ||
		!bytes.Contains(result.Content, []byte(`"summary_omitted":true`)) ||
		!bytes.Contains(result.Content, []byte(`[REDACTED]/secret.go`)) ||
		!bytes.Contains(result.Content, []byte(`"unpriced_calls":1`)) {
		t.Fatalf("export omission receipts missing: %s", result.Content)
	}
}

func TestExportServiceValidatesSessionBeforeSnapshot(t *testing.T) {
	snapshotter := &countingSnapshotter{}
	service := NewExportService(
		sessionReader{sessionID: "session-1"},
		snapshotter,
		nil,
		"",
	)
	if _, err := service.Export(t.Context(), ExportRequest{
		SessionID: "foreign",
	}); err == nil {
		t.Fatal("foreign session export succeeded")
	}
	if snapshotter.calls != 0 {
		t.Fatalf("snapshot calls = %d, want 0", snapshotter.calls)
	}
}

func TestExportServiceRejectsSessionFromAnotherWorkspace(t *testing.T) {
	snapshotter := &countingSnapshotter{}
	service := NewExportService(
		sessionReader{
			sessionID: "session-1", workspaceRoot: "/workspace-b",
		},
		snapshotter,
		nil,
		"/workspace-a",
	)
	if _, err := service.Export(t.Context(), ExportRequest{
		SessionID: "session-1",
	}); err == nil {
		t.Fatal("cross-Workspace session export succeeded")
	}
	if snapshotter.calls != 0 {
		t.Fatalf("snapshot calls = %d, want 0", snapshotter.calls)
	}
}

type exportSnapshotter struct {
	envelopes []observation.Envelope
}

func (s exportSnapshotter) Snapshot(
	context.Context,
) ([]observation.Envelope, error) {
	return append([]observation.Envelope(nil), s.envelopes...), nil
}

type countingSnapshotter struct{ calls int }

func (s *countingSnapshotter) Snapshot(
	context.Context,
) ([]observation.Envelope, error) {
	s.calls++
	return nil, nil
}

type exportUsageReader struct {
	aggregates []usage.Aggregate
}

func (r exportUsageReader) QueryAggregates(
	context.Context,
	usage.Query,
) ([]usage.Aggregate, error) {
	return append([]usage.Aggregate(nil), r.aggregates...), nil
}

func (s sessionReader) ThreadIDs(
	_ context.Context,
	sessionID string,
) ([]protocol.ThreadID, error) {
	if sessionID != s.sessionID {
		return nil, context.Canceled
	}
	return []protocol.ThreadID{
		protocol.ThreadID("thread-" + sessionID),
	}, nil
}

func (s sessionReader) TurnIDs(
	_ context.Context,
	sessionID string,
) ([]protocol.TurnID, error) {
	if sessionID != s.sessionID {
		return nil, context.Canceled
	}
	return []protocol.TurnID{
		protocol.TurnID("turn-" + sessionID),
	}, nil
}

func exportEnvelope(
	sequence uint64,
	sessionID string,
	class observation.DataClass,
	recordedAt time.Time,
) observation.Envelope {
	return observation.Envelope{
		SchemaVersion: observation.SchemaVersion,
		ID: observation.ObservationID(
			"obs_" + strings.Repeat(string(rune('0'+sequence)), 32),
		),
		Kind:             observation.KindTurnStarted,
		ObservedSequence: sequence,
		Sequence:         sequence,
		RecordedAt:       recordedAt,
		Identity: observation.Identity{
			RuntimeID: "runtime-test",
			SessionID: sessionID,
			TurnID:    protocol.TurnID("turn-" + sessionID),
		},
		Policy: observation.DataPolicy{
			Class: class, Redaction: observation.RedactionApplied,
		},
		Summary: json.RawMessage(`{"status":"ok"}`),
	}
}
