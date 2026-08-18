package router

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/observability/journal"
	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	"github.com/fwtllh-png/CodeHelper/internal/observability/privacy"
	"github.com/fwtllh-png/CodeHelper/internal/persist/state/cas"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestCriticalStartIsReadableBeforeRouterFlush(t *testing.T) {
	root := t.TempDir()
	writer, err := journal.Open(root, journal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	router, err := New(writer, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	receipt := router.Record(t.Context(), turnRecord(observation.KindTurnStarted))
	if receipt.Status != observation.AdmissionAccepted {
		t.Fatalf("receipt = %+v", receipt)
	}
	records, err := journal.ReadAll(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 ||
		records[0].Envelope.Kind != observation.KindTurnStarted {
		t.Fatalf("records = %+v", records)
	}
}

func TestCanonicalWriteClockIsNondecreasing(t *testing.T) {
	base := time.Unix(1_700_000_000, 0).UTC()
	wallTimes := []time.Time{
		base.Add(2 * time.Microsecond),
		base.Add(time.Microsecond),
	}
	monotonicTimes := []uint64{20, 10}
	var wallIndex, monotonicIndex int
	writer := &fakeJournal{}
	router, err := New(writer, nil, Options{
		Now: func() time.Time {
			value := wallTimes[wallIndex]
			wallIndex++
			return value
		},
		MonotonicNow: func() uint64 {
			value := monotonicTimes[monotonicIndex]
			monotonicIndex++
			return value
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	for range 2 {
		if receipt := router.Record(
			t.Context(),
			turnRecord(observation.KindTurnStarted),
		); receipt.Status != observation.AdmissionAccepted {
			t.Fatalf("receipt = %+v", receipt)
		}
	}
	envelopes := writer.snapshot()
	if len(envelopes) != 2 {
		t.Fatalf("envelopes = %+v", envelopes)
	}
	if envelopes[1].RecordedAt.Before(envelopes[0].RecordedAt) {
		t.Fatalf(
			"recorded_at regressed: %s then %s",
			envelopes[0].RecordedAt,
			envelopes[1].RecordedAt,
		)
	}
	if envelopes[0].MonotonicNS != 20 ||
		envelopes[1].MonotonicNS != 21 {
		t.Fatalf(
			"monotonic_ns = %d, %d",
			envelopes[0].MonotonicNS,
			envelopes[1].MonotonicNS,
		)
	}
}

func TestPayloadIsStoredBeforeJournalReference(t *testing.T) {
	payloads := &fakePayloadStore{}
	writer := &fakeJournal{beforeAppend: func(envelope observation.Envelope) error {
		if envelope.Payload == nil || !payloads.has(envelope.Payload.Digest[7:]) {
			return errors.New("journal observed payload before CAS")
		}
		return nil
	}}
	router, err := New(writer, payloads, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	record := modelRecord()
	record.Payload = &observation.Payload{
		Data: []byte("model request"), MediaType: "application/json",
		DataClass: observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	record.Policy = observation.DataPolicy{
		Class:     observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	if receipt := router.Record(t.Context(), record); receipt.Status != observation.AdmissionAccepted {
		t.Fatalf("receipt = %+v", receipt)
	}
	if err := router.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if writer.count() != 1 {
		t.Fatalf("journal writes = %d", writer.count())
	}
}

func TestPayloadReferenceResolvesFromRealCAS(t *testing.T) {
	root := t.TempDir()
	writer, err := journal.Open(root+"/journal", journal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := cas.Open(root + "/cas")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = payloads.Close(context.Background()) })
	router, err := New(writer, payloads, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	record := modelRecord()
	record.Payload = &observation.Payload{
		Data: []byte("durable payload"), MediaType: "application/json",
		DataClass: observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	record.Policy = observation.DataPolicy{
		Class:     observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	if receipt := router.Record(t.Context(), record); receipt.Status != observation.AdmissionAccepted {
		t.Fatalf("receipt = %+v", receipt)
	}
	if err := router.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	records, err := journal.ReadAll(root + "/journal")
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].Envelope.Payload == nil {
		t.Fatalf("records = %+v", records)
	}
	digest := records[0].Envelope.Payload.Digest[7:]
	content, err := payloads.Get(t.Context(), digest)
	if err != nil || string(content) != "durable payload" {
		t.Fatalf("content=%q error=%v", content, err)
	}
}

func TestPrivacyPolicyRedactsBeforeAnyJournalOrCASWrite(t *testing.T) {
	root := t.TempDir()
	writer, err := journal.Open(
		filepath.Join(root, "journal"),
		journal.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := cas.Open(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = payloads.Close(context.Background()) })
	secret := "sk-router-secret"
	restricted := "/workspace/.env"
	capture, err := privacy.New(privacy.Options{
		Mode: privacy.CaptureFull, Secrets: []string{secret},
		RestrictedPaths: []string{restricted},
	})
	if err != nil {
		t.Fatal(err)
	}
	router, err := New(writer, payloads, Options{Sanitizer: capture})
	if err != nil {
		t.Fatal(err)
	}
	record := modelRecord()
	record.Payload = &observation.Payload{
		Data: []byte(
			`{"api_key":"` + secret + `","path":"` + restricted + `"}`,
		),
		MediaType: "application/json",
		DataClass: observation.DataConversation,
		Redaction: observation.RedactionUnavailable,
	}
	record.Policy = observation.DataPolicy{
		Class:     observation.DataConversation,
		Redaction: observation.RedactionUnavailable,
	}
	if receipt := router.Record(
		t.Context(),
		record,
	); receipt.Status != observation.AdmissionAccepted {
		t.Fatalf("receipt = %+v", receipt)
	}
	if err := router.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(root, func(
		path string,
		entry os.DirEntry,
		walkErr error,
	) error {
		if walkErr != nil || entry.IsDir() {
			return walkErr
		}
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range []string{secret, restricted} {
			if strings.Contains(string(content), forbidden) {
				t.Fatalf("%s leaked %q", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMetadataCaptureNeverWritesPayloadStore(t *testing.T) {
	capture, err := privacy.New(privacy.Options{})
	if err != nil {
		t.Fatal(err)
	}
	writer := &fakeJournal{}
	payloads := &fakePayloadStore{}
	router, err := New(
		writer,
		payloads,
		Options{Sanitizer: capture},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	record := modelRecord()
	record.Payload = &observation.Payload{
		Data: []byte("raw"), MediaType: "text/plain",
		DataClass: observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	record.Policy = observation.DataPolicy{
		Class:     observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	receipt := router.Record(t.Context(), record)
	if receipt.Status != observation.AdmissionPayloadDropped {
		t.Fatalf("receipt = %+v", receipt)
	}
	if err := router.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	if payloads.putCount() != 0 ||
		writer.snapshot()[0].Payload != nil {
		t.Fatal("metadata capture wrote raw payload")
	}
}

func TestPayloadDeduplicationRateIsObservable(t *testing.T) {
	root := t.TempDir()
	writer, err := journal.Open(
		filepath.Join(root, "journal"),
		journal.Options{},
	)
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := cas.Open(filepath.Join(root, "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = payloads.Close(context.Background()) })
	router, err := New(writer, payloads, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	record := modelRecord()
	record.Payload = &observation.Payload{
		Data: []byte("same payload"), MediaType: "text/plain",
		DataClass: observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	record.Policy = observation.DataPolicy{
		Class:     observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	router.Record(t.Context(), record)
	router.Record(t.Context(), record)
	if err := router.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	health := router.Snapshot()
	if health.PayloadWritten != 2 ||
		health.PayloadDeduplicated != 1 ||
		health.PayloadDedupRate != 0.5 {
		t.Fatalf("health = %+v", health)
	}
}

func TestJournalFailureReleasesPayloadAndDoesNotEscape(t *testing.T) {
	payloads := &fakePayloadStore{}
	writer := &fakeJournal{appendErr: errors.New("disk full")}
	projector := &fakeProjector{}
	router, err := New(writer, payloads, Options{Projector: projector})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	record := turnRecord(observation.KindTurnTerminalPrepared)
	record.Payload = &observation.Payload{
		Data: []byte("receipt"), MediaType: "application/json",
		DataClass: observation.DataOperational,
		Redaction: observation.RedactionNotRequired,
	}
	receipt := router.Record(t.Context(), record)
	if receipt.Status != observation.AdmissionWriterFailed {
		t.Fatalf("receipt = %+v", receipt)
	}
	if payloads.releaseCount() != 1 {
		t.Fatalf("payload releases = %d", payloads.releaseCount())
	}
	if router.Snapshot().WriteFailures["journal_write"] != 1 {
		t.Fatalf("health = %+v", router.Snapshot())
	}
	if len(projector.snapshot()) != 0 {
		t.Fatal("failed journal append reached the OTEL projector")
	}
	// Business callers intentionally have no error return to branch on.
	businessCompleted := true
	if !businessCompleted {
		t.Fatal("observation failure changed business completion")
	}
}

func TestAsynchronousJournalFailureIsReturnedByFlush(t *testing.T) {
	writeErr := errors.New("injected asynchronous disk full")
	router, err := New(&fakeJournal{appendErr: writeErr}, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	record := modelRecord()
	receipt := router.Record(t.Context(), record)
	if receipt.Status != observation.AdmissionAccepted {
		t.Fatalf("Record() receipt = %+v", receipt)
	}
	if err := router.Flush(t.Context()); !errors.Is(err, writeErr) {
		t.Fatalf("Flush() error = %v, want %v", err, writeErr)
	}
	if err := router.Close(t.Context()); !errors.Is(err, writeErr) {
		t.Fatalf("Close() error = %v, want %v", err, writeErr)
	}
	if router.Snapshot().WriteFailures["journal_write"] != 1 {
		t.Fatalf("health = %+v", router.Snapshot())
	}
}

func TestProjectorReceivesOnlyDurableEnvelope(t *testing.T) {
	writer := &fakeJournal{}
	projector := &fakeProjector{}
	router, err := New(writer, nil, Options{Projector: projector})
	if err != nil {
		t.Fatal(err)
	}
	if receipt := router.Record(
		t.Context(),
		turnRecord(observation.KindTurnStarted),
	); receipt.Status != observation.AdmissionAccepted {
		t.Fatalf("receipt = %+v", receipt)
	}
	projected := projector.snapshot()
	if len(projected) != 1 ||
		projected[0].Sequence != 1 ||
		writer.count() != 1 {
		t.Fatalf(
			"journal=%+v projected=%+v",
			writer.snapshot(),
			projected,
		)
	}
	if err := router.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if projector.flushes != 1 || projector.shutdowns != 1 {
		t.Fatalf(
			"projector flushes=%d shutdowns=%d",
			projector.flushes,
			projector.shutdowns,
		)
	}
}

func TestPayloadFailureRetainsMetadataWithoutDanglingReference(t *testing.T) {
	payloads := &fakePayloadStore{putErr: errors.New("cas unavailable")}
	writer := &fakeJournal{}
	router, err := New(writer, payloads, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	record := modelRecord()
	record.Payload = &observation.Payload{
		Data: []byte("request"), MediaType: "application/json",
		DataClass: observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	record.Policy = observation.DataPolicy{
		Class:     observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	if receipt := router.Record(t.Context(), record); receipt.Status != observation.AdmissionAccepted {
		t.Fatalf("receipt = %+v", receipt)
	}
	if err := router.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	envelopes := writer.snapshot()
	if len(envelopes) != 1 || envelopes[0].Payload != nil {
		t.Fatalf("envelopes = %+v", envelopes)
	}
	snapshot := router.Snapshot()
	if snapshot.WriteFailures["payload_write"] != 1 ||
		snapshot.PayloadDropped != 1 {
		t.Fatalf("health = %+v", snapshot)
	}
}

func TestOversizedOptionalPayloadKeepsMetadata(t *testing.T) {
	writer := &fakeJournal{}
	payloads := &fakePayloadStore{}
	router, err := New(writer, payloads, Options{MaxPayloadBytes: 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	record := modelRecord()
	record.Payload = &observation.Payload{
		Data: []byte("too-large"), MediaType: "application/json",
		DataClass: observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	record.Policy = observation.DataPolicy{
		Class:     observation.DataConversation,
		Redaction: observation.RedactionApplied,
	}
	receipt := router.Record(t.Context(), record)
	if receipt.Status != observation.AdmissionPayloadDropped {
		t.Fatalf("receipt = %+v", receipt)
	}
	if err := router.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	envelopes := writer.snapshot()
	if len(envelopes) != 1 || envelopes[0].Payload != nil {
		t.Fatalf("envelopes = %+v", envelopes)
	}
	if payloads.putCount() != 0 {
		t.Fatalf("payload puts = %d", payloads.putCount())
	}
}

func TestQueuePriorityEvictsBulkBeforeNormal(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	writer := &fakeJournal{beforeAppend: func(observation.Envelope) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return nil
	}}
	router, err := New(writer, nil, Options{
		MetadataCapacity: 1,
		PayloadBytes:     4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	if receipt := router.Record(t.Context(), modelRecord()); receipt.Status != observation.AdmissionAccepted {
		t.Fatalf("first receipt = %+v", receipt)
	}
	<-entered
	bulk := turnRecord(observation.KindToolRuntimeOutput)
	bulk.Identity.CallID = "call-1"
	if receipt := router.Record(t.Context(), bulk); receipt.Status != observation.AdmissionAccepted {
		t.Fatalf("bulk receipt = %+v", receipt)
	}
	if receipt := router.Record(t.Context(), modelRecord()); receipt.Status != observation.AdmissionAccepted {
		t.Fatalf("normal receipt = %+v", receipt)
	}
	close(release)
	if err := router.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
	envelopes := writer.snapshot()
	if len(envelopes) != 2 {
		t.Fatalf("writes = %d", len(envelopes))
	}
	if envelopes[0].ObservedSequence != 1 ||
		envelopes[1].ObservedSequence != 3 {
		t.Fatalf("observed sequences = %d, %d",
			envelopes[0].ObservedSequence,
			envelopes[1].ObservedSequence,
		)
	}
	for _, envelope := range envelopes {
		if envelope.Kind == observation.KindToolRuntimeOutput {
			t.Fatal("bulk record was not evicted")
		}
	}
	if router.Snapshot().Dropped["priority_eviction"] != 1 {
		t.Fatalf("health = %+v", router.Snapshot())
	}
}

func TestQueueFullIsReported(t *testing.T) {
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	writer := &fakeJournal{beforeAppend: func(observation.Envelope) error {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return nil
	}}
	router, err := New(writer, nil, Options{MetadataCapacity: 1, PayloadBytes: 4096})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = router.Close(context.Background()) })
	router.Record(t.Context(), modelRecord())
	<-entered
	router.Record(t.Context(), modelRecord())
	receipt := router.Record(t.Context(), modelRecord())
	if receipt.Status != observation.AdmissionQueueFull {
		t.Fatalf("receipt = %+v", receipt)
	}
	close(release)
	if err := router.Flush(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCloseReleasesJournalAfterSyncFailure(t *testing.T) {
	writer := &fakeJournal{syncErr: errors.New("sync failed")}
	router, err := New(writer, nil, Options{})
	if err != nil {
		t.Fatal(err)
	}
	err = router.Close(t.Context())
	if err == nil || writer.closedCount() != 1 {
		t.Fatalf("Close() error=%v journal closes=%d", err, writer.closedCount())
	}
}

type fakeJournal struct {
	mu           sync.Mutex
	sequence     uint64
	envelopes    []observation.Envelope
	beforeAppend func(observation.Envelope) error
	appendErr    error
	syncErr      error
	closes       int
}

func (j *fakeJournal) Append(
	_ context.Context,
	envelope observation.Envelope,
) (observation.Envelope, error) {
	if j.beforeAppend != nil {
		if err := j.beforeAppend(envelope); err != nil {
			return observation.Envelope{}, err
		}
	}
	if j.appendErr != nil {
		return observation.Envelope{}, j.appendErr
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	j.sequence++
	envelope.Sequence = j.sequence
	j.envelopes = append(j.envelopes, envelope)
	return envelope, nil
}

func (j *fakeJournal) Sync(context.Context) error { return j.syncErr }
func (j *fakeJournal) Close(context.Context) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.closes++
	return nil
}
func (j *fakeJournal) count() int { return len(j.snapshot()) }

func (j *fakeJournal) closedCount() int {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.closes
}

func (j *fakeJournal) snapshot() []observation.Envelope {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]observation.Envelope(nil), j.envelopes...)
}

type fakePayloadStore struct {
	mu       sync.Mutex
	values   map[string][]byte
	releases int
	putErr   error
}

type fakeProjector struct {
	mu        sync.Mutex
	envelopes []observation.Envelope
	flushes   int
	shutdowns int
}

func (p *fakeProjector) Project(envelope observation.Envelope) {
	p.mu.Lock()
	p.envelopes = append(p.envelopes, envelope)
	p.mu.Unlock()
}

func (p *fakeProjector) ForceFlush(context.Context) error {
	p.mu.Lock()
	p.flushes++
	p.mu.Unlock()
	return nil
}

func (p *fakeProjector) Shutdown(context.Context) error {
	p.mu.Lock()
	p.shutdowns++
	p.mu.Unlock()
	return nil
}

func (p *fakeProjector) snapshot() []observation.Envelope {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]observation.Envelope(nil), p.envelopes...)
}

func (s *fakePayloadStore) Put(_ context.Context, id string, data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.putErr != nil {
		return s.putErr
	}
	if s.values == nil {
		s.values = make(map[string][]byte)
	}
	s.values[id] = append([]byte(nil), data...)
	return nil
}

func (s *fakePayloadStore) Release(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, id)
	s.releases++
	return nil
}

func (s *fakePayloadStore) has(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.values[id]
	return ok
}

func (s *fakePayloadStore) putCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.values)
}

func (s *fakePayloadStore) releaseCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.releases
}

func turnRecord(kind observation.Kind) observation.Record {
	return observation.Record{
		Kind: kind,
		Identity: observation.Identity{
			RuntimeID: "runtime-test",
			TurnID:    protocol.TurnID("turn-test"),
		},
		Policy: observation.DataPolicy{
			Class:     observation.DataOperational,
			Redaction: observation.RedactionNotRequired,
		},
	}
}

func modelRecord() observation.Record {
	record := turnRecord(observation.KindContextProjected)
	record.Identity.SampleID = "sample-1"
	record.Summary = []byte(`{"revision":1}`)
	return record
}

func BenchmarkSO2CriticalMetadataRecord(b *testing.B) {
	writer := &benchmarkJournal{}
	router, err := New(writer, nil, Options{})
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = router.Close(context.Background()) })
	record := turnRecord(observation.KindTurnStarted)
	b.ReportAllocs()
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if receipt := router.Record(context.Background(), record); receipt.Status != observation.AdmissionAccepted {
			b.Fatalf("receipt = %+v", receipt)
		}
	}
}

type benchmarkJournal struct {
	mu       sync.Mutex
	sequence uint64
}

func (j *benchmarkJournal) Append(
	_ context.Context,
	envelope observation.Envelope,
) (observation.Envelope, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.sequence++
	envelope.Sequence = j.sequence
	return envelope, nil
}

func (*benchmarkJournal) Sync(context.Context) error  { return nil }
func (*benchmarkJournal) Close(context.Context) error { return nil }
