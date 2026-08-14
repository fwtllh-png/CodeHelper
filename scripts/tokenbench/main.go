// Command tokenbench runs and compares the identical-prompt token benchmark.
package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/host/bench"
	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
)

const schemaVersion = 1

type manifest struct {
	SchemaVersion int       `json:"schema_version"`
	Mode          string    `json:"mode"`
	Commit        string    `json:"commit"`
	Dirty         bool      `json:"dirty"`
	Platform      string    `json:"platform"`
	GoVersion     string    `json:"go_version"`
	Provider      string    `json:"provider,omitempty"`
	Model         string    `json:"model,omitempty"`
	ConfigDigest  string    `json:"config_digest,omitempty"`
	Runs          int       `json:"runs"`
	PromptDigest  string    `json:"prompt_digest"`
	FixtureDigest string    `json:"fixture_digest"`
	GeneratedAt   time.Time `json:"generated_at"`
}

type stats struct {
	Samples int    `json:"samples"`
	Min     uint64 `json:"min"`
	P50     uint64 `json:"p50"`
	P75     uint64 `json:"p75"`
	P90     uint64 `json:"p90"`
	Max     uint64 `json:"max"`
	MAD     uint64 `json:"mad"`
}

type report struct {
	SchemaVersion         int               `json:"schema_version"`
	Mode                  string            `json:"mode"`
	Runs                  int               `json:"runs"`
	Passed                int               `json:"passed"`
	Input                 stats             `json:"input_tokens"`
	UncachedInput         stats             `json:"uncached_input_tokens"`
	CachedInput           stats             `json:"cached_input_tokens"`
	CachedShareBP         stats             `json:"cached_share_basis_points"`
	Output                stats             `json:"output_tokens"`
	Reasoning             stats             `json:"reasoning_tokens"`
	Calls                 stats             `json:"sample_count"`
	AttributionCoverageBP stats             `json:"attribution_coverage_basis_points"`
	DurationMS            stats             `json:"duration_ms"`
	CostUpperBound        stats             `json:"cost_upper_bound_microunits"`
	CostKnown             bool              `json:"cost_known"`
	UnpricedCalls         int               `json:"unpriced_calls"`
	EstimatorErrorP95     float64           `json:"estimator_error_p95"`
	ContextP50            map[string]uint64 `json:"context_p50"`
	Reasons               map[string]int    `json:"sample_reasons"`
}

type breakdown struct {
	Run     int                         `json:"run"`
	Sample  uint32                      `json:"sample"`
	Input   uint64                      `json:"input_tokens"`
	Cached  uint64                      `json:"cached_tokens"`
	Context *protocol.SampleContextData `json:"context,omitempty"`
}

type comparison struct {
	SchemaVersion int              `json:"schema_version"`
	Baseline      string           `json:"baseline"`
	Candidate     string           `json:"candidate"`
	Deltas        map[string]delta `json:"deltas"`
	Status        string           `json:"status"`
}

type delta struct {
	Baseline  uint64  `json:"baseline_p50"`
	Candidate uint64  `json:"candidate_p50"`
	Absolute  int64   `json:"absolute"`
	Relative  float64 `json:"relative"`
}

func main() {
	if len(os.Args) < 2 {
		fatal(errors.New("usage: tokenbench run|compare"))
	}
	var err error
	switch os.Args[1] {
	case "run":
		err = run(os.Args[2:])
	case "live":
		err = runLive(os.Args[2:])
	case "compare":
		err = compare(os.Args[2:])
	case "merge":
		err = merge(os.Args[2:])
	default:
		err = fmt.Errorf("unknown command %q", os.Args[1])
	}
	if err != nil {
		fatal(err)
	}
}

