package plugin

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/Masterminds/semver/v3"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const stateSchemaV1 = 1
const maxStateBytes = 4 << 20
const maxLifecycleReceipts = 64

// PluginState is the durable trust and enablement record for one plugin.
type PluginState struct {
	Receipt              Receipt           `json:"receipt"`
	Enabled              bool              `json:"enabled"`
	Source               RootKind          `json:"source"`
	StagedHash           string            `json:"staged_hash,omitempty"`
	Activation           *ActivationRecord `json:"activation,omitempty"`
	DisabledCapabilities map[string]bool   `json:"disabled_capabilities,omitempty"`
}

func (s *PluginState) UnmarshalJSON(data []byte) error {
	type wireState struct {
		Receipt              *Receipt          `json:"receipt"`
		Enabled              *bool             `json:"enabled"`
		Source               *RootKind         `json:"source"`
		StagedHash           string            `json:"staged_hash,omitempty"`
		Activation           *ActivationRecord `json:"activation,omitempty"`
		DisabledCapabilities map[string]bool   `json:"disabled_capabilities,omitempty"`
	}
	var wire wireState
	if err := decodeStrict(data, &wire); err != nil {
		return err
	}
	if wire.Receipt == nil || wire.Enabled == nil || wire.Source == nil {
		return errors.New("plugin state record is incomplete")
	}
	*s = PluginState{
		Receipt: *wire.Receipt, Enabled: *wire.Enabled,
		Source: *wire.Source, StagedHash: wire.StagedHash,
		Activation:           cloneActivation(wire.Activation),
		DisabledCapabilities: cloneBoolMap(wire.DisabledCapabilities),
	}
	return nil
}

// PersistentState is the complete on-disk registry state.
type PersistentState struct {
	SchemaVersion       int                           `json:"schema_version"`
	Plugins             map[string]PluginState        `json:"plugins"`
	LifecycleReceipts   map[string][]ActivationRecord `json:"lifecycle_receipts,omitempty"`
	SecurityRevocations map[string]uint64             `json:"security_revocations,omitempty"`
}

// StateStore serializes process and cross-process state transactions.
type StateStore struct {
	path      string
	base      string
	directory *sandbox.Workspace
	mu        sync.Mutex
}

