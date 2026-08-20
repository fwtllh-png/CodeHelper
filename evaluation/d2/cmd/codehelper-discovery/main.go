package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/d2"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/freeze"
)

func main() {
	os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr))
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}
	switch args[0] {
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "qualify":
		return runQualify(args[1:], stdout, stderr)
	case "qualify-drivers":
		return runQualifyDrivers(ctx, args[1:], stdout, stderr)
	case "campaign":
		return runCampaign(ctx, args[1:], stdout, stderr)
	case "help", "-h", "--help":
		printUsage(stdout)
		return 0
	default:
		fmt.Fprintf(stderr, "codehelper-discovery: unknown command %q\n", args[0])
		printUsage(stderr)
		return 2
	}
}

func runCampaign(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
) int {
	flags := flag.NewFlagSet("campaign", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	id := flags.String("id", "", "immutable D2 Campaign Round ID")
	lockPath := flags.String("discovery-lock", "", "qualified D2 Discovery Lock")
	planPath := flags.String("plan", "", "qualified Campaign Plan")
	inventoryPath := flags.String("inventory", "", "qualified Driver Inventory")
	runtimePath := flags.String("runtime", "", "frozen production Runtime binary")
	vsixPath := flags.String("vsix", "", "frozen official VSIX")
	extensionPath := flags.String(
		"extension",
		"extensions/vscode",
		"official VS Code extension source",
	)
	npm := flags.String("npm", "npm", "npm executable")
	live := flags.Bool("live", false, "execute separately budgeted live-model Cases")
	campaignPath := flags.String(
		"campaign-spec",
		"evaluation/spec/d2-campaign.json",
		"D2 campaign contract",
	)
	output := flags.String("output", "", "private immutable D2 Campaign output")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 ||
		strings.TrimSpace(*id) == "" ||
		strings.TrimSpace(*lockPath) == "" ||
		strings.TrimSpace(*planPath) == "" ||
		strings.TrimSpace(*inventoryPath) == "" ||
		strings.TrimSpace(*runtimePath) == "" ||
		strings.TrimSpace(*vsixPath) == "" ||
		strings.TrimSpace(*output) == "" {
		fmt.Fprintln(
			stderr,
			"codehelper-discovery: campaign requires --id, --discovery-lock, --plan, --inventory, --runtime, --vsix, and --output",
		)
		return 2
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(absoluteRoot, filepath.FromSlash(path))
	}
	lock, err := d2.ReadDiscoveryLock(resolve(*lockPath))
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	plan, err := d2.ReadPlan(resolve(*planPath))
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	inventory, err := d2.ReadDriverInventory(resolve(*inventoryPath))
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	bundle, err := d2.LoadCampaign(absoluteRoot, *campaignPath)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	round, err := d2.RunCampaignRound(ctx, d2.CampaignOptions{
		Root:      absoluteRoot,
		ID:        *id,
		Output:    resolve(*output),
		Runtime:   resolve(*runtimePath),
		VSIX:      resolve(*vsixPath),
		Extension: resolve(*extensionPath),
		NPM:       *npm,
		Live:      *live,
		Lock:      lock,
		Campaign:  bundle.Campaign,
		Plan:      plan,
		Inventory: inventory,
	})
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	raw, err := json.Marshal(round)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	if err := d2.ValidateSchema(
		absoluteRoot,
		"evaluation/schema/discovery-round.schema.json",
		raw,
	); err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	if err := d2.WriteCampaignBundle(
		resolve(*output),
		round,
		plan,
		inventory,
		lock,
	); err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	writeSummary(stdout, struct {
		ID            string `json:"id"`
		Status        string `json:"status"`
		Scheduled     int    `json:"scheduled"`
		Settled       int    `json:"settled"`
		Passed        int    `json:"passed"`
		Failed        int    `json:"failed"`
		Unavailable   int    `json:"unavailable"`
		Invalid       int    `json:"invalid"`
		BudgetSkipped int    `json:"budget_skipped"`
		Observations  int    `json:"observations"`
		Output        string `json:"output"`
	}{
		ID: round.ID, Status: round.Status,
		Scheduled: round.Scheduled, Settled: round.Settled,
		Passed: round.Passed, Failed: round.Failed,
		Unavailable: round.Unavailable, Invalid: round.Invalid,
		BudgetSkipped: round.BudgetSkipped,
		Observations:  len(round.Observations),
		Output:        resolve(*output),
	})
	return 0
}

func runQualifyDrivers(
	ctx context.Context,
	args []string,
	stdout, stderr io.Writer,
) int {
	flags := flag.NewFlagSet("qualify-drivers", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	id := flags.String("id", "", "immutable D2 Driver qualification ID")
	baseLockPath := flags.String("base-lock", "", "frozen base Harness Lock")
	runtimePath := flags.String("runtime", "", "frozen production Runtime binary")
	vsixPath := flags.String("vsix", "", "frozen official VSIX")
	extensionPath := flags.String(
		"extension",
		"extensions/vscode",
		"official VS Code extension source",
	)
	npm := flags.String("npm", "npm", "npm executable")
	campaignPath := flags.String(
		"campaign",
		"evaluation/spec/d2-campaign.json",
		"D2 campaign contract",
	)
	output := flags.String("output", "", "private D2 Driver qualification output")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 ||
		strings.TrimSpace(*id) == "" ||
		strings.TrimSpace(*baseLockPath) == "" ||
		strings.TrimSpace(*runtimePath) == "" ||
		strings.TrimSpace(*vsixPath) == "" ||
		strings.TrimSpace(*output) == "" {
		fmt.Fprintln(
			stderr,
			"codehelper-discovery: qualify-drivers requires --id, --base-lock, --runtime, --vsix, and --output",
		)
		return 2
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(absoluteRoot, filepath.FromSlash(path))
	}
	base, err := freeze.Read(resolve(*baseLockPath))
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	bundle, err := d2.LoadCampaign(absoluteRoot, *campaignPath)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	plan, err := d2.BuildPlan(bundle.Campaign)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	inventory, err := d2.BuildDriverInventory(bundle.Campaign, plan)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	candidate, err := d2.BuildDiscoveryLock(d2.LockOptions{
		Root:       absoluteRoot,
		ID:         *id,
		Base:       base,
		Campaign:   bundle,
		Plan:       plan,
		InputRoots: d2.DefaultInputRoots(),
	})
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	foundation, err := d2.RunQualification(
		absoluteRoot,
		*id,
		bundle,
		plan,
		candidate,
	)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	report, err := d2.RunDriverQualification(ctx, d2.DriverQualificationOptions{
		Root:       absoluteRoot,
		ID:         *id,
		Runtime:    resolve(*runtimePath),
		VSIX:       resolve(*vsixPath),
		Extension:  resolve(*extensionPath),
		NPM:        *npm,
		Foundation: foundation,
		Lock:       candidate,
		Campaign:   bundle.Campaign,
		Plan:       plan,
		Inventory:  inventory,
	})
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	qualified, err := d2.QualifyDriverLock(candidate, report)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	outputPath := resolve(*output)
	if err := d2.WriteDriverQualificationBundle(
		outputPath,
		plan,
		inventory,
		foundation,
		report,
		qualified,
	); err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	writeSummary(stdout, struct {
		ID              string `json:"id"`
		Status          string `json:"status"`
		LockIdentity    string `json:"lock_identity"`
		Checks          int    `json:"checks"`
		Cases           int    `json:"cases"`
		Drivers         int    `json:"drivers"`
		Faults          int    `json:"faults"`
		InventoryDigest string `json:"inventory_digest"`
		Output          string `json:"output"`
	}{
		ID:              *id,
		Status:          qualified.Status,
		LockIdentity:    qualified.LockIdentity,
		Checks:          report.Scheduled,
		Cases:           len(inventory.Cases),
		Drivers:         len(inventory.Drivers),
		Faults:          len(inventory.Faults),
		InventoryDigest: inventory.EvidenceDigest,
		Output:          outputPath,
	})
	return 0
}

func runCheck(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	campaignPath := flags.String(
		"campaign",
		"evaluation/spec/d2-campaign.json",
		"D2 campaign contract",
	)
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "codehelper-discovery: check accepts no arguments")
		return 2
	}
	bundle, err := d2.LoadCampaign(*root, *campaignPath)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	plan, err := d2.BuildPlan(bundle.Campaign)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	writeSummary(stdout, struct {
		CampaignID      string `json:"campaign_id"`
		CampaignDigest  string `json:"campaign_digest"`
		PlannerDigest   string `json:"planner_digest"`
		Families        int    `json:"families"`
		Cases           int    `json:"cases"`
		PairwiseCovered int    `json:"pairwise_covered"`
		PairwiseTotal   int    `json:"pairwise_total"`
	}{
		CampaignID:      bundle.Campaign.ID,
		CampaignDigest:  bundle.Digest,
		PlannerDigest:   plan.EvidenceDigest,
		Families:        len(bundle.Campaign.Families),
		Cases:           len(plan.Cases),
		PairwiseCovered: plan.Coverage.PairwiseCovered,
		PairwiseTotal:   plan.Coverage.PairwiseTotal,
	})
	return 0
}

