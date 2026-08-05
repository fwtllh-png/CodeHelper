package tui

import (
	"strings"
	"testing"
)

func TestWrapAwareLineBreaksURLAtSlash(t *testing.T) {
	url := "https://example.com/very/long/path/to/resource/file.go?query=1&x=2"
	got := wrapAwareLine(url, 32)
	if !strings.Contains(got, "\n") {
		t.Fatalf("expected wrap, got %q", got)
	}
	for _, line := range strings.Split(got, "\n") {
		if displayWidth(line) > 32 {
			t.Fatalf("line too wide (%d): %q", displayWidth(line), line)
		}
	}
	// Prefer break after /
	if !strings.Contains(got, "/\n") && !strings.Contains(got, "path/\n") && !strings.Contains(got, "com/\n") {
		// At least one slash-adjacent break should appear for this URL.
		joined := strings.ReplaceAll(got, "\n", "")
		if joined != url {
			t.Fatalf("wrap corrupted URL: %q", got)
		}
		// soft requirement: some line should end mid-path with slash retained on left or right
		ok := false
		for _, line := range strings.Split(got, "\n") {
			if strings.HasSuffix(line, "/") || strings.HasPrefix(line, "path") || strings.HasPrefix(line, "to/") {
				ok = true
			}
		}
		if !ok {
			t.Fatalf("expected path-aware break, got %q", got)
		}
	}
}

func TestWrapAwareLineBreaksAbsPath(t *testing.T) {
	path := "/Users/bytedance/go/src/code.byted.org/fuweiting.pro/flow/codehelper/internal/host/tui/wrap.go"
	got := wrapAwareLine(path, 40)
	if !strings.Contains(got, "\n") {
		t.Fatalf("expected wrap, got %q", got)
	}
	restored := strings.ReplaceAll(got, "\n", "")
	if restored != path {
		t.Fatalf("path corrupted: %q", got)
	}
}

func TestWrapAwareLineShortUnchanged(t *testing.T) {
	s := "hello world"
	if got := wrapAwareLine(s, 80); got != s {
		t.Fatalf("got %q", got)
	}
}
