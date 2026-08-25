package web

import (
	"bufio"
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRunContextExposesOnlyWebStartupFlags(t *testing.T) {
	for _, legacyCommand := range []string{"web", "exec", "tui", "doctor"} {
		var stdout, stderr bytes.Buffer
		if code := RunContext(
			t.Context(),
			[]string{legacyCommand},
			&stdout,
			&stderr,
		); code != 2 {
			t.Fatalf("%s exit = %d, stderr = %q", legacyCommand, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "unexpected arguments") {
			t.Fatalf("%s stderr = %q", legacyCommand, stderr.String())
		}
	}

	var stdout, stderr bytes.Buffer
	if code := RunContext(
		t.Context(),
		[]string{"--version"},
		&stdout,
		&stderr,
	); code != 0 {
		t.Fatalf("--version exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "codehelper") {
		t.Fatalf("--version output = %q", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	if code := RunContext(
		t.Context(),
		[]string{"--help"},
		&stdout,
		&stderr,
	); code != 0 {
		t.Fatalf("--help exit = %d, stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Run the local CodeHelper Web workspace") {
		t.Fatalf("--help output = %q", stdout.String())
	}
}

func TestRunContextStartsAndStopsWebHost(t *testing.T) {
	workspace := t.TempDir()
	if err := exec.Command("git", "-C", workspace, "init", "-q").Run(); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs(
		filepath.Join("..", "..", "..", "testdata", "providers", "openai"),
	)
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "state")
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	outputReader, outputWriter := io.Pipe()
	exitCode := make(chan int, 1)
	go func() {
		exitCode <- RunContext(ctx, []string{
			"--workspace", workspace,
			"--data-dir", dataDir,
			"--provider-fixture", fixture,
			"--provider", "openai",
			"--model", "fixture-model",
			"--port", "0",
			"--no-open",
		}, outputWriter, io.Discard)
		_ = outputWriter.Close()
	}()

	url := waitForReadyURL(t, outputReader)
	response, err := http.Get(strings.TrimSuffix(url, "/") + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d", response.StatusCode)
	}

	cancel()
	select {
	case code := <-exitCode:
		if code != 0 {
			t.Fatalf("exit = %d", code)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Web host did not stop")
	}
}

func waitForReadyURL(t *testing.T, reader io.Reader) string {
	t.Helper()
	result := make(chan struct {
		url string
		err error
	}, 1)
	go func() {
		scanner := bufio.NewScanner(reader)
		for scanner.Scan() {
			const prefix = "CodeHelper Runtime Ready: "
			if url, ok := strings.CutPrefix(scanner.Text(), prefix); ok {
				result <- struct {
					url string
					err error
				}{url: url}
				return
			}
		}
		result <- struct {
			url string
			err error
		}{err: scanner.Err()}
	}()
	select {
	case ready := <-result:
		if ready.err != nil {
			t.Fatalf("read Web readiness: %v", ready.err)
		}
		if ready.url == "" {
			t.Fatal("Web host exited before readiness")
		}
		return ready.url
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for Web readiness")
		return ""
	}
}

func TestProbeWebReadinessRequiresTrustedReadyEndpoint(t *testing.T) {
	ready := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/healthz" {
			t.Errorf("probe path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"version":1,"status":"ready"}`))
	}))
	defer ready.Close()
	if err := probeWebReadiness(t.Context(), ready.URL+"/untrusted"); err != nil {
		t.Fatalf("ready owner rejected: %v", err)
	}

	notReady := httptest.NewServer(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		http.Error(writer, `{"version":1,"status":"initializing"}`, http.StatusServiceUnavailable)
	}))
	defer notReady.Close()
	if err := probeWebReadiness(t.Context(), notReady.URL); err == nil {
		t.Fatal("unready owner accepted")
	}

	redirect := httptest.NewServer(http.RedirectHandler(ready.URL, http.StatusFound))
	defer redirect.Close()
	if err := probeWebReadiness(t.Context(), redirect.URL); err == nil ||
		!strings.Contains(err.Error(), "redirects are forbidden") {
		t.Fatalf("redirect probe error = %v", err)
	}

	if err := probeWebReadiness(context.Background(), "http://localhost:1234/"); err == nil {
		t.Fatal("non-canonical loopback owner URL accepted")
	}
}
