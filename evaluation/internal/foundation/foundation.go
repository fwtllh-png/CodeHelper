package foundation

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

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/oracle"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/replay"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

const SchemaVersion = 2

type Manifest struct {
	SchemaVersion       int           `json:"schema_version"`
	ID                  string        `json:"id"`
	Status              string        `json:"status"`
	Contracts           []ContractRef `json:"contracts"`
	OracleCatalog       string        `json:"oracle_catalog"`
	MutationCatalog     string        `json:"mutation_catalog"`
	CorePack            string        `json:"core_pack"`
	ImpactPolicy        string        `json:"impact_policy"`
	ImplementationRoots []string      `json:"implementation_roots"`
}

type ContractRef struct {
	ID            string `json:"id"`
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	Owner         string `json:"owner"`
	Producer      string `json:"producer"`
	Consumer      string `json:"consumer"`
	AbsenceStatus string `json:"absence_status"`
}

type OracleCatalog struct {
	SchemaVersion int              `json:"schema_version"`
	Oracles       []OracleContract `json:"oracles"`
}

type OracleContract struct {
	ID              string   `json:"id"`
	Evidence        []string `json:"evidence"`
	Producers       []string `json:"producers"`
	AbsenceStatus   string   `json:"absence_status"`
	ProvedZero      bool     `json:"proved_zero"`
	NegativeControl string   `json:"negative_control"`
}

type MutationCatalog struct {
	SchemaVersion int                `json:"schema_version"`
	Mutations     []MutationContract `json:"mutations"`
}

type MutationContract struct {
	ID                  replay.MutationKind `json:"id"`
	ReplayLevel         replay.Level        `json:"replay_level"`
	MinimumExecutions   int                 `json:"minimum_executions"`
	ExpectedObservation string              `json:"expected_observation"`
}

type Bundle struct {
	Root             string
	Manifest         Manifest
	Oracles          OracleCatalog
	Mutations        MutationCatalog
	HarnessInputHash string
}

func Load(root, manifestPath string) (Bundle, error) {
	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return Bundle{}, err
	}
	var manifest Manifest
	manifestFile, err := resolveWithin(absoluteRoot, manifestPath)
	if err != nil {
		return Bundle{}, err
	}
	manifestRaw, err := decodeFile(manifestFile, &manifest)
	if err != nil {
		return Bundle{}, fmt.Errorf("decode Foundation manifest: %w", err)
	}
	var oracles OracleCatalog
	oracleFile, err := resolveWithin(absoluteRoot, manifest.OracleCatalog)
	if err != nil {
		return Bundle{}, err
	}
	oracleRaw, err := decodeFile(oracleFile, &oracles)
	if err != nil {
		return Bundle{}, fmt.Errorf("decode Oracle catalog: %w", err)
	}
	var mutations MutationCatalog
	mutationFile, err := resolveWithin(absoluteRoot, manifest.MutationCatalog)
	if err != nil {
		return Bundle{}, err
	}
	mutationRaw, err := decodeFile(mutationFile, &mutations)
	if err != nil {
		return Bundle{}, fmt.Errorf("decode Mutation catalog: %w", err)
	}
	bundle := Bundle{
		Root: absoluteRoot, Manifest: manifest,
		Oracles: oracles, Mutations: mutations,
	}
	if err := bundle.Validate(); err != nil {
		return Bundle{}, err
	}
	parts := []string{
		string(manifestRaw),
		string(oracleRaw),
		string(mutationRaw),
	}
	for _, contract := range manifest.Contracts {
		path, err := resolveWithin(absoluteRoot, contract.Path)
		if err != nil {
			return Bundle{}, err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return Bundle{}, err
		}
		parts = append(parts, contract.Path+"\x00"+string(content))
	}
	for _, path := range []string{manifest.CorePack, manifest.ImpactPolicy} {
		resolved, err := resolveWithin(absoluteRoot, path)
		if err != nil {
			return Bundle{}, err
		}
		content, err := os.ReadFile(resolved)
		if err != nil {
			return Bundle{}, err
		}
		parts = append(parts, path+"\x00"+string(content))
	}
	for _, root := range manifest.ImplementationRoots {
		resolved, err := resolveWithin(absoluteRoot, root)
		if err != nil {
			return Bundle{}, err
		}
		if err := filepath.WalkDir(
			resolved,
			func(path string, entry os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				if entry.IsDir() || filepath.Ext(path) != ".go" {
					return nil
				}
				content, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				relative, err := filepath.Rel(absoluteRoot, path)
				if err != nil {
					return err
				}
				parts = append(
					parts,
					filepath.ToSlash(relative)+"\x00"+string(content),
				)
				return nil
			},
		); err != nil {
			return Bundle{}, err
		}
	}
	slices.Sort(parts)
	bundle.HarnessInputHash = spec.DigestString(strings.Join(parts, "\x00"))
	return bundle, nil
}

