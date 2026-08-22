package protocol

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

type OperationID string

type EventID string

type ThreadID string

type TurnID string

type ItemID string

type RunID string

type NodeID string

type AttemptID string

type EffectID string

type Cursor uint64

func NewSessionID() (string, error) {
	return newID("session")
}

func NewWorkspaceID() (string, error) {
	return newID("workspace")
}

func NewThreadID() (ThreadID, error) {
	value, err := newID("thread")
	return ThreadID(value), err
}

func NewTurnID() (TurnID, error) {
	value, err := newID("turn")
	return TurnID(value), err
}

func NewItemID() (ItemID, error) {
	value, err := newID("item")
	return ItemID(value), err
}

func NewRunID() (RunID, error) {
	value, err := newID("run")
	return RunID(value), err
}

func NewNodeID() (NodeID, error) {
	value, err := newID("node")
	return NodeID(value), err
}

func NewAttemptID() (AttemptID, error) {
	value, err := newID("attempt")
	return AttemptID(value), err
}

func NewEffectID() (EffectID, error) {
	value, err := newID("effect")
	return EffectID(value), err
}

func NewWindowID() (string, error) {
	return newID("window")
}

func newID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}
	return prefix + "_" + hex.EncodeToString(value[:]), nil
}
