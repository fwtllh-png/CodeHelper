package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestChromeBrowserNavigatesClicksAndFills(t *testing.T) {
	binary := findChromeBinary()
	if binary == "" {
		t.Skip("Chromium browser is unavailable")
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = writer.Write([]byte(`<!doctype html><html><body>
			<input id="query"><button id="go" onclick="document.body.dataset.clicked='yes'">Go</button>
		</body></html>`))
	}))
	defer server.Close()

	runtime := newChromeBrowser(binary)
	defer runtime.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	snapshot, err := runtime.Navigate(ctx, server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, `id="query"`) {
		t.Fatalf("navigate snapshot = %q", snapshot)
	}
	if err := runtime.Fill(ctx, "#query", "hello"); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Click(ctx, "#go"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = runtime.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(snapshot, `data-clicked="yes"`) ||
		!strings.Contains(snapshot, `value="hello"`) {
		t.Fatalf("interactive snapshot = %q", snapshot)
	}
}
