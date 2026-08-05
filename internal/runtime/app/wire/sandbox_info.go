package wire

import "github.com/fwtllh-png/CodeHelper/internal/security/sandbox"

// SandboxReport is a CLI-safe sandbox capability snapshot.
type SandboxReport struct {
	Source    string `json:"source"`
	Platform  string `json:"platform"`
	Backend   string `json:"backend"`
	Strength  string `json:"strength"`
	Available bool   `json:"available"`
	OK        bool   `json:"ok,omitempty"`
	Message   string `json:"message,omitempty"`
}

func DeclaredSandbox() SandboxReport {
	cap := sandbox.DeclaredCapability()
	return SandboxReport{
		Source: "declared", Platform: cap.Platform, Backend: cap.Backend,
		Strength: string(cap.Strength), Available: cap.Available,
	}
}

func ProbeSandbox() SandboxReport {
	cap := sandbox.Probe()
	return SandboxReport{
		Source: "probed", Platform: cap.Platform, Backend: cap.Backend,
		Strength: string(cap.Strength), Available: cap.Available,
	}
}

// CheckSandbox hermetically validates declared sandbox posture for CLI gates.
// It does not require a strong backend to pass — only that capability is coherent.
func CheckSandbox() SandboxReport {
	declared := DeclaredSandbox()
	report := declared
	report.Source = "check"
	if report.Platform == "" || report.Backend == "" || report.Strength == "" {
		report.OK = false
		report.Message = "incomplete declared sandbox capability"
		return report
	}
	if report.Available && report.Strength == string(sandbox.StrengthNone) {
		report.OK = false
		report.Message = "available capability cannot report strength=none"
		return report
	}
	report.OK = true
	report.Message = "declared sandbox posture is coherent"
	return report
}
