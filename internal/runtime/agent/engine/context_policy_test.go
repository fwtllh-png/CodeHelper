package engine

import "testing"

func TestContextWindowThresholdsDerivePrepareFromExplicitCompactLimit(t *testing.T) {
	prepare, compact, emergency := contextWindowThresholds(
		CompactWindowPolicy{AutoTokens: 512},
		4096,
	)
	if prepare != 433 || compact != 512 || emergency != 3481 {
		t.Fatalf(
			"thresholds = (%d, %d, %d), want (433, 512, 3481)",
			prepare,
			compact,
			emergency,
		)
	}
}
