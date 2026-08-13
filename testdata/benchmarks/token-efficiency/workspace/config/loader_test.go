package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadNestedIncludesAndStableDeduplication(t *testing.T) {
	root := t.TempDir()
	writeFixture(t, filepath.Join(root, "root.conf"), "entry alpha\ninclude child/one.conf\nentry omega\n")
	writeFixture(t, filepath.Join(root, "child", "one.conf"), "entry beta\ninclude two.conf\nentry alpha\n")
	writeFixture(t, filepath.Join(root, "child", "two.conf"), "entry gamma\n")

	got, err := Load(filepath.Join(root, "root.conf"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"alpha", "beta", "gamma", "omega"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Load() = %v, want %v", got, want)
	}
}

func writeFixture(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
