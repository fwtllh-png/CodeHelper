package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os/exec"
	"strings"
)

func runProof(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
) int {
	if len(args) == 0 || args[0] != "go-test" {
		fmt.Fprintln(stderr, "codehelper-eval: expected proof go-test")
		return 2
	}
	flags := flag.NewFlagSet("proof go-test", flag.ContinueOnError)
	flags.SetOutput(stderr)
	minimum := flags.Int("minimum", 1, "minimum executed and passed tests")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	command := flags.Args()
	if *minimum < 1 || len(command) < 3 ||
		command[0] != "go" || command[1] != "test" {
		fmt.Fprintln(
			stderr,
			"codehelper-eval: proof go-test requires --minimum N -- go test ...",
		)
		return 2
	}
	arguments := append([]string{"test", "-json"}, command[2:]...)
	process := exec.CommandContext(ctx, "go", arguments...)
	var output, errorOutput bytes.Buffer
	process.Stdout = &output
	process.Stderr = &errorOutput
	runErr := process.Run()
	_, stdoutErr := stdout.Write(output.Bytes())
	_, stderrErr := stderr.Write(errorOutput.Bytes())
	if runErr != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", runErr)
		return 1
	}
	if stdoutErr != nil || stderrErr != nil {
		fmt.Fprintln(
			stderr,
			"codehelper-eval:",
			errors.Join(stdoutErr, stderrErr),
		)
		return 1
	}
	type testEvent struct {
		Action  string `json:"Action"`
		Package string `json:"Package"`
		Test    string `json:"Test"`
	}
	started := make(map[string]struct{})
	passed := make(map[string]struct{})
	scanner := bufio.NewScanner(bytes.NewReader(output.Bytes()))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var event testEvent
		if json.Unmarshal(scanner.Bytes(), &event) != nil ||
			strings.TrimSpace(event.Test) == "" {
			continue
		}
		key := event.Package + "\x00" + event.Test
		switch event.Action {
		case "run":
			started[key] = struct{}{}
		case "pass":
			passed[key] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintln(stderr, "codehelper-eval:", err)
		return 1
	}
	for key := range passed {
		if _, exists := started[key]; !exists {
			delete(passed, key)
		}
	}
	if len(started) < *minimum || len(passed) < *minimum {
		fmt.Fprintf(
			stderr,
			"codehelper-eval: go-test proof executed=%d passed=%d minimum=%d\n",
			len(started),
			len(passed),
			*minimum,
		)
		return 1
	}
	return 0
}