func runLive(args []string) error {
	flags := flag.NewFlagSet("tokenbench live", flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	taskPath := flags.String(
		"task", "testdata/benchmarks/token-efficiency", "canonical task directory",
	)
	binary := flags.String("binary", "bin/codehelper", "CodeHelper binary under test")
	config := flags.String("config", "", "live provider configuration")
	output := flags.String("output", ".tmp/token-efficiency/live", "artifact directory")
	runs := flags.Int("runs", 5, "number of repeated runs")
	maxSteps := flags.Int("max-steps", 32, "maximum model steps per live run")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *config == "" {
		return errors.New("live benchmark unavailable: --config is required")
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	taskDir := filepath.Join(absoluteRoot, filepath.FromSlash(*taskPath))
	prompt, err := os.ReadFile(filepath.Join(taskDir, "prompt.txt"))
	if err != nil {
		return err
	}
	binaryPath := resolveUnder(absoluteRoot, *binary)
	configPath := resolveUnder(absoluteRoot, *config)
	artifactDir := resolveUnder(absoluteRoot, *output)
	if err := os.RemoveAll(artifactDir); err != nil {
		return err
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return err
	}
	results := make([]bench.Result, 0, *runs)
	for index := range *runs {
		fmt.Fprintf(os.Stderr, "tokenbench: live run %d/%d started\n", index+1, *runs)
		result, runErr := runLiveOnce(
			binaryPath, configPath, filepath.Join(taskDir, "workspace"), string(prompt), *maxSteps,
		)
		if runErr != nil {
			_ = writeJSON(filepath.Join(artifactDir, "failure.json"), map[string]any{
				"run": index + 1, "error": runErr.Error(), "partial_result": result,
			})
			return fmt.Errorf("live run %d: %w", index+1, runErr)
		}
		results = append(results, result)
		fmt.Fprintf(
			os.Stderr, "tokenbench: live run %d/%d passed calls=%d input=%d\n",
			index+1, *runs, result.UsageCalls, result.InputTokens,
		)
	}
	runManifest, err := buildManifest(absoluteRoot, taskDir, *runs)
	if err != nil {
		return err
	}
	runManifest.Mode = "live"
	if len(results) != 0 && len(results[0].Samples) != 0 {
		runManifest.Provider = results[0].Samples[0].Provider
		runManifest.Model = results[0].Samples[0].Model
	}
	if configData, readErr := os.ReadFile(configPath); readErr == nil {
		runManifest.ConfigDigest = digest(configData)
	}
	summary := summarize(results)
	summary.Mode = "live"
	if err := writeJSON(filepath.Join(artifactDir, "manifest.json"), runManifest); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(artifactDir, "samples.ndjson"), results); err != nil {
		return err
	}
	if err := writeBreakdowns(filepath.Join(artifactDir, "context-breakdown.ndjson"), results); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(artifactDir, "report.json"), summary); err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(artifactDir, "report.md"),
		[]byte(renderMarkdown(runManifest, summary)),
		0o644,
	)
}

