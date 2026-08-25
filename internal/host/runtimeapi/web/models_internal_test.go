package web

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestModelTestReportsProviderCatalogResult(t *testing.T) {
	server := &Server{capacity: defaultCapacity()}
	dependencies := Dependencies{
		DefaultProfile: protocol.SessionProfile{Provider: "fixture"},
		ModelProbe: func(_ context.Context, model string) (bool, error) {
			return model == "available-model", nil
		},
	}
	for _, test := range []struct {
		model, status string
	}{
		{model: "available-model", status: "available"},
		{model: "missing-model", status: "not_listed"},
	} {
		request := httptest.NewRequest(
			"POST",
			"/api/v1/model/test",
			strings.NewReader(`{"model":"`+test.model+`"}`),
		)
		result, err := server.modelTest(request, dependencies)
		if err != nil {
			t.Fatal(err)
		}
		if result.(ModelTestResult).Status != test.status {
			t.Fatalf("status = %q, want %q", result.(ModelTestResult).Status, test.status)
		}
	}
}
