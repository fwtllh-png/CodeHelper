// Package orchestrate binds workflow runs to durable fleet + lane records.
package orchestrate

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/orchestration/workflow"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// RuntimeDriver runs workflow tasks through wire.NewExec + StartTurn.
type RuntimeDriver struct {
	mu          sync.Mutex
	FixturePath string
	DataDir     string
	Workspace   string
	Provider    string
	Model       string
	Timeout     time.Duration

	LastContent     string
	Tasks           int
	SpentTokens     uint64
	SpentCostMicros uint64
}

func (d *RuntimeDriver) SpawnTask(
	parent context.Context,
	req workflow.TaskRequest,
) (workflow.TaskResult, error) {
	if err := workflow.ValidateTaskRequest(req); err != nil {
		return workflow.TaskResult{Success: false, Error: err.Error()}, err
	}
	d.mu.Lock()
	d.Tasks++
	d.mu.Unlock()
	ctx := parent
	cancel := func() {}
	if d.Timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, d.Timeout)
	}
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

	content, usage, permissionDigests, err := runFixtureTurn(
		ctx,
		session.Runtime,
		req.Prompt,
	)
	if err != nil {
		return workflow.TaskResult{
			Success: false, Error: err.Error(), Usage: usage,
			PermissionDigests: permissionDigests,
		}, err
	}
	d.mu.Lock()
	d.SpentTokens += usage.Tokens
	if usage.CostKnown {
		d.SpentCostMicros += usage.CostMicros
	}
	d.LastContent = content
	d.mu.Unlock()
	data, err := workflow.ValidateTaskOutput(req, content)
	if err != nil {
		return workflow.TaskResult{
			Success: false, Content: content, Error: err.Error(), Usage: usage,
			PermissionDigests: permissionDigests,
		}, err
	}
	return workflow.TaskResult{
		Success: true, Content: content, Data: data, Usage: usage,
		PermissionDigests: permissionDigests,
	}, nil
}

func (d *RuntimeDriver) CancelAll() error { return nil }

func (d *RuntimeDriver) Budget() workflow.BudgetSnapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return workflow.BudgetSnapshot{
		SpentTokens:  d.SpentTokens,
		SpentCostUSD: float64(d.SpentCostMicros) / 1e6,
	}
}

func (d *RuntimeDriver) Progress(event workflow.ProgressEvent) error { return nil }

func runFixtureTurn(
	ctx context.Context,
	runtime *app.Runtime,
	prompt string,
) (string, workflow.WorkUsage, []string, error) {
	var usage workflow.WorkUsage
	var permissionDigests []string
	threadID, err := protocol.NewThreadID()
	if err != nil {
		return "", usage, nil, err
	}
	turnID, err := protocol.NewTurnID()
	if err != nil {
		return "", usage, nil, err
	}
	itemID, err := protocol.NewItemID()
	if err != nil {
		return "", usage, nil, err
	}
	operation, err := protocol.NewOperation(&protocol.StartTurnPayload{
		ThreadID: threadID, TurnID: turnID, ItemID: itemID, Prompt: prompt,
	})
	if err != nil {
		return "", usage, nil, err
	}
	events, err := runtime.Events(context.Background(), 0)
	if err != nil {
		return "", usage, nil, err
	}
	if err := runtime.Submit(ctx, operation); err != nil {
		return "", usage, nil, err
	}
	var builder strings.Builder
	for {
		select {
		case <-ctx.Done():
			return builder.String(), usage, permissionDigests, ctx.Err()
		case event, ok := <-events:
			if !ok {
				return builder.String(), usage, permissionDigests,
					fmt.Errorf("event stream closed")
			}
			if event.TurnID != turnID {
				continue
			}
			switch event.Kind {
			case protocol.EventOutputDelta:
				if data, _ := event.Data.(*protocol.OutputDeltaData); data != nil {
					builder.WriteString(data.Text)
				}
			case protocol.EventExecutionReceipt:
				if data, _ := event.Data.(*protocol.ExecutionReceiptData); data != nil {
					usage = workflow.WorkUsage{
						Tokens:    data.InputTokens + data.OutputTokens,
						CostKnown: data.CostKnown,
					}
					if data.CostKnown {
						usage.CostMicros = data.CostMicrounits
					}
					permissionDigests = append(
						[]string(nil),
						data.PermissionDigests...,
					)
				}
			case protocol.EventTurnCompleted:
				content := strings.TrimSpace(builder.String())
				if content == "" {
					content = "turn.completed"
				}
				return content, usage, permissionDigests, nil
			case protocol.EventTurnFailed:
				data, _ := event.Data.(*protocol.TurnFailedData)
				msg := "turn failed"
				if data != nil {
					msg = data.Message
				}
				return builder.String(), usage, permissionDigests, fmt.Errorf("%s", msg)
			case protocol.EventTurnCanceled:
				return builder.String(), usage, permissionDigests, fmt.Errorf("turn canceled")
			case protocol.EventOperationRejected:
				data, _ := event.Data.(*protocol.OperationRejectedData)
				msg := "rejected"
				if data != nil {
					msg = data.Message
				}
				return builder.String(), usage, permissionDigests, fmt.Errorf("%s", msg)
			}
		}
	}
}
