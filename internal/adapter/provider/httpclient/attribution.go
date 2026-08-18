package httpclient

import (
	"context"
	"errors"
	"net"
	"net/http/httptrace"
	"sync/atomic"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type requestPhase struct {
	value atomic.Uint32
}

const (
	requestPhaseConnection uint32 = iota + 1
	requestPhaseTLS
	requestPhaseHeaders
)

func newRequestPhase() *requestPhase {
	value := &requestPhase{}
	value.value.Store(requestPhaseConnection)
	return value
}

func (p *requestPhase) trace() *httptrace.ClientTrace {
	return &httptrace.ClientTrace{
		ConnectStart: func(_, _ string) {
			p.value.Store(requestPhaseConnection)
		},
		TLSHandshakeStart: func() {
			p.value.Store(requestPhaseTLS)
		},
		GotConn: func(httptrace.GotConnInfo) {
			p.value.Store(requestPhaseHeaders)
		},
		GotFirstResponseByte: func() {
			p.value.Store(requestPhaseHeaders)
		},
	}
}

func (p *requestPhase) stage() protocol.FaultStage {
	switch p.value.Load() {
	case requestPhaseTLS:
		return protocol.FaultStageTLSHandshake
	case requestPhaseHeaders:
		return protocol.FaultStageResponseHeaders
	default:
		return protocol.FaultStageConnection
	}
}

func (c *Client) deadlineFor(stage protocol.FaultStage) time.Duration {
	switch stage {
	case protocol.FaultStageTLSHandshake:
		return c.deadlines.TLSHandshake
	case protocol.FaultStageResponseHeaders:
		return c.deadlines.ResponseHeaders
	default:
		return c.deadlines.Connection
	}
}

func providerTransportFault(
	err error,
	request provider.ModelRequest,
	transportRequestID string,
	stage protocol.FaultStage,
	timeout time.Duration,
) *protocol.Problem {
	code := protocol.CodeUnavailable
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &networkError) && networkError.Timeout()) {
		code = protocol.CodeDeadlineExceeded
	}
	metadata := protocol.FaultMetadata{
		Origin: protocol.FaultOriginProvider,
		Stage:  stage,
		OperationID: firstNonEmpty(
			request.LogicalRequestID,
			transportRequestID,
		),
		RetryOwner:  protocol.FaultRetryOwnerEngine,
		ResumeHint:  protocol.FaultResumeRetryStep,
		Disposition: protocol.FaultRetryStep,
		SideEffects: protocol.SideEffectUnchanged,
	}
	if code == protocol.CodeDeadlineExceeded {
		metadata.Deadline = &protocol.DeadlineMetadata{
			Scope:     deadlineScope(stage),
			TimeoutMS: uint64(timeout / time.Millisecond),
		}
	}
	return protocol.NewFault(
		code,
		"provider request failed during "+string(stage),
		retryableTransportError(err),
		metadata,
		err,
	)
}

func operationContextFault(err error, operationID string) error {
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return protocol.NewFault(
		protocol.CodeDeadlineExceeded,
		"provider operation deadline exceeded",
		true,
		protocol.FaultMetadata{
			Origin:      protocol.FaultOriginRuntime,
			Stage:       protocol.FaultStageModelSample,
			OperationID: operationID,
			RetryOwner:  protocol.FaultRetryOwnerHost,
			ResumeHint:  protocol.FaultResumeResumeTurn,
			Disposition: protocol.FaultResumeTurn,
			SideEffects: protocol.SideEffectUnchanged,
			Deadline: &protocol.DeadlineMetadata{
				Scope: protocol.DeadlineHostOperation,
			},
		},
		err,
	)
}

func attributeProviderFault(
	err error,
	request provider.ModelRequest,
	transportRequestID string,
	stage protocol.FaultStage,
) error {
	var problem *protocol.Problem
	if !errors.As(err, &problem) || problem == nil {
		return err
	}
	problem.Fault = &protocol.FaultMetadata{
		Origin: protocol.FaultOriginProvider,
		Stage:  stage,
		OperationID: firstNonEmpty(
			request.LogicalRequestID,
			transportRequestID,
		),
		RetryOwner:  protocol.FaultRetryOwnerEngine,
		ResumeHint:  protocol.FaultResumeRetryStep,
		Disposition: protocol.FaultRetryStep,
		SideEffects: protocol.SideEffectUnchanged,
	}
	return problem
}

func deadlineScope(stage protocol.FaultStage) protocol.DeadlineScope {
	switch stage {
	case protocol.FaultStageTLSHandshake:
		return protocol.DeadlineProviderTLSHandshake
	case protocol.FaultStageResponseHeaders:
		return protocol.DeadlineProviderResponseHeaders
	default:
		return protocol.DeadlineProviderConnection
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func completeTransportMetadata(
	request provider.ModelRequest,
	call providerwire.PreparedCall,
	transportRequestID string,
) provider.TransportMetadata {
	metadata := providerwire.MetadataWithProjection(
		call.Body,
		call.Body,
		false,
		call.Projection,
	)
	metadata.LogicalRequestID = request.LogicalRequestID
	metadata.TransportRequestID = transportRequestID
	metadata.Attempt = request.TransportAttempt
	return metadata
}
