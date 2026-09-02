package web

import (
	"net/http"
	"strings"

	"github.com/fwtllh-png/QCode/internal/runtime/protocol"
)

func (s *Server) workspaceGitSwitch(
	r *http.Request,
	dependencies Dependencies,
) (any, error) {
	if dependencies.Workspace == nil {
		return nil, unavailable("workspace Git operations are unavailable")
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		return nil, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"Idempotency-Key header is required",
			false,
			nil,
		)
	}
	var request struct {
		Branch string `json:"branch"`
	}
	if err := s.decodeRequest(r, &request); err != nil {
		return nil, err
	}
	activity := dependencies.Runtime.Snapshot(r.Context())
	if activity.ActiveTurns != 0 || activity.PendingOperations != 0 {
		return nil, protocol.NewProblem(
			protocol.CodeConflict,
			"finish active work before switching branches",
			true,
			nil,
		)
	}
	state, err := dependencies.Workspace.SwitchBranch(
		r.Context(),
		request.Branch,
	)
	if err != nil {
		return nil, protocol.NewProblem(
			protocol.CodeConflict,
			err.Error(),
			false,
			err,
		)
	}
	return state, nil
}
