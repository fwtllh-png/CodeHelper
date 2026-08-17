package health

import (
	"errors"
	"sync"
	"testing"
)

func TestTrackerSeparatesDropsAndFailures(t *testing.T) {
	tracker := NewTracker()
	tracker.Accepted()
	tracker.Written(true, true)
	tracker.PayloadDropped()
	tracker.Drop("queue_full")
	tracker.Failure("journal_write", errors.New("disk full"))
	tracker.Queue(3, 1024, 1)
	snapshot := tracker.Snapshot()
	if snapshot.Accepted != 1 || snapshot.Written != 1 ||
		snapshot.PayloadWritten != 1 ||
		snapshot.PayloadDeduplicated != 1 ||
		snapshot.PayloadDedupRate != 1 ||
		snapshot.PayloadDropped != 1 ||
		snapshot.Dropped["queue_full"] != 1 ||
		snapshot.WriteFailures["journal_write"] != 1 ||
		snapshot.QueueDepth != 3 || snapshot.QueueBytes != 1024 ||
		snapshot.InFlight != 1 || snapshot.LastError == "" ||
		snapshot.LastErrorAt == nil {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	snapshot.Dropped["queue_full"] = 99
	if tracker.Snapshot().Dropped["queue_full"] != 1 {
		t.Fatal("snapshot leaked mutable map")
	}
}

func TestTrackerIsConcurrent(t *testing.T) {
	tracker := NewTracker()
	var wait sync.WaitGroup
	for range 32 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range 100 {
				tracker.Accepted()
				tracker.Written(false, false)
			}
		}()
	}
	wait.Wait()
	snapshot := tracker.Snapshot()
	if snapshot.Accepted != 3200 || snapshot.Written != 3200 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
}
