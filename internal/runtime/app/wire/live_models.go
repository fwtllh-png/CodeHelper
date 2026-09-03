package wire

import (
	"context"

	"github.com/fwtllh-png/QCode/internal/adapter/model"
	providermodels "github.com/fwtllh-png/QCode/internal/adapter/provider/modelcatalog"
)

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
