package web

import (
	"context"
	"os"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket/wsjson"
	"github.com/fwtllh-png/QCode/internal/runtime/app"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func TestWebSocketSustainedStreamingSoak(t *testing.T) {
	durationValue := os.Getenv("QCODE_WEB_STREAMING_SOAK_DURATION")
	if durationValue == "" {
		t.Skip("set QCODE_WEB_STREAMING_SOAK_DURATION=1h to run the release soak")
	}
	duration, err := time.ParseDuration(durationValue)
	if err != nil {
		t.Fatalf("parse soak duration: %v", err)
	}
	if duration < time.Hour &&
		os.Getenv("QCODE_WEB_STREAMING_SOAK_ALLOW_SHORT") != "1" {
		t.Fatalf("release soak duration = %s, want at least 1h", duration)
	}

	lifecycle := &eventAuthorizationLifecycle{summary: protocol.SessionSummary{
		Version: protocol.SessionLifecycleVersion, Revision: 1,
		SessionID: "session-soak", ThreadID: "thread_external",
		Title: "Streaming soak", Status: protocol.SessionStatusRunning,
		Isolation: "shared", WorkspaceRoot: "/workspace",
		WorkspaceLabel: "workspace", ExecutionTarget: "local",
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC(),
	}}
	application := app.NewRuntime(app.Options{
		EventHistory: 2048, SubscriberBuffer: 128,
		SessionLifecycle: lifecycle, WorkspaceRoot: "/workspace",
	})
	t.Cleanup(func() { _ = application.Close(context.Background()) })
	_, origin, token := runningWebServer(t, application, Capacity{})
	connection := openWebSocket(t, origin, token, 0)
	baselineGoroutines := runtime.NumGoroutine()
	baselineDescriptors, descriptorsAvailable := openDescriptorCount()
	runtime.GC()
	var baselineMemory runtime.MemStats
	runtime.ReadMemStats(&baselineMemory)

	var received atomic.Uint64
	readContext, cancelRead := context.WithCancel(t.Context())
	readDone := make(chan error, 1)
	go func() {
		for {
			var frame eventFrame
			if err := wsjson.Read(readContext, connection, &frame); err != nil {
				readDone <- err
				return
			}
			if frame.Type == "event" && frame.Event != nil {
				received.Add(1)
			}
		}
	}()

	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(duration)
	defer deadline.Stop()
	var published uint64
run:
	for {
		select {
		case <-deadline.C:
			break run
		case err := <-readDone:
			t.Fatalf("stream reader stopped after %d events: %v", published, err)
		case <-ticker.C:
			if err := application.PublishExternal(
				&protocol.OutputDeltaData{Text: "stream"},
			); err != nil {
				t.Fatalf("publish event %d: %v", published+1, err)
			}
			published++
		}
	}
	waitDeadline := time.Now().Add(10 * time.Second)
	for received.Load() < published && time.Now().Before(waitDeadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if published == 0 || received.Load() != published {
		t.Fatalf("published=%d received=%d", published, received.Load())
	}

	cancelRead()
	_ = connection.CloseNow()
	select {
	case <-readDone:
	case <-time.After(5 * time.Second):
		t.Fatal("stream reader did not stop")
	}
	runtime.GC()
	var finalMemory runtime.MemStats
	runtime.ReadMemStats(&finalMemory)
	const maxHeapGrowth = 32 << 20
	if finalMemory.HeapAlloc > baselineMemory.HeapAlloc+maxHeapGrowth {
		t.Fatalf(
			"heap grew from %d to %d bytes",
			baselineMemory.HeapAlloc,
			finalMemory.HeapAlloc,
		)
	}
	if goroutines := runtime.NumGoroutine(); goroutines > baselineGoroutines+8 {
		t.Fatalf(
			"goroutines grew from %d to %d",
			baselineGoroutines,
			goroutines,
		)
	}
	if descriptorsAvailable {
		if descriptors, available := openDescriptorCount(); available &&
			descriptors > baselineDescriptors+8 {
			t.Fatalf(
				"descriptors grew from %d to %d",
				baselineDescriptors,
				descriptors,
			)
		}
	}
}
