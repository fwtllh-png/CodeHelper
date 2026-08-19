package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/corepack"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/corpus"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/replay"
)

func runOracle(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(stderr, "codehelper-eval: oracle requires the check command")
		return 2
	}
	flags := flag.NewFlagSet("oracle check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	packPath := flags.String(
		"pack",
		"evaluation/scenarios/core/pack.json",
		"core scenario pack",
	)
	impactPath := flags.String(
		"impact-map",
		"evaluation/impact-map.json",
		"changed-path impact map",
	)
	corpusPath := flags.String(
		"corpus",
		"evaluation/corpus",
		"replay corpus root",
	)
	minimum := flags.Int("minimum", 30, "minimum core scenario families")
	replayRuns := flags.Int("replay-runs", 500, "deterministic Replay runs")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *minimum < 30 || *replayRuns < 500 {
		fmt.Fprintln(
			stderr,
			"codehelper-eval: oracle check requires minimum >= 30 and replay-runs >= 500",
		)
		return 2
	}
	bundle, err := corepack.Load(*root, *packPath, *impactPath)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	report, err := bundle.Check()
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if report.Families < *minimum {
		fmt.Fprintf(
			stderr,
			"codehelper-eval: core pack has %d families, minimum is %d\n",
			report.Families,
			*minimum,
		)
		return 1
	}
	resolvedCorpus := *corpusPath
	if !filepath.IsAbs(resolvedCorpus) {
		resolvedCorpus = filepath.Join(bundle.Root, filepath.FromSlash(resolvedCorpus))
	}
	verified, err := corpus.VerifyAll(resolvedCorpus, localSanitizer())
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if err := replayFlakeGate(verified, *replayRuns); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	result, _ := json.Marshal(struct {
		Status       string               `json:"status"`
		Core         corepack.CheckReport `json:"core"`
		Corpus       int                  `json:"corpus"`
		ReplayRuns   int                  `json:"replay_runs"`
		ReplayFlakes int                  `json:"replay_flakes"`
	}{
		Status: "passed", Core: report, Corpus: len(verified),
		ReplayRuns: *replayRuns, ReplayFlakes: 0,
	})
	fmt.Fprintln(stdout, "ORACLE_RESULT="+string(result))
	return 0
}

func runImpact(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "select" {
		fmt.Fprintln(stderr, "codehelper-eval: impact requires the select command")
		return 2
	}
	flags := flag.NewFlagSet("impact select", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	packPath := flags.String(
		"pack",
		"evaluation/scenarios/core/pack.json",
		"core scenario pack",
	)
	impactPath := flags.String(
		"impact-map",
		"evaluation/impact-map.json",
		"changed-path impact map",
	)
	var paths stringList
	flags.Var(&paths, "path", "changed repository-relative path; repeatable")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || len(paths) == 0 {
		fmt.Fprintln(stderr, "codehelper-eval: impact select requires at least one --path")
		return 2
	}
	bundle, err := corepack.Load(*root, *packPath, *impactPath)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	selection, err := bundle.SelectDetailed(paths)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	type selectedScenario struct {
		ID     string `json:"id"`
		Family string `json:"family"`
		Risk   string `json:"risk"`
	}
	result := make([]selectedScenario, 0, len(selection.Scenarios))
	for _, scenario := range selection.Scenarios {
		result = append(result, selectedScenario{
			ID: scenario.ID, Family: scenario.Family, Risk: string(scenario.Risk),
		})
	}
	encoded, _ := json.Marshal(struct {
		Paths         []string            `json:"paths"`
		Scenarios     []selectedScenario  `json:"scenarios"`
		MatchedRules  map[string][]string `json:"matched_rules"`
		FallbackPaths []string            `json:"fallback_paths"`
		ExcludedPaths []string            `json:"excluded_paths"`
	}{
		Paths: paths, Scenarios: result,
		MatchedRules:  selection.MatchedRules,
		FallbackPaths: selection.FallbackPaths,
		ExcludedPaths: selection.ExcludedPaths,
	})
	fmt.Fprintln(stdout, "IMPACT_RESULT="+string(encoded))
	return 0
}

func replayFlakeGate(verified []corpus.Verified, runs int) error {
	if len(verified) == 0 {
		return errors.New("Replay flake gate has an empty corpus")
	}
	type signature struct {
		Terminal           string `json:"terminal"`
		FailureSignature   string `json:"failure_signature"`
		DuplicateEvents    int    `json:"duplicate_events"`
		IncompleteRequests int    `json:"incomplete_requests"`
		OrphanResponses    int    `json:"orphan_responses"`
		Interrupted        bool   `json:"interrupted"`
	}
	baseline := make(map[string]string, len(verified))
	for _, entry := range verified {
		value := signature{
			Terminal:           entry.Outcome.Terminal,
			FailureSignature:   entry.Outcome.FailureSignature,
			DuplicateEvents:    entry.Outcome.DuplicateEvents,
			IncompleteRequests: entry.Outcome.IncompleteRequests,
			OrphanResponses:    entry.Outcome.OrphanResponses,
			Interrupted:        entry.Outcome.Interrupted,
		}
		encoded, _ := json.Marshal(value)
		baseline[entry.Manifest.ID] = string(encoded)
	}
	for index := range runs {
		entry := verified[index%len(verified)]
		outcome, err := replay.Execute(entry.Events)
		if err != nil {
			return fmt.Errorf(
				"Replay flake run %d corpus %s: %w",
				index+1,
				entry.Manifest.ID,
				err,
			)
		}
		value := signature{
			Terminal:           outcome.Terminal,
			FailureSignature:   outcome.FailureSignature,
			DuplicateEvents:    outcome.DuplicateEvents,
			IncompleteRequests: outcome.IncompleteRequests,
			OrphanResponses:    outcome.OrphanResponses,
			Interrupted:        outcome.Interrupted,
		}
		encoded, _ := json.Marshal(value)
		if string(encoded) != baseline[entry.Manifest.ID] {
			return fmt.Errorf(
				"Replay flake run %d corpus %s changed outcome",
				index+1,
				entry.Manifest.ID,
			)
		}
	}
	return nil
}

type stringList []string

func (s *stringList) String() string {
	return strings.Join(*s, ",")
}

func (s *stringList) Set(value string) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return errors.New("path cannot be empty")
	}
	*s = append(*s, value)
	return nil
}
