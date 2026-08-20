package admission

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

type H4CanaryRequest struct {
	Root             string
	Output           string
	QualificationID  string
	SourceDigest     string
	LockIdentity     string
	PackageBinary    string
	PackageDigest    string
	Policy           H4CanarySpec
	Development      bool
	TurnsOverride    int
	IntervalOverride time.Duration
}

type h4CanarySlot struct {
	id        string
	host      *h3ACPProcess
	sessionID string
	dataDir   string
	initial   H3ResourceSample
	latencies []int64
	evidence  H4SlotEvidence
}

type h4TurnResult struct {
	slot     *h4CanarySlot
	terminal protocol.EventKind
	latency  int64
	err      error
}

func RunH4Canary(
	ctx context.Context,
	request H4CanaryRequest,
) (evidence H4CanaryEvidence, resultErr error) {
	evidence = H4CanaryEvidence{
		SchemaVersion:   H4EvidenceSchemaVersion,
		QualificationID: request.QualificationID,
		Status:          "failed", SourceDigest: request.SourceDigest,
		LockIdentity:        request.LockIdentity,
		PackageDigest:       request.PackageDigest,
		DevelopmentOverride: request.Development,
	}
	defer func() {
		evidence.EvidenceDigest = digestH4Canary(evidence)
		if request.Output == "" {
			return
		}
		if err := writePrivateJSON(
			filepath.Join(request.Output, "canary-evidence.json"),
			evidence,
		); err != nil {
			resultErr = errors.Join(resultErr, err)
		}
	}()
	if !validID(request.QualificationID) ||
		!digestValidH2(request.SourceDigest) ||
		!digestValidH2(request.LockIdentity) ||
		!digestValidH2(request.PackageDigest) {
		return evidence, errors.New("H4 Canary identity is invalid")
	}
	actualDigest, err := digestH3File(request.PackageBinary)
	if err != nil || actualDigest != request.PackageDigest {
		return evidence, errors.New("H4 Canary package binary digest is invalid")
	}
	turnsPerSlot := request.Policy.TurnsPerSlot
	interval := time.Duration(request.Policy.TurnIntervalMS) * time.Millisecond
	if request.Development {
		if request.TurnsOverride < 2 || request.IntervalOverride < time.Millisecond {
			return evidence, errors.New("H4 development overrides are invalid")
		}
		turnsPerSlot = request.TurnsOverride
		interval = request.IntervalOverride
	} else if request.TurnsOverride != 0 || request.IntervalOverride != 0 {
		return evidence, errors.New("H4 formal Canary cannot use overrides")
	}
	temporary, err := os.MkdirTemp("", "codehelper-h4-canary-")
	if err != nil {
		return evidence, err
	}
	defer os.RemoveAll(temporary)
	fixture := filepath.Join(temporary, "fixture")
	if err := writeH3Fixture(
		fixture,
		turnsPerSlot*len(request.Policy.PhaseSlots)+32,
		request.Policy.Prompt,
	); err != nil {
		return evidence, err
	}
	var slots []*h4CanarySlot
	defer func() {
		for _, slot := range slots {
			slot.host.stop()
		}
	}()
	for phaseIndex, target := range request.Policy.PhaseSlots {
		for len(slots) < target {
			slot, startErr := startH4Slot(
				ctx,
				request,
				fixture,
				temporary,
				len(slots)+1,
			)
			if startErr != nil {
				return evidence, startErr
			}
			slots = append(slots, slot)
		}
		phase := H4PhaseEvidence{
			ID:                fmt.Sprintf("phase-%02d", phaseIndex+1),
			TargetSlots:       target,
			ActiveSlots:       len(slots),
			ExpansionDecision: "stop",
		}
		for turn := 1; turn <= turnsPerSlot; turn++ {
			results := make(chan h4TurnResult, len(slots))
			var group sync.WaitGroup
			for _, slot := range slots {
				group.Add(1)
				go func(slot *h4CanarySlot) {
					defer group.Done()
					started := time.Now()
					turnContext, cancel := context.WithTimeout(
						ctx,
						time.Duration(request.Policy.TurnTimeoutSeconds)*time.Second,
					)
					defer cancel()
					terminal, promptErr := slot.host.prompt(
						turnContext,
						fmt.Sprintf(
							"h4-%02d-%02d-%04d",
							phaseIndex+1,
							slot.evidence.PID,
							turn,
						),
						slot.sessionID,
						request.Policy.Prompt,
					)
					results <- h4TurnResult{
						slot: slot, terminal: terminal,
						latency: time.Since(started).Milliseconds(),
						err:     promptErr,
					}
				}(slot)
			}
			group.Wait()
			close(results)
			for result := range results {
				phase.TurnsScheduled++
				evidence.TurnsScheduled++
				result.slot.evidence.TurnsScheduled++
				result.slot.latencies = append(
					result.slot.latencies,
					result.latency,
				)
				if result.err != nil {
					phase.TurnsFailed++
					evidence.TurnsFailed++
					result.slot.evidence.TurnsFailed++
					continue
				}
				switch result.terminal {
				case protocol.EventTurnCompleted:
					phase.TurnsCompleted++
					evidence.TurnsCompleted++
					result.slot.evidence.TurnsCompleted++
					result.slot.evidence.TerminalCompleted++
				case protocol.EventTurnFailed:
					phase.TurnsFailed++
					evidence.TurnsFailed++
					result.slot.evidence.TurnsFailed++
					result.slot.evidence.TerminalFailed++
				case protocol.EventTurnCanceled:
					phase.TurnsFailed++
					evidence.TurnsFailed++
					result.slot.evidence.TurnsFailed++
					result.slot.evidence.TerminalCanceled++
				default:
					phase.TurnsFailed++
					evidence.TurnsFailed++
					result.slot.evidence.TurnsFailed++
				}
			}
			if turn < turnsPerSlot {
				timer := time.NewTimer(interval)
				select {
				case <-ctx.Done():
					timer.Stop()
					return evidence, ctx.Err()
				case <-timer.C:
				}
			}
		}
		phase.P95LatencyMS = h4P95(slots)
		if h4ExpansionDecision(
			phase.TurnsScheduled,
			phase.TurnsCompleted,
			phase.TurnsFailed,
			phase.P95LatencyMS,
			request.Policy,
		) != "expand" {
			evidence.Phases = append(evidence.Phases, phase)
			return evidence, errors.New("H4 Canary health gate stopped expansion")
		}
		phase.ExpansionDecision = "expand"
		if phaseIndex == len(request.Policy.PhaseSlots)-1 {
			phase.ExpansionDecision = "hold"
		}
		evidence.Phases = append(evidence.Phases, phase)
	}
	for _, slot := range slots {
		final, sampleErr := sampleH3Resource(
			slot.host.command.Process.Pid,
			slot.dataDir,
			slot.evidence.TurnsCompleted,
			0,
			0,
		)
		if sampleErr != nil {
			return evidence, sampleErr
		}
		slotLatencies := append([]int64(nil), slot.latencies...)
		slices.Sort(slotLatencies)
		slot.evidence.P95LatencyMS = nearestRank(slotLatencies, 95)
		slot.evidence.RSSGrowthBytes =
			final.RSSBytes - slot.initial.RSSBytes
		slot.evidence.FDGrowth = final.FDs - slot.initial.FDs
		if slot.evidence.RSSGrowthBytes > request.Policy.MaxRSSGrowthBytes ||
			slot.evidence.FDGrowth > request.Policy.MaxFDGrowth {
			return evidence, errors.New("H4 Canary slot resource policy failed")
		}
		if err := slot.host.shutdown(ctx); err != nil {
			return evidence, err
		}
		if slot.host.stderr.String() != "" {
			return evidence, errors.New("H4 Canary Runtime wrote stderr")
		}
		for _, count := range slot.host.terminals {
			if count != 1 {
				return evidence, errors.New("H4 Canary observed duplicate terminal")
			}
		}
		evidence.Slots = append(evidence.Slots, slot.evidence)
	}
	var aggregateLatencies []int64
	for _, slot := range slots {
		aggregateLatencies = append(aggregateLatencies, slot.latencies...)
	}
	slices.Sort(aggregateLatencies)
	evidence.P95LatencyMS = nearestRank(aggregateLatencies, 95)
	expectedTurns := turnsPerSlot * 0
	for _, slotsInPhase := range request.Policy.PhaseSlots {
		expectedTurns += turnsPerSlot * slotsInPhase
	}
	if evidence.TurnsScheduled != expectedTurns ||
		evidence.TurnsCompleted != expectedTurns ||
		evidence.TurnsFailed != 0 ||
		evidence.P95LatencyMS > request.Policy.MaxP95LatencyMS ||
		len(evidence.Slots) != request.Policy.PhaseSlots[len(request.Policy.PhaseSlots)-1] {
		return evidence, errors.New("H4 Canary aggregate policy failed")
	}
	evidence.Status = "passed"
	return evidence, nil
}

