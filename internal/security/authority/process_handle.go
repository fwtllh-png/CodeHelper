package authority

import (
	"errors"
	"slices"
	"strings"
)

type ProcessAction string

const (
	ProcessObserve ProcessAction = "observe"
	ProcessStdin   ProcessAction = "stdin"
	ProcessSignal  ProcessAction = "signal"
	ProcessWait    ProcessAction = "wait"
	ProcessCancel  ProcessAction = "cancel"
)

type ProcessHandleCapability struct {
	id              string
	nonce           string
	leaseID         string
	sessionID       string
	threadID        string
	turnID          string
	operationDigest string
	processID       string
	generation      uint64
	actions         []ProcessAction
}

type ProcessHandleRequest struct {
	SessionID  string
	ThreadID   string
	TurnID     string
	ProcessID  string
	Generation uint64
	Actions    []ProcessAction
}

type ProcessHandleSnapshot struct {
	ID              string          `json:"id"`
	LeaseID         string          `json:"lease_id"`
	SessionID       string          `json:"session_id"`
	ThreadID        string          `json:"thread_id"`
	TurnID          string          `json:"turn_id"`
	OperationDigest string          `json:"operation_digest"`
	ProcessID       string          `json:"process_id"`
	Generation      uint64          `json:"generation"`
	Actions         []ProcessAction `json:"actions"`
	Terminal        bool            `json:"terminal"`
}

type processHandleRecord struct {
	handle   ProcessHandleCapability
	terminal bool
}

func (a *LeaseAuthority) IssueProcessHandle(
	lease ExecutionLease,
	request ProcessHandleRequest,
) (ProcessHandleCapability, error) {
	if a == nil {
		return ProcessHandleCapability{}, errors.New("lease authority is required")
	}
	actions, err := normalizeProcessActions(request.Actions)
	if err != nil {
		return ProcessHandleCapability{}, err
	}
	if strings.TrimSpace(request.SessionID) == "" ||
		strings.TrimSpace(request.ThreadID) == "" ||
		strings.TrimSpace(request.TurnID) == "" ||
		strings.TrimSpace(request.ProcessID) == "" ||
		request.Generation == 0 {
		return ProcessHandleCapability{}, errors.New("process handle identity is incomplete")
	}
	id, err := randomToken(a.random)
	if err != nil {
		return ProcessHandleCapability{}, err
	}
	nonce, err := randomToken(a.random)
	if err != nil {
		return ProcessHandleCapability{}, err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	record, err := a.authenticRecord(lease)
	if err != nil {
		return ProcessHandleCapability{}, err
	}
	if record.state != LeaseConsumed {
		return ProcessHandleCapability{}, errors.New(
			"process handle requires a consumed execution lease",
		)
	}
	if a.handles[id] != nil {
		return ProcessHandleCapability{}, errors.New(
			"process handle identity collision",
		)
	}
	handle := ProcessHandleCapability{
		id: id, nonce: nonce, leaseID: lease.id,
		sessionID: request.SessionID, threadID: request.ThreadID,
		turnID: request.TurnID, operationDigest: lease.operationDigest,
		processID: request.ProcessID, generation: request.Generation,
		actions: actions,
	}
	a.handles[id] = &processHandleRecord{handle: handle}
	return cloneProcessHandle(handle), nil
}

func (a *LeaseAuthority) ValidateProcessHandle(
	handle ProcessHandleCapability,
	sessionID, threadID, turnID, processID string,
	generation uint64,
	action ProcessAction,
) error {
	if a == nil {
		return errors.New("lease authority is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	record, err := a.authenticHandle(handle)
	if err != nil {
		return err
	}
	if record.terminal {
		return errors.New("process handle is terminal")
	}
	if record.handle.sessionID != sessionID ||
		record.handle.threadID != threadID ||
		record.handle.turnID != turnID ||
		record.handle.processID != processID ||
		record.handle.generation != generation {
		return errors.New("process handle binding changed")
	}
	if !slices.Contains(record.handle.actions, action) {
		return errors.New("process handle action is not authorized")
	}
	return nil
}

func (a *LeaseAuthority) CompleteProcessHandle(
	handle ProcessHandleCapability,
) error {
	if a == nil {
		return errors.New("lease authority is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	record, err := a.authenticHandle(handle)
	if err != nil {
		return err
	}
	record.terminal = true
	return nil
}

func (a *LeaseAuthority) ProcessHandleSnapshot(
	handle ProcessHandleCapability,
) (ProcessHandleSnapshot, error) {
	if a == nil {
		return ProcessHandleSnapshot{}, errors.New("lease authority is required")
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	record, err := a.authenticHandle(handle)
	if err != nil {
		return ProcessHandleSnapshot{}, err
	}
	return ProcessHandleSnapshot{
		ID: record.handle.id, LeaseID: record.handle.leaseID,
		SessionID: record.handle.sessionID, ThreadID: record.handle.threadID,
		TurnID: record.handle.turnID, OperationDigest: record.handle.operationDigest,
		ProcessID: record.handle.processID, Generation: record.handle.generation,
		Actions:  append([]ProcessAction(nil), record.handle.actions...),
		Terminal: record.terminal,
	}, nil
}

func (a *LeaseAuthority) authenticHandle(
	handle ProcessHandleCapability,
) (*processHandleRecord, error) {
	if handle.id == "" || handle.nonce == "" {
		return nil, errors.New("process handle is invalid")
	}
	record := a.handles[handle.id]
	if record == nil || record.handle.nonce != handle.nonce {
		return nil, errors.New("process handle is not authentic")
	}
	return record, nil
}

func normalizeProcessActions(actions []ProcessAction) ([]ProcessAction, error) {
	result := append([]ProcessAction(nil), actions...)
	slices.Sort(result)
	result = slices.Compact(result)
	for _, action := range result {
		switch action {
		case ProcessObserve, ProcessStdin, ProcessSignal, ProcessWait, ProcessCancel:
		default:
			return nil, errors.New("process handle action is invalid")
		}
	}
	if len(result) == 0 {
		return nil, errors.New("process handle requires at least one action")
	}
	return result, nil
}

func cloneProcessHandle(source ProcessHandleCapability) ProcessHandleCapability {
	cloned := source
	cloned.actions = append([]ProcessAction(nil), source.actions...)
	return cloned
}
