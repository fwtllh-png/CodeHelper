package compatibility_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/compatibility"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestManifestMatchesProtocolAndExtension(t *testing.T) {
	manifest, err := compatibility.Load()
	if err != nil {
		t.Fatal(err)
	}
	if manifest.OperationSchemaVersion != protocol.Version {
		t.Fatalf(
			"operation schema version = %d, protocol = %d",
			manifest.OperationSchemaVersion,
			protocol.Version,
		)
	}
	data, err := os.ReadFile(filepath.Join(
		"..", "..", "extensions", "vscode", "package.json",
	))
	if err != nil {
		t.Fatal(err)
	}
	var extension struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &extension); err != nil {
		t.Fatal(err)
	}
	if extension.Version != manifest.ExtensionVersion {
		t.Fatalf(
			"extension version = %q, compatibility = %q",
			extension.Version,
			manifest.ExtensionVersion,
		)
	}
}

func TestManifestIncludesBuildTarget(t *testing.T) {
	manifest := compatibility.MustLoad()
	for _, target := range manifest.Targets {
		if target.OS == runtime.GOOS && target.Arch == runtime.GOARCH {
			return
		}
	}
	t.Fatalf("build target %s/%s is absent", runtime.GOOS, runtime.GOARCH)
}
