package web

import "testing"

func TestBrowserDriverStatusNeverClaimsRealEngine(t *testing.T) {
	t.Setenv("CODEHELPER_BROWSER_FIXTURE", "1")
	if got := BrowserDriverStatus(); got != BrowserDriverFakeFixture {
		t.Fatalf("fixture driver status = %q want %q", got, BrowserDriverFakeFixture)
	}

	t.Setenv("CODEHELPER_BROWSER_FIXTURE", "")
	t.Setenv("CODEHELPER_BROWSER_BINARY", "definitely-not-installed-codehelper-browser")
	if got := BrowserDriverStatus(); got != BrowserDriverUnavailable {
		t.Fatalf("missing binary driver status = %q want %q", got, BrowserDriverUnavailable)
	}
}
