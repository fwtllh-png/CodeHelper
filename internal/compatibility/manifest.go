package compatibility

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
)

//go:embed compatibility.json
var manifestJSON []byte

type ProtocolRange struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

type Target struct {
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	VSCodeTarget string `json:"vscode_target"`
}

type Manifest struct {
	SchemaVersion          int           `json:"schema_version"`
	ExtensionVersion       string        `json:"extension_version"`
	BinaryVersionRange     string        `json:"binary_version_range"`
	ACPProtocol            ProtocolRange `json:"acp_protocol"`
	OperationSchemaVersion int           `json:"operation_schema_version"`
	RequiredMethods        []string      `json:"required_methods"`
	RequiredFeatures       []string      `json:"required_features"`
	Targets                []Target      `json:"targets"`
	Channels               []string      `json:"channels"`
}

func Load() (Manifest, error) {
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestJSON))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("decode compatibility manifest: %w", err)
	}
	if err := manifest.Validate(); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func MustLoad() Manifest {
	manifest, err := Load()
	if err != nil {
		panic(err)
	}
	return manifest
}

func (m Manifest) Validate() error {
	if m.SchemaVersion != 1 || m.ExtensionVersion == "" ||
		m.BinaryVersionRange == "" || m.ACPProtocol.Min < 1 ||
		m.ACPProtocol.Max < m.ACPProtocol.Min ||
		m.OperationSchemaVersion < 1 {
		return errors.New("compatibility manifest version fields are invalid")
	}
	if err := uniqueNonEmpty(m.RequiredMethods, "required method"); err != nil {
		return err
	}
	if err := uniqueNonEmpty(m.RequiredFeatures, "required feature"); err != nil {
		return err
	}
	if err := uniqueNonEmpty(m.Channels, "channel"); err != nil {
		return err
	}
	if len(m.Targets) == 0 {
		return errors.New("compatibility manifest requires targets")
	}
	targets := make(map[string]struct{}, len(m.Targets))
	for _, target := range m.Targets {
		if target.OS == "" || target.Arch == "" || target.VSCodeTarget == "" {
			return errors.New("compatibility target fields are required")
		}
		key := target.OS + "/" + target.Arch
		if _, exists := targets[key]; exists {
			return fmt.Errorf("duplicate compatibility target %s", key)
		}
		targets[key] = struct{}{}
	}
	return nil
}

func uniqueNonEmpty(values []string, name string) error {
	if len(values) == 0 {
		return fmt.Errorf("compatibility manifest requires %ss", name)
	}
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		if value == "" {
			return fmt.Errorf("compatibility %s is empty", name)
		}
		if _, exists := seen[value]; exists {
			return fmt.Errorf("duplicate compatibility %s %q", name, value)
		}
		seen[value] = struct{}{}
	}
	return nil
}