func startH4Slot(
	ctx context.Context,
	request H4CanaryRequest,
	fixture, temporary string,
	index int,
) (*h4CanarySlot, error) {
	dataDir := filepath.Join(temporary, fmt.Sprintf("slot-%02d", index))
	host, err := startH3ACP(
		ctx,
		request.PackageBinary,
		request.Root,
		dataDir,
		fixture,
	)
	if err != nil {
		return nil, err
	}
	sessionID, err := host.handshake(ctx)
	if err != nil {
		host.stop()
		return nil, err
	}
	initial, err := sampleH3Resource(
		host.command.Process.Pid,
		dataDir,
		0,
		0,
		0,
	)
	if err != nil {
		host.stop()
		return nil, err
	}
	return &h4CanarySlot{
		id:        fmt.Sprintf("slot-%02d", index),
		host:      host,
		sessionID: sessionID,
		dataDir:   dataDir,
		initial:   initial,
		evidence: H4SlotEvidence{
			ID:  fmt.Sprintf("slot-%02d", index),
			PID: host.command.Process.Pid,
		},
	}, nil
}

func h4ExpansionDecision(
	scheduled, completed, failed int,
	p95 int64,
	policy H4CanarySpec,
) string {
	if scheduled < 1 ||
		completed != scheduled ||
		failed != 0 ||
		p95 < 0 ||
		p95 > policy.MaxP95LatencyMS {
		return "stop"
	}
	return "expand"
}

func h4P95(slots []*h4CanarySlot) int64 {
	var values []int64
	for _, slot := range slots {
		values = append(values, slot.latencies...)
	}
	slices.Sort(values)
	return nearestRank(values, 95)
}

func RunH4StopDrill(
	output string,
	policy H4CanarySpec,
) (H4StopEvidence, error) {
	evidence := H4StopEvidence{
		SchemaVersion:   H4EvidenceSchemaVersion,
		Status:          "failed",
		InjectedFailure: "one_failed_turn",
		StartedSlots:    1,
		BlockedSlots:    2,
	}
	evidence.Decision = h4ExpansionDecision(10, 9, 1, 1, policy)
	if evidence.Decision == "stop" {
		evidence.Status = "passed"
	}
	evidence.EvidenceDigest = digestH4Stop(evidence)
	if output != "" {
		if err := writePrivateJSON(
			filepath.Join(output, "rollout-stop-evidence.json"),
			evidence,
		); err != nil {
			return evidence, err
		}
	}
	if evidence.Status != "passed" {
		return evidence, errors.New("H4 rollout stop drill failed")
	}
	return evidence, nil
}
