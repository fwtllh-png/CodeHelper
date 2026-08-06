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

type Cursor uint64

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
