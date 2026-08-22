package workspacequery

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestServiceBrowsesSearchesAndReadsWithinWorkspace(t *testing.T) {
	root := t.TempDir()
	initRepository(t, root)
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "src", "main.go"),
		[]byte("package main\n\nfunc hello() {}\n"),
		0o600,
	); err != nil {
		t.Fatal(err)
	}
	service, err := New(root, queryTestBackend{})
	if err != nil {
		t.Fatal(err)
	}
	browse, err := service.Browse(t.Context(), ".", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(browse.Entries) != 1 ||
		browse.Entries[0].Path != "src" ||
		browse.Entries[0].Kind != "directory" {
		t.Fatalf("browse = %+v", browse)
	}
	search, err := service.Search(t.Context(), "hello", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(search.Matches) != 1 ||
		search.Matches[0].Path != "src/main.go" ||
		search.Matches[0].Line != 3 {
		t.Fatalf("search = %+v", search)
	}
	resource, err := service.Resource(t.Context(), "src/main.go")
	if err != nil {
		t.Fatal(err)
	}
	if resource.Content != "package main\n\nfunc hello() {}\n" ||
		resource.Digest == "" {
		t.Fatalf("resource = %+v", resource)
	}
}

func TestServiceRejectsEscapesAndSkippedPaths(t *testing.T) {
	root := t.TempDir()
	initRepository(t, root)
	service, err := New(root, queryTestBackend{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Resource(t.Context(), "../secret"); err == nil {
		t.Fatal("expected traversal rejection")
	}
	if _, err := service.Resource(t.Context(), "node_modules/secret"); err == nil {
		t.Fatal("expected skipped path rejection")
	}
}

func TestServiceDoesNotExposeGitIgnoredFiles(t *testing.T) {
	root := t.TempDir()
	initRepository(t, root)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte(".env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=hidden\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("git", "add", "-f", ".env")
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git add ignored file: %v: %s", err, output)
	}
	service, err := New(root, queryTestBackend{})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Search(t.Context(), "hidden", 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Matches) != 0 {
		t.Fatalf("ignored search matches = %+v", result.Matches)
	}
	if _, err := service.Resource(t.Context(), ".env"); err == nil {
		t.Fatal("expected ignored resource rejection")
	}
}

func TestServiceReadsOnlyEnumeratedSupportedImages(t *testing.T) {
	root := t.TempDir()
	initRepository(t, root)
	png := append([]byte("\x89PNG\r\n\x1a\n"), []byte("fixture")...)
	if err := os.WriteFile(filepath.Join(root, "diagram.png"), png, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "fake.png"), []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	service, err := New(root, queryTestBackend{})
	if err != nil {
		t.Fatal(err)
	}
	image, err := service.Image(t.Context(), "diagram.png")
	if err != nil {
		t.Fatal(err)
	}
	if image.Path != "diagram.png" ||
		image.MediaType != "image/png" ||
		image.Digest == "" ||
		string(image.Data) != string(png) {
		t.Fatalf("image = %+v", image)
	}
	if _, err := service.Image(t.Context(), "fake.png"); err == nil {
		t.Fatal("text file with an image suffix was accepted")
	}
	if _, err := service.Image(t.Context(), "../diagram.png"); err == nil {
		t.Fatal("image traversal was accepted")
	}
}

func initRepository(t *testing.T, root string) {
	t.Helper()
	command := exec.Command("git", "init", "-q")
	command.Dir = root
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "HOME="+root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, output)
	}
}

type queryTestBackend struct{}

func (queryTestBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "test", Backend: "passthrough",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (queryTestBackend) Prepare(
	_ context.Context,
	command sandbox.Command,
) (sandbox.Command, error) {
	return command, nil
}
