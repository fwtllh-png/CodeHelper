package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type SourceKind string

const (
	SourceBuiltin    SourceKind = "builtin"
	SourceRepository SourceKind = "repository"
	SourceUser       SourceKind = "user"
	SourceManaged    SourceKind = "managed"
)

type SourceRef struct {
	Kind     SourceKind `json:"kind"`
	ID       string     `json:"id"`
	Priority int        `json:"priority"`
	Revision uint64     `json:"revision"`
	Digest   string     `json:"digest"`
}

func (s SourceRef) Validate() error {
	switch s.Kind {
	case SourceBuiltin, SourceRepository, SourceUser, SourceManaged:
	default:
		return errors.New("extension source kind is invalid")
	}
	if strings.TrimSpace(s.ID) == "" || s.Revision == 0 ||
		strings.TrimSpace(s.Digest) == "" {
		return errors.New("extension source identity, revision, and digest are required")
	}
	return nil
}

type Candidate struct {
	ID         string
	Kind       string
	Name       string
	Version    string
	Digest     string
	Generation uint64
	Enabled    bool
	Source     SourceRef
}

func (c Candidate) Validate() error {
	if strings.TrimSpace(c.ID) == "" || strings.TrimSpace(c.Kind) == "" ||
		strings.TrimSpace(c.Name) == "" {
		return errors.New("extension candidate identity is required")
	}
	if len(c.ID) > 256 || len(c.Name) > 256 || len(c.Version) > 128 {
		return errors.New("extension candidate identity exceeds limit")
	}
	if err := c.Source.Validate(); err != nil {
		return err
	}
	return nil
}

type Source interface {
	Reference() SourceRef
	Resolve(context.Context) ([]Candidate, error)
}

type StaticSource struct {
	Ref        SourceRef
	Candidates []Candidate
}

func (s StaticSource) Reference() SourceRef { return s.Ref }

func (s StaticSource) Resolve(ctx context.Context) ([]Candidate, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	values := cloneCandidates(s.Candidates)
	for index := range values {
		values[index].Source = s.Ref
	}
	return values, nil
}

type Resolver struct{}

func (Resolver) Resolve(
	ctx context.Context,
	sources ...Source,
) ([]Candidate, error) {
	type sourceValue struct {
		ref    SourceRef
		source Source
	}
	values := make([]sourceValue, 0, len(sources))
	for _, source := range sources {
		if source == nil {
			continue
		}
		ref := source.Reference()
		if err := ref.Validate(); err != nil {
			return nil, err
		}
		values = append(values, sourceValue{ref: ref, source: source})
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].ref.Priority != values[j].ref.Priority {
			return values[i].ref.Priority > values[j].ref.Priority
		}
		if values[i].ref.Kind != values[j].ref.Kind {
			return values[i].ref.Kind < values[j].ref.Kind
		}
		return values[i].ref.ID < values[j].ref.ID
	})
	selected := make(map[string]Candidate)
	priorities := make(map[string]int)
	for _, value := range values {
		resolved, err := value.source.Resolve(ctx)
		if err != nil {
			return nil, fmt.Errorf("resolve extension source %q: %w", value.ref.ID, err)
		}
		for index := range resolved {
			resolved[index].Source = value.ref
			if err := resolved[index].Validate(); err != nil {
				return nil, fmt.Errorf("extension source %q: %w", value.ref.ID, err)
			}
			currentPriority, exists := priorities[resolved[index].ID]
			if exists && currentPriority == value.ref.Priority {
				return nil, fmt.Errorf(
					"extension %q has ambiguous sources at priority %d",
					resolved[index].ID,
					value.ref.Priority,
				)
			}
			if exists {
				continue
			}
			selected[resolved[index].ID] = resolved[index]
			priorities[resolved[index].ID] = value.ref.Priority
		}
	}
	result := make([]Candidate, 0, len(selected))
	for _, candidate := range selected {
		result = append(result, candidate)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

type PermissionBinding struct {
	ExtensionID string `json:"extension_id"`
	Digest      string `json:"digest"`
}

type Plan struct {
	Revision         uint64              `json:"revision"`
	Digest           string              `json:"digest"`
	PermissionDigest string              `json:"permission_digest"`
	SourceDigest     string              `json:"source_digest"`
	Extensions       []Candidate         `json:"extensions"`
	Permissions      []PermissionBinding `json:"permissions"`
}

func (p Plan) Validate() error {
	if p.Revision == 0 || strings.TrimSpace(p.Digest) == "" ||
		strings.TrimSpace(p.PermissionDigest) == "" ||
		strings.TrimSpace(p.SourceDigest) == "" {
		return errors.New("extension plan identity is incomplete")
	}
	for _, candidate := range p.Extensions {
		if err := candidate.Validate(); err != nil {
			return err
		}
	}
	expected, err := planDigest(p.PermissionDigest, p.SourceDigest, p.Extensions, p.Permissions)
	if err != nil {
		return err
	}
	if p.Digest != expected {
		return errors.New("extension plan digest mismatch")
	}
	return nil
}

func (p Plan) Clone() Plan {
	p.Extensions = cloneCandidates(p.Extensions)
	p.Permissions = append([]PermissionBinding(nil), p.Permissions...)
	return p
}

func (p Plan) WithRevision(revision uint64) (Plan, error) {
	p.Revision = revision
	if err := p.Validate(); err != nil {
		return Plan{}, err
	}
	return p.Clone(), nil
}

type Compiler struct{}

func (Compiler) Compile(
	candidates []Candidate,
	permissionDigest string,
) (Plan, error) {
	permissionDigest = strings.TrimSpace(permissionDigest)
	if permissionDigest == "" {
		return Plan{}, errors.New("extension plan permission digest is required")
	}
	candidates = cloneCandidates(candidates)
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	permissions := make([]PermissionBinding, 0, len(candidates))
	for _, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return Plan{}, err
		}
		permissions = append(permissions, PermissionBinding{
			ExtensionID: candidate.ID, Digest: permissionDigest,
		})
	}
	sourceDigest, err := sourceDigest(candidates)
	if err != nil {
		return Plan{}, err
	}
	digest, err := planDigest(permissionDigest, sourceDigest, candidates, permissions)
	if err != nil {
		return Plan{}, err
	}
	result := Plan{
		Revision: 1, Digest: digest,
		PermissionDigest: permissionDigest, SourceDigest: sourceDigest,
		Extensions: candidates, Permissions: permissions,
	}
	if err := result.Validate(); err != nil {
		return Plan{}, err
	}
	return result, nil
}

