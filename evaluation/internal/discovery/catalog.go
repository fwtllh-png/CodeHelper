package discovery

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/corepack"
)

const SchemaVersion = 1

type Catalog struct {
	SchemaVersion int        `json:"schema_version"`
	Scenarios     []Binding  `json:"scenarios"`
	Hosts         []HostCase `json:"hosts"`
}

type Binding struct {
	ID           string   `json:"id"`
	Host         string   `json:"host"`
	Command      []string `json:"command"`
	Proves       []string `json:"proves"`
	MinimumTests int      `json:"minimum_tests,omitempty"`
}

type HostCase struct {
	ID           string   `json:"id"`
	Host         string   `json:"host"`
	Command      []string `json:"command"`
	MinimumTests int      `json:"minimum_tests,omitempty"`
}

func Load(root, path string, pack corepack.Pack) (Catalog, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Catalog{}, err
	}
	absolutePath := path
	if !filepath.IsAbs(absolutePath) {
		absolutePath = filepath.Join(absoluteRoot, filepath.FromSlash(path))
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return Catalog{}, errors.New("D1 catalog escapes repository root")
	}
	raw, err := os.ReadFile(absolutePath)
	if err != nil {
		return Catalog{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var catalog Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return Catalog{}, fmt.Errorf("decode D1 catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return Catalog{}, errors.New("D1 catalog contains multiple values")
	}
	if err := catalog.Validate(pack); err != nil {
		return Catalog{}, err
	}
	return catalog, nil
}

func (c Catalog) Validate(pack corepack.Pack) error {
	if c.SchemaVersion != SchemaVersion {
		return fmt.Errorf(
			"D1 catalog schema_version must be %d",
			SchemaVersion,
		)
	}
	expected := make(map[string]corepack.Scenario, len(pack.Scenarios))
	for _, scenario := range pack.Scenarios {
		expected[scenario.ID] = scenario
	}
	seen := make(map[string]struct{}, len(c.Scenarios))
	commands := make(map[string]string, len(c.Scenarios))
	for _, binding := range c.Scenarios {
		scenario, exists := expected[binding.ID]
		if !exists {
			return fmt.Errorf("D1 binding %q has no Core Scenario", binding.ID)
		}
		if _, duplicate := seen[binding.ID]; duplicate {
			return fmt.Errorf("duplicate D1 binding %q", binding.ID)
		}
		seen[binding.ID] = struct{}{}
		if !validID(binding.Host) || len(binding.Command) < 2 ||
			!validCommand(binding.Command) ||
			binding.Command[0] != "go" || binding.Command[1] != "test" ||
			binding.MinimumTests < 0 {
			return fmt.Errorf("D1 binding %q is invalid", binding.ID)
		}
		proves := append([]string(nil), binding.Proves...)
		slices.Sort(proves)
		expectedFacts := append([]string(nil), scenario.ExpectedFacts...)
		slices.Sort(expectedFacts)
		if !slices.Equal(proves, expectedFacts) {
			return fmt.Errorf(
				"D1 binding %q does not prove its expected facts",
				binding.ID,
			)
		}
		commandKey := strings.Join(binding.Command, "\x00")
		if owner, duplicate := commands[commandKey]; duplicate {
			return fmt.Errorf(
				"D1 bindings %q and %q reuse one verification",
				owner,
				binding.ID,
			)
		}
		commands[commandKey] = binding.ID
	}
	if len(seen) != len(expected) {
		return fmt.Errorf(
			"D1 catalog covers %d of %d Core Scenarios",
			len(seen),
			len(expected),
		)
	}

	requiredHosts := []string{"acp", "cli", "tui", "vscode", "worker"}
	hostIDs := make(map[string]struct{}, len(c.Hosts))
	hostKinds := make(map[string]struct{}, len(c.Hosts))
	for _, host := range c.Hosts {
		if !validID(host.ID) || !validID(host.Host) ||
			!validCommand(host.Command) || host.MinimumTests < 0 {
			return fmt.Errorf("D1 Host Case %q is invalid", host.ID)
		}
		if _, duplicate := hostIDs[host.ID]; duplicate {
			return fmt.Errorf("duplicate D1 Host Case %q", host.ID)
		}
		hostIDs[host.ID] = struct{}{}
		hostKinds[host.Host] = struct{}{}
	}
	for _, required := range requiredHosts {
		if _, exists := hostKinds[required]; !exists {
			return fmt.Errorf("D1 catalog has no %s Host Case", required)
		}
	}
	return nil
}

func validCommand(command []string) bool {
	if len(command) == 0 {
		return false
	}
	for _, argument := range command {
		if strings.TrimSpace(argument) == "" ||
			strings.ContainsRune(argument, '\x00') {
			return false
		}
	}
	return true
}

func validID(value string) bool {
	if value == "" || len(value) > 96 {
		return false
	}
	for index, character := range value {
		if character >= 'a' && character <= 'z' ||
			character >= '0' && character <= '9' ||
			index > 0 && (character == '-' || character == '_') {
			continue
		}
		return false
	}
	return true
}
