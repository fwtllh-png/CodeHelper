package acp

import (
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func validateDefaultProfile(
	profile protocol.SessionProfile,
	options Options,
) error {
	if err := profile.Validate(); err != nil {
		return fmt.Errorf("ACP default session profile: %w", err)
	}
	if profile.Provider != options.ProviderID ||
		profile.Model != options.ModelID {
		return errors.New(
			"ACP default session profile does not match the selected route",
		)
	}
	capabilities := protocol.SessionProfileCapabilities{
		Provider: options.ProviderID, Model: options.ModelID,
		ModelCapabilities: options.ModelCapabilities,
	}
	if err := capabilities.Validate(profile); err != nil {
		return fmt.Errorf("ACP model capabilities: %w", err)
	}
	return nil
}
