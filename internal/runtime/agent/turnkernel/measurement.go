package turnkernel

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const TerminalMeasurementVersion uint32 = 1

type DurationMeasurement struct {
	Recorded     bool  `json:"recorded"`
	Milliseconds int64 `json:"milliseconds,omitempty"`
}

type TerminalLatencyMeasurement struct {
	Turn         DurationMeasurement `json:"turn"`
	FirstOutput  DurationMeasurement `json:"first_output"`
	Provider     DurationMeasurement `json:"provider"`
	Tool         DurationMeasurement `json:"tool"`
	ApprovalWait DurationMeasurement `json:"approval_wait"`
	Verification DurationMeasurement `json:"verification"`
	Persistence  DurationMeasurement `json:"persistence"`
}

type TerminalMeasurementSnapshot struct {
	Version       uint32                     `json:"version"`
	FrozenAt      time.Time                  `json:"frozen_at"`
	Latency       TerminalLatencyMeasurement `json:"latency"`
	Usage         UsageState                 `json:"usage"`
	UsageRecorded bool                       `json:"usage_recorded"`
	UsageDigest   string                     `json:"usage_digest"`
	Digest        string                     `json:"digest"`
}

func NewTerminalMeasurementSnapshot(
	frozenAt time.Time,
	latency *TerminalLatencyMeasurement,
	usage UsageState,
	usageRecorded bool,
) (TerminalMeasurementSnapshot, error) {
	snapshot := TerminalMeasurementSnapshot{
		Version:  TerminalMeasurementVersion,
		FrozenAt: frozenAt.UTC(),
		Usage:    usage, UsageRecorded: usageRecorded,
	}
	if latency != nil {
		snapshot.Latency = *latency
	}
	usageDigest, err := terminalUsageDigest(snapshot.Usage, usageRecorded)
	if err != nil {
		return TerminalMeasurementSnapshot{}, err
	}
	snapshot.UsageDigest = usageDigest
	digest, err := terminalMeasurementDigest(snapshot)
	if err != nil {
		return TerminalMeasurementSnapshot{}, err
	}
	snapshot.Digest = digest
	if err := ValidateTerminalMeasurement(snapshot); err != nil {
		return TerminalMeasurementSnapshot{}, err
	}
	return snapshot, nil
}

func ValidateTerminalMeasurement(
	snapshot TerminalMeasurementSnapshot,
) error {
	if snapshot.Version != TerminalMeasurementVersion {
		return fmt.Errorf(
			"terminal measurement version must be %d",
			TerminalMeasurementVersion,
		)
	}
	if snapshot.FrozenAt.IsZero() {
		return errors.New("terminal measurement frozen_at is required")
	}
	for name, value := range map[string]DurationMeasurement{
		"turn":          snapshot.Latency.Turn,
		"first_output":  snapshot.Latency.FirstOutput,
		"provider":      snapshot.Latency.Provider,
		"tool":          snapshot.Latency.Tool,
		"approval_wait": snapshot.Latency.ApprovalWait,
		"verification":  snapshot.Latency.Verification,
		"persistence":   snapshot.Latency.Persistence,
	} {
		if value.Milliseconds < 0 {
			return fmt.Errorf(
				"terminal measurement %s duration is negative",
				name,
			)
		}
		if !value.Recorded && value.Milliseconds != 0 {
			return fmt.Errorf(
				"terminal measurement %s is unrecorded with a value",
				name,
			)
		}
	}
	if snapshot.UsageRecorded && !snapshot.Usage.Frozen {
		return errors.New("terminal measurement usage is not frozen")
	}
	expectedUsage, err := terminalUsageDigest(
		snapshot.Usage,
		snapshot.UsageRecorded,
	)
	if err != nil {
		return err
	}
	if snapshot.UsageDigest != expectedUsage {
		return errors.New("terminal measurement usage digest mismatch")
	}
	expected, err := terminalMeasurementDigest(snapshot)
	if err != nil {
		return err
	}
	if snapshot.Digest != expected {
		return errors.New("terminal measurement digest mismatch")
	}
	return nil
}

func (s TerminalMeasurementSnapshot) Recorded() bool {
	return s.Latency.Turn.Recorded && s.UsageRecorded
}

func terminalUsageDigest(usage UsageState, recorded bool) (string, error) {
	encoded, err := json.Marshal(struct {
		Recorded bool       `json:"recorded"`
		Usage    UsageState `json:"usage"`
	}{Recorded: recorded, Usage: usage})
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func terminalMeasurementDigest(
	snapshot TerminalMeasurementSnapshot,
) (string, error) {
	snapshot.Digest = ""
	encoded, err := json.Marshal(snapshot)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}
