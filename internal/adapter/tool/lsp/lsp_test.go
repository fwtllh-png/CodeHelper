package lsp

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
)

func TestDescriptorReflectsInstalledLanguageServers(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	descriptor := (&Tool{}).Descriptor()
	if descriptor.Availability != tool.AvailabilityUnavailable ||
		descriptor.UnavailableReason == "" {
		t.Fatalf("descriptor without servers = %+v", descriptor)
	}

	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "clangd"), []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	descriptor = (&Tool{}).Descriptor()
	if descriptor.Availability != tool.AvailabilityAvailable {
		t.Fatalf("descriptor with clangd = %+v", descriptor)
	}
	files := descriptor.InputSchema["properties"].(map[string]any)["files"].(map[string]any)
	if files["minItems"] != 1 {
		t.Fatalf("files schema = %#v", files)
	}
}
