package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

var credentialReferenceName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
var environmentVariableName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// CredentialSlot is a named non-secret credential reference.
type CredentialSlot struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
	Env  string `json:"env,omitempty"`
	Ref  string `json:"ref,omitempty"`
}

type credentialSlotFile struct {
	Slots []CredentialSlot `json:"slots"`
}

func credentialSlotsPath(configPath string) string {
	dir := filepath.Dir(configPath)
	return filepath.Join(dir, "credential-slots.json")
}

func loadCredentialSlots(configPath string) ([]CredentialSlot, error) {
	path := credentialSlotsPath(configPath)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var file credentialSlotFile
	if err := json.Unmarshal(data, &file); err != nil {
		return nil, err
	}
	return file.Slots, nil
}

func saveCredentialSlots(configPath string, slots []CredentialSlot) error {
	path := credentialSlotsPath(configPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	sort.Slice(slots, func(i, j int) bool { return slots[i].Name < slots[j].Name })
	data, err := json.MarshalIndent(credentialSlotFile{Slots: slots}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}

func setCredentialSlot(configPath string, slot CredentialSlot) error {
	if strings.TrimSpace(slot.Name) == "" {
		return fmt.Errorf("slot name is required")
	}
	if slot.Kind == "" {
		slot.Kind = "env"
	}
	if !oneOf(slot.Kind, "env", "file", "keyring") {
		return fmt.Errorf("kind must be env, file, or keyring")
	}
	if !credentialReferenceName.MatchString(slot.Name) {
		return fmt.Errorf("slot name must be a credential reference name")
	}
	switch slot.Kind {
	case "env":
		if !environmentVariableName.MatchString(slot.Env) || slot.Ref != "" {
			return fmt.Errorf("env slots require only a valid environment variable name")
		}
	case "file":
		if slot.Env != "" || !validCredentialFileReference(slot.Ref) {
			return fmt.Errorf("file slots require only a relative credential file reference")
		}
	case "keyring":
		if slot.Env != "" || !credentialReferenceName.MatchString(slot.Ref) {
			return fmt.Errorf("keyring slots require only a keyring entry name")
		}
	}
	slots, err := loadCredentialSlots(configPath)
	if err != nil {
		return err
	}
	replaced := false
	for i, existing := range slots {
		if existing.Name == slot.Name {
			slots[i] = slot
			replaced = true
			break
		}
	}
	if !replaced {
		slots = append(slots, slot)
	}
	return saveCredentialSlots(configPath, slots)
}

func clearCredentialSlot(configPath, name string) error {
	slots, err := loadCredentialSlots(configPath)
	if err != nil {
		return err
	}
	out := make([]CredentialSlot, 0, len(slots))
	for _, slot := range slots {
		if slot.Name == name {
			continue
		}
		out = append(out, slot)
	}
	return saveCredentialSlots(configPath, out)
}

func validCredentialFileReference(value string) bool {
	if value == "" || filepath.IsAbs(value) || filepath.Clean(value) != value {
		return false
	}
	return value != "." && value != ".." &&
		!strings.HasPrefix(value, ".."+string(filepath.Separator))
}
