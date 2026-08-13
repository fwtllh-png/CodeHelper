package bench

import (
	"fmt"
	"slices"
)

// Ratio is a measured rate with its evidence counts. Value is nil when there
// was no eligible sample, so "not measured" cannot be mistaken for zero.
type Ratio struct {
	Value       *float64 `json:"value"`
	Numerator   int      `json:"numerator"`
	Denominator int      `json:"denominator"`
}

// LatencyPercentiles reports nearest-rank task wall-clock percentiles.
type LatencyPercentiles struct {
	Samples int   `json:"samples"`
	P50MS   int64 `json:"p50_ms"`
	P95MS   int64 `json:"p95_ms"`
}

// BaselineMetrics is the Stage 0 upgrade baseline derived from task results.
type BaselineMetrics struct {
	TaskSuccessRate      Ratio              `json:"task_success_rate"`
	Latency              LatencyPercentiles `json:"latency"`
	RetryRate            Ratio              `json:"retry_rate"`
	VerificationCoverage Ratio              `json:"verification_coverage"`
	UnknownCostRate      Ratio              `json:"unknown_cost_rate"`
	RecoverySuccessRate  Ratio              `json:"recovery_success_rate"`
	RetryAttempts        int                `json:"retry_attempts"`
}

// AgentEvaluationMetrics are release-facing Multi-Agent quality signals.
// Ratios retain their denominators so an empty pack cannot report false green.
type AgentEvaluationMetrics struct {
	Scenarios             int   `json:"scenarios"`
	ExplicitCompliance    Ratio `json:"explicit_compliance"`
	AdaptiveCompliance    Ratio `json:"adaptive_compliance"`
	LocalExecutionRate    Ratio `json:"local_execution_rate"`
	FalseSpawnRate        Ratio `json:"false_spawn_rate"`
	AgentCompletionRate   Ratio `json:"agent_completion_rate"`
	ParallelAdmissionRate Ratio `json:"parallel_admission_rate"`
}

// AgentEvaluationThresholds are checked by the Multi-Agent release gate.
// Every rate requires evidence; an empty denominator always fails closed.
type AgentEvaluationThresholds struct {
	SchemaVersion                int     `json:"schema_version"`
	MinimumScenarios             int     `json:"minimum_scenarios"`
	MinimumExplicitCompliance    float64 `json:"minimum_explicit_compliance"`
	MinimumAdaptiveCompliance    float64 `json:"minimum_adaptive_compliance"`
	MinimumLocalExecutionRate    float64 `json:"minimum_local_execution_rate"`
	MaximumFalseSpawnRate        float64 `json:"maximum_false_spawn_rate"`
	MinimumAgentCompletionRate   float64 `json:"minimum_agent_completion_rate"`
	MinimumParallelAdmissionRate float64 `json:"minimum_parallel_admission_rate"`
}

func baselineMetrics(results []Result) BaselineMetrics {
	metrics := BaselineMetrics{}
	durations := make([]int64, 0, len(results))
	retriedTasks := 0
	recoveredTasks := 0
	verificationApplicable := 0
	verificationCovered := 0
	usageCalls := 0
	unpricedCalls := 0
	passed := 0
	available := 0
	for _, result := range results {
		if result.Status == "unavailable" {
			continue
		}
		available++
		if result.Passed {
			passed++
		}
		if result.DurationMS >= 0 {
			durations = append(durations, result.DurationMS)
		}
		if result.RetryAttempts > 0 {
			retriedTasks++
			metrics.RetryAttempts += result.RetryAttempts
			if result.Passed {
				recoveredTasks++
			}
		}
		if result.VerificationApplicable {
			verificationApplicable++
			if result.VerificationCovered {
				verificationCovered++
			}
		}
		usageCalls += result.UsageCalls
		unpricedCalls += result.UnpricedCalls
	}
	metrics.TaskSuccessRate = ratio(passed, available)
	metrics.RetryRate = ratio(retriedTasks, available)
	metrics.VerificationCoverage = ratio(
		verificationCovered,
		verificationApplicable,
	)
	metrics.UnknownCostRate = ratio(unpricedCalls, usageCalls)
	metrics.RecoverySuccessRate = ratio(recoveredTasks, retriedTasks)
	slices.Sort(durations)
	metrics.Latency = LatencyPercentiles{
		Samples: len(durations),
		P50MS:   nearestRank(durations, 50),
		P95MS:   nearestRank(durations, 95),
	}
	return metrics
}

