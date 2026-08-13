package fixture

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRoutesKeepIndependentStreamCursors(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "fixture.json", `{
		"protocol":"openai_chat",
		"path":"/chat/completions",
		"model":"fixture-model",
		"streams":["default-1.sse","default-2.sse"],
		"routes":[
			{"match":["child-role"],"streams":["child-1.sse","child-2.sse"]}
		]
	}`)
	for name, body := range map[string]string{
		"default-1.sse": "default-1",
		"default-2.sse": "default-2",
		"child-1.sse":   "child-1",
		"child-2.sse":   "child-2",
	} {
		writeFixtureFile(t, root, name, body)
	}
	server, err := Start(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})

	for _, test := range []struct {
		marker string
		want   string
	}{
		{marker: "parent", want: "default-1"},
		{marker: "child-role", want: "child-1"},
		{marker: "parent", want: "default-2"},
		{marker: "child-role", want: "child-2"},
	} {
		payload := []byte(`{"model":"fixture-model","stream":true,"marker":"` +
			test.marker + `"}`)
		response, err := http.Post(
			server.URL+"/chat/completions",
			"application/json",
			bytes.NewReader(payload),
		)
		if err != nil {
			t.Fatal(err)
		}
		body, readErr := io.ReadAll(response.Body)
		closeErr := response.Body.Close()
		if readErr != nil || closeErr != nil {
			t.Fatal(readErr, closeErr)
		}
		if response.StatusCode != http.StatusOK || string(body) != test.want {
			t.Fatalf(
				"marker %q: status=%d body=%q, want %q",
				test.marker, response.StatusCode, body, test.want,
			)
		}
	}
}

func TestStreamPlaceholdersUseLatestToolAndPromptValues(t *testing.T) {
	root := t.TempDir()
	writeFixtureFile(t, root, "fixture.json", `{
		"protocol":"openai_chat",
		"path":"/chat/completions",
		"model":"fixture-model",
		"streams":["stream.sse"]
	}`)
	writeFixtureFile(
		t, root, "stream.sse",
		`agent={{agent_id}} digest={{preview_digest}}`,
	)
	server, err := Start(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := server.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	digest := strings.Repeat("a", 64)
	payload := []byte(`{
		"model":"fixture-model",
		"stream":true,
		"messages":[
			{"role":"assistant","content":"old agent-1"},
			{"role":"user","content":"integrate agent-7"},
			{"role":"tool","content":"preview_digest=` + digest + `"}
		]
	}`)
	response, err := http.Post(
		server.URL+"/chat/completions",
		"application/json",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil {
		t.Fatal(readErr, closeErr)
	}
	if response.StatusCode != http.StatusOK ||
		string(body) != "agent=agent-7 digest="+digest {
		t.Fatalf("status=%d body=%q", response.StatusCode, body)
	}
}

func writeFixtureFile(t *testing.T, root, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, name), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
