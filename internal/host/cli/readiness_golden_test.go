package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/constitution"
)

func TestDoctorJSONGolden(t *testing.T) {
	report := DoctorReportPayload{
		Readiness: protocol.MustReadiness(
			protocol.ReadinessCheck{
				ID: "content.ocr", Status: protocol.ReadinessDegraded,
				Reason: "ocr dependency is unavailable",
				Impact: "ocr content processing is unavailable",
				Action: "install tesseract",
			},
			protocol.ReadinessCheck{
				ID: "runtime.sandbox", Status: protocol.ReadinessReady,
				Reason: "strong fixture sandbox is available",
			},
		),
		Product: "codehelper", Sandbox: "fixture/strong",
		Features: map[string]string{
			"content.ocr": "degraded",
			"exec":        "ready",
		},
		Constitution: constitution.Status{Loaded: true, RuleCount: 2},
	}
	report.OK = report.Status == protocol.ReadinessReady
	var output bytes.Buffer
	if err := writeDoctorJSON(&output, report); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(filepath.Join("testdata", "doctor-readiness.golden.json"))
	if err != nil {
		t.Fatal(err)
	}
	if output.String() != string(want) {
		t.Fatalf("doctor JSON drift\ngot:  %s\nwant: %s", output.String(), want)
	}
}
