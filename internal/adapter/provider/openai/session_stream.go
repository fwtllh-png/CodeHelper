package openai

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/fwtllh-png/QCode/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/QCode/internal/adapter/provider/wire"
	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func newResponsesSocketStream(
	ctx context.Context,
	session *responsesSession,
	input []json.RawMessage,
	property string,
	projection provider.ProjectionReceipt,
	requestProjection provider.ProjectionContext,
	idleTimeout time.Duration,
) *responsesSocketStream {
	return &responsesSocketStream{
		ctx: ctx, session: session, input: input, property: property,
		routeDigest: projection.RouteDigest,
		windowID:    requestProjection.WindowID,
		recoveryID:  requestProjection.RecoveryID,
		idleTimeout: idleTimeout, decoder: ResponsesDecoder{
			CaptureState: true, CaptureReplay: true,
		},
	}
}

func sessionMetadata(
	request provider.ModelRequest,
	call providerwire.PreparedCall,
	payload []byte,
	incremental bool,
	projection provider.ProjectionReceipt,
) provider.TransportMetadata {
	metadata := providerwire.MetadataWithProjection(
		call.Body,
		payload,
		incremental,
		projection,
	)
	metadata.LogicalRequestID = request.LogicalRequestID
	metadata.Attempt = request.TransportAttempt
	return metadata
}

func (s *responsesSocketStream) read() error {
	data, err := s.session.conn.Read(s.ctx)
	if err != nil {
		s.session.forceHTTP = true
		s.session.invalidate()
		if errors.Is(err, io.EOF) {
			return protocol.NewProblem(
				protocol.CodeUnavailable,
				"Responses WebSocket ended before completion",
				true,
				io.ErrUnexpectedEOF,
			)
		}
		return err
	}
	events, err := s.decoder.Decode(data)
	if err != nil {
		s.session.forceHTTP = true
		s.session.invalidate()
		return err
	}
	s.queue = append(s.queue, events...)
	return nil
}

func (s *responsesSocketStream) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	s.session.lastUsed = time.Now()
	if s.session.idle != nil {
		s.session.idle.Stop()
	}
	if s.idleTimeout > 0 {
		session := s.session
		session.idle = time.AfterFunc(s.idleTimeout, func() {
			session.mu.Lock()
			defer session.mu.Unlock()
			if time.Since(session.lastUsed) >= s.idleTimeout {
				session.invalidate()
			}
		})
	}
	s.session.mu.Unlock()
	return nil
}

var _ provider.Stream = (*responsesSocketStream)(nil)
