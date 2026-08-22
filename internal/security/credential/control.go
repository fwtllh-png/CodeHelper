package credential

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/fwtllh-png/CodeHelper/internal/security/keyring"
)

const controlVersion = 1

type controlConfig struct {
	Version    int       `json:"version"`
	Generation uint64    `json:"generation"`
	Reference  Reference `json:"reference"`
}

type recoveryIntent struct {
	Version            int       `json:"version"`
	OperationID        string    `json:"operation_id"`
	ExpectedGeneration uint64    `json:"expected_generation"`
	OldReference       Reference `json:"old_reference"`
	NewReference       Reference `json:"new_reference"`
	Phase              string    `json:"phase"`
}

type Control struct {
	mu        sync.Mutex
	root      string
	registry  string
	namespace string
	base      Reference
	keyring   store
}

func OpenControl(
	ctx context.Context,
	dataDir, workspaceID, provider string,
	base Reference,
) (*Control, Reference, error) {
	if err := ctx.Err(); err != nil {
		return nil, Reference{}, err
	}
	if strings.TrimSpace(dataDir) == "" ||
		strings.TrimSpace(workspaceID) == "" ||
		strings.TrimSpace(provider) == "" {
		return nil, Reference{}, errors.New(
			"credential control data directory, workspace, and provider are required",
		)
	}
	digest := sha256.Sum256([]byte(workspaceID + "\x00" + provider))
	registry := filepath.Join(dataDir, "credential-control")
	control := &Control{
		root:      filepath.Join(registry, hex.EncodeToString(digest[:16])),
		registry:  registry,
		namespace: hex.EncodeToString(digest[:8]),
		base:      base,
		keyring:   keyringStore(),
	}
	if err := control.reconcile(ctx); err != nil {
		return nil, Reference{}, err
	}
	config, err := control.readConfig()
	if err != nil {
		return nil, Reference{}, err
	}
	if config.Generation == 0 {
		return control, base, nil
	}
	return control, config.Reference, nil
}

func keyringStore() store {
	return keyring.New()
}

func (c *Control) rotate(
	ctx context.Context,
	current Reference,
	secret string,
) (Reference, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return Reference{}, err
	}
	config, err := c.readConfig()
	if err != nil {
		return Reference{}, err
	}
	if config.Generation == 0 {
		config.Reference = c.base
	}
	if config.Reference != current {
		return Reference{}, errors.New("credential config generation changed")
	}
	operationID, err := randomID()
	if err != nil {
		return Reference{}, err
	}
	next := Reference{
		Kind: "keyring",
		Name: "web/" + c.namespace + "/" + operationID,
	}
	intent := recoveryIntent{
		Version:            controlVersion,
		OperationID:        operationID,
		ExpectedGeneration: config.Generation,
		OldReference:       current,
		NewReference:       next,
		Phase:              "prepared",
	}
	if err := c.writeIntent(intent); err != nil {
		return Reference{}, err
	}
	if err := c.keyring.Set(next.Name, secret); err != nil {
		return Reference{}, err
	}
	if err := c.writeConfigCAS(config.Generation, next); err != nil {
		_ = c.keyring.Delete(next.Name)
		return Reference{}, err
	}
	intent.Phase = "config_committed"
	if err := c.writeIntent(intent); err != nil {
		return Reference{}, err
	}
	return next, nil
}

func (c *Control) clear(
	ctx context.Context,
	current Reference,
) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	config, err := c.readConfig()
	if err != nil {
		return err
	}
	if config.Generation == 0 {
		config.Reference = c.base
	}
	if config.Reference != current {
		return errors.New("credential config generation changed")
	}
	operationID, err := randomID()
	if err != nil {
		return err
	}
	intent := recoveryIntent{
		Version:            controlVersion,
		OperationID:        operationID,
		ExpectedGeneration: config.Generation,
		OldReference:       current,
		Phase:              "prepared",
	}
	if err := c.writeIntent(intent); err != nil {
		return err
	}
	if err := c.writeConfigCAS(config.Generation, Reference{}); err != nil {
		return err
	}
	intent.Phase = "config_committed"
	return c.writeIntent(intent)
}

