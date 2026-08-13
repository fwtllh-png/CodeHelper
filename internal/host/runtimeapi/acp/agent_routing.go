package acp

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/orchestration/subagent"
	taskstate "github.com/fwtllh-png/CodeHelper/internal/orchestration/task"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (s *Server) bindPendingRequest(
	binding sessionBinding,
	payload protocol.OperationPayload,
) error {
	switch value := payload.(type) {
	case *protocol.ApprovalDecisionPayload:
		pending, ok := s.dependencies.Runtime.PendingApproval(value.RequestID)
		if !ok {
			return nil
		}
		owner := s.sessionForThread(pending.ThreadID)
		if source := pending.Data.Source; source != nil && source.SessionID != "" {
			owner = source.SessionID
		}
		if err := requireRequestOwner("approval", value.RequestID, owner, binding.ID); err != nil {
			return err
		}
		s.bindThread(pending.ThreadID, binding.ID)
		value.ThreadID, value.TurnID = pending.ThreadID, pending.TurnID
	case *protocol.InputReplyPayload:
		pending, ok := s.dependencies.Runtime.PendingInput(value.RequestID)
		if !ok {
			return nil
		}
		owner := s.sessionForThread(pending.ThreadID)
		if err := requireRequestOwner("input", value.RequestID, owner, binding.ID); err != nil {
			return err
		}
		value.ThreadID, value.TurnID = pending.ThreadID, pending.TurnID
	}
	return nil
}

func requireRequestOwner(kind, requestID, owner, sessionID string) error {
	if owner == "" {
		return fmt.Errorf("%s request %s has no Session owner", kind, requestID)
	}
	if owner != sessionID {
		return fmt.Errorf(
			"%s request %s belongs to session %s",
			kind, requestID, owner,
		)
	}
	return nil
}

func (s *Server) bind(binding sessionBinding) {
	var agentThreads []protocol.ThreadID
	if s.dependencies.Agents != nil {
		agents := s.dependencies.Agents.List(subagent.ListFilter{
			SessionID: binding.ID, IncludeClosed: true,
		})
		agentThreads = make([]protocol.ThreadID, 0, len(agents))
		for _, agent := range agents {
			if agent.ThreadID != "" {
				agentThreads = append(
					agentThreads,
					protocol.ThreadID(agent.ThreadID),
				)
			}
		}
	}
	s.mu.Lock()
	s.sessions[binding.ID] = binding
	s.threads[binding.ThreadID] = binding.ID
	for _, threadID := range agentThreads {
		s.threads[threadID] = binding.ID
	}
	s.mu.Unlock()
}

func (s *Server) bindAgentThread(event protocol.Event) {
	s.mu.Lock()
	s.bindAgentThreadLocked(event)
	s.mu.Unlock()
}

func (s *Server) bindAgentThreadLocked(event protocol.Event) {
	workspace, sessionID := s.workspaceEventIdentity(event)
	if workspace == "" || sessionID == "" {
		return
	}
	normalized, err := taskstate.NormalizeWorkspaceRoot(workspace)
	if err != nil || normalized != s.options.WorkspaceRoot {
		return
	}
	agentID := agentEventID(event.Data)
	if agentID == "" {
		return
	}
	threadID := protocol.ThreadID(subagent.ThreadIDFor(agentID))
	if owner := s.threads[threadID]; owner == "" || owner == sessionID {
		s.threads[threadID] = sessionID
	}
}

func agentEventID(data protocol.EventData) string {
	switch value := data.(type) {
	case *protocol.AgentSpawnedData:
		return value.AgentID
	case *protocol.ApprovalRequiredData:
		if value.Source != nil {
			return value.Source.AgentID
		}
	case *protocol.ApprovalResolvedData:
		if value.Source != nil {
			return value.Source.AgentID
		}
	}
	return ""
}
