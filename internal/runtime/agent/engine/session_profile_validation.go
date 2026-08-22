package engine

import (
	"errors"
	"fmt"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func (e *Engine) ValidateSessionProfile(profile protocol.SessionProfile) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.validateSessionProfileLocked(profile)
}

func (e *Engine) validateSessionProfileLocked(
	profile protocol.SessionProfile,
) error {
	if e.runningScope() != nil {
		return errors.New("session profile cannot change while a turn is active")
	}
	route := e.options.Routes.Act()
	if profile.Provider != route.ProviderID() || profile.Model != route.Model().ID {
		return errors.New("session profile route is unavailable in this runtime")
	}
	if profile.ReasoningEffort != "" && !route.Model().Capabilities.Reasoning {
		return errors.New("session profile model does not support reasoning effort")
	}
	for _, id := range profile.EnabledToolIDs {
		if _, _, ok := tool.ParseCatalogToolID(id); !ok {
			return fmt.Errorf("session profile tool id %q is invalid", id)
		}
	}
	return nil
}