func (c *Control) reconcile(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	entries, err := os.ReadDir(c.intentDir())
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read credential recovery intents: %w", err)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	config, err := c.readConfig()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		path := filepath.Join(c.intentDir(), entry.Name())
		var intent recoveryIntent
		if err := readJSON(path, &intent); err != nil {
			return fmt.Errorf("read credential recovery intent: %w", err)
		}
		if intent.Version != controlVersion ||
			intent.OperationID == "" ||
			(intent.NewReference != (Reference{}) &&
				!c.owns(intent.NewReference)) ||
			(intent.Phase != "prepared" &&
				intent.Phase != "config_committed" &&
				intent.Phase != "completed") {
			return errors.New("credential recovery intent is invalid")
		}
		if intent.Phase == "completed" {
			continue
		}
		committed := config.Generation > intent.ExpectedGeneration &&
			config.Reference == intent.NewReference
		if !committed {
			if intent.NewReference.Kind == "keyring" {
				if err := c.keyring.Delete(intent.NewReference.Name); err != nil {
					return fmt.Errorf(
						"remove uncommitted credential: %w",
						err,
					)
				}
			}
			intent.Phase = "completed"
			if err := writeJSONAtomic(path, intent); err != nil {
				return err
			}
			continue
		}
		canDelete, err := c.canDelete(intent.OldReference)
		if err != nil {
			return err
		}
		if canDelete && intent.OldReference != intent.NewReference {
			if err := c.keyring.Delete(intent.OldReference.Name); err != nil {
				return err
			}
		}
		intent.Phase = "completed"
		if err := writeJSONAtomic(path, intent); err != nil {
			return err
		}
	}
	return nil
}

func (c *Control) canDelete(reference Reference) (bool, error) {
	if !c.owns(reference) {
		return false, nil
	}
	registry := c.registry
	if registry == "" {
		registry = filepath.Dir(c.root)
	}
	entries, err := os.ReadDir(registry)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		var config controlConfig
		err := readJSON(
			filepath.Join(registry, entry.Name(), "config.json"),
			&config,
		)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return false, err
		}
		if config.Reference == reference {
			return false, nil
		}
	}
	return true, nil
}

func (c *Control) owns(reference Reference) bool {
	return reference.Kind == "keyring" &&
		strings.HasPrefix(reference.Name, "web/"+c.namespace+"/") &&
		len(strings.TrimPrefix(reference.Name, "web/"+c.namespace+"/")) == 32
}

func (c *Control) readConfig() (controlConfig, error) {
	var config controlConfig
	err := readJSON(c.configPath(), &config)
	if errors.Is(err, os.ErrNotExist) {
		return controlConfig{Version: controlVersion}, nil
	}
	if err != nil {
		return controlConfig{}, fmt.Errorf("read credential config: %w", err)
	}
	if config.Version != controlVersion || config.Generation == 0 ||
		(config.Reference != (Reference{}) && !c.owns(config.Reference)) {
		return controlConfig{}, errors.New("credential config is invalid")
	}
	return config, nil
}

func (c *Control) writeConfigCAS(expected uint64, reference Reference) error {
	current, err := c.readConfig()
	if err != nil {
		return err
	}
	if current.Generation != expected {
		return errors.New("credential config generation changed")
	}
	return writeJSONAtomic(c.configPath(), controlConfig{
		Version:    controlVersion,
		Generation: expected + 1,
		Reference:  reference,
	})
}

func (c *Control) writeIntent(intent recoveryIntent) error {
	return writeJSONAtomic(
		filepath.Join(c.intentDir(), intent.OperationID+".json"),
		intent,
	)
}

func (c *Control) configPath() string {
	return filepath.Join(c.root, "config.json")
}

func (c *Control) intentDir() string {
	return filepath.Join(c.root, "intents")
}

func randomID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

func readJSON(path string, target any) error {
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return errors.New("credential control file must be regular")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	return nil
}

func writeJSONAtomic(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	temp, err := os.OpenFile(
		path+".tmp",
		os.O_CREATE|os.O_EXCL|os.O_WRONLY,
		0o600,
	)
	if errors.Is(err, os.ErrExist) {
		if removeErr := os.Remove(path + ".tmp"); removeErr != nil {
			return errors.Join(err, removeErr)
		}
		temp, err = os.OpenFile(
			path+".tmp",
			os.O_CREATE|os.O_EXCL|os.O_WRONLY,
			0o600,
		)
	}
	if err != nil {
		return err
	}
	tempPath := temp.Name()
	cleanup := func() {
		_ = temp.Close()
		_ = os.Remove(tempPath)
	}
	if _, err := temp.Write(data); err != nil {
		cleanup()
		return err
	}
	if err := temp.Sync(); err != nil {
		cleanup()
		return err
	}
	if err := temp.Close(); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	if err := os.Rename(tempPath, path); err != nil {
		_ = os.Remove(tempPath)
		return err
	}
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}
