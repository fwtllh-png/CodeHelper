// Package sessiondelta defines the durable Agent session snapshot contract.
package sessiondelta

import (
	"encoding/json"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool/interact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/contextstore"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/evidence"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/workingset"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/durablecodec"
)

type Compaction struct {
	Count int `json:"count"`
}

type State struct {
	Turn         uint64                     `json:"turn,omitempty"`
	HistoryTurns map[string]uint64          `json:"history_turns,omitempty"`
	WorkingSet   workingset.Delta           `json:"working_set"`
	Evidence     evidence.Delta             `json:"evidence"`
	Failures     compact.FailureDelta       `json:"failures"`
	Compaction   Compaction                 `json:"compaction"`
	Plan         *interact.Plan             `json:"plan,omitempty"`
	World        contextstore.WorldBaseline `json:"world,omitempty"`
	Window       contextstore.WindowLedger  `json:"window"`
}

type Delta struct {
	TurnID         string                     `json:"turn_id"`
	Turn           uint64                     `json:"turn,omitempty"`
	BaseRevision   uint64                     `json:"base_revision"`
	History        []provider.Message         `json:"history"`
	MessageTurns   []uint64                   `json:"message_turns,omitempty"`
	HistoryTurns   map[string]uint64          `json:"history_turns,omitempty"`
	Usage          provider.Usage             `json:"usage"`
	CostMicrounits uint64                     `json:"cost_microunits"`
	WorkingSet     workingset.Delta           `json:"working_set"`
	Evidence       evidence.Delta             `json:"evidence"`
	Failures       compact.FailureDelta       `json:"failures"`
	Compaction     Compaction                 `json:"compaction"`
	Plan           *interact.Plan             `json:"plan,omitempty"`
	World          contextstore.WorldBaseline `json:"world,omitempty"`
	Window         contextstore.WindowLedger  `json:"window"`
	Digest         string                     `json:"digest"`
}

type deltaJSON Delta

func (d Delta) MarshalJSON() ([]byte, error) {
	raw, err := json.Marshal(deltaJSON(d))
	if err != nil {
		return nil, err
	}
	return durablecodec.EncodeJSON(raw, d.BaseRevision)
}

func (d *Delta) UnmarshalJSON(raw []byte) error {
	decoded, err := durablecodec.DecodeJSON(raw)
	if err != nil {
		return err
	}
	return json.Unmarshal(decoded, (*deltaJSON)(d))
}
