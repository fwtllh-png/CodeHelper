package rlm

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestSubQueryBridgeRequiresCapabilityAndBoundsRequests(t *testing.T) {
	var calls atomic.Int32
	bridge, err := startSubQueryBridge(
		FuncSubQuery(func(
			_ context.Context,
			prompt string,
			slice string,
		) (string, error) {
			calls.Add(1)
			return prompt + slice, nil
		}),
		NewGovernor(Limits{}),
		1,
	)
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	if len(bridge.token) < 40 {
		t.Fatalf("bridge token length = %d", len(bridge.token))
	}
	if bridge.server.ReadHeaderTimeout != 2*time.Second ||
		bridge.server.ReadTimeout != 5*time.Second ||
		bridge.server.IdleTimeout != 5*time.Second ||
		bridge.server.WriteTimeout != 605*time.Second ||
		bridge.server.MaxHeaderBytes != 8<<10 {
		t.Fatalf("bridge timeouts = %+v", bridge.server)
	}

	unauthorized := postSubQuery(t, bridge, `{"prompt":"denied"}`, "")
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d", unauthorized.StatusCode)
	}
	_ = unauthorized.Body.Close()

	oversizedBody := `{"prompt":"` +
		strings.Repeat("x", maxSubQueryRequestBytes) +
		`"}`
	oversized := postSubQuery(t, bridge, oversizedBody, bridge.token)
	if oversized.StatusCode != http.StatusRequestEntityTooLarge {
		body, _ := io.ReadAll(oversized.Body)
		t.Fatalf("oversized status = %d body=%s", oversized.StatusCode, body)
	}
	_ = oversized.Body.Close()

	oversizedTrailing := postSubQuery(
		t,
		bridge,
		`{"prompt":"ok"}`+strings.Repeat(" ", maxSubQueryRequestBytes),
		bridge.token,
	)
	if oversizedTrailing.StatusCode != http.StatusRequestEntityTooLarge {
		body, _ := io.ReadAll(oversizedTrailing.Body)
		t.Fatalf(
			"oversized trailing status = %d body=%s",
			oversizedTrailing.StatusCode,
			body,
		)
	}
	_ = oversizedTrailing.Body.Close()

	valid := postSubQuery(t, bridge, `{"prompt":"ok"}`, bridge.token)
	if valid.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(valid.Body)
		t.Fatalf("valid status = %d body=%s", valid.StatusCode, body)
	}
	_ = valid.Body.Close()
	if calls.Load() != 1 {
		t.Fatalf("sub-query calls = %d, want 1", calls.Load())
	}
}

func postSubQuery(
	t *testing.T,
	bridge *subQueryBridge,
	body string,
	token string,
) *http.Response {
	t.Helper()
	request, err := http.NewRequest(
		http.MethodPost,
		bridge.BaseURL()+"/v1/sub_query",
		bytes.NewBufferString(body),
	)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	return response
}
