package cli

import (
	"context"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/app/wire"
)

func listLiveModels(
	providerID string,
	credentialOverride model.CredentialRef,
) (map[string]any, error) {
	return wire.ListLiveModels(
		context.Background(),
		providerID,
		credentialOverride,
	)
}
