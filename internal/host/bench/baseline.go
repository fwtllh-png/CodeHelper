package bench

import (
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

// AgentEvaluationMetrics summarize Multi-Agent benchmark observations.
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
