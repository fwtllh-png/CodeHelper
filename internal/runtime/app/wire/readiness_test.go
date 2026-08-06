package wire

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

func TestRuntimeReadinessBlocksMissingStrongSandbox(t *testing.T) {
	checks := RuntimeReadiness(SandboxReport{
		Platform: "fixture", Backend: "none", Strength: "none",
	})
	report := protocol.MustReadiness(checks...)
	if report.Status != protocol.ReadinessBlocked || report.ExitCode() != 2 {
		t.Fatalf("report = %+v", report)
	}
	check, ok := report.Check("runtime.sandbox")
	if !ok || check.Action == "" || check.Impact == "" {
		t.Fatalf("sandbox check = %+v, present=%v", check, ok)
	}
}

func TestRuntimeReadinessAcceptsStrongSandbox(t *testing.T) {
	checks := RuntimeReadiness(SandboxReport{
		Platform: "fixture", Backend: "fixture", Strength: "strong", Available: true,
	})
	report := protocol.MustReadiness(checks...)
	check, ok := report.Check("runtime.sandbox")
	if !ok || check.Status != protocol.ReadinessReady {
		t.Fatalf("sandbox check = %+v, present=%v", check, ok)
	}
}
