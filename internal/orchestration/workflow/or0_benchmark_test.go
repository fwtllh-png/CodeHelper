package workflow_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
)

type baselineDriver struct{}

func (baselineDriver) SpawnTask(
	context.Context,
	workflow.TaskRequest,
) (workflow.TaskResult, error) {
	return workflow.TaskResult{Success: true, Content: "ok"}, nil
}

func (baselineDriver) CancelAll() error                      { return nil }
func (baselineDriver) Budget() workflow.BudgetSnapshot       { return workflow.BudgetSnapshot{} }
func (baselineDriver) Progress(workflow.ProgressEvent) error { return nil }

func BenchmarkOR0WorkflowDAG(b *testing.B) {
	for _, size := range []int{100, 1000} {
		b.Run(fmt.Sprintf("nodes_%d", size), func(b *testing.B) {
			spec := baselineDAG(size)
			b.ReportAllocs()
			b.ResetTimer()
			for index := 0; index < b.N; index++ {
				run, err := workflow.NewRuntime().Run(
					context.Background(),
					workflow.RunOptions{
						ID:   fmt.Sprintf("baseline-%d", index),
						Spec: spec, Driver: baselineDriver{},
					},
				)
				if err != nil {
					b.Fatal(err)
				}
				if len(run.Nodes) != size {
					b.Fatalf("nodes = %d, want %d", len(run.Nodes), size)
				}
			}
		})
	}
}

func baselineDAG(size int) workflow.Spec {
	nodes := make([]workflow.Node, size)
	for index := range nodes {
		id := fmt.Sprintf("node-%04d", index)
		nodes[index] = workflow.Node{
			ID: id, Kind: workflow.NodeTask, Prompt: id,
		}
		if index > 0 {
			nodes[index].Needs = []string{nodes[index-1].ID}
		}
	}
	return workflow.Spec{
		ID: "or0-baseline", Goal: "measure workflow DAG execution",
		Budget: workflow.Budget{MaxSteps: size, MaxParallel: 32},
		Nodes:  nodes,
	}
}
