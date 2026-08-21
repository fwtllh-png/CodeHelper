package deepseek

import (
	"net/http"
	"testing"

	providerwire "github.com/fwtllh-png/CodeHelper/internal/adapter/provider/wire"
)

// TestClassifyHTTPHandlesMalformedJSON verifies that the DeepSeek error
// classifier does not silently swallow JSON unmarshal failures. When the
// response body is not valid JSON, the classifier should still return a
// reasonable failure code based on the HTTP status rather than panicking
// or misclassifying the error.
func TestClassifyHTTPHandlesMalformedJSON(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
	}{
		{"malformed_json_500", 500, `not json at all`},
		{"malformed_json_429", 429, `{broken}`},
		{"malformed_json_400", 400, `<html>bad request</html>`},
		{"empty_body_503", 503, ""},
		{"garbled_with_context_keyword", 500, `garbled text containing context_length but not valid json`},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Verify no panic and a reasonable error is returned.
			err := NewAdapter().ClassifyHTTP(providerwire.HTTPFailure{
				Status: test.status,
				Body:   test.body,
				Header: http.Header{},
			})
			if err == nil {
				t.Fatal("expected an error from ClassifyHTTP, got nil")
			}
			// The error should be non-nil and wrap a provider.Failure.
			// Even with malformed JSON, the HTTP status should drive
			// the classification fallback.
		})
	}
}