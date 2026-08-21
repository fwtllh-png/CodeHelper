// verify-reliability-behavior runs the tests referenced in the reliability
// matrix and verifies they pass. This complements the static assertion verifier
// by actually executing the tests and checking their runtime behavior.
//
// Usage:
//
//	go run ./scripts/verify-reliability-behavior.go \
//	    -matrix testdata/contracts/reliability-matrix.json
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ReliabilityMatrix struct {
	Version    int         `json:"version"`
	Boundaries []Boundary  `json:"boundaries"`
}

type Boundary struct {
	ID         string              `json:"id"`
	Owner      string              `json:"owner"`
	Invariants []string            `json:"invariants"`
	Cases      map[string]TestCase `json:"cases"`
}

type TestCase struct {
	Package       string `json:"package"`
	Test          string `json:"test"`
	ExpectedState string `json:"expected_state"`
	Recovery      string `json:"recovery"`
}

func main() {
	matrixPath := ""

	for i := 1; i < len(os.Args); i++ {
		switch os.Args[i] {
		case "-matrix":
			i++
			matrixPath = os.Args[i]
		}
	}

	if matrixPath == "" {
		fmt.Fprintln(os.Stderr, "usage: verify-reliability-behavior -matrix <path>")
		os.Exit(2)
	}

	root := findRoot()
	matrixFullPath := filepath.Join(root, matrixPath)

	data, err := os.ReadFile(matrixFullPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read matrix: %v\n", err)
		os.Exit(1)
	}
	var matrix ReliabilityMatrix
	if err := json.Unmarshal(data, &matrix); err != nil {
		fmt.Fprintf(os.Stderr, "parse matrix: %v\n", err)
		os.Exit(1)
	}

	// Collect unique (package, test) pairs.
	type testRef struct {
		pkg  string
		test string
	}
	seen := make(map[testRef]bool)
	for _, boundary := range matrix.Boundaries {
		for _, testCase := range boundary.Cases {
			ref := testRef{testCase.Package, testCase.Test}
			seen[ref] = true
		}
	}

	failures := 0
	total := 0
	for ref := range seen {
		total++
		cmd := exec.Command("go", "test", "-count=1", "-run", "^"+ref.test+"$", ref.pkg)
		cmd.Dir = root
		output, err := cmd.CombinedOutput()
		if err != nil {
			failures++
			// Extract the last few lines of output for the error message.
			lines := strings.Split(strings.TrimSpace(string(output)), "\n")
			lastLines := lines
			if len(lines) > 5 {
				lastLines = lines[len(lines)-5:]
			}
			fmt.Fprintf(os.Stderr, "FAIL %s %s: %v\n%s\n",
				ref.pkg, ref.test, err, strings.Join(lastLines, "\n"))
		}
	}

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "\n%d/%d behavioral tests failed\n", failures, total)
		os.Exit(1)
	}
	fmt.Printf("reliability behavioral check passed: %d tests verified\n", total)
}

func findRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		dir = "."
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}