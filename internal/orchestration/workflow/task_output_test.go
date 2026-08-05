package workflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
)

func TestTaskRequestRejectsUnsupportedProfileBeforeExecution(t *testing.T) {
	err := workflow.ValidateTaskRequest(workflow.TaskRequest{
		Prompt: "inspect", Profile: "fast",
	})
	if !errors.Is(err, workflow.ErrUnsupportedProfile) {
		t.Fatalf("profile error = %v", err)
	}
	spec := workflow.Spec{
		Goal: "inspect",
		Nodes: []workflow.Node{{
			ID: "task", Kind: workflow.NodeTask,
			Prompt: "inspect", Profile: "fast",
		}},
	}
	if err := spec.Validate(); !errors.Is(err, workflow.ErrInvalidSpec) ||
		!errors.Is(err, workflow.ErrUnsupportedProfile) {
		t.Fatalf("spec profile error = %v", err)
	}
}

func TestTaskOutputValidatesResponseSchema(t *testing.T) {
	request := workflow.TaskRequest{
		Prompt: "count packages",
		Schema: json.RawMessage(`{
			"type":"object",
			"properties":{"packages":{"type":"integer"}},
			"required":["packages"],
			"additionalProperties":false
		}`),
	}
	if err := workflow.ValidateTaskRequest(request); err != nil {
		t.Fatal(err)
	}
	data, err := workflow.ValidateTaskOutput(request, `{"packages":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"packages":1}` {
		t.Fatalf("validated data = %s", data)
	}
	for _, content := range []string{
		`{"packages":"one"}`,
		"```json\n{\"packages\":1}\n```",
		`{"packages":1} trailing`,
	} {
		if _, err := workflow.ValidateTaskOutput(request, content); !errors.Is(
			err,
			workflow.ErrResponseSchema,
		) {
			t.Fatalf("content %q error = %v", content, err)
		}
	}
}

func TestTaskSchemaRejectsExternalReferences(t *testing.T) {
	err := workflow.ValidateTaskRequest(workflow.TaskRequest{
		Prompt: "inspect",
		Schema: json.RawMessage(
			`{"$ref":"https://schemas.example.test/workflow.json"}`,
		),
	})
	if !errors.Is(err, workflow.ErrResponseSchema) ||
		!strings.Contains(err.Error(), "external schema reference") {
		t.Fatalf("external ref error = %v", err)
	}
}

func TestSpecCarriesResponseSchemaToDriver(t *testing.T) {
	driver := &capturingDriver{
		result: workflow.TaskResult{Success: true, Content: `{"packages":1}`},
	}
	schema := json.RawMessage(`{"type":"object"}`)
	run, err := workflow.NewRuntime().Run(t.Context(), workflow.RunOptions{
		Spec: workflow.Spec{
			Goal: "inspect",
			Nodes: []workflow.Node{{
				ID: "count", Kind: workflow.NodeTask,
				Prompt: "count packages", Schema: schema,
			}},
		},
		Driver: driver,
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != workflow.RunCompleted ||
		len(driver.requests) != 1 ||
		string(driver.requests[0].Schema) != string(schema) {
		t.Fatalf("run=%+v requests=%+v", run, driver.requests)
	}
	changed := workflow.Spec{
		Goal: "inspect",
		Nodes: []workflow.Node{{
			ID: "count", Kind: workflow.NodeTask,
			Prompt: "count packages",
			Schema: json.RawMessage(`{"type":"array"}`),
		}},
	}
	if changed.Fingerprint() == (workflow.Spec{
		Goal: "inspect",
		Nodes: []workflow.Node{{
			ID: "count", Kind: workflow.NodeTask,
			Prompt: "count packages", Schema: schema,
		}},
	}).Fingerprint() {
		t.Fatal("response schema change did not change the resume fingerprint")
	}
}

type capturingDriver struct {
	requests []workflow.TaskRequest
	result   workflow.TaskResult
}

func (d *capturingDriver) SpawnTask(
	_ context.Context,
	request workflow.TaskRequest,
) (workflow.TaskResult, error) {
	d.requests = append(d.requests, request)
	return d.result, nil
}

func (*capturingDriver) CancelAll() error { return nil }

func (*capturingDriver) Budget() workflow.BudgetSnapshot {
	return workflow.BudgetSnapshot{}
}

func (*capturingDriver) Progress(workflow.ProgressEvent) error { return nil }
