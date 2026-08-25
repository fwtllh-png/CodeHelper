package modelcatalog

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/model"
)

func TestProbeReportsWhetherConnectionListsExactModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(
		response http.ResponseWriter,
		request *http.Request,
	) {
		if request.URL.Path != "/v1/models" {
			http.NotFound(response, request)
			return
		}
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(
			`{"data":[{"id":"model-a"},{"id":"model-b"}]}`,
		))
	}))
	defer server.Close()

	for _, test := range []struct {
		model string
		want  bool
	}{
		{model: "model-a", want: true},
		{model: "model-c", want: false},
	} {
		got, err := Probe(
			t.Context(),
			"openai-compatible",
			server.URL+"/v1",
			model.CredentialRef{},
			test.model,
		)
		if err != nil {
			t.Fatal(err)
		}
		if got != test.want {
			t.Fatalf("Probe(%q) = %v, want %v", test.model, got, test.want)
		}
	}
}