func runLiveOnce(
	binary, config, seed, prompt string,
	maxSteps int,
) (bench.Result, error) {
	temporary, err := os.MkdirTemp("", "codehelper-token-live-")
	if err != nil {
		return bench.Result{}, err
	}
	defer os.RemoveAll(temporary)
	workspace := filepath.Join(temporary, "workspace")
	if err := copyTree(seed, workspace); err != nil {
		return bench.Result{}, err
	}
	loaderBefore, err := os.ReadFile(filepath.Join(workspace, "config", "loader.go"))
	if err != nil {
		return bench.Result{}, err
	}
	testBefore, err := os.ReadFile(filepath.Join(workspace, "config", "loader_test.go"))
	if err != nil {
		return bench.Result{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	started := time.Now()
	command := exec.CommandContext(
		ctx, binary, "exec", "--config", config,
		"--data-dir", filepath.Join(temporary, "state"),
		"--workspace", workspace, "--enable-tools", "--posture", "bypass",
		"--max-steps", fmt.Sprint(maxSteps), "--output-format", "stream-json", prompt,
	)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	runErr := command.Run()
	result := bench.Result{
		Task: "token-efficiency", Category: "token_efficiency", Status: "passed", Passed: true,
	}
	usage := make(map[uint32]protocol.UsageData)
	var terminalErr error
	scanner := bufio.NewScanner(&stdout)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var event protocol.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return bench.Result{}, fmt.Errorf("decode live event: %w", err)
		}
		switch data := event.Data.(type) {
		case *protocol.UsageData:
			usage[data.Sample] = *data
		case *protocol.TurnFailedData:
			terminalErr = fmt.Errorf("turn failed: %s", data.Message)
		case *protocol.TurnCanceledData:
			terminalErr = errors.New("turn canceled")
		}
	}
	if err := scanner.Err(); err != nil {
		return bench.Result{}, err
	}
	for _, sample := range usage {
		result.Samples = append(result.Samples, sample)
		result.InputTokens += sample.InputTokens
		result.OutputTokens += sample.OutputTokens
		result.ReasoningTokens += sample.ReasoningTokens
		result.CachedTokens += sample.CachedTokens
		result.CostMicrounits += sample.CostMicrounits
		if !sample.CostKnown {
			result.UnpricedCalls++
		}
	}
	slices.SortFunc(result.Samples, func(left, right protocol.UsageData) int {
		return int(left.Sample) - int(right.Sample)
	})
	result.UncachedInputTokens = result.InputTokens - min(result.InputTokens, result.CachedTokens)
	result.UsageCalls = len(result.Samples)
	if runErr != nil {
		result.Status, result.Passed = "failed", false
		return result, fmt.Errorf("%w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	if terminalErr != nil {
		result.Status, result.Passed = "failed", false
		return result, terminalErr
	}
	if result.UsageCalls == 0 {
		return result, errors.New("live run emitted no usage")
	}
	result.Terminal = bench.TerminalCompleted
	result.DurationMS = time.Since(started).Milliseconds()
	verify := exec.CommandContext(ctx, "go", "test", "./...")
	verify.Dir = workspace
	if output, err := verify.CombinedOutput(); err != nil {
		return result, fmt.Errorf("verify live workspace: %w: %s", err, output)
	}
	loaderAfter, err := os.ReadFile(filepath.Join(workspace, "config", "loader.go"))
	if err != nil || bytes.Equal(loaderBefore, loaderAfter) {
		return result, errors.New("live run did not change config/loader.go")
	}
	testAfter, err := os.ReadFile(filepath.Join(workspace, "config", "loader_test.go"))
	cycleTest := filepath.Join(workspace, "config", "loader_cycle_test.go")
	if (err != nil || bytes.Equal(testBefore, testAfter)) && !regularFile(cycleTest) {
		return result, errors.New("live run neither updated nor added focused tests")
	}
	return result, nil
}

func run(args []string) error {
	flags := flag.NewFlagSet("tokenbench run", flag.ContinueOnError)
	root := flags.String("root", ".", "repository root")
	taskPath := flags.String(
		"task", "testdata/benchmarks/token-efficiency", "canonical task directory",
	)
	output := flags.String("output", ".tmp/token-efficiency/current", "artifact directory")
	runs := flags.Int("runs", 5, "number of repeated runs")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *runs < 1 {
		return errors.New("runs must be positive")
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		return err
	}
	taskDir := filepath.Join(absoluteRoot, filepath.FromSlash(*taskPath))
	task, err := bench.LoadTask(taskDir)
	if err != nil {
		return err
	}
	artifactDir := filepath.Join(absoluteRoot, filepath.FromSlash(*output))
	if err := os.RemoveAll(artifactDir); err != nil {
		return err
	}
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return err
	}
	results := make([]bench.Result, 0, *runs)
	for index := range *runs {
		fmt.Fprintf(os.Stderr, "tokenbench: hermetic run %d/%d started\n", index+1, *runs)
		result := bench.RunTask(context.Background(), task)
		results = append(results, result)
		if !result.Passed {
			return fmt.Errorf("run %d failed: %v", index+1, result.Failures)
		}
		fmt.Fprintf(
			os.Stderr, "tokenbench: hermetic run %d/%d passed calls=%d input=%d\n",
			index+1, *runs, result.UsageCalls, result.InputTokens,
		)
	}
	runManifest, err := buildManifest(absoluteRoot, taskDir, *runs)
	if err != nil {
		return err
	}
	runManifest.Provider, runManifest.Model = "fixture", "fixture-model"
	summary := summarize(results)
	if err := writeJSON(filepath.Join(artifactDir, "manifest.json"), runManifest); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(artifactDir, "samples.ndjson"), results); err != nil {
		return err
	}
	if err := writeBreakdowns(filepath.Join(artifactDir, "context-breakdown.ndjson"), results); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(artifactDir, "report.json"), summary); err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(artifactDir, "report.md"),
		[]byte(renderMarkdown(runManifest, summary)),
		0o644,
	)
}

