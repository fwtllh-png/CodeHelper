package joblog_test

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/persist/joblog"
)

func TestRangeReadsFromAnyOffsetAndReportsWhatIsLeft(t *testing.T) {
	store, err := joblog.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, chunk := range []string{"alpha\n", "beta\n", "gamma\n"} {
		if err := store.Append("job-1", []byte(chunk)); err != nil {
			t.Fatal(err)
		}
	}
	whole := "alpha\nbeta\ngamma\n"

	data, total, err := store.Range("job-1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != whole || total != uint64(len(whole)) {
		t.Fatalf("range = %q total=%d", data, total)
	}
	// A limit truncates the read but not the reported total, so a caller can tell
	// there is more to come.
	data, total, err = store.Range("job-1", 6, 4)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "beta" || total != uint64(len(whole)) {
		t.Fatalf("bounded range = %q total=%d", data, total)
	}
	// A caller that is up to date is not wrong: no data, no error.
	data, _, err = store.Range("job-1", total, 0)
	if err != nil || len(data) != 0 {
		t.Fatalf("range at the end = %q, %v", data, err)
	}
}

func TestAppendsSurviveReopeningTheStore(t *testing.T) {
	directory := t.TempDir()
	store, err := joblog.New(directory)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Append("job-1", []byte("first\n")); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := joblog.New(directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Append("job-1", []byte("second\n")); err != nil {
		t.Fatal(err)
	}
	data, _, err := reopened.Range("job-1", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "first\nsecond\n" {
		t.Fatalf("reopened log = %q, want the earlier bytes kept", data)
	}
}

func TestAJobWithNoLogIsReportedAsMissing(t *testing.T) {
	store, err := joblog.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, _, err := store.Range("absent", 0, 0); !errors.Is(err, joblog.ErrNotFound) {
		t.Fatalf("range error = %v, want ErrNotFound", err)
	}
	if _, err := store.Size("absent"); !errors.Is(err, joblog.ErrNotFound) {
		t.Fatalf("size error = %v, want ErrNotFound", err)
	}
}

// A job id is used as a file name, so an id that could escape the directory has
// to be refused rather than sanitised into something that silently collides.
func TestIdsThatWouldEscapeTheDirectoryAreRefused(t *testing.T) {
	store, err := joblog.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	for _, id := range []string{"", ".", "..", "../escape", `sub\escape`, "a/b"} {
		if err := store.Append(id, []byte("x")); err == nil {
			t.Fatalf("append accepted id %q", id)
		}
	}
}

func TestARelativeDirectoryIsRefused(t *testing.T) {
	if _, err := joblog.New(filepath.Join("relative", "jobs")); err == nil {
		t.Fatal("a relative directory should be refused")
	}
}

func TestRemoveDiscardsALog(t *testing.T) {
	store, err := joblog.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.Append("job-1", []byte("bytes")); err != nil {
		t.Fatal(err)
	}
	if err := store.Remove("job-1"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Range("job-1", 0, 0); !errors.Is(err, joblog.ErrNotFound) {
		t.Fatalf("range after remove = %v, want ErrNotFound", err)
	}
	// Removing what is not there is not a failure: retention is idempotent.
	if err := store.Remove("job-1"); err != nil {
		t.Fatal(err)
	}
	// The store still works after a removal, including for the same id.
	if err := store.Append("job-1", []byte("again")); err != nil {
		t.Fatal(err)
	}
	data, _, err := store.Range("job-1", 0, 0)
	if err != nil || !strings.Contains(string(data), "again") {
		t.Fatalf("reused id = %q, %v", data, err)
	}
}
