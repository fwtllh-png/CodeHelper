package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

type ExtensionControlKind string

const (
	ExtensionControlSkill ExtensionControlKind = "skill"
	ExtensionControlAll   ExtensionControlKind = "all"
)

type ExtensionControlAction string

const (
	ExtensionActionList        ExtensionControlAction = "list"
	ExtensionActionDetail      ExtensionControlAction = "detail"
	ExtensionActionHealth      ExtensionControlAction = "health"
	ExtensionActionPermissions ExtensionControlAction = "permissions"
	ExtensionActionReceipts    ExtensionControlAction = "receipts"
	ExtensionActionEnable      ExtensionControlAction = "enable"
	ExtensionActionDisable     ExtensionControlAction = "disable"
	ExtensionActionRevoke      ExtensionControlAction = "revoke"
	ExtensionActionLint        ExtensionControlAction = "lint"
	ExtensionActionLock        ExtensionControlAction = "lock"
	ExtensionActionVerify      ExtensionControlAction = "verify"
)

type ExtensionControlOperation struct {
	Version   int                    `json:"version"`
	ID        string                 `json:"id"`
	Kind      ExtensionControlKind   `json:"kind"`
	Action    ExtensionControlAction `json:"action"`
	Name      string                 `json:"name,omitempty"`
	CreatedAt time.Time              `json:"created_at"`
}

type ExtensionCapabilityProjection struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Enabled          bool   `json:"enabled"`
	SourceDigest     string `json:"source_digest,omitempty"`
	PermissionDigest string `json:"permission_digest,omitempty"`
	AuthorityToken   string `json:"authority_token,omitempty"`
}

type ExtensionProjection struct {
	Kind         ExtensionControlKind            `json:"kind"`
	Name         string                          `json:"name"`
	Version      string                          `json:"version,omitempty"`
	Source       string                          `json:"source,omitempty"`
	Publisher    string                          `json:"publisher,omitempty"`
	Trust        string                          `json:"trust,omitempty"`
	Digest       string                          `json:"digest,omitempty"`
	Generation   uint64                          `json:"generation,omitempty"`
	Enabled      bool                            `json:"enabled"`
	Health       string                          `json:"health"`
	Permissions  []string                        `json:"permissions,omitempty"`
	Capabilities []ExtensionCapabilityProjection `json:"capabilities,omitempty"`
	LastAction   string                          `json:"last_action,omitempty"`
	ChangedAt    *time.Time                      `json:"changed_at,omitempty"`
}

type ExtensionControlReceipt struct {
	OperationID string                 `json:"operation_id"`
	Action      ExtensionControlAction `json:"action"`
	Kind        ExtensionControlKind   `json:"kind"`
	Name        string                 `json:"name,omitempty"`
	Status      string                 `json:"status"`
	Digest      string                 `json:"digest"`
	Revision    uint64                 `json:"revision"`
	OccurredAt  time.Time              `json:"occurred_at"`
}

type ExtensionControlMetrics struct {
	Operations      uint64 `json:"operations"`
	Committed       uint64 `json:"committed"`
	Failed          uint64 `json:"failed"`
	Duplicates      uint64 `json:"duplicates"`
	Revokes         uint64 `json:"revokes"`
	SubscriberDrops uint64 `json:"subscriber_drops"`
}

type ExtensionControlTrace struct {
	OperationID string                 `json:"operation_id"`
	Action      ExtensionControlAction `json:"action"`
	Kind        ExtensionControlKind   `json:"kind"`
	Status      string                 `json:"status"`
	DurationMS  uint64                 `json:"duration_ms"`
	OccurredAt  time.Time              `json:"occurred_at"`
}

type ExtensionControlAlert struct {
	Code       string    `json:"code"`
	Count      uint64    `json:"count"`
	LastSeenAt time.Time `json:"last_seen_at"`
}

