package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/corepack"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/foundation"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/replay"
)

func runFoundation(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(stderr, "codehelper-eval: foundation requires the check command")
		return 2
	}
	flags := flag.NewFlagSet("foundation check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	manifest := flags.String(
		"manifest",
		"evaluation/spec/foundation.json",
		"Foundation manifest",
	)
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "codehelper-eval: foundation check accepts no arguments")
		return 2
	}
	bundle, err := foundation.Load(*root, *manifest)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	core, err := corepack.Load(
		*root,
		bundle.Manifest.CorePack,
		bundle.Manifest.ImpactPolicy,
	)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	coreReport, err := core.Check()
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	events, err := foundationReplayEvents()
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	executors := replay.ProductionExecutors()
	for _, level := range []replay.Level{
		replay.LevelProvider,
		replay.LevelRuntime,
		replay.LevelHost,
	} {
		candidate := events
		if level != replay.LevelProvider {
			candidate = events[1:]
			candidate, err = evidence.Seal(candidate)
			if err != nil {
				fmt.Fprintln(stderr, "codehelper-eval:", err)
				return 1
			}
		}
		if _, err := replay.ExecuteAt(
			ctx,
			level,
			candidate,
			executors,
		); err != nil {
			fmt.Fprintf(stderr, "codehelper-eval: production %s Replay: %v\n", level, err)
			return 1
		}
	}
	mutationRuns := 0
	for _, contract := range bundle.Mutations.Mutations {
		base := events
		if contract.ReplayLevel == replay.LevelRuntime {
			base, err = foundationRuntimeEvents()
			if err != nil {
				fmt.Fprintln(stderr, "codehelper-eval:", err)
				return 1
			}
		}
		sequence := base[0].Sequence
		if contract.ID == replay.MutationTruncate {
			sequence = base[len(base)-1].Sequence
		}
		mutated, err := replay.Mutate(base, replay.Mutation{
			Kind: contract.ID, Sequence: sequence, DelayMS: 3,
		})
		if err != nil {
			fmt.Fprintf(stderr, "codehelper-eval: mutation %s: %v\n", contract.ID, err)
			return 1
		}
		if _, err := replay.ExecuteAt(
			ctx,
			contract.ReplayLevel,
			mutated,
			executors,
		); err != nil {
			fmt.Fprintf(
				stderr,
				"codehelper-eval: mutation %s production %s Replay: %v\n",
				contract.ID,
				contract.ReplayLevel,
				err,
			)
			return 1
		}
		mutationRuns++
	}
	encoded, _ := json.Marshal(struct {
		Status           string               `json:"status"`
		FoundationDigest string               `json:"foundation_digest"`
		Contracts        int                  `json:"contracts"`
		Oracles          int                  `json:"oracles"`
		Mutations        int                  `json:"mutations"`
		Core             corepack.CheckReport `json:"core"`
		ProductionReplay []string             `json:"production_replay"`
		MutationRuns     int                  `json:"mutation_runs"`
	}{
		Status: "passed", FoundationDigest: bundle.HarnessInputHash,
		Contracts:        len(bundle.Manifest.Contracts),
		Oracles:          len(bundle.Oracles.Oracles),
		Mutations:        len(bundle.Mutations.Mutations),
		Core:             coreReport,
		ProductionReplay: []string{"provider", "runtime", "host"},
		MutationRuns:     mutationRuns,
	})
	fmt.Fprintln(stdout, "FOUNDATION_RESULT="+string(encoded))
	return 0
}

func foundationRuntimeEvents() ([]evidence.Envelope, error) {
	return evidence.Seal([]evidence.Envelope{
		{
			OffsetMS: 0,
			Source:   evidence.SourceRuntime,
			Kind:     "turn.started",
			Identity: evidence.Identity{
				Capture: "capture-001",
				Turn:    "turn-001",
			},
			Policy: evidence.Policy{
				Class:     evidence.DataOperational,
				Redaction: evidence.RedactionNotRequired,
			},
			Data: []byte(`{"metadata":true}`),
		},
		{
			OffsetMS: 1,
			Source:   evidence.SourceRuntime,
			Kind:     "turn.failed",
			Identity: evidence.Identity{
				Capture: "capture-001",
				Turn:    "turn-001",
			},
			Policy: evidence.Policy{
				Class:     evidence.DataOperational,
				Redaction: evidence.RedactionNotRequired,
			},
			Data: []byte(`{"metadata":true}`),
		},
	})
}

func foundationReplayEvents() ([]evidence.Envelope, error) {
	return evidence.Seal([]evidence.Envelope{
		{
			OffsetMS: 0,
			Source:   evidence.SourceProvider,
			Kind:     "provider.frame",
			Identity: evidence.Identity{Capture: "capture-001"},
			Policy: evidence.Policy{
				Class:     evidence.DataOperational,
				Redaction: evidence.RedactionNotRequired,
			},
			Data: []byte(`{"wire_sequence":1,"metadata":true}`),
		},
		{
			OffsetMS: 1,
			Source:   evidence.SourceRuntime,
			Kind:     "turn.failed",
			Identity: evidence.Identity{
				Capture: "capture-001",
				Turn:    "turn-001",
			},
			Policy: evidence.Policy{
				Class:     evidence.DataOperational,
				Redaction: evidence.RedactionNotRequired,
			},
			Data: []byte(`{"metadata":true}`),
		},
	})
}
