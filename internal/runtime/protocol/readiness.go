package protocol

import (
	"fmt"
	"sort"
)

// ReadinessStatus is the operational state shared by every host.
type ReadinessStatus string

const (
	ReadinessReady    ReadinessStatus = "ready"
	ReadinessDegraded ReadinessStatus = "degraded"
	ReadinessBlocked  ReadinessStatus = "blocked"
)

// ReadinessCheck explains one capability in terms an operator can act on.
type ReadinessCheck struct {
	ID     string          `json:"id"`
	Status ReadinessStatus `json:"status"`
	Reason string          `json:"reason"`
	Impact string          `json:"impact,omitempty"`
	Action string          `json:"action,omitempty"`
}

// Readiness is the single readiness conclusion projected by hosts.
type Readiness struct {
	Status ReadinessStatus  `json:"status"`
	Checks []ReadinessCheck `json:"checks"`
}

// NewReadiness validates, sorts, and aggregates checks. Blocked outranks
// degraded, and an empty set is ready because no failed condition was observed.
func NewReadiness(checks ...ReadinessCheck) (Readiness, error) {
	result := Readiness{
		Status: ReadinessReady,
		Checks: append([]ReadinessCheck(nil), checks...),
	}
	seen := make(map[string]struct{}, len(result.Checks))
	for _, check := range result.Checks {
		if check.ID == "" {
			return Readiness{}, fmt.Errorf("readiness check id is required")
		}
		if _, exists := seen[check.ID]; exists {
			return Readiness{}, fmt.Errorf("duplicate readiness check %q", check.ID)
		}
		seen[check.ID] = struct{}{}
		if check.Reason == "" {
			return Readiness{}, fmt.Errorf("readiness check %q reason is required", check.ID)
		}
		switch check.Status {
		case ReadinessReady:
		case ReadinessDegraded:
			if result.Status == ReadinessReady {
				result.Status = ReadinessDegraded
			}
		case ReadinessBlocked:
			result.Status = ReadinessBlocked
		default:
			return Readiness{}, fmt.Errorf(
				"readiness check %q has invalid status %q", check.ID, check.Status,
			)
		}
	}
	sort.Slice(result.Checks, func(i, j int) bool {
		return result.Checks[i].ID < result.Checks[j].ID
	})
	return result, nil
}

// ExitCode maps operational state to the shared command contract.
func (r Readiness) ExitCode() int {
	switch r.Status {
	case ReadinessReady:
		return 0
	case ReadinessDegraded:
		return 1
	case ReadinessBlocked:
		return 2
	default:
		return 2
	}
}

// Check returns a named check without exposing mutable report storage.
func (r Readiness) Check(id string) (ReadinessCheck, bool) {
	for _, check := range r.Checks {
		if check.ID == id {
			return check, true
		}
	}
	return ReadinessCheck{}, false
}
