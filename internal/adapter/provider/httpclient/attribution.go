package httpclient

import (
	"github.com/fwtllh-png/CodeHelper/internal/adapter/provider"
	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
)

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
