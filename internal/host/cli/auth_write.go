package cli

import (
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"
)

func writeCredentialConfig(path, kind, name string) error {
	if path == "" {
		return fmt.Errorf("config path is required")
	}
	doc := map[string]any{}
	if data, err := os.ReadFile(path); err == nil {
		if err := toml.Unmarshal(data, &doc); err != nil {
			return fmt.Errorf("parse config: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if kind == "" && name == "" {
		delete(doc, "credential")
	} else {
		doc["credential"] = map[string]any{"kind": kind, "name": name}
	}
	out, err := toml.Marshal(doc)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, out, 0o600)
}
