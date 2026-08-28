package authority

import (
	"strings"
	"testing"
)

func TestArtifactManifestBindsWholeTree(t *testing.T) {
	manifest, err := NewArtifactManifest(ArtifactManifest{
		ID: "artifact-1", Generation: 2,
		SourceWorkspaceID:         strings.Repeat("a", 64),
		SourceWorkspaceGeneration: 3,
		ProducerOperationDigest:   strings.Repeat("b", 64),
		Entries: []ArtifactEntry{
			{Path: "Resources/config.json", Digest: strings.Repeat("c", 64), Size: 20},
			{
				Path: "MacOS/app", Digest: strings.Repeat("d", 64),
				Size: 100, Executable: true, Mode: 0o755,
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Entries[0].Path != "MacOS/app" || manifest.Digest == "" {
		t.Fatalf("manifest = %+v", manifest)
	}
	manifest.Entries[1].Digest = strings.Repeat("e", 64)
	if err := manifest.Validate(); err == nil {
		t.Fatal("mutated bundle resource was accepted")
	}
}

func TestArtifactManifestRejectsTraversal(t *testing.T) {
	_, err := NewArtifactManifest(ArtifactManifest{
		ID: "artifact-1", Generation: 1,
		SourceWorkspaceID:         strings.Repeat("a", 64),
		SourceWorkspaceGeneration: 1,
		ProducerOperationDigest:   strings.Repeat("b", 64),
		Entries: []ArtifactEntry{{
			Path: "../escape", Digest: strings.Repeat("c", 64),
		}},
	})
	if err == nil {
		t.Fatal("artifact traversal was accepted")
	}
}
