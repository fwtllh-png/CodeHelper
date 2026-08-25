package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type contract struct {
	Version         int      `json:"version"`
	ReferenceCommit string   `json:"reference_commit"`
	MinimumScore    int      `json:"minimum_score"`
	Domains         []domain `json:"domains"`
	Blockers        []check  `json:"blockers"`
}

type domain struct {
	ID     string  `json:"id"`
	Points int     `json:"points"`
	Checks []check `json:"checks"`
}

type check struct {
	ID       string   `json:"id"`
	Points   int      `json:"points,omitempty"`
	Evidence []string `json:"evidence"`
}

func main() {
	root := "."
	path := "testdata/contracts/web-harness-parity.json"
	if len(os.Args) > 3 {
		fmt.Fprintln(os.Stderr, "usage: webharnessparity [contract] [root]")
		os.Exit(2)
	}
	if len(os.Args) >= 2 {
		path = os.Args[1]
	}
	if len(os.Args) == 3 {
		root = os.Args[2]
	}
	value, err := readContract(filepath.Join(root, path))
	if err == nil {
		err = validate(value, root)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "Web Harness parity invalid:", err)
		os.Exit(1)
	}
	fmt.Printf(
		"Web Harness parity contract valid: 100/100 points, %d blockers\n",
		len(value.Blockers),
	)
}

func readContract(path string) (contract, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return contract{}, err
	}
	var value contract
	decoder := json.NewDecoder(strings.NewReader(string(content)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return contract{}, err
	}
	return value, nil
}

func validate(value contract, root string) error {
	if value.Version != 1 {
		return fmt.Errorf("version = %d, want 1", value.Version)
	}
	if len(value.ReferenceCommit) != 40 {
		return errors.New("reference commit must be a full SHA")
	}
	if value.MinimumScore != 95 {
		return fmt.Errorf("minimum score = %d, want 95", value.MinimumScore)
	}
	expected := map[string]int{
		"shell_brand_responsive": 12,
		"empty_composer":         12,
		"chat_presentation":      18,
		"scroll_interaction":     13,
		"streaming_performance":  15,
		"trajectory_stats":       20,
		"motion_feedback":        5,
		"theme_a11y_zoom":        5,
	}
	total := 0
	seen := make(map[string]struct{}, len(value.Domains))
	for _, domain := range value.Domains {
		want, ok := expected[domain.ID]
		if !ok || want != domain.Points {
			return fmt.Errorf("domain %q has invalid points %d", domain.ID, domain.Points)
		}
		if _, duplicate := seen[domain.ID]; duplicate {
			return fmt.Errorf("duplicate domain %q", domain.ID)
		}
		seen[domain.ID] = struct{}{}
		points := 0
		for _, item := range domain.Checks {
			if item.ID == "" || item.Points <= 0 || len(item.Evidence) == 0 {
				return fmt.Errorf("domain %q contains an incomplete check", domain.ID)
			}
			if err := validateEvidence(root, item.Evidence); err != nil {
				return fmt.Errorf("%s/%s: %w", domain.ID, item.ID, err)
			}
			points += item.Points
		}
		if points != domain.Points {
			return fmt.Errorf(
				"domain %q checks total %d, want %d",
				domain.ID,
				points,
				domain.Points,
			)
		}
		total += points
	}
	if len(seen) != len(expected) || total != 100 {
		return fmt.Errorf("parity domains total %d with %d domains", total, len(seen))
	}
	for _, blocker := range value.Blockers {
		if blocker.ID == "" || len(blocker.Evidence) == 0 {
			return errors.New("blocker is incomplete")
		}
		if err := validateEvidence(root, blocker.Evidence); err != nil {
			return fmt.Errorf("blocker %s: %w", blocker.ID, err)
		}
	}
	return nil
}

func validateEvidence(root string, values []string) error {
	for _, value := range values {
		path, selector, _ := strings.Cut(value, "#")
		content, err := os.ReadFile(filepath.Join(root, path))
		if err != nil {
			return fmt.Errorf("read evidence %q: %w", value, err)
		}
		if selector != "" && !strings.Contains(string(content), selector) {
			return fmt.Errorf("evidence selector %q is missing", value)
		}
	}
	return nil
}
