package otel

import "testing"

func TestEnvironmentExporterAliases(t *testing.T) {
	tests := map[string]ExportProtocol{
		"none":     ExportOff,
		"disabled": ExportOff,
		"http":     ExportHTTP,
	}
	for input, want := range tests {
		t.Run(input, func(t *testing.T) {
			t.Setenv("CODEHELPER_OTEL_EXPORTER", input)
			if got := environmentOptions().Protocol; got != want {
				t.Fatalf("protocol = %q, want %q", got, want)
			}
		})
	}
}

func TestEnvironmentExporterDefaultsOffWithoutEndpoint(t *testing.T) {
	t.Setenv("CODEHELPER_OTEL_EXPORTER", "")
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "")
	if got := environmentOptions().Protocol; got != ExportOff {
		t.Fatalf("protocol = %q, want %q", got, ExportOff)
	}
}
