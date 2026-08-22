package web

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"testing"
)

func TestAssetsContainBootSurface(t *testing.T) {
	bundle, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"index.html", "theme-bootstrap.js"} {
		if _, err := fs.Stat(bundle, name); err != nil {
			t.Fatalf("embedded asset %q: %v", name, err)
		}
	}
}

func TestEmbeddedAssetManifestMatchesBytes(t *testing.T) {
	bundle, err := Assets()
	if err != nil {
		t.Fatal(err)
	}
	content, err := fs.ReadFile(bundle, "asset-manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version int `json:"version"`
		Files   []struct {
			Path   string `json:"path"`
			SHA256 string `json:"sha256"`
			Bytes  int    `json:"bytes"`
		} `json:"files"`
	}
	if err := json.Unmarshal(content, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.Version != 1 || len(manifest.Files) == 0 {
		t.Fatalf("manifest = %+v", manifest)
	}
	listed := make(map[string]bool, len(manifest.Files))
	for _, file := range manifest.Files {
		data, err := fs.ReadFile(bundle, file.Path)
		if err != nil {
			t.Fatalf("read embedded asset %q: %v", file.Path, err)
		}
		sum := sha256.Sum256(data)
		if len(data) != file.Bytes || hex.EncodeToString(sum[:]) != file.SHA256 {
			t.Fatalf("embedded asset %q does not match its manifest", file.Path)
		}
		listed[file.Path] = true
	}
	if err := fs.WalkDir(bundle, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || path == "asset-manifest.json" {
			return err
		}
		if !listed[path] {
			t.Errorf("embedded asset %q is missing from its manifest", path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
