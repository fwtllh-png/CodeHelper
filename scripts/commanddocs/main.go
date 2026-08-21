// Command commanddocs updates or checks generated CLI command lists.
package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
)

const (
	beginMarker = "<!-- BEGIN GENERATED COMMAND LIST -->"
	endMarker   = "<!-- END GENERATED COMMAND LIST -->"
)

func main() {
	check := flag.Bool("check", false, "fail when generated command lists are stale")
	flag.Parse()

	root, err := repositoryRoot()
	if err != nil {
		fatal(err)
	}
	targets := []struct {
		path   string
		locale string
	}{
		{path: "docs/zh-CN/usage.md", locale: "zh-CN"},
	}
	stale := false
	for _, target := range targets {
		path := filepath.Join(root, target.path)
		current, err := os.ReadFile(path)
		if err != nil {
			fatal(err)
		}
		next, err := replaceGeneratedBlock(
			current,
			cli.RenderCommandReference(target.locale),
		)
		if err != nil {
			fatal(fmt.Errorf("%s: %w", target.path, err))
		}
		if bytes.Equal(current, next) {
			continue
		}
		if *check {
			fmt.Fprintf(os.Stderr, "stale generated command list: %s\n", target.path)
			stale = true
			continue
		}
		if err := os.WriteFile(path, next, 0o644); err != nil {
			fatal(err)
		}
		fmt.Printf("updated %s\n", target.path)
	}
	if stale {
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

func replaceGeneratedBlock(document []byte, generated string) ([]byte, error) {
	text := string(document)
	start := strings.Index(text, beginMarker)
	end := strings.Index(text, endMarker)
	if start < 0 || end < 0 || end < start {
		return nil, fmt.Errorf("generated command-list markers are missing or invalid")
	}
	end += len(endMarker)
	block := beginMarker + "\n" + generated + "\n" + endMarker
	return []byte(text[:start] + block + text[end:]), nil
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "commanddocs:", err)
	os.Exit(1)
}