func sourceDigest(candidates []Candidate) (string, error) {
	type sourceIdentity struct {
		ExtensionID string    `json:"extension_id"`
		Source      SourceRef `json:"source"`
		Digest      string    `json:"digest"`
		Generation  uint64    `json:"generation"`
		Enabled     bool      `json:"enabled"`
	}
	values := make([]sourceIdentity, 0, len(candidates))
	for _, candidate := range candidates {
		values = append(values, sourceIdentity{
			ExtensionID: candidate.ID, Source: candidate.Source,
			Digest: candidate.Digest, Generation: candidate.Generation,
			Enabled: candidate.Enabled,
		})
	}
	data, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	return sha256Digest(data), nil
}

func planDigest(
	permissionDigest string,
	sourceDigest string,
	candidates []Candidate,
	permissions []PermissionBinding,
) (string, error) {
	canonical := struct {
		PermissionDigest string              `json:"permission_digest"`
		SourceDigest     string              `json:"source_digest"`
		Extensions       []candidateIdentity `json:"extensions"`
		Permissions      []PermissionBinding `json:"permissions"`
	}{
		PermissionDigest: permissionDigest,
		SourceDigest:     sourceDigest,
		Extensions:       canonicalCandidates(candidates),
		Permissions:      append([]PermissionBinding(nil), permissions...),
	}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	return sha256Digest(data), nil
}

type candidateIdentity struct {
	ID         string    `json:"id"`
	Kind       string    `json:"kind"`
	Name       string    `json:"name"`
	Version    string    `json:"version"`
	Digest     string    `json:"digest"`
	Generation uint64    `json:"generation"`
	Enabled    bool      `json:"enabled"`
	Source     SourceRef `json:"source"`
}

func canonicalCandidates(values []Candidate) []candidateIdentity {
	result := make([]candidateIdentity, 0, len(values))
	for _, value := range values {
		result = append(result, candidateIdentity{
			ID: value.ID, Kind: value.Kind, Name: value.Name,
			Version: value.Version, Digest: value.Digest,
			Generation: value.Generation, Enabled: value.Enabled,
			Source: value.Source,
		})
	}
	return result
}

func sha256Digest(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func cloneCandidates(values []Candidate) []Candidate {
	return append([]Candidate(nil), values...)
}