func agentEvaluationMetrics(results []Result) AgentEvaluationMetrics {
	var metrics AgentEvaluationMetrics
	var explicitOK, explicitTotal int
	var adaptiveOK, adaptiveTotal int
	var localOK, localTotal, falseSpawns int
	var spawned, terminal int
	var parallelOK, parallelTotal int
	for _, result := range results {
		if result.ExpectedAgentSpawns == nil || result.Status == "unavailable" {
			continue
		}
		metrics.Scenarios++
		compliant := result.AgentSpawns == *result.ExpectedAgentSpawns
		switch result.DelegationMode {
		case "explicit":
			explicitTotal++
			if compliant {
				explicitOK++
			}
		case "adaptive":
			adaptiveTotal++
			if compliant {
				adaptiveOK++
			}
		}
		if *result.ExpectedAgentSpawns == 0 {
			localTotal++
			if result.AgentSpawns == 0 {
				localOK++
			} else {
				falseSpawns++
			}
		}
		spawned += result.AgentSpawns
		terminal += result.AgentTerminals
		if result.ExpectedAgentConcurrency != nil {
			parallelTotal++
			if result.AgentMaxConcurrency >= *result.ExpectedAgentConcurrency {
				parallelOK++
			}
		}
	}
	metrics.ExplicitCompliance = ratio(explicitOK, explicitTotal)
	metrics.AdaptiveCompliance = ratio(adaptiveOK, adaptiveTotal)
	metrics.LocalExecutionRate = ratio(localOK, localTotal)
	metrics.FalseSpawnRate = ratio(falseSpawns, localTotal)
	metrics.AgentCompletionRate = ratio(terminal, spawned)
	metrics.ParallelAdmissionRate = ratio(parallelOK, parallelTotal)
	return metrics
}

// ValidateAgentEvaluation applies the checked-in release thresholds.
func ValidateAgentEvaluation(
	metrics AgentEvaluationMetrics,
	thresholds AgentEvaluationThresholds,
) []string {
	var failures []string
	if thresholds.SchemaVersion != 1 {
		failures = append(failures, fmt.Sprintf(
			"agent threshold schema version = %d want 1",
			thresholds.SchemaVersion,
		))
	}
	if metrics.Scenarios < thresholds.MinimumScenarios {
		failures = append(failures, fmt.Sprintf(
			"agent scenarios = %d want at least %d",
			metrics.Scenarios,
			thresholds.MinimumScenarios,
		))
	}
	checkMinimum := func(name string, measured Ratio, minimum float64) {
		if measured.Value == nil {
			failures = append(failures, name+" has no evidence")
			return
		}
		if *measured.Value < minimum {
			failures = append(failures, fmt.Sprintf(
				"%s = %.4f want at least %.4f",
				name, *measured.Value, minimum,
			))
		}
	}
	checkMaximum := func(name string, measured Ratio, maximum float64) {
		if measured.Value == nil {
			failures = append(failures, name+" has no evidence")
			return
		}
		if *measured.Value > maximum {
			failures = append(failures, fmt.Sprintf(
				"%s = %.4f want at most %.4f",
				name, *measured.Value, maximum,
			))
		}
	}
	checkMinimum(
		"explicit compliance",
		metrics.ExplicitCompliance,
		thresholds.MinimumExplicitCompliance,
	)
	checkMinimum(
		"adaptive compliance",
		metrics.AdaptiveCompliance,
		thresholds.MinimumAdaptiveCompliance,
	)
	checkMinimum(
		"local execution rate",
		metrics.LocalExecutionRate,
		thresholds.MinimumLocalExecutionRate,
	)
	checkMaximum(
		"false spawn rate",
		metrics.FalseSpawnRate,
		thresholds.MaximumFalseSpawnRate,
	)
	checkMinimum(
		"agent completion rate",
		metrics.AgentCompletionRate,
		thresholds.MinimumAgentCompletionRate,
	)
	checkMinimum(
		"parallel admission rate",
		metrics.ParallelAdmissionRate,
		thresholds.MinimumParallelAdmissionRate,
	)
	return failures
}

func ratio(numerator, denominator int) Ratio {
	result := Ratio{Numerator: numerator, Denominator: denominator}
	if denominator == 0 {
		return result
	}
	value := float64(numerator) / float64(denominator)
	result.Value = &value
	return result
}

func nearestRank(sorted []int64, percentile int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	rank := (percentile*len(sorted) + 99) / 100
	rank = max(rank, 1)
	return sorted[rank-1]
}
