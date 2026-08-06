// Command upgradebaseline runs the hermetic coding suite and writes versioned
// coding-baseline evidence.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/host/bench"
)

func main() {
	suite := flag.String(
		"suite",
		"testdata/benchmarks",
		"benchmark suite directory",
	)
	output := flag.String("output", "", "JSON output path (stdout when empty)")
	timeout := flag.Duration("timeout", 10*time.Minute, "suite timeout")
	flag.Parse()

	root, err := repositoryRoot()
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report, err := bench.RunSuite(ctx, filepath.Join(root, *suite))
	if err != nil {
		fatal(err)
	}

	writer := io.Writer(os.Stdout)
	var file *os.File
	if *output != "" {
		path := filepath.Join(root, *output)
		if mkdirErr := os.MkdirAll(filepath.Dir(path), 0o755); mkdirErr != nil {
			fatal(mkdirErr)
		}
		file, err = os.Create(path)
		if err != nil {
			fatal(err)
		}
		defer file.Close()
		writer = file
	}
	if err := report.Encode(writer); err != nil {
		fatal(err)
	}
	if report.Unavailable > 0 {
		fmt.Fprintf(
			os.Stderr,
			"upgradebaseline: %d/%d tasks unavailable on %s\n",
			report.Unavailable,
			report.Total,
			report.Platform,
		)
	}
	if !report.BaselineOK() {
		fmt.Fprintf(
			os.Stderr,
			"upgradebaseline: suite passed %d/%d available tasks\n",
			report.Passed,
			report.Available,
		)
		os.Exit(1)
	}
}

func repositoryRoot() (string, error) {
	current, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(current, "go.mod")); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("repository root not found")
		}
		current = parent
	}
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "upgradebaseline:", err)
	os.Exit(1)
}