type ExtensionControlDiagnostics struct {
	Metrics ExtensionControlMetrics `json:"metrics"`
	Traces  []ExtensionControlTrace `json:"traces,omitempty"`
	Alerts  []ExtensionControlAlert `json:"alerts,omitempty"`
}

type ExtensionControlResult struct {
	OperationID string                       `json:"operation_id"`
	Duplicate   bool                         `json:"duplicate,omitempty"`
	Revision    uint64                       `json:"revision"`
	Extensions  []ExtensionProjection        `json:"extensions,omitempty"`
	Detail      json.RawMessage              `json:"detail,omitempty"`
	Receipt     *ExtensionControlReceipt     `json:"receipt,omitempty"`
	Receipts    []ExtensionControlReceipt    `json:"receipts,omitempty"`
	Diagnostics *ExtensionControlDiagnostics `json:"diagnostics,omitempty"`
}

type ExtensionControlEvent struct {
	Sequence    uint64                  `json:"sequence"`
	OperationID string                  `json:"operation_id"`
	Action      ExtensionControlAction  `json:"action"`
	Projection  *ExtensionProjection    `json:"projection,omitempty"`
	Receipt     ExtensionControlReceipt `json:"receipt"`
	OccurredAt  time.Time               `json:"occurred_at"`
}

var extensionControlIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)

func NewExtensionControlOperation(
	kind ExtensionControlKind,
	action ExtensionControlAction,
) (ExtensionControlOperation, error) {
	id, err := newID("extop")
	if err != nil {
		return ExtensionControlOperation{}, err
	}
	operation := ExtensionControlOperation{
		Version: Version, ID: id, Kind: kind, Action: action,
		CreatedAt: time.Now().UTC(),
	}
	return operation, nil
}

func (o ExtensionControlOperation) Validate() error {
	if o.Version != Version || !extensionControlIDPattern.MatchString(o.ID) ||
		o.CreatedAt.IsZero() {
		return errors.New("extension operation requires version, id, and created_at")
	}
	switch o.Kind {
	case ExtensionControlSkill, ExtensionControlAll:
	default:
		return errors.New("extension operation kind is invalid")
	}
	switch o.Action {
	case ExtensionActionList, ExtensionActionDetail, ExtensionActionHealth,
		ExtensionActionPermissions, ExtensionActionReceipts:
	case ExtensionActionEnable, ExtensionActionDisable, ExtensionActionRevoke,
		ExtensionActionLint:
		if strings.TrimSpace(o.Name) == "" {
			return errors.New("extension mutation requires name")
		}
	case ExtensionActionLock, ExtensionActionVerify:
	default:
		return fmt.Errorf("extension operation action %q is invalid", o.Action)
	}
	if o.Kind == ExtensionControlAll && !o.Query() {
		return errors.New("extension mutation cannot target kind all")
	}
	return nil
}

func (o ExtensionControlOperation) Query() bool {
	switch o.Action {
	case ExtensionActionList, ExtensionActionDetail, ExtensionActionHealth,
		ExtensionActionPermissions, ExtensionActionReceipts:
		return true
	default:
		return false
	}
}

func ReduceExtensionControlEvents(
	events []ExtensionControlEvent,
) ([]ExtensionProjection, error) {
	state := make(map[string]ExtensionProjection)
	var previous uint64
	for _, event := range events {
		if event.Sequence == 0 || event.Sequence <= previous ||
			event.OperationID == "" || event.Receipt.OperationID != event.OperationID ||
			event.Receipt.Revision != event.Sequence {
			return nil, errors.New("extension control event sequence is invalid")
		}
		previous = event.Sequence
		if event.Projection == nil {
			continue
		}
		key := string(event.Projection.Kind) + "\x00" + event.Projection.Name
		state[key] = *event.Projection
	}
	result := make([]ExtensionProjection, 0, len(state))
	for _, value := range state {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].Name < result[j].Name
	})
	return result, nil
}
