package web

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/QCode/internal/buildinfo"
	"github.com/fwtllh-png/QCode/internal/platform/ownerlease"
)

func TestWebOwnerBuildIncludesBuildDate(t *testing.T) {
	got := webOwnerBuild(buildinfo.Info{
		Version: "dev", Commit: "commit", BuildDate: "date",
	})
	if got != "dev+commit@date" {
		t.Fatalf("build identity = %q", got)
	}
}

func TestWebOwnerKindSeparatesDevelopmentOwners(t *testing.T) {
	if got := webOwnerKind(true); got != "web-dev" {
		t.Fatalf("development owner kind = %q", got)
	}
	if got := webOwnerKind(false); got != "web" {
		t.Fatalf("installed owner kind = %q", got)
	}
}

func TestReplaceWebOwnerWaitsForHeldLease(t *testing.T) {
	path := filepath.Join(t.TempDir(), "owner.lock")
	first, err := ownerlease.Acquire(path, ownerlease.Metadata{
		PID: 41, OwnerKind: "web", Build: "dev+old",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })

	_, err = ownerlease.Acquire(path, ownerlease.Metadata{
		OwnerKind: "web", Build: "new",
	})
	var held *ownerlease.HeldError
	if !errors.As(err, &held) {
		t.Fatalf("held owner error = %v", err)
	}

	interrupted := 0
	replacement, err := replaceWebOwner(
		context.Background(),
		path,
		ownerlease.Metadata{OwnerKind: "web-dev", Build: "new"},
		held.Metadata,
		func(pid int) error {
			interrupted = pid
			return first.Close()
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if interrupted != 41 {
		t.Fatalf("interrupted pid = %d", interrupted)
	}

	_, err = ownerlease.Acquire(path, ownerlease.Metadata{OwnerKind: "web"})
	var replacementHeld *ownerlease.HeldError
	if !errors.As(err, &replacementHeld) {
		t.Fatalf("replacement lease error = %v", err)
	}
	if replacementHeld.Metadata.Build != "new" {
		t.Fatalf("replacement metadata = %+v", replacementHeld.Metadata)
	}
}

func TestReplaceWebOwnerRejectsOtherOwnerKinds(t *testing.T) {
	for _, held := range []ownerlease.Metadata{
		{PID: 41, OwnerKind: "worker"},
		{PID: 42, OwnerKind: "web", Build: "v1.0.0+commit"},
	} {
		_, err := replaceWebOwner(
			context.Background(),
			filepath.Join(t.TempDir(), "owner.lock"),
			ownerlease.Metadata{OwnerKind: "web-dev"},
			held,
			func(int) error {
				t.Fatal("unexpected interrupt")
				return nil
			},
		)
		if err == nil {
			t.Fatalf("expected owner rejection for %+v", held)
		}
	}
}
