package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateHashesCompleteDistribution(t *testing.T) {
	dist := t.TempDir()
	writeAsset(t, dist, "index.html", "<main></main>")
	writeAsset(t, dist, "theme-bootstrap.js", "void 0")
	writeAsset(t, dist, "assets/app.js", "void 0")
	writeAsset(t, dist, "assets/app.css", ":root{}")

	content, err := generate(dist)
	if err != nil {
		t.Fatal(err)
	}
	var value manifest
	if err := json.Unmarshal(content, &value); err != nil {
		t.Fatal(err)
	}
	if value.Version != 1 || len(value.Files) != 4 {
		t.Fatalf("manifest = %+v", value)
	}
	for _, file := range value.Files {
		if len(file.SHA256) != 64 || file.Bytes == 0 || file.MediaType == "" {
			t.Fatalf("asset = %+v", file)
		}
	}
}

func TestGenerateRejectsIncompleteDistribution(t *testing.T) {
	dist := t.TempDir()
	writeAsset(t, dist, "index.html", "<main></main>")
	if _, err := generate(dist); err == nil {
		t.Fatal("incomplete distribution was accepted")
	}
}

func writeAsset(t *testing.T, root, name, content string) {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
