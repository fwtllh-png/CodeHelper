package web

import (
	"context"
	"errors"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/platform/workspacequery"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const WorkspaceCatalogVersion = 1

const workspaceHeader = "X-CodeHelper-Workspace-ID"

type WorkspaceDescriptor struct {
	ID           string                   `json:"id"`
	Root         string                   `json:"root"`
	Label        string                   `json:"label"`
	Ready        bool                     `json:"ready"`
	SessionCount int                      `json:"session_count"`
	Problem      string                   `json:"problem,omitempty"`
	Git          *workspacequery.GitState `json:"git,omitempty"`
}

type WorkspaceCatalog struct {
	Version            int                   `json:"version"`
	DefaultWorkspaceID string                `json:"default_workspace_id"`
	Workspaces         []WorkspaceDescriptor `json:"workspaces"`
}

type WorkspaceController interface {
	List(context.Context) (WorkspaceCatalog, error)
	Add(context.Context, string) (WorkspaceDescriptor, error)
}

type WorkspaceAddRequest struct {
	Path string `json:"path"`
}

type WorkspaceAddResult struct {
	Workspace WorkspaceDescriptor `json:"workspace"`
}

func (s *Server) workspaceList(r *http.Request) (any, error) {
	if err := s.decodeRequest(r, &struct{}{}); err != nil {
		return nil, err
	}
	if s.workspaceControl == nil {
		return nil, unavailable("workspace management is unavailable")
	}
	return s.workspaceControl.List(r.Context())
}

func (s *Server) workspaceAdd(r *http.Request) (any, error) {
	if s.workspaceControl == nil {
		return nil, unavailable("workspace management is unavailable")
	}
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		return nil, protocol.NewProblem(
			protocol.CodeInvalidArgument,
			"Idempotency-Key header is required",
			false,
			nil,
		)
	}
	var request WorkspaceAddRequest
	if err := s.decodeRequest(r, &request); err != nil {
		return nil, err
	}
	workspace, err := s.workspaceControl.Add(r.Context(), request.Path)
	if err != nil {
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
	return WorkspaceAddResult{Workspace: workspace}, nil
}

func (s *Server) workspaceCatalog() WorkspaceCatalog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := WorkspaceCatalog{
		Version:            WorkspaceCatalogVersion,
		DefaultWorkspaceID: s.defaultWorkspaceID,
		Workspaces:         make([]WorkspaceDescriptor, 0, len(s.workspaces)),
	}
	for id, dependencies := range s.workspaces {
		result.Workspaces = append(result.Workspaces, WorkspaceDescriptor{
			ID: id, Root: dependencies.WorkspaceRoot,
			Label: filepath.Base(dependencies.WorkspaceRoot), Ready: true,
		})
	}
	sort.Slice(result.Workspaces, func(i, j int) bool {
		return result.Workspaces[i].Root < result.Workspaces[j].Root
	})
	return result
}

func (s *Server) workspaceSnapshot(
	workspaceID string,
) (Dependencies, *protocol.Problem, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	workspaceID = strings.TrimSpace(workspaceID)
	if workspaceID == "" {
		workspaceID = s.defaultWorkspaceID
	}
	if workspaceID == "" {
		return s.dependencies, s.bootProblem, false
	}
	dependencies, found := s.workspaces[workspaceID]
	return dependencies, s.bootProblem, found
}
