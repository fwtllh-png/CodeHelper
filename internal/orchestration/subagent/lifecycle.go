package subagent

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (m *Manager) transitionLocked(
	agent *Agent,
	status Status,
	turnID, message, actor, reason string,
	result *Result,
) error {
	if agent == nil {
		return errors.New("agent is required")
	}
	if !CanTransition(agent.Status, status) {
		return fmt.Errorf(
			"agent %s cannot transition from %s to %s",
			agent.ID, agent.Status, status,
		)
	}
	reserve := !occupiesSlot(agent.Status) && occupiesSlot(status)
	ledger := m.ledgers[agent.SessionID]
	if reserve && m.active[agent.SessionID] >= m.budget.MaxParallel {
		return errors.New("subagent concurrency budget exhausted")
	}
	var reserveTokens, reserveMicros uint64
	if reserve {
		tokenLimit := agent.Budget.MaxTokens
		if tokenLimit > 0 {
			if agent.SpentTokens >= tokenLimit {
				return errors.New("subagent token lifecycle budget exhausted")
			}
			reserveTokens = tokenLimit - agent.SpentTokens
		}
		microLimit := uint64(agent.Budget.MaxCostUSD * 1e6)
		if microLimit > 0 {
			if agent.SpentMicros >= microLimit {
				return errors.New("subagent cost lifecycle budget exhausted")
			}
			reserveMicros = microLimit - agent.SpentMicros
		}
		if m.budget.MaxTokens > 0 &&
			ledger.SpentTokens+ledger.ReservedTokens+reserveTokens >
				m.budget.MaxTokens {
			return errors.New("subagent token reservation exceeds tree budget")
		}
		maxMicros := uint64(m.budget.MaxCostUSD * 1e6)
		if maxMicros > 0 &&
			ledger.SpentMicros+ledger.ReservedMicros+reserveMicros >
				maxMicros {
			return errors.New("subagent cost reservation exceeds tree budget")
		}
	}
	release := occupiesSlot(agent.Status) && !occupiesSlot(status)
	transition := GraphTransition{
		SessionID: agent.SessionID,
		AgentID:   agent.ID, Path: agent.Path,
		ExpectedRevision: agent.Revision, Status: status,
		TurnID: turnID, Message: message,
		OperationID: fmt.Sprintf("agent:%s:%d", agent.ID, agent.Revision+1),
		Actor:       actor, Reason: reason, Result: result,
		ReserveTokens: reserveTokens, ReserveMicros: reserveMicros,
		ReleaseBudget: release,
		CreatedAt:     time.Now().UTC(),
	}
	if result != nil {
		stored := *result
		transition.Result = &stored
		transition.ReasonCode = stored.ReasonCode
		if stored.ReasonCode != "" && strings.TrimSpace(message) == "" {
			transition.Message = stored.ReasonCode
		}
		envelope := completionEnvelope(agent, stored)
		body, err := json.Marshal(envelope)
		if err != nil {
			return err
		}
		target := BindSessionParent(agent.Parent)
		completion, err := m.mailbox.Prepare(Message{
			SessionID: agent.SessionID,
			From:      agent.ID, To: target, Kind: MessageCompletion,
			PayloadRef: envelope.ResultRef, Body: body,
		})
		if err != nil {
			return err
		}
		transition.Completion = &envelope
		transition.CompletionMessage = &completion
	}
	if err := m.recordTransitionLocked(transition); err != nil {
		return err
	}
	previous := agent.Status
	agent.Status = status
	agent.Revision++
	agent.LastMessage = message
	if turnID != "" {
		agent.TurnID = turnID
	}
	if result != nil {
		stored := *transition.Result
		agent.Result = &stored
		if integrationBaseline(agent, stored) {
			agent.IntegrationResult = &stored
		}
		ledger.SpentTokens += stored.Usage.Tokens()
		ledger.SpentMicros += stored.Usage.CostMicrounits
		agent.SpentTokens += stored.Usage.Tokens()
		agent.SpentMicros += stored.Usage.CostMicrounits
		m.mailbox.Accept(*transition.CompletionMessage)
	}
	switch {
	case !occupiesSlot(previous) && occupiesSlot(status):
		m.active[agent.SessionID]++
		ledger.ReservedSlots++
		agent.ReservedTokens = reserveTokens
		agent.ReservedMicros = reserveMicros
		ledger.ReservedTokens += reserveTokens
		ledger.ReservedMicros += reserveMicros
	case occupiesSlot(previous) && !occupiesSlot(status):
		m.active[agent.SessionID]--
		ledger.ReservedSlots--
		ledger.ReservedTokens -= agent.ReservedTokens
		ledger.ReservedMicros -= agent.ReservedMicros
		agent.ReservedTokens = 0
		agent.ReservedMicros = 0
	}
	m.ledgers[agent.SessionID] = ledger
	m.wait.Broadcast()
	return nil
}

func occupiesSlot(status Status) bool {
	switch status {
	case StatusStarting, StatusRunning, StatusWaiting:
		return true
	default:
		return false
	}
}

func OccupiesSlot(status Status) bool { return occupiesSlot(status) }

func completionEnvelope(agent *Agent, result Result) CompletionEnvelope {
	paths := result.WritePaths()
	if len(paths) > 64 {
		paths = paths[:64]
	}
	return CompletionEnvelope{
		AgentPath: agent.Path, Status: result.Status,
		Summary: firstLine(strings.TrimSpace(result.Summary), 400),
		ResultRef: fmt.Sprintf(
			"agent-result://%s/%s/%d",
			agent.SessionID, agent.ID, agent.Revision+1,
		),
		ReceiptRef:   result.Context.Digest,
		ChangedPaths: append([]string(nil), paths...),
		Verification: result.Verification, Usage: result.Usage,
		IntegrationReady: integrationReady(agent, result),
	}
}

func integrationReady(agent *Agent, result Result) bool {
	return integrationBaseline(agent, result) &&
		len(result.Unresolved) == 0
}

func integrationBaseline(agent *Agent, result Result) bool {
	return agent != nil &&
		agent.Isolated &&
		result.Status == StatusCompleted &&
		len(result.WritePaths()) > 0
}

func promptWithMessages(prompt string, messages []Message) string {
	if len(messages) == 0 {
		return prompt
	}
	var out strings.Builder
	if strings.TrimSpace(prompt) != "" {
		out.WriteString(prompt)
		out.WriteString("\n\n")
	}
	out.WriteString("<agent_mailbox>\n")
	for _, message := range messages {
		encoded, _ := json.Marshal(message)
		out.Write(encoded)
		out.WriteByte('\n')
	}
	out.WriteString("</agent_mailbox>")
	return out.String()
}
