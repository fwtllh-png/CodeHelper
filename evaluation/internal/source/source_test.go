package source

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestResolveSourceIdentityTracksCommittedAndDirtyContent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is unavailable")
	}
	root := t.TempDir()
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "evaluation@example.invalid")
	runGit(t, root, "config", "user.name", "Evaluation Test")
	write(t, filepath.Join(root, "tracked.txt"), "base\n")
	runGit(t, root, "add", "tracked.txt")
	runGit(t, root, "commit", "-m", "base")

	clean, err := Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if clean.Commit == "" || clean.Dirty || clean.DirtyDigest == "" {
		t.Fatalf("clean identity = %+v", clean)
	}

	write(t, filepath.Join(root, "tracked.txt"), "changed\n")
	write(t, filepath.Join(root, "untracked.txt"), "new\n")
	dirty, err := Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty.Dirty || dirty.DirtyDigest == clean.DirtyDigest {
		t.Fatalf("dirty identity = %+v, clean = %+v", dirty, clean)
	}
	again, err := Resolve(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	if again != dirty {
		t.Fatalf("source identity drifted: first=%+v second=%+v", dirty, again)
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = root
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}
