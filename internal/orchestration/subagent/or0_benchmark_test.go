package subagent_test

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
)

func BenchmarkOR0ResidentAgents(b *testing.B) {
	root := b.TempDir()
	for _, count := range []int{8, 32} {
		b.Run(fmt.Sprintf("agents_%d", count), func(b *testing.B) {
			b.ReportAllocs()
			for iteration := 0; iteration < b.N; iteration++ {
				manager, err := subagent.Open(subagent.Options{
					Root: filepath.Join(
						root,
						fmt.Sprintf("%d-%d", count, iteration),
					),

					Gate: &fakeGate{}, Budget: subagent.Budget{
						MaxDepth: 5, MaxParallel: count,
						MaxResident: count, MaxTotal: count,
					}, Workspace: root, SessionID: "or0-baseline",
				})
				if err != nil {
					b.Fatal(err)
				}
				for index := 0; index < count; index++ {
					if _, err := manager.Spawn(
						"",
						subagent.RoleExplore,
						fmt.Sprintf("inspect-%d", index),
					); err != nil {
						b.Fatal(err)
					}
				}
			}
		})
	}
}
