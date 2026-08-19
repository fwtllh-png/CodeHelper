package admission

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
)

const H1SchemaVersion = 1

type H1Catalog struct {
	SchemaVersion int      `json:"schema_version"`
	Cases         []H1Case `json:"cases"`
}

type H1Case struct {
	ID           string   `json:"id"`
	Lane         string   `json:"lane"`
	Kind         string   `json:"kind"`
	Command      []string `json:"command"`
	Environment  []string `json:"environment,omitempty"`
	MinimumTests int      `json:"minimum_tests,omitempty"`
}

func LoadH1(root, path string) (H1Catalog, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return H1Catalog{}, err
	}
	absolutePath := path
	if !filepath.IsAbs(absolutePath) {
		absolutePath = filepath.Join(absoluteRoot, filepath.FromSlash(path))
	}
	relative, err := filepath.Rel(absoluteRoot, absolutePath)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return H1Catalog{}, errors.New("H1 catalog escapes repository root")
	}
	raw, err := os.ReadFile(absolutePath)
	if err != nil {
		return H1Catalog{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var catalog H1Catalog
	if err := decoder.Decode(&catalog); err != nil {
		return H1Catalog{}, fmt.Errorf("decode H1 catalog: %w", err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return H1Catalog{}, errors.New("H1 catalog contains multiple values")
	}
	if err := catalog.Validate(); err != nil {
		return H1Catalog{}, err
	}
	return catalog, nil
}

func (c H1Catalog) Validate() error {
	if c.SchemaVersion != H1SchemaVersion {
		return fmt.Errorf(
			"H1 catalog schema_version must be %d",
			H1SchemaVersion,
		)
	}
	if len(c.Cases) < 18 {
		return errors.New("H1 catalog has an incomplete denominator")
	}
	seen := make(map[string]struct{}, len(c.Cases))
	commands := make(map[string]string, len(c.Cases))
	lanes := make(map[string]struct{})
	for _, item := range c.Cases {
		if !validID(item.ID) || !validID(item.Lane) ||
			!slices.Contains(
				[]string{"go_test", "electron", "vscode_runtime"},
				item.Kind,
			) ||
			len(item.Command) == 0 || item.MinimumTests < 0 {
			return fmt.Errorf("H1 case %q is invalid", item.ID)
		}
		if item.Kind == "go_test" &&
			(len(item.Command) < 2 ||
				item.Command[0] != "go" ||
				item.Command[1] != "test") {
			return fmt.Errorf("H1 Go case %q is not a Go test", item.ID)
		}
		if _, duplicate := seen[item.ID]; duplicate {
			return fmt.Errorf("duplicate H1 case %q", item.ID)
		}
		seen[item.ID] = struct{}{}
		lanes[item.Lane] = struct{}{}
		for _, value := range append(
			append([]string(nil), item.Command...),
			item.Environment...,
		) {
			if strings.TrimSpace(value) == "" ||
				strings.ContainsRune(value, '\x00') {
				return fmt.Errorf("H1 case %q contains invalid text", item.ID)
			}
		}
		key := strings.Join(item.Command, "\x00")
		if owner, duplicate := commands[key]; duplicate {
			return fmt.Errorf(
				"H1 cases %q and %q reuse one verification",
				owner,
				item.ID,
			)
		}
		commands[key] = item.ID
	}
	for _, lane := range []string{
		"extension_host",
		"process",
		"provider",
		"persistence",
		"filesystem",
	} {
		if _, exists := lanes[lane]; !exists {
			return fmt.Errorf("H1 catalog has no %s lane", lane)
		}
	}
	return nil
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
