// Package orchestrate binds workflow runs to durable fleet + lane records.
package orchestrate

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// RuntimeDriver runs workflow tasks through wire.NewExec + StartTurn.
type RuntimeDriver struct {
	FixturePath string
	DataDir     string
	Workspace   string
	Provider    string
	Model       string
	Timeout     time.Duration

	LastContent string
	Tasks       int
}

func (d *RuntimeDriver) SpawnTask(
	parent context.Context,
	req workflow.TaskRequest,
) (workflow.TaskResult, error) {
	if err := workflow.ValidateTaskRequest(req); err != nil {
		return workflow.TaskResult{Success: false, Error: err.Error()}, err
	}
	d.Tasks++
	timeout := d.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()

	workspace := d.Workspace
	if workspace == "" {
		workspace = "."
	}
	overrides := config.Overrides{Workspace: &workspace}
	if d.DataDir != "" {
		overrides.StateDataDir = &d.DataDir
	}
	if d.Provider != "" {
		overrides.Provider = &d.Provider
	}
	if d.Model != "" {
		overrides.Model = &d.Model
	}
	session, err := wire.NewExec(ctx, wire.ExecOptions{
		FixturePath:     d.FixturePath,
		ConfigOverrides: overrides,
	})
	if err != nil {
		return workflow.TaskResult{Success: false, Error: err.Error()}, err
	}
	defer func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = session.Close(closeCtx)
		closeCancel()
	}()

	content, err := runFixtureTurn(ctx, session.Runtime, req.Prompt)
	if err != nil {
		return workflow.TaskResult{Success: false, Error: err.Error()}, err
	}
	d.LastContent = content
	data, err := workflow.ValidateTaskOutput(req, content)
	if err != nil {
		return workflow.TaskResult{Success: false, Content: content, Error: err.Error()}, err
	}
	return workflow.TaskResult{Success: true, Content: content, Data: data}, nil
}

func (d *RuntimeDriver) CancelAll() error { return nil }

func (d *RuntimeDriver) Budget() workflow.BudgetSnapshot {
	return workflow.BudgetSnapshot{}
}

func (d *RuntimeDriver) Progress(event workflow.ProgressEvent) error { return nil }

func runFixtureTurn(ctx context.Context, runtime *app.Runtime, prompt string) (string, error) {
	threadID, err := protocol.NewThreadID()
	if err != nil {
		return "", err
	}
	turnID, err := protocol.NewTurnID()
	if err != nil {
		return "", err
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return "", err
	}
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID, Prompt: prompt,
	})
	if err != nil {
		return "", err
	}
	events, err := runtime.Events(context.Background(), 0)
	if err != nil {
		return "", err
	}
	if err := runtime.Submit(ctx, operation); err != nil {
		return "", err
	}
	var builder strings.Builder
	for {
		select {
		case <-ctx.Done():
			return builder.String(), ctx.Err()
		case event, ok := <-events:
			if !ok {
				return builder.String(), fmt.Errorf("event stream closed")
			}
			if event.TurnID != turnID {
				continue
			}
			switch event.Kind {
			case protocol.EventOutputDelta:
				if data, _ := event.Data.(*protocol.OutputDeltaData); data != nil {
					builder.WriteString(data.Text)
				}
			case protocol.EventTurnCompleted:
				content := strings.TrimSpace(builder.String())
				if content == "" {
					content = "turn.completed"
				}
				return content, nil
			case protocol.EventTurnFailed:
				data, _ := event.Data.(*protocol.TurnFailedData)
				msg := "turn failed"
				if data != nil {
					msg = data.Message
				}
				return builder.String(), fmt.Errorf("%s", msg)
			case protocol.EventTurnCanceled:
				return builder.String(), fmt.Errorf("turn canceled")
			case protocol.EventOperationRejected:
				data, _ := event.Data.(*protocol.OperationRejectedData)
				msg := "rejected"
				if data != nil {
					msg = data.Message
				}
				return builder.String(), fmt.Errorf("%s", msg)
			}
		}
	}
}

// EncodeInspect is a helper for JSON diagnostics.
func EncodeInspect(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
