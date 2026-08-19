package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/capture"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/corpus"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/evidence"
	replayengine "github.com/fwtllh-png/CodeHelper/evaluation/internal/replay"
)

func runCapture(
	_ context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || args[0] != "promote" {
		fmt.Fprintln(stderr, "codehelper-eval: capture requires the promote command")
		return 2
	}
	flags := flag.NewFlagSet("capture promote", flag.ContinueOnError)
	flags.SetOutput(stderr)
	input := flags.String("input", "", "private raw capture path")
	format := flags.String("format", "", "capture format")
	prefix := flags.String("prefix", "", "corpus id prefix")
	batchID := flags.String("batch", "", "promotion batch id")
	reviewPath := flags.String("review", "", "approved promotion-review.json")
	output := flags.String(
		"output",
		".tmp/evaluation/promotion",
		"private promotion staging root",
	)
	sourceClass := flags.String(
		"source-class",
		string(corpus.SourceSynthetic),
		"source class",
	)
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || strings.TrimSpace(*input) == "" ||
		strings.TrimSpace(*format) == "" || strings.TrimSpace(*prefix) == "" ||
		strings.TrimSpace(*batchID) == "" ||
		strings.TrimSpace(*reviewPath) == "" {
		fmt.Fprintln(
			stderr,
			"codehelper-eval: capture promote requires --input, --format, --prefix, --batch, and --review",
		)
		return 2
	}
	raw, sourceDigest, err := capture.ReadWithDigest(
		*input,
		capture.Format(*format),
	)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	slices := capture.Slices(raw)
	if len(slices) == 0 {
		fmt.Fprintln(stderr, "codehelper-eval: capture produced no causal slices")
		return 1
	}
	sanitizer := localSanitizer()
	reviewRaw, err := os.ReadFile(*reviewPath)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	var review corpus.PromotionReview
	decoder := json.NewDecoder(bytes.NewReader(reviewRaw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&review); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval: decode promotion review")
		return 1
	}
	batch, err := corpus.PromoteBatch(corpus.BatchPromotion{
		BatchID:      *batchID,
		OutputRoot:   *output,
		EntryPrefix:  *prefix,
		SourceFormat: capture.Format(*format),
		SourceClass:  corpus.SourceClass(*sourceClass),
		SourceDigest: sourceDigest,
		Slices:       slices,
		Sanitizer:    sanitizer,
		Review:       review,
	})
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	encoded, _ := json.Marshal(struct {
		SourceDigest string   `json:"source_digest"`
		Batch        string   `json:"batch"`
		Entries      []string `json:"entries"`
	}{
		SourceDigest: sourceDigest, Batch: batch.ID, Entries: batch.Entries,
	})
	fmt.Fprintln(stdout, "CAPTURE_PROMOTION_RESULT="+string(encoded))
	return 0
}