func compare(args []string) error {
	flags := flag.NewFlagSet("tokenbench compare", flag.ContinueOnError)
	baselinePath := flags.String("baseline", "", "baseline report.json")
	candidatePath := flags.String("candidate", "", "candidate report.json")
	output := flags.String("output", "", "optional comparison JSON")
	maxRegression := flags.Float64("max-regression", 0.05, "maximum input median regression")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *baselinePath == "" || *candidatePath == "" {
		return errors.New("baseline and candidate reports are required")
	}
	baseline, err := readReport(*baselinePath)
	if err != nil {
		return err
	}
	candidate, err := readReport(*candidatePath)
	if err != nil {
		return err
	}
	result := comparison{
		SchemaVersion: schemaVersion, Baseline: *baselinePath, Candidate: *candidatePath,
		Status: "passed", Deltas: map[string]delta{
			"input_tokens":          deltaFor(baseline.Input.P50, candidate.Input.P50),
			"uncached_input_tokens": deltaFor(baseline.UncachedInput.P50, candidate.UncachedInput.P50),
			"output_tokens":         deltaFor(baseline.Output.P50, candidate.Output.P50),
			"reasoning_tokens":      deltaFor(baseline.Reasoning.P50, candidate.Reasoning.P50),
			"sample_count":          deltaFor(baseline.Calls.P50, candidate.Calls.P50),
		},
	}
	if result.Deltas["input_tokens"].Relative > *maxRegression {
		result.Status = "failed"
	}
	var encoded []byte
	encoded, err = json.MarshalIndent(result, "", "  ")
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')
	if *output != "" {
		if err := os.MkdirAll(filepath.Dir(*output), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(*output, encoded, 0o644); err != nil {
			return err
		}
	} else {
		_, err = os.Stdout.Write(encoded)
	}
	if result.Status != "passed" {
		return errors.New("token benchmark comparison failed")
	}
	return err
}

func merge(args []string) error {
	flags := flag.NewFlagSet("tokenbench merge", flag.ContinueOnError)
	output := flags.String("output", "", "merged artifact directory")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *output == "" || flags.NArg() < 2 {
		return errors.New("merge requires --output and at least two artifact directories")
	}
	var manifests []manifest
	var results []bench.Result
	for _, directory := range flags.Args() {
		var current manifest
		if err := readJSON(filepath.Join(directory, "manifest.json"), &current); err != nil {
			return err
		}
		batch, err := readResults(filepath.Join(directory, "samples.ndjson"))
		if err != nil {
			return err
		}
		manifests, results = append(manifests, current), append(results, batch...)
	}
	first := manifests[0]
	for _, current := range manifests[1:] {
		if current.Mode != first.Mode || current.PromptDigest != first.PromptDigest ||
			current.FixtureDigest != first.FixtureDigest ||
			!sameIfPresent(current.Provider, first.Provider) ||
			!sameIfPresent(current.Model, first.Model) ||
			!sameIfPresent(current.ConfigDigest, first.ConfigDigest) {
			return errors.New("artifact manifests are not comparable")
		}
		if first.Provider == "" {
			first.Provider = current.Provider
		}
		if first.Model == "" {
			first.Model = current.Model
		}
		if first.ConfigDigest == "" {
			first.ConfigDigest = current.ConfigDigest
		}
	}
	if err := os.RemoveAll(*output); err != nil {
		return err
	}
	if err := os.MkdirAll(*output, 0o755); err != nil {
		return err
	}
	first.Runs, first.GeneratedAt = len(results), time.Now().UTC()
	summary := summarize(results)
	summary.Mode = first.Mode
	if err := writeJSON(filepath.Join(*output, "manifest.json"), first); err != nil {
		return err
	}
	if err := writeNDJSON(filepath.Join(*output, "samples.ndjson"), results); err != nil {
		return err
	}
	if err := writeBreakdowns(filepath.Join(*output, "context-breakdown.ndjson"), results); err != nil {
		return err
	}
	if err := writeJSON(filepath.Join(*output, "report.json"), summary); err != nil {
		return err
	}
	return os.WriteFile(
		filepath.Join(*output, "report.md"),
		[]byte(renderMarkdown(first, summary)),
		0o644,
	)
}

func sameIfPresent(left, right string) bool {
	return left == "" || right == "" || left == right
}

func buildManifest(root, taskDir string, runs int) (manifest, error) {
	prompt, err := os.ReadFile(filepath.Join(taskDir, "prompt.txt"))
	if err != nil {
		return manifest{}, err
	}
	commit, err := gitOutput(root, "rev-parse", "HEAD")
	if err != nil {
		return manifest{}, err
	}
	status, err := gitOutput(root, "status", "--porcelain")
	if err != nil {
		return manifest{}, err
	}
	fixtureDigest, err := treeDigest(taskDir)
	if err != nil {
		return manifest{}, err
	}
	return manifest{
		SchemaVersion: schemaVersion, Mode: "hermetic", Commit: commit,
		Dirty: status != "", Platform: runtime.GOOS + "/" + runtime.GOARCH,
		GoVersion: runtime.Version(), Runs: runs, PromptDigest: digest(prompt),
		FixtureDigest: fixtureDigest, GeneratedAt: time.Now().UTC(),
	}, nil
}

func summarize(results []bench.Result) report {
	input, uncached, cached, cacheShare := make([]uint64, 0, len(results)),
		make([]uint64, 0, len(results)), make([]uint64, 0, len(results)),
		make([]uint64, 0, len(results))
	output, reasoning, calls, attributionCoverage, durations, costs := make([]uint64, 0, len(results)),
		make([]uint64, 0, len(results)), make([]uint64, 0, len(results)),
		make([]uint64, 0, len(results)),
		make([]uint64, 0, len(results)), make([]uint64, 0, len(results))
	contextValues := make(map[string][]uint64)
	reasons := make(map[string]int)
	var errors []float64
	passed := 0
	unpricedCalls := 0
	for _, result := range results {
		if result.Passed {
			passed++
		}
		input, uncached = append(input, result.InputTokens), append(uncached, result.UncachedInputTokens)
		cached = append(cached, result.CachedTokens)
		share := uint64(0)
		if result.InputTokens != 0 {
			share = result.CachedTokens * 10_000 / result.InputTokens
		}
		cacheShare = append(cacheShare, share)
		output, reasoning = append(output, result.OutputTokens), append(reasoning, result.ReasoningTokens)
		calls = append(calls, uint64(len(result.Samples)))
		durations, costs = append(durations, uint64(max(0, result.DurationMS))),
			append(costs, result.CostMicrounits)
		unpricedCalls += result.UnpricedCalls
		runContext := make(map[string]uint64)
		attributedSamples := 0
		for _, sample := range result.Samples {
			if sample.Context == nil {
				continue
			}
			attributedSamples++
			value := sample.Context
			reasons[value.Reason]++
			for name, tokens := range map[string]uint64{
				"stable": value.StableTokens, "history_user": value.HistoryUserTokens,
				"history_assistant": value.HistoryAssistantTokens,
				"history_tool":      value.HistoryToolTokens, "history_other": value.HistoryOtherTokens,
				"dynamic": value.DynamicTokens, "continuation": value.ContinuationTokens,
				"tool_definitions": value.ToolDefinitionTokens,
				"provider_framing": value.ProviderFramingTokens,
			} {
				runContext[name] += tokens
			}
			if sample.InputTokens != 0 {
				difference := absDiff(sample.InputTokens, value.EstimatedTokens)
				errors = append(errors, float64(difference)/float64(sample.InputTokens))
			}
		}
		for name, tokens := range runContext {
			contextValues[name] = append(contextValues[name], tokens)
		}
		coverage := uint64(0)
		if len(result.Samples) != 0 {
			coverage = uint64(attributedSamples) * 10_000 / uint64(len(result.Samples))
		}
		attributionCoverage = append(attributionCoverage, coverage)
	}
	contextP50 := make(map[string]uint64, len(contextValues))
	for name, values := range contextValues {
		contextP50[name] = calculate(values).P50
	}
	slices.Sort(errors)
	errorP95 := 0.0
	if len(errors) != 0 {
		errorP95 = errors[nearestIndex(len(errors), 95)]
	}
	return report{
		SchemaVersion: schemaVersion, Mode: "hermetic", Runs: len(results), Passed: passed,
		Input: calculate(input), UncachedInput: calculate(uncached), CachedInput: calculate(cached),
		CachedShareBP: calculate(cacheShare), Output: calculate(output),
		Reasoning: calculate(reasoning), Calls: calculate(calls),
		AttributionCoverageBP: calculate(attributionCoverage),
		DurationMS:            calculate(durations),
		CostUpperBound:        calculate(costs), CostKnown: unpricedCalls == 0,
		UnpricedCalls:     unpricedCalls,
		EstimatorErrorP95: errorP95,
		ContextP50:        contextP50, Reasons: reasons,
	}
}

func calculate(values []uint64) stats {
	values = append([]uint64(nil), values...)
	slices.Sort(values)
	if len(values) == 0 {
		return stats{}
	}
	median := values[nearestIndex(len(values), 50)]
	deviations := make([]uint64, len(values))
	for index, value := range values {
		deviations[index] = absDiff(value, median)
	}
	slices.Sort(deviations)
	return stats{
		Samples: len(values), Min: values[0], P50: median,
		P75: values[nearestIndex(len(values), 75)],
		P90: values[nearestIndex(len(values), 90)], Max: values[len(values)-1],
		MAD: deviations[nearestIndex(len(deviations), 50)],
	}
}

func nearestIndex(length, percentile int) int {
	index := (percentile*length + 99) / 100
	return max(1, index) - 1
}

func deltaFor(baseline, candidate uint64) delta {
	result := delta{Baseline: baseline, Candidate: candidate}
	result.Absolute = int64(candidate) - int64(baseline)
	if baseline != 0 {
		result.Relative = float64(result.Absolute) / float64(baseline)
	}
	return result
}

func renderMarkdown(manifest manifest, report report) string {
	return fmt.Sprintf(
		"# Token Efficiency Baseline\n\n"+
			"- Commit: `%s`\n- Dirty: `%t`\n- Prompt: `%s`\n- Runs: `%d/%d` passed\n\n"+
			"| Metric | P50 | P90 | MAD |\n| --- | ---: | ---: | ---: |\n"+
			"| Input tokens | %d | %d | %d |\n"+
			"| Uncached input tokens | %d | %d | %d |\n"+
			"| Cached input tokens | %d | %d | %d |\n"+
			"| Cached share (bp) | %d | %d | %d |\n"+
			"| Output tokens | %d | %d | %d |\n"+
			"| Sample count | %d | %d | %d |\n"+
			"| Attribution coverage (bp) | %d | %d | %d |\n\n"+
			"Estimator error P95: `%.2f%%`\n",
		manifest.Commit, manifest.Dirty, manifest.PromptDigest, report.Passed, report.Runs,
		report.Input.P50, report.Input.P90, report.Input.MAD,
		report.UncachedInput.P50, report.UncachedInput.P90, report.UncachedInput.MAD,
		report.CachedInput.P50, report.CachedInput.P90, report.CachedInput.MAD,
		report.CachedShareBP.P50, report.CachedShareBP.P90, report.CachedShareBP.MAD,
		report.Output.P50, report.Output.P90, report.Output.MAD,
		report.Calls.P50, report.Calls.P90, report.Calls.MAD,
		report.AttributionCoverageBP.P50,
		report.AttributionCoverageBP.P90,
		report.AttributionCoverageBP.MAD,
		report.EstimatorErrorP95*100,
	)
}

func writeBreakdowns(path string, results []bench.Result) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for run, result := range results {
		for _, sample := range result.Samples {
			if err := encoder.Encode(breakdown{
				Run: run + 1, Sample: sample.Sample, Input: sample.InputTokens,
				Cached: sample.CachedTokens, Context: sample.Context,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func writeNDJSON(path string, values []bench.Result) error {
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer file.Close()
	encoder := json.NewEncoder(file)
	for _, value := range values {
		if err := encoder.Encode(value); err != nil {
			return err
		}
	}
	return nil
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}

func readReport(path string) (report, error) {
	file, err := os.Open(path)
	if err != nil {
		return report{}, err
	}
	defer file.Close()
	var value report
	err = json.NewDecoder(bufio.NewReader(file)).Decode(&value)
	return value, err
}

func readJSON(path string, value any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	return json.NewDecoder(file).Decode(value)
}

func readResults(path string) ([]bench.Result, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var results []bench.Result
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), 4<<20)
	for scanner.Scan() {
		var result bench.Result
		if err := json.Unmarshal(scanner.Bytes(), &result); err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	return results, scanner.Err()
}

func treeDigest(root string) (string, error) {
	hash := sha256.New()
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		_, _ = io.WriteString(hash, filepath.ToSlash(relative)+"\x00")
		_, _ = hash.Write(data)
		return nil
	})
	return hex.EncodeToString(hash.Sum(nil)), err
}

func digest(value []byte) string {
	sum := sha256.Sum256(value)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func gitOutput(root string, args ...string) (string, error) {
	command := exec.Command("git", args...)
	command.Dir = root
	output, err := command.Output()
	return strings.TrimSpace(string(output)), err
}

func resolveUnder(root, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(root, filepath.FromSlash(path))
}

func copyTree(source, destination string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}

func regularFile(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func absDiff(left, right uint64) uint64 {
	if left > right {
		return left - right
	}
	return right - left
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "tokenbench:", err)
	os.Exit(1)
}
