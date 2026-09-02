package wire

import (
	"context"

	"github.com/fwtllh-png/QCode/internal/adapter/model"
	"github.com/fwtllh-png/QCode/internal/adapter/provider/modelcatalog"
)

type DiscoveredModel = modelcatalog.DiscoveredModel

type ModelProbeResult struct {
	Models       []DiscoveredModel
	Capabilities model.Capabilities
	Warning      string
}

func ProbeModelConnection(
	ctx context.Context,
	providerID, baseURL, modelID, apiKey string,
	credential model.CredentialRef,
) (ModelProbeResult, error) {
	var (
		listed       map[string]any
		capabilities model.Capabilities
		listErr      error
		probeErr     error
	)
	if apiKey != "" {
		listed, listErr = modelcatalog.Discover(
			ctx,
			providerID,
			baseURL,
			apiKey,
		)
		capabilities, probeErr = modelcatalog.ProbeCapabilities(
			ctx,
			baseURL,
			apiKey,
			modelID,
		)
	} else {
		listed, listErr = modelcatalog.List(
			ctx,
			providerID,
			baseURL,
			credential,
		)
		capabilities, probeErr = modelcatalog.ProbeCapabilitiesWithCredential(
			ctx,
			baseURL,
			credential,
			modelID,
		)
	}
	if probeErr != nil {
		return ModelProbeResult{}, probeErr
	}
	result := ModelProbeResult{Capabilities: capabilities}
	result.Models, _ = listed["model_metadata"].([]modelcatalog.DiscoveredModel)
	if listErr != nil {
		result.Warning = listErr.Error()
	}
	return result, nil
}