func runReplay(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "check" {
		fmt.Fprintln(stderr, "codehelper-eval: replay requires the check command")
		return 2
	}
	flags := flag.NewFlagSet("replay check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("corpus", "evaluation/corpus", "replay corpus root")
	minimum := flags.Int("minimum", 1, "minimum valid corpus entries")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 || *minimum < 1 {
		fmt.Fprintln(stderr, "codehelper-eval: replay check requires a positive --minimum")
		return 2
	}
	verified, err := corpus.VerifyAll(*root, localSanitizer())
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	if len(verified) < *minimum {
		fmt.Fprintf(
			stderr,
			"codehelper-eval: replay corpus has %d entries, minimum is %d\n",
			len(verified),
			*minimum,
		)
		return 1
	}
	mutations := 0
	coverage := make(map[replayengine.MutationKind]int)
	for _, entry := range verified {
		executed, mutationErr := verifyMutations(entry.Events, entry.Outcome)
		if mutationErr != nil {
			fmt.Fprintf(
				stderr,
				"codehelper-eval: corpus %s mutation check: %v\n",
				entry.Manifest.ID,
				mutationErr,
			)
			return 1
		}
		mutations += len(executed)
		for _, kind := range executed {
			coverage[kind]++
		}
	}
	for _, required := range []replayengine.MutationKind{
		replayengine.MutationSplit,
		replayengine.MutationDelay,
		replayengine.MutationDuplicate,
		replayengine.MutationTruncate,
		replayengine.MutationInterrupt,
		replayengine.MutationUnknown,
		replayengine.MutationMalformed,
	} {
		if coverage[required] == 0 {
			fmt.Fprintf(
				stderr,
				"codehelper-eval: required mutation %s executed zero times\n",
				required,
			)
			return 1
		}
	}
	result, _ := json.Marshal(struct {
		Status           string                            `json:"status"`
		Corpus           int                               `json:"corpus"`
		Mutations        int                               `json:"mutations"`
		MutationCoverage map[replayengine.MutationKind]int `json:"mutation_coverage"`
	}{
		Status: "passed", Corpus: len(verified), Mutations: mutations,
		MutationCoverage: coverage,
	})
	fmt.Fprintln(stdout, "REPLAY_RESULT="+string(result))
	return 0
}

func verifyMutations(
	events []evidence.Envelope,
	base replayengine.Outcome,
) ([]replayengine.MutationKind, error) {
	sequence := events[0].Sequence
	variants := []replayengine.Mutation{
		{Kind: replayengine.MutationDelay, Sequence: sequence, DelayMS: 250},
		{Kind: replayengine.MutationDuplicate, Sequence: sequence},
		{Kind: replayengine.MutationUnknown, Sequence: sequence},
		{Kind: replayengine.MutationInterrupt, Sequence: sequence},
		{Kind: replayengine.MutationMalformed, Sequence: sequence},
	}
	if len(events) > 1 {
		variants = append(variants, replayengine.Mutation{
			Kind: replayengine.MutationTruncate, Sequence: events[len(events)-1].Sequence,
		})
	}
	if events[0].Source == evidence.SourceProvider {
		variants = append(variants, replayengine.Mutation{
			Kind: replayengine.MutationSplit, Sequence: sequence,
		})
	}
	var executed []replayengine.MutationKind
	for _, variant := range variants {
		mutated, err := replayengine.Mutate(events, variant)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", variant.Kind, err)
		}
		outcome, err := replayengine.Execute(mutated)
		if err != nil {
			if variant.Kind == replayengine.MutationDuplicate {
				executed = append(executed, variant.Kind)
				continue
			}
			return nil, fmt.Errorf("%s replay: %w", variant.Kind, err)
		}
		switch variant.Kind {
		case replayengine.MutationDelay, replayengine.MutationUnknown:
			if outcome.FailureSignature != base.FailureSignature {
				return nil, fmt.Errorf(
					"%s changed signature from %s to %s",
					variant.Kind,
					base.FailureSignature,
					outcome.FailureSignature,
				)
			}
		case replayengine.MutationDuplicate:
			if outcome.DuplicateEvents == 0 {
				return nil, errors.New("duplicate mutation was not observable")
			}
		case replayengine.MutationInterrupt:
			if !outcome.Interrupted ||
				outcome.FailureSignature != "transport_interrupted" {
				return nil, errors.New("interrupt mutation was not observable")
			}
		case replayengine.MutationMalformed:
			if len(outcome.Events) == 0 ||
				outcome.Events[0].Kind != "provider.malformed_event" {
				return nil, errors.New("malformed mutation was not observable")
			}
		}
		executed = append(executed, variant.Kind)
	}
	return executed, nil
}

func localSanitizer() capture.SanitizerOptions {
	var paths []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		paths = append(paths, home)
	}
	if working, err := os.Getwd(); err == nil && working != "" {
		paths = append(paths, working)
	}
	return capture.SanitizerOptions{RestrictedPaths: paths}
}
