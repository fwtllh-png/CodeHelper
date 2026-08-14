package contextstore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

// WindowLedger is the durable token baseline for one compaction window.
// Provider-observed input is authoritative; estimates price only later deltas.
type WindowLedger struct {
	ID                       string `json:"id"`
	Number                   uint64 `json:"number"`
	PrefillTokens            uint64 `json:"prefill_tokens,omitempty"`
	PrefillObserved          bool   `json:"prefill_observed,omitempty"`
	FullActiveTokens         uint64 `json:"full_active_tokens,omitempty"`
	ObservedEstimateTokens   uint64 `json:"observed_estimate_tokens,omitempty"`
	ObservedContextDigest    string `json:"observed_context_digest,omitempty"`
	ToolDefinitionTokens     uint64 `json:"tool_definition_tokens,omitempty"`
	LastProviderInputTokens  uint64 `json:"last_provider_input_tokens,omitempty"`
	LastProviderCachedTokens uint64 `json:"last_provider_cached_tokens,omitempty"`
	Digest                   string `json:"digest"`
}

// WindowProjection is the complete accounting used for one provider request.
type WindowProjection struct {
	ID                   string
	Number               uint64
	Observed             bool
	FullActiveTokens     uint64
	PrefillTokens        uint64
	BodyTokens           uint64
	ToolDefinitionTokens uint64
	PendingTokens        uint64
	OutputReserve        uint64
	AutoCompactLimit     uint64
	HardLimit            uint64
	EstimatedTokens      uint64
}

func NewWindowLedger(id string, number uint64) (WindowLedger, error) {
	if id == "" || number == 0 {
		return WindowLedger{}, errors.New("window id and number are required")
	}
	value := WindowLedger{ID: id, Number: number}
	value.seal()
	return value, nil
}

func CloneWindowLedger(value WindowLedger) WindowLedger {
	return value
}

func (w WindowLedger) Valid() bool {
	return w.ID != "" && w.Number != 0 && w.Digest != "" &&
		w.Digest == w.digest()
}

// Prepare establishes an estimated prefill until the first provider usage
// arrives, then projects the latest observed full input plus only the estimate
// delta introduced after that observation.
func (w *WindowLedger) Prepare(
	context *protocol.SampleContextData,
	outputReserve uint64,
	autoCompactLimit uint64,
	hardLimit uint64,
) WindowProjection {
	if context == nil {
		return WindowProjection{}
	}
	if !w.Valid() {
		return WindowProjection{
			FullActiveTokens:     context.EstimatedTokens,
			PrefillTokens:        context.EstimatedTokens,
			ToolDefinitionTokens: context.ToolDefinitionTokens,
			PendingTokens:        context.EstimatedTokens,
			OutputReserve:        outputReserve, AutoCompactLimit: autoCompactLimit,
			HardLimit: hardLimit, EstimatedTokens: context.EstimatedTokens,
		}
	}
	if w.PrefillTokens == 0 {
		w.PrefillTokens = context.EstimatedTokens
	}
	full := context.EstimatedTokens
	pending := context.EstimatedTokens
	if w.ObservedEstimateTokens != 0 && w.LastProviderInputTokens != 0 {
		if context.EstimatedTokens >= w.ObservedEstimateTokens {
			delta := context.EstimatedTokens - w.ObservedEstimateTokens
			full = w.LastProviderInputTokens + delta
			pending = delta
		} else {
			delta := w.ObservedEstimateTokens - context.EstimatedTokens
			full = w.LastProviderInputTokens - min(w.LastProviderInputTokens, delta)
			pending = 0
		}
	}
	w.FullActiveTokens = full
	w.ToolDefinitionTokens = context.ToolDefinitionTokens
	w.seal()
	return WindowProjection{
		ID: w.ID, Number: w.Number, Observed: w.PrefillObserved,
		FullActiveTokens: full, PrefillTokens: w.PrefillTokens,
		BodyTokens:           full - min(full, w.PrefillTokens),
		ToolDefinitionTokens: context.ToolDefinitionTokens,
		PendingTokens:        pending, OutputReserve: outputReserve,
		AutoCompactLimit: autoCompactLimit, HardLimit: hardLimit,
		EstimatedTokens: context.EstimatedTokens,
	}
}

// Observe replaces the estimate-led baseline with provider usage for the
// exact Snapshot identified by context.
func (w *WindowLedger) Observe(
	context protocol.SampleContextData,
	inputTokens uint64,
	cachedTokens uint64,
) {
	if !w.Valid() || inputTokens == 0 || context.EstimatedTokens == 0 {
		return
	}
	if !w.PrefillObserved {
		w.PrefillTokens = inputTokens
		w.PrefillObserved = true
	}
	w.FullActiveTokens = inputTokens
	w.ObservedEstimateTokens = context.EstimatedTokens
	w.ObservedContextDigest = context.ContextDigest
	w.ToolDefinitionTokens = context.ToolDefinitionTokens
	w.LastProviderInputTokens = inputTokens
	w.LastProviderCachedTokens = min(inputTokens, cachedTokens)
	w.seal()
}

func (w WindowLedger) Advance(id string) (WindowLedger, error) {
	return NewWindowLedger(id, w.Number+1)
}

func (w WindowLedger) digest() string {
	w.Digest = ""
	encoded, _ := json.Marshal(w)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func (w *WindowLedger) seal() {
	w.Digest = w.digest()
}
