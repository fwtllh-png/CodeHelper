package web

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const SetupCatalogVersion = 1

type SetupProvider struct {
	ID             string `json:"id"`
	DisplayName    string `json:"display_name"`
	Protocol       string `json:"protocol"`
	RequiresAPIKey bool   `json:"requires_api_key"`
	Custom         bool   `json:"custom,omitempty"`
}

type SetupCatalog struct {
	Version   int             `json:"version"`
	Providers []SetupProvider `json:"providers"`
}

type SetupRequest struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
	Protocol string `json:"protocol,omitempty"`
}

type SetupResult struct {
	Ready bool `json:"ready"`
}

type SetupOptions struct {
	WorkspaceRoot     string
	WorkspaceIdentity protocol.WorkspaceIdentity
	Catalog           SetupCatalog
	Apply             func(context.Context, SetupRequest) error
}

func (o SetupOptions) validate() error {
	if strings.TrimSpace(o.WorkspaceRoot) == "" {
		return errors.New("setup workspace root is required")
	}
	if err := o.WorkspaceIdentity.Validate(); err != nil {
		return err
	}
	if o.Catalog.Version != SetupCatalogVersion || len(o.Catalog.Providers) == 0 {
		return errors.New("setup provider catalog is required")
	}
	if o.Apply == nil {
		return errors.New("setup apply handler is required")
	}
	return nil
}

func (s *Server) setupApply(r *http.Request, _ Dependencies) (any, error) {
	if s.setup == nil {
		return nil, unavailable("Runtime setup is unavailable")
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		return nil, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"Idempotency-Key header is required",
			false,
			nil,
		)
	}
	var request SetupRequest
	if err := s.decodeRequest(r, &request); err != nil {
		return nil, err
	}
	s.setupMu.Lock()
	defer s.setupMu.Unlock()
	if err := s.setup.Apply(r.Context(), request); err != nil {
		var problem *protocol.Problem
		if errors.As(err, &problem) {
			return nil, err
		}
		return nil, protocol.NewProblem(
			protocol.CodeUnavailable,
			err.Error(),
			true,
			err,
		)
	}
	return SetupResult{Ready: s.ready.Load()}, nil
}
