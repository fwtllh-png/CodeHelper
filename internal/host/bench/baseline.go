package bench

import "slices"

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