func (b Bundle) Validate() error {
	if b.Manifest.SchemaVersion != SchemaVersion ||
		b.Oracles.SchemaVersion != SchemaVersion ||
		b.Mutations.SchemaVersion != SchemaVersion {
		return fmt.Errorf("Foundation contracts require schema_version %d", SchemaVersion)
	}
	if b.Manifest.ID != "production-evaluation-foundation" ||
		b.Manifest.Status != "implementation" {
		return errors.New("Foundation identity or status is invalid")
	}
	if len(b.Manifest.Contracts) == 0 {
		return errors.New("Foundation contract inventory is empty")
	}
	seenContracts := make(map[string]struct{}, len(b.Manifest.Contracts))
	for _, contract := range b.Manifest.Contracts {
		if !validID(contract.ID) || !validID(contract.Kind) ||
			!validID(contract.Owner) || !validID(contract.Producer) ||
			!validID(contract.Consumer) ||
			contract.AbsenceStatus != string(spec.StatusInvalid) ||
			!validRelativePath(contract.Path) {
			return fmt.Errorf("Foundation contract %q is invalid", contract.ID)
		}
		if _, exists := seenContracts[contract.ID]; exists {
			return fmt.Errorf("duplicate Foundation contract %q", contract.ID)
		}
		seenContracts[contract.ID] = struct{}{}
	}
	if err := validateOracleCatalog(b.Oracles); err != nil {
		return err
	}
	if err := validateMutationCatalog(b.Mutations); err != nil {
		return err
	}
	for _, path := range []string{
		b.Manifest.OracleCatalog,
		b.Manifest.MutationCatalog,
		b.Manifest.CorePack,
		b.Manifest.ImpactPolicy,
	} {
		if !validRelativePath(path) {
			return fmt.Errorf("Foundation path %q is invalid", path)
		}
	}
	if len(b.Manifest.ImplementationRoots) == 0 {
		return errors.New("Foundation implementation root inventory is empty")
	}
	for _, path := range b.Manifest.ImplementationRoots {
		if !validRelativePath(path) {
			return fmt.Errorf("Foundation implementation root %q is invalid", path)
		}
	}
	return nil
}

func validateOracleCatalog(catalog OracleCatalog) error {
	seen := make(map[string]struct{}, len(catalog.Oracles))
	for _, contract := range catalog.Oracles {
		if !oracle.IsOracleID(contract.ID) ||
			len(contract.Evidence) == 0 ||
			len(contract.Producers) == 0 ||
			contract.AbsenceStatus != string(spec.StatusInvalid) ||
			!validID(contract.NegativeControl) {
			return fmt.Errorf("Oracle contract %q is invalid", contract.ID)
		}
		if _, exists := seen[contract.ID]; exists {
			return fmt.Errorf("duplicate Oracle contract %q", contract.ID)
		}
		seen[contract.ID] = struct{}{}
	}
	for _, id := range oracle.AllIDs {
		if _, exists := seen[id]; !exists {
			return fmt.Errorf("Oracle catalog omits %q", id)
		}
	}
	return nil
}

func validateMutationCatalog(catalog MutationCatalog) error {
	seen := make(map[replay.MutationKind]struct{}, len(catalog.Mutations))
	required := []replay.MutationKind{
		replay.MutationSplit, replay.MutationDelay,
		replay.MutationDuplicate, replay.MutationTruncate,
		replay.MutationInterrupt, replay.MutationUnknown,
		replay.MutationMalformed,
	}
	for _, contract := range catalog.Mutations {
		if !slices.Contains(required, contract.ID) ||
			contract.MinimumExecutions < 1 ||
			!validID(contract.ExpectedObservation) {
			return fmt.Errorf("Mutation contract %q is invalid", contract.ID)
		}
		switch contract.ReplayLevel {
		case replay.LevelProvider, replay.LevelRuntime, replay.LevelHost:
		default:
			return fmt.Errorf("Mutation %q has invalid Replay level", contract.ID)
		}
		if _, exists := seen[contract.ID]; exists {
			return fmt.Errorf("duplicate Mutation contract %q", contract.ID)
		}
		seen[contract.ID] = struct{}{}
	}
	for _, id := range required {
		if _, exists := seen[id]; !exists {
			return fmt.Errorf("Mutation catalog omits %q", id)
		}
	}
	return nil
}

func decodeFile(path string, target any) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return nil, err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("document must contain exactly one JSON value")
	}
	return data, nil
}

func resolveWithin(root, relative string) (string, error) {
	if !validRelativePath(relative) {
		return "", fmt.Errorf("path %q is not repository-relative", relative)
	}
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	back, err := filepath.Rel(root, absolute)
	if err != nil || back == ".." || strings.HasPrefix(filepath.ToSlash(back), "../") {
		return "", fmt.Errorf("path %q escapes repository root", relative)
	}
	return absolute, nil
}

func validRelativePath(value string) bool {
	return strings.TrimSpace(value) != "" &&
		!filepath.IsAbs(value) &&
		value != ".." &&
		!strings.HasPrefix(value, "../") &&
		!strings.Contains(value, "\\") &&
		filepath.ToSlash(filepath.Clean(value)) == value
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
