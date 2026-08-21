package engine

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/agent/compact"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const defaultWriteReservationEntities = 8

func (e *Engine) ContextAdmission(
	additions []compact.TruthEntity,
	resolvedIDs []string,
) compact.AdmissionDecision {
	current := e.buildTruthCapsule(e.buildCompactSummary(nil))
	return (compact.ContextAdmissionController{
		Policy: e.options.Context.TruthRetention,
	}).Decide(current, compact.AdmissionRequest{
		BaseContextRevision:  e.sessionRevision,
		RouteCompatibility:   current.CompatibilityHash,
		AddedMandatory:       additions,
		ResolvedMandatoryIDs: resolvedIDs,
	})
}

func (e *Engine) admitToolBatch(calls []provider.ToolCall) error {
	var reservations []compact.TruthEntity
	for _, call := range calls {
		if call.Name == "request_user_input" {
			entity := compact.NewTruthEntity(
				compact.EntityPendingInput,
				call.ID,
				"pending user input",
				"runtime.input",
			)
			entity.Turn = e.turn
			reservations = append(reservations, entity)
		}
		_, descriptor, _, err := e.options.Tools.ResolveBound(
			call.Name,
			bindingForCall(call),
		)
		if err != nil || descriptor.AccessMode != tool.AccessWrite {
			continue
		}
		count := max(
			len(descriptor.ResourceResolver.Templates),
			defaultWriteReservationEntities,
		)
		for index := 0; index < count; index++ {
			key := fmt.Sprintf("reservation:%s:%d", call.ID, index)
			entity := compact.NewTruthEntity(
				compact.EntityChange,
				key,
				"reserved workspace change for "+call.Name,
				"runtime.evidence",
			)
			entity.Turn = e.turn
			reservations = append(reservations, entity)
		}
	}
	if len(reservations) == 0 {
		return nil
	}
	decision := e.ContextAdmission(reservations, nil)
	if decision.Allowed {
		return nil
	}
	return protocol.NewProblem(
		protocol.CodeResourceExhausted,
		"context admission rejected write reservation: "+decision.Reason,
		false,
		nil,
	)
}
