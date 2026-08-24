package engine

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerfixture "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/fixture"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	observationjournal "github.com/fwtllh-png/CodeHelper/internal/observability/journal"
	"github.com/fwtllh-png/CodeHelper/internal/observability/observation"
	observationrouter "github.com/fwtllh-png/CodeHelper/internal/observability/router"
	"github.com/fwtllh-png/CodeHelper/internal/observability/trace"
	sqlitestate "github.com/fwtllh-png/CodeHelper/internal/persist/state/sqlite"
	turnstate "github.com/fwtllh-png/CodeHelper/internal/persist/state/turnstate"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/turnkernel"
)

var observationBenchmarkTurnID atomic.Uint64

func BenchmarkSO2TurnObservationOverhead(b *testing.B) {
	route := testRoute(b)
	b.Run("disabled", func(b *testing.B) {
		database, coordinator := benchmarkCoordinator(b)
		for index := range 5 {
			runBenchmarkTurn(
				b, route, nil, coordinator,
				benchmarkTurnID("disabled-warm", uint64(index)),
			)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			runBenchmarkTurn(
				b, route, nil, coordinator,
				benchmarkTurnID("disabled", 0),
			)
		}
		_ = database
	})
	database, coordinator := benchmarkCoordinator(b)
	writer, err := observationjournal.Open(
		b.TempDir(),
		observationjournal.Options{MaxSegmentBytes: 256 << 20},
	)
	if err != nil {
		b.Fatal(err)
	}
	observations, err := observationrouter.New(
		writer,
		nil,
		observationrouter.Options{},
	)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = observations.Close(context.Background()) })
	b.Run("enabled", func(b *testing.B) {
		for index := range 5 {
			runBenchmarkTurn(
				b, route, observations, coordinator,
				benchmarkTurnID("enabled-warm", uint64(index)),
			)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for b.Loop() {
			runBenchmarkTurn(
				b, route, observations, coordinator,
				benchmarkTurnID("enabled", 0),
			)
		}
	})
	_ = database
}

func benchmarkTurnID(prefix string, salt uint64) string {
	return fmt.Sprintf(
		"%s-%d-%d",
		prefix,
		observationBenchmarkTurnID.Add(1),
		salt,
	)
}

func runBenchmarkTurn(
	b *testing.B,
	route model.ReadyRoute,
	observations observation.Recorder,
	coordinator turnkernel.CoordinatorRuntime,
	turnID string,
) {
	b.Helper()
	engine, err := New(Options{ProviderConfig: ProviderConfig{Provider: observationBenchmarkProvider{},
		Route: route,

		MaxOutputTokens: 128}, ToolConfig: ToolConfig{Tools: tool.NewRegistry(nil, nil)}, TelemetryConfig: TelemetryConfig{Observability: trace.Runtime{
		Recorder:  observations,
		RuntimeID: "runtime-benchmark",
	}}, LifecycleConfig: LifecycleConfig{SessionID: "session-benchmark",
		TurnCoordinatorRuntime: coordinator},
	})
	if err != nil {
		b.Fatal(err)
	}
	result, err := engine.RunForTurn(
		context.Background(),
		turnID,
		"answer",
		nil,
	)
	if err != nil {
		b.Fatal(err)
	}
	if result.State != Completed {
		b.Fatalf("state = %s", result.State)
	}
}

func benchmarkCoordinator(
	b *testing.B,
) (*sqlitestate.Store, turnkernel.CoordinatorRuntime) {
	b.Helper()
	database, err := sqlitestate.Open(
		context.Background(),
		filepath.Join(b.TempDir(), "state-v1.db"),
		sqlitestate.Options{},
	)
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = database.Close() })
	runtime, err := turnkernel.NewStoreCoordinatorRuntime(
		turnstate.NewSQLiteRepository(database),
	)
	if err != nil {
		b.Fatal(err)
	}
	return database, runtime
}

type observationBenchmarkProvider struct{}

func (observationBenchmarkProvider) Stream(
	context.Context,
	provider.ModelRequest,
) (provider.Stream, error) {
	return &delayedObservationBenchmarkStream{
		Stream: &providerfixture.SliceStream{Events: []provider.StreamEvent{
			{Type: provider.EventMessageStart},
			{Type: provider.EventTextDelta, Text: "done"},
			{Type: provider.EventMessageStop},
		}},
	}, nil
}

type delayedObservationBenchmarkStream struct {
	provider.Stream
	once sync.Once
}

func (s *delayedObservationBenchmarkStream) Recv() (provider.StreamEvent, error) {
	s.once.Do(func() { time.Sleep(5 * time.Millisecond) })
	return s.Stream.Recv()
}
