package egress_test

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/security/egress"
)

func TestGateDeniesUntilGranted(t *testing.T) {
	gate := &egress.Gate{Enforce: true}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(server.Close)

	client := egress.WrapClient(&http.Client{}, gate)
	resp, err := client.Get(server.URL)
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected egress denied before grant")
	}
	if !errors.Is(err, egress.ErrDenied) {
		t.Fatalf("error = %v, want ErrDenied", err)
	}
	if !strings.Contains(err.Error(), "egress denied") {
		t.Fatalf("error = %q, want stable egress denied text", err)
	}
	host, protocol, ok := egress.DeniedTarget(err)
	if !ok || host != "127.0.0.1" || protocol != "http" {
		t.Fatalf("DeniedTarget() = %q, %q, %t", host, protocol, ok)
	}

	if !gate.AllowURL(server.URL) {
		t.Fatal("AllowURL failed")
	}
	resp, err = client.Get(server.URL + "/ping")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("body = %q", body)
	}
}

func TestGateOpenWhenNotEnforcing(t *testing.T) {
	gate := &egress.Gate{Enforce: false}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(server.Close)
	client := egress.WrapClient(&http.Client{}, gate)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestNilGateLeavesClientOpen(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(server.Close)
	client := egress.WrapClient(&http.Client{}, nil)
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
}

func TestRedirectToUngrantedHostIsDenied(t *testing.T) {
	gate := &egress.Gate{Enforce: true}
	gate.Allow("origin.test", "https")

	var sawOther bool
	base := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.URL.Hostname() {
		case "origin.test":
			return &http.Response{
				StatusCode: http.StatusFound,
				Header:     http.Header{"Location": []string{"https://other.test/path"}},
				Body:       http.NoBody,
				Request:    req,
			}, nil
		case "other.test":
			sawOther = true
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       http.NoBody,
				Request:    req,
			}, nil
		default:
			t.Fatalf("unexpected host %q", req.URL.Hostname())
			return nil, nil
		}
	})
	client := &http.Client{Transport: gate.RoundTripper(base)}
	resp, err := client.Get("https://origin.test/")
	if err == nil {
		resp.Body.Close()
		t.Fatal("expected redirect target to be denied")
	}
	if !errors.Is(err, egress.ErrDenied) {
		t.Fatalf("error = %v, want ErrDenied", err)
	}
	if sawOther {
		t.Fatal("RoundTrip reached the ungranted host")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }
