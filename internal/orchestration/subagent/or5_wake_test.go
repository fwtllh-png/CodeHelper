package subagent_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
)

func TestParentCompletionWakeP95(t *testing.T) {
	const samples = 64
	manager, err := subagent.Open(subagent.Options{
		Root: t.TempDir(), Gate: &fakeGate{}, Budget: subagent.Budget{
			MaxParallel: 1, MaxResident: 1, MaxTotal: samples,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	latencies := make([]time.Duration, 0, samples)
	for index := 0; index < samples; index++ {
		agent, err := manager.Spawn("", subagent.RoleExplore, "wake")
		if err != nil {
			t.Fatal(err)
		}
		ready := make(chan struct{})
		done := make(chan error, 1)
		go func(agentID string) {
			close(ready)
			_, waitErr := manager.Wait(
				context.Background(),
				[]string{agentID},
				time.Second,
			)
			done <- waitErr
		}(agent.ID)
		<-ready
		time.Sleep(time.Millisecond)
		started := time.Now()
		if err := manager.Complete(agent.ID, "done"); err != nil {
			t.Fatal(err)
		}
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		latencies = append(latencies, time.Since(started))
	}
	sort.Slice(latencies, func(left, right int) bool {
		return latencies[left] < latencies[right]
	})
	p95 := latencies[(samples*95+99)/100-1]
	if p95 > 50*time.Millisecond {
		t.Fatalf("parent completion wake p95 = %s, want <= 50ms", p95)
	}
	t.Logf("parent completion wake p95 = %s", p95)
}
