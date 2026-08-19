package spec

import (
	"fmt"
	"runtime"
	"slices"
	"strings"
	"time"
)

type EffectiveConfig struct {
	Requirements Requirements
	Budgets      Budgets
}

func Effective(suite Suite, scenario Scenario) EffectiveConfig {
	return EffectiveConfig{
		Requirements: Requirements{
			Commands:     unionStrings(suite.Requirements.Commands, scenario.Requirements.Commands),
			Platforms:    intersectStrings(suite.Requirements.Platforms, scenario.Requirements.Platforms),
			Capabilities: unionStrings(suite.Requirements.Capabilities, scenario.Requirements.Capabilities),
		},
		Budgets: Budgets{
			WallTimeMS:     minPositive(suite.Budgets.WallTimeMS, scenario.Budgets.WallTimeMS),
			MaxAttempts:    minPositive(suite.Budgets.MaxAttempts, scenario.Budgets.MaxAttempts),
			MaxOutputBytes: minPositive64(suite.Budgets.MaxOutputBytes, scenario.Budgets.MaxOutputBytes),
		},
	}
}

func Admit(
	suite Suite,
	scenario Scenario,
	runs []RunRecord,
	now time.Time,
) AdmissionDecision {
	decision := AdmissionDecision{
		Allowed:  true,
		Blocking: suite.ReleasePolicy.Blocking,
		Reasons:  []string{},
	}
	if len(runs) == 0 {
		return deny(decision, "run denominator is empty")
	}
	valid := 0
	for _, run := range runs {
		if run.SuiteID != suite.ID || run.ScenarioID != scenario.ID {
			decision = deny(decision, "run identity does not match suite and scenario")
			continue
		}
		switch run.Status {
		case StatusPassed, StatusFailed:
			valid++
		}
		if slices.Contains(suite.ReleasePolicy.AllowedStatuses, run.Status) {
			continue
		}
		if exceptionAllows(suite, scenario.ID, run.Status, now) {
			continue
		}
		decision = deny(
			decision,
			fmt.Sprintf("attempt %d status %s is not admitted", run.Attempt, run.Status),
		)
	}
	if valid < suite.ReleasePolicy.MinimumValidRuns {
		decision = deny(
			decision,
			fmt.Sprintf(
				"valid runs %d is below minimum %d",
				valid,
				suite.ReleasePolicy.MinimumValidRuns,
			),
		)
	}
	if scenario.Risk == RiskP0 {
		for _, run := range runs {
			if run.Status != StatusPassed {
				decision = deny(decision, "P0 work admits only passed")
				break
			}
		}
	}
	return decision
}

func CurrentPlatformAllowed(requirements Requirements) bool {
	return slices.Contains(requirements.Platforms, runtime.GOOS)
}

func exceptionAllows(
	suite Suite,
	scenarioID string,
	status Status,
	now time.Time,
) bool {
	if suite.Risk == RiskP0 {
		return false
	}
	for _, exception := range suite.Exceptions {
		expires, err := time.Parse(time.DateOnly, exception.ExpiresOn)
		if err != nil || !expires.After(now.UTC().Truncate(24*time.Hour)) {
			continue
		}
		if slices.Contains(exception.ScenarioIDs, scenarioID) &&
			slices.Contains(exception.AllowedStatuses, status) {
			return true
		}
	}
	return false
}

func deny(decision AdmissionDecision, reason string) AdmissionDecision {
	decision.Allowed = false
	if !slices.Contains(decision.Reasons, reason) {
		decision.Reasons = append(decision.Reasons, reason)
	}
	return decision
}

func unionStrings(groups ...[]string) []string {
	var result []string
	for _, group := range groups {
		for _, value := range group {
			value = strings.TrimSpace(value)
			if value != "" && !slices.Contains(result, value) {
				result = append(result, value)
			}
		}
	}
	slices.Sort(result)
	return result
}

func intersectStrings(left, right []string) []string {
	var result []string
	for _, value := range left {
		if slices.Contains(right, value) && !slices.Contains(result, value) {
			result = append(result, value)
		}
	}
	slices.Sort(result)
	return result
}

func minPositive(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func minPositive64(left, right int64) int64 {
	if left < right {
		return left
	}
	return right
}
