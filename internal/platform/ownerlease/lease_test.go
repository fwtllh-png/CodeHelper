package ownerlease

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireRejectsConcurrentOwnerAndAllowsTakeoverAfterClose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lock")
	first, err := Acquire(path, Metadata{OwnerKind: "web", PublicURL: "http://127.0.0.1:1/"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = Acquire(path, Metadata{OwnerKind: "tui"})
	var held *HeldError
	if !errors.As(err, &held) {
		t.Fatalf("second Acquire error = %v, want HeldError", err)
	}
	if held.Metadata.PublicURL != "http://127.0.0.1:1/" {
		t.Fatalf("held metadata = %#v", held.Metadata)
	}

	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := Acquire(path, Metadata{OwnerKind: "tui"})
	if err != nil {
		t.Fatalf("takeover after close: %v", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestLeaseKeepsLockByteOutsideMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lock")
	lease, err := Acquire(path, Metadata{OwnerKind: "web"})
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) <= metadataOffset || data[0] != 0 {
		t.Fatalf("lease metadata does not preserve the lock byte: %q", data)
	}
	var metadata Metadata
	if err := json.Unmarshal(data[metadataOffset:], &metadata); err != nil {
		t.Fatalf("decode lease metadata: %v", err)
	}
	if metadata.OwnerKind != "web" {
		t.Fatalf("metadata = %#v", metadata)
	}
}

func TestAcquireRejectsSymbolicLink(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "target")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "owner.lock")
	if err := os.Symlink(target, path); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if _, err := Acquire(path, Metadata{OwnerKind: "web"}); err == nil {
		t.Fatal("expected symbolic link rejection")
	}
}

func TestPathCanonicalizesDataDirectoryAliases(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "state")
	if err := os.Mkdir(physical, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "state-alias")
	if err := os.Symlink(physical, alias); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if got, want := Path(alias, "workspace"), Path(physical, "workspace"); got != want {
		t.Fatalf("lease path through alias = %q, want %q", got, want)
	}
}

func TestPathCanonicalizesAliasBeforeMissingSuffix(t *testing.T) {
	root := t.TempDir()
	physical := filepath.Join(root, "state")
	if err := os.Mkdir(physical, 0o700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "state-alias")
	if err := os.Symlink(physical, alias); err != nil {
		t.Skipf("symbolic links unavailable: %v", err)
	}
	if got, want := Path(
		filepath.Join(alias, "future"),
		"workspace",
	), Path(
		filepath.Join(physical, "future"),
		"workspace",
	); got != want {
		t.Fatalf("lease path through missing alias suffix = %q, want %q", got, want)
	}
}
