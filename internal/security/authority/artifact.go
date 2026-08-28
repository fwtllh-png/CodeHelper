package authority

import (
	"errors"
	"path/filepath"
	"sort"
	"strings"
)

const ArtifactManifestVersion = 1

type ArtifactEntry struct {
	Path       string `json:"path"`
	Digest     string `json:"digest"`
	Mode       uint32 `json:"mode"`
	Size       int64  `json:"size"`
	Executable bool   `json:"executable,omitempty"`
}

type ArtifactManifest struct {
	Version                   int             `json:"version"`
	ID                        string          `json:"id"`
	Generation                uint64          `json:"generation"`
	SourceWorkspaceID         string          `json:"source_workspace_id"`
	SourceWorkspaceGeneration uint64          `json:"source_workspace_generation"`
	ProducerOperationDigest   string          `json:"producer_operation_digest"`
	Entries                   []ArtifactEntry `json:"entries"`
	Digest                    string          `json:"digest"`
}

type ArtifactBinding struct {
	ManifestDigest string
	Generation     uint64
	Value          any
}

type AuthorizedProcessGrant struct {
	Operation  ExecutionOperation
	Lease      ExecutionLease
	Validation LeaseValidation
	Artifact   any
}

type FileBinding struct {
	MutationDigest string
	Value          any
}

type AuthorizedFileGrant struct {
	Operation  ExecutionOperation
	Lease      ExecutionLease
	Validation LeaseValidation
	Plan       any
}

func NewArtifactManifest(manifest ArtifactManifest) (ArtifactManifest, error) {
	manifest.Version = ArtifactManifestVersion
	manifest.Digest = ""
	manifest.Entries = append([]ArtifactEntry(nil), manifest.Entries...)
	for index := range manifest.Entries {
		entry := &manifest.Entries[index]
		entry.Path = filepath.Clean(strings.TrimSpace(entry.Path))
	}
	sort.Slice(manifest.Entries, func(i, j int) bool {
		return manifest.Entries[i].Path < manifest.Entries[j].Path
	})
	digest, err := artifactManifestDigest(manifest)
	if err != nil {
		return ArtifactManifest{}, err
	}
	manifest.Digest = digest
	return manifest, manifest.Validate()
}

func (m ArtifactManifest) Validate() error {
	if m.Version != ArtifactManifestVersion ||
		strings.TrimSpace(m.ID) == "" ||
		m.Generation == 0 ||
		!validDigest(m.SourceWorkspaceID) ||
		m.SourceWorkspaceGeneration == 0 ||
		!validDigest(m.ProducerOperationDigest) ||
		!validDigest(m.Digest) ||
		len(m.Entries) == 0 {
		return errors.New("artifact manifest is incomplete")
	}
	previous := ""
	for _, entry := range m.Entries {
		if entry.Path == "" || entry.Path == "." ||
			filepath.IsAbs(entry.Path) ||
			entry.Path == ".." ||
			strings.HasPrefix(entry.Path, ".."+string(filepath.Separator)) ||
			!validDigest(entry.Digest) ||
			entry.Size < 0 {
			return errors.New("artifact manifest entry is invalid")
		}
		if previous != "" && entry.Path <= previous {
			return errors.New("artifact manifest entries are not unique and sorted")
		}
		previous = entry.Path
	}
	expected, err := artifactManifestDigest(m)
	if err != nil {
		return err
	}
	if expected != m.Digest {
		return errors.New("artifact manifest digest mismatch")
	}
	return nil
}

func artifactManifestDigest(manifest ArtifactManifest) (string, error) {
	manifest.Digest = ""
	return digestValue(manifest)
}
