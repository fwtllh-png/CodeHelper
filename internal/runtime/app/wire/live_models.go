package wire

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	providermodels "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/modelcatalog"
)

func ListLiveModels(
	ctx context.Context,
	providerID string,
	credential model.CredentialRef,
) (map[string]any, error) {
	return providermodels.List(ctx, providerID, "", credential)
}

func ProbeLiveModel(
	ctx context.Context,
	providerID, baseURL string,
	credential model.CredentialRef,
	modelID string,
) (bool, error) {
	return providermodels.Probe(
		ctx,
		providerID,
		baseURL,
		credential,
		modelID,
	)
}