func runQualify(args []string, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("qualify", flag.ContinueOnError)
	flags.SetOutput(stderr)
	root := flags.String("root", ".", "repository root")
	id := flags.String("id", "", "immutable D2 qualification ID")
	baseLockPath := flags.String("base-lock", "", "frozen base Harness Lock")
	campaignPath := flags.String(
		"campaign",
		"evaluation/spec/d2-campaign.json",
		"D2 campaign contract",
	)
	output := flags.String("output", "", "private D2 qualification output")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() != 0 ||
		strings.TrimSpace(*id) == "" ||
		strings.TrimSpace(*baseLockPath) == "" ||
		strings.TrimSpace(*output) == "" {
		fmt.Fprintln(
			stderr,
			"codehelper-discovery: qualify requires --id, --base-lock, and --output",
		)
		return 2
	}
	absoluteRoot, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	resolve := func(path string) string {
		if filepath.IsAbs(path) {
			return filepath.Clean(path)
		}
		return filepath.Join(absoluteRoot, filepath.FromSlash(path))
	}
	base, err := freeze.Read(resolve(*baseLockPath))
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	bundle, err := d2.LoadCampaign(absoluteRoot, *campaignPath)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	plan, err := d2.BuildPlan(bundle.Campaign)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	candidate, err := d2.BuildDiscoveryLock(d2.LockOptions{
		Root:       absoluteRoot,
		ID:         *id,
		Base:       base,
		Campaign:   bundle,
		Plan:       plan,
		InputRoots: d2.DefaultInputRoots(),
	})
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	report, err := d2.RunQualification(
		absoluteRoot,
		*id,
		bundle,
		plan,
		candidate,
	)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	qualified, err := d2.QualifyDiscoveryLock(candidate, report)
	if err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	outputPath := resolve(*output)
	if err := d2.WriteQualificationBundle(
		outputPath,
		plan,
		report,
		qualified,
	); err != nil {
		fmt.Fprintln(stderr, "codehelper-discovery:", err)
		return 1
	}
	writeSummary(stdout, struct {
		ID           string `json:"id"`
		Status       string `json:"status"`
		LockIdentity string `json:"lock_identity"`
		Checks       int    `json:"checks"`
		Cases        int    `json:"cases"`
		Output       string `json:"output"`
	}{
		ID:           *id,
		Status:       qualified.Status,
		LockIdentity: qualified.LockIdentity,
		Checks:       report.Scheduled,
		Cases:        len(plan.Cases),
		Output:       outputPath,
	})
	return 0
}

func writeSummary(output io.Writer, value any) {
	raw, _ := json.Marshal(value)
	fmt.Fprintln(output, string(raw))
}

func printUsage(output io.Writer) {
	fmt.Fprintln(
		output,
		"usage: codehelper-discovery <check|qualify|qualify-drivers|campaign> [flags]",
	)
}