func OpenStateStore(path string) (*StateStore, error) {
	if path == "" {
		return nil, errors.New("plugin state path is required")
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	parent, err := safeDirectory(filepath.Dir(absolute), false)
	if err != nil {
		return nil, fmt.Errorf("validate plugin state directory: %w", err)
	}
	directory, err := sandbox.NewWorkspace(parent)
	if err != nil {
		return nil, err
	}
	base := filepath.Base(absolute)
	return &StateStore{
		path: filepath.Join(parent, base), base: base, directory: directory,
	}, nil
}

func (s *StateStore) Path() string {
	if s == nil {
		return ""
	}
	return s.path
}

// Read returns an isolated snapshot and fails closed on malformed or unsafe
// state. A missing state file is an empty state.
func (s *StateStore) Read() (PersistentState, error) {
	if s == nil {
		return PersistentState{}, errors.New("plugin state store is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := acquireStateLock(s.path + ".lock")
	if err != nil {
		return PersistentState{}, err
	}
	defer lock.Close()
	return s.readUnlocked()
}

// Update applies one atomic, durable state transaction.
func (s *StateStore) Update(update func(*PersistentState) error) error {
	if s == nil || update == nil {
		return errors.New("plugin state update is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, err := acquireStateLock(s.path + ".lock")
	if err != nil {
		return err
	}
	defer lock.Close()
	state, err := s.readUnlocked()
	if err != nil {
		return err
	}
	if err := update(&state); err != nil {
		return err
	}
	if err := validatePersistentState(state); err != nil {
		return err
	}
	return s.writeUnlocked(state)
}

func (s *StateStore) readUnlocked() (PersistentState, error) {
	info, err := os.Lstat(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return PersistentState{
			SchemaVersion: stateSchemaV1, Plugins: make(map[string]PluginState),
			LifecycleReceipts:   make(map[string][]ActivationRecord),
			SecurityRevocations: make(map[string]uint64),
		}, nil
	}
	if err != nil {
		return PersistentState{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return PersistentState{}, errors.New("plugin state is not a regular file")
	}
	if err := rejectMultiplyLinked(s.path, info); err != nil {
		return PersistentState{}, err
	}
	file, err := s.directory.OpenFile(s.base)
	if err != nil {
		return PersistentState{}, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, maxStateBytes+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return PersistentState{}, err
	}
	if len(data) > maxStateBytes {
		return PersistentState{}, errors.New("plugin state exceeds size limit")
	}
	var state PersistentState
	if err := decodeStrict(data, &state); err != nil {
		return PersistentState{}, fmt.Errorf("decode plugin state: %w", err)
	}
	if err := validatePersistentState(state); err != nil {
		return PersistentState{}, err
	}
	return clonePersistentState(state), nil
}

func (s *StateStore) writeUnlocked(state PersistentState) error {
	data, err := json.Marshal(state)
	if err != nil {
		return err
	}
	return s.directory.AtomicWrite(s.base, data, 0o600)
}

func validatePersistentState(state PersistentState) error {
	if state.SchemaVersion != stateSchemaV1 || state.Plugins == nil {
		return errors.New("plugin state is missing or unsupported")
	}
	for name, record := range state.Plugins {
		if err := validatePluginName(name); err != nil {
			return fmt.Errorf("plugin state key %q: %w", name, err)
		}
		if err := validateReceipt(record.Receipt); err != nil {
			return fmt.Errorf("plugin state receipt %q: %w", name, err)
		}
		if record.Source > RootBuiltin {
			return fmt.Errorf("plugin state source %q is invalid", name)
		}
		if record.StagedHash != "" && !validContentAddress(record.StagedHash) {
			return fmt.Errorf("plugin state staged hash %q is invalid", name)
		}
		if record.Enabled && record.StagedHash == "" {
			return fmt.Errorf("enabled plugin %q has no staged content", name)
		}
		for capability, disabled := range record.DisabledCapabilities {
			if err := validatePluginName(capability); err != nil || !disabled {
				return fmt.Errorf(
					"plugin state capability %q for %q is invalid", capability, name,
				)
			}
		}
		if record.Activation != nil {
			if err := validateActivationRecord(*record.Activation); err != nil {
				return fmt.Errorf("plugin activation %q: %w", name, err)
			}
			active := record.Activation.Active
			if active.Name != name || record.Source != RootBuiltin ||
				record.Receipt.Trust != TrustSignedRegistry ||
				record.StagedHash != active.ContentHash ||
				record.Receipt.ContentHash != active.ContentHash ||
				record.Receipt.Version != active.Version ||
				record.Receipt.Publisher != active.Publisher {
				return fmt.Errorf("plugin activation %q does not match state", name)
			}
			receipts := state.LifecycleReceipts[name]
			if len(receipts) == 0 || len(receipts) > maxLifecycleReceipts {
				return fmt.Errorf("plugin lifecycle receipts %q are missing or excessive", name)
			}
			last := receipts[len(receipts)-1]
			if last.Active.ContentHash != active.ContentHash ||
				last.Action != record.Activation.Action ||
				!last.ChangedAt.Equal(record.Activation.ChangedAt) {
				return fmt.Errorf("plugin activation %q does not match receipt journal", name)
			}
		} else if record.Receipt.Trust == TrustSignedRegistry {
			return fmt.Errorf("signed plugin %q has no activation receipt", name)
		}
	}
	for name, receipts := range state.LifecycleReceipts {
		if err := validatePluginName(name); err != nil {
			return fmt.Errorf("plugin lifecycle key %q: %w", name, err)
		}
		if len(receipts) == 0 || len(receipts) > maxLifecycleReceipts {
			return fmt.Errorf("plugin lifecycle receipts %q are missing or excessive", name)
		}
		for _, receipt := range receipts {
			if err := validateActivationRecord(receipt); err != nil {
				return fmt.Errorf("plugin lifecycle receipt %q: %w", name, err)
			}
			if receipt.Active.Name != name {
				return fmt.Errorf("plugin lifecycle receipt %q has wrong identity", name)
			}
		}
	}
	for name, generation := range state.SecurityRevocations {
		if err := validatePluginName(name); err != nil || generation == 0 {
			return fmt.Errorf("plugin security revocation %q is invalid", name)
		}
		if _, active := state.Plugins[name]; active {
			return fmt.Errorf(
				"plugin security revocation %q conflicts with active state", name,
			)
		}
	}
	return nil
}

func validateReceipt(receipt Receipt) error {
	if receipt.SchemaVersion != 1 || receipt.Generation == 0 ||
		receipt.ReviewedAt.IsZero() {
		return errors.New("receipt is missing or unsupported")
	}
	for _, value := range []string{receipt.ContentHash, receipt.CapabilityHash} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != sha256.Size {
			return errors.New("receipt contains an invalid hash")
		}
	}
	switch receipt.Trust {
	case "", TrustUnsignedLocal:
		if (receipt.Version == "") != (receipt.Publisher == "") {
			return errors.New("unsigned-local receipt has partial package identity")
		}
		if receipt.Version != "" {
			if _, err := semver.StrictNewVersion(receipt.Version); err != nil {
				return errors.New("unsigned-local receipt version is not strict SemVer")
			}
			if err := validatePublisher(receipt.Publisher); err != nil {
				return err
			}
		}
		if receipt.ArtifactHash != "" || receipt.ManifestHash != "" ||
			receipt.Signature != "" {
			return errors.New("unsigned-local receipt contains registry signature identity")
		}
	case TrustSignedRegistry:
		if _, err := semver.StrictNewVersion(receipt.Version); err != nil {
			return errors.New("signed receipt version is not strict SemVer")
		}
		if err := validatePublisher(receipt.Publisher); err != nil {
			return err
		}
		for _, value := range []string{receipt.ArtifactHash, receipt.ManifestHash} {
			decoded, err := hex.DecodeString(value)
			if err != nil || len(decoded) != sha256.Size {
				return errors.New("signed receipt contains an invalid hash")
			}
		}
		signature, err := base64.StdEncoding.DecodeString(receipt.Signature)
		if err != nil || len(signature) != ed25519.SignatureSize {
			return errors.New("signed receipt contains an invalid signature")
		}
	default:
		return errors.New("receipt trust mode is invalid")
	}
	return nil
}

func clonePersistentState(state PersistentState) PersistentState {
	result := PersistentState{
		SchemaVersion:       state.SchemaVersion,
		Plugins:             make(map[string]PluginState, len(state.Plugins)),
		LifecycleReceipts:   make(map[string][]ActivationRecord, len(state.LifecycleReceipts)),
		SecurityRevocations: make(map[string]uint64, len(state.SecurityRevocations)),
	}
	for name, record := range state.Plugins {
		record.Activation = cloneActivation(record.Activation)
		record.DisabledCapabilities = cloneBoolMap(record.DisabledCapabilities)
		result.Plugins[name] = record
	}
	for name, receipts := range state.LifecycleReceipts {
		result.LifecycleReceipts[name] = cloneActivationReceipts(receipts)
	}
	for name, generation := range state.SecurityRevocations {
		result.SecurityRevocations[name] = generation
	}
	return result
}

func cloneBoolMap(values map[string]bool) map[string]bool {
	if values == nil {
		return nil
	}
	result := make(map[string]bool, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func cloneActivationReceipts(values []ActivationRecord) []ActivationRecord {
	if values == nil {
		return nil
	}
	result := make([]ActivationRecord, len(values))
	for index := range values {
		cloned := cloneActivation(&values[index])
		result[index] = *cloned
	}
	return result
}

func cloneActivation(value *ActivationRecord) *ActivationRecord {
	if value == nil {
		return nil
	}
	result := *value
	result.Previous = append([]VerifiedRelease(nil), value.Previous...)
	return &result
}
