package web

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBrowserDriverStatusReportsConfiguredEngine(t *testing.T) {
	t.Setenv("CODEHELPER_BROWSER_FIXTURE", "1")
	if got := BrowserDriverStatus(); got != BrowserDriverFakeFixture {
		t.Fatalf("fixture driver status = %q want %q", got, BrowserDriverFakeFixture)
	}

	t.Setenv("CODEHELPER_BROWSER_FIXTURE", "")
	t.Setenv("CODEHELPER_BROWSER_BINARY", "definitely-not-installed-codehelper-browser")
	if got := BrowserDriverStatus(); got != BrowserDriverUnavailable {
		t.Fatalf("missing binary driver status = %q want %q", got, BrowserDriverUnavailable)
	}

	binary := filepath.Join(t.TempDir(), "chrome")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEHELPER_BROWSER_BINARY", binary)
	if got := BrowserDriverStatus(); got != BrowserDriverRealChrome {
		t.Fatalf("configured browser status = %q want %q", got, BrowserDriverRealChrome)
	}
}
