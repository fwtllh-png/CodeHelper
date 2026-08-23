package engine

import (
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	agentcontext "github.com/fwtllh-png/CodeHelper/internal/runtime/agent/context"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const defaultWriteReservationEntities = 8

func (e *Engine) ContextAdmission(
	additions []agentcontext.TruthEntity,
	resolvedIDs []string,
) agentcontext.AdmissionDecision {
	current := e.buildTruthCapsule(e.buildCompactSummary(nil))
	return (agentcontext.ContextAdmissionController{
		Policy: e.options.Context.TruthRetention,
	}).Decide(current, agentcontext.AdmissionRequest{
		BaseContextRevision:  e.sessionRevision,
		RouteCompatibility:   current.CompatibilityHash,
		AddedMandatory:       additions,
		ResolvedMandatoryIDs: resolvedIDs,
	})
}

func (e *Engine) admitToolBatch(calls []provider.ToolCall) error {
	var reservations []agentcontext.TruthEntity
	for _, call := range calls {
		if call.Name == "request_user_input" {
			entity := agentcontext.NewTruthEntity(
				agentcontext.EntityPendingInput,
				call.ID,
				"pending user input",
				"runtime.input",
			)
			entity.Turn = e.turn
			reservations = append(reservations, entity)
		}
		_, descriptor, _, err := e.options.Tools.ResolveBound(
			call.Name,
			tool.BindingForCall(call),
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
			entity := agentcontext.NewTruthEntity(
				agentcontext.EntityChange,
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
