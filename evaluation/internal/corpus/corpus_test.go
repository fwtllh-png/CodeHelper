package corpus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/capture"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
)

func TestPromoteAndVerifyCorpus(t *testing.T) {
	root := t.TempDir()
	slice := corpusSlice()
	manifest, err := Promote(Promotion{
		ID:           "real-trace-01",
		OutputRoot:   root,
		SourceFormat: capture.FormatVSCodeRuntime,
		SourceClass:  SourceRealPrivate,
		SourceDigest: digest("a"),
		Slice:        slice,
		Sanitizer: capture.SanitizerOptions{
			Secrets: []string{"private-secret"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.FailureSignature != "turn_failed" ||
		manifest.ContentMode != "metadata_only" {
		t.Fatalf("manifest = %+v", manifest)
	}
	verified, err := VerifyAll(root, capture.SanitizerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(verified) != 1 ||
		verified[0].Outcome.FailureSignature != "turn_failed" {
		t.Fatalf("verified = %+v", verified)
	}
	for _, name := range []string{"manifest.json", "events.jsonl"} {
		info, err := os.Stat(filepath.Join(root, "real-trace-01", name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("%s mode = %o, want 600", name, info.Mode().Perm())
		}
	}
}

func TestPromotionRejectsFailureSignatureDriftWithoutPartialOutput(t *testing.T) {
	root := t.TempDir()
	slice := corpusSlice()
	slice.Signature = "turn_completed"
	_, err := Promote(Promotion{
		ID: "rejected-trace", OutputRoot: root,
		SourceFormat: capture.FormatVSCodeRuntime,
		SourceClass:  SourceRealPrivate, SourceDigest: digest("b"),
		Slice: slice,
	})
	if err == nil || !strings.Contains(err.Error(), "failure signature changed") {
		t.Fatalf("signature drift error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "rejected-trace")); !os.IsNotExist(statErr) {
		t.Fatalf("partial promotion remains: %v", statErr)
	}
}

func TestVerifyRejectsTamperedCorpusAndUnknownManifestField(t *testing.T) {
	root := t.TempDir()
	_, err := Promote(Promotion{
		ID: "tamper-trace", OutputRoot: root,
		SourceFormat: capture.FormatVSCodeRuntime,
		SourceClass:  SourceRealPrivate, SourceDigest: digest("c"),
		Slice: corpusSlice(),
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "tamper-trace")
	eventsPath := filepath.Join(directory, "events.jsonl")
	if err := os.WriteFile(eventsPath, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(directory, capture.SanitizerOptions{}); err == nil ||
		!strings.Contains(err.Error(), "trace digest") {
		t.Fatalf("tamper error = %v", err)
	}

	manifestPath := filepath.Join(directory, "manifest.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["unknown"] = true
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Verify(directory, capture.SanitizerOptions{}); err == nil ||
		!strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown field error = %v", err)
	}
}

func TestQualifiedPromotionIsAtomicAndReviewBound(t *testing.T) {
	root := t.TempDir()
	sourceDigest := digest("d")
	review := PromotionReview{
		SchemaVersion: QualifiedSchemaVersion,
		ID:            "review-01", BatchID: "batch-01", Reviewer: "reviewer-01",
		Decision: "approved", SourceDigest: sourceDigest, ReviewedOn: "2026-08-19",
	}
	batch, err := PromoteBatch(BatchPromotion{
		BatchID: "batch-01", OutputRoot: root, EntryPrefix: "trace",
		SourceFormat: capture.FormatVSCodeRuntime,
		SourceClass:  SourceSynthetic, SourceDigest: sourceDigest,
		Slices:    []capture.Slice{corpusSlice()},
		Sanitizer: capture.SanitizerOptions{}, Review: review,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Entries) != 1 {
		t.Fatalf("batch = %+v", batch)
	}
	verified, err := VerifyBatch(
		filepath.Join(root, "batch-01"),
		capture.SanitizerOptions{},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(verified.Entries) != 1 ||
		verified.Entries[0].Manifest.ReplayLevel != "structural" {
		t.Fatalf("verified batch = %+v", verified)
	}
}

func TestQualifiedPromotionRollsBackCompleteBatch(t *testing.T) {
	root := t.TempDir()
	sourceDigest := digest("e")
	valid := corpusSlice()
	invalid := corpusSlice()
	invalid.Signature = "turn_completed"
	_, err := PromoteBatch(BatchPromotion{
		BatchID: "batch-rollback", OutputRoot: root, EntryPrefix: "trace",
		SourceFormat: capture.FormatVSCodeRuntime,
		SourceClass:  SourceSynthetic, SourceDigest: sourceDigest,
		Slices: []capture.Slice{valid, invalid},
		Review: PromotionReview{
			SchemaVersion: QualifiedSchemaVersion,
			ID:            "review-rollback", BatchID: "batch-rollback",
			Reviewer: "reviewer-01", Decision: "approved",
			SourceDigest: sourceDigest, ReviewedOn: "2026-08-19",
		},
	})
	if err == nil {
		t.Fatal("qualified promotion accepted invalid second slice")
	}
	if _, statErr := os.Stat(filepath.Join(root, "batch-rollback")); !os.IsNotExist(statErr) {
		t.Fatalf("partial qualified batch remains: %v", statErr)
	}
}

func TestQualifiedVerificationRejectsUnscannedFile(t *testing.T) {
	root := t.TempDir()
	sourceDigest := digest("f")
	_, err := PromoteBatch(BatchPromotion{
		BatchID: "batch-extra", OutputRoot: root, EntryPrefix: "trace",
		SourceFormat: capture.FormatVSCodeRuntime,
		SourceClass:  SourceSynthetic, SourceDigest: sourceDigest,
		Slices: []capture.Slice{corpusSlice()},
		Review: PromotionReview{
			SchemaVersion: QualifiedSchemaVersion,
			ID:            "review-extra", BatchID: "batch-extra",
			Reviewer: "reviewer-01", Decision: "approved",
			SourceDigest: sourceDigest, ReviewedOn: "2026-08-19",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "batch-extra")
	if err := os.WriteFile(
		filepath.Join(directory, "unscanned.txt"),
		[]byte("secret"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBatch(directory, capture.SanitizerOptions{}); err == nil ||
		!strings.Contains(err.Error(), "unscanned batch file") {
		t.Fatalf("extra file error = %v", err)
	}
}

func TestQualifiedVerificationScansManifestContent(t *testing.T) {
	root := t.TempDir()
	sourceDigest := digest("1")
	_, err := PromoteBatch(BatchPromotion{
		BatchID: "batch-manifest", OutputRoot: root, EntryPrefix: "trace",
		SourceFormat: capture.FormatVSCodeRuntime,
		SourceClass:  SourceSynthetic, SourceDigest: sourceDigest,
		Slices: []capture.Slice{corpusSlice()},
		Review: PromotionReview{
			SchemaVersion: QualifiedSchemaVersion,
			ID:            "review-manifest", BatchID: "batch-manifest",
			Reviewer: "reviewer-01", Decision: "approved",
			SourceDigest: sourceDigest, ReviewedOn: "2026-08-19",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "batch-manifest")
	manifestPath := filepath.Join(
		directory,
		"entries",
		"trace-01",
		"manifest.json",
	)
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["failure_signature"] = "sk_foundation_secret"
	raw, _ = json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyBatch(directory, capture.SanitizerOptions{}); err == nil {
		t.Fatal("qualified verification accepted secret in manifest")
	}
}

func corpusSlice() capture.Slice {
	return capture.Slice{
		Kind: "full", Index: 1, SourceCount: 2, Signature: "turn_failed",
		Events: []evidence.RawEnvelope{
			{
				ObservedSequence: 1, ObservedAt: "2026-08-19T00:00:00Z",
				Source: evidence.SourceRuntime, Kind: "turn.started",
				Identity: evidence.Identity{
					Capture: "private-capture", Session: "private-session",
					Turn: "private-turn", Operation: "private-operation",
				},
				Data: map[string]any{"shape": "metadata"},
			},
			{
				ObservedSequence: 2, ObservedAt: "2026-08-19T00:00:01Z",
				Source: evidence.SourceRuntime, Kind: "turn.failed",
				Identity: evidence.Identity{
					Capture: "private-capture", Session: "private-session",
					Turn: "private-turn", Operation: "private-operation",
				},
				Data: map[string]any{"message": "private-secret"},
			},
		},
	}
}

func digest(character string) string {
	return "sha256:" + strings.Repeat(character, 64)
}
