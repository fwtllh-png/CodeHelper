package plugin

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const (
	activationSchemaV1    = 1
	maxActivationHistory  = 16
	maxCompressedArtifact = 64 << 20
)

// VerifiedRelease is immutable evidence created only after signature, archive,
// manifest, compatibility, capability, and content checks have all passed.
type VerifiedRelease struct {
	Name             string    `json:"name"`
	Version          string    `json:"version"`
	Publisher        string    `json:"publisher"`
	Generation       uint64    `json:"generation"`
	IndexGeneration  uint64    `json:"index_generation"`
	ArtifactSHA256   string    `json:"artifact_sha256"`
	ManifestSHA256   string    `json:"manifest_sha256"`
	CapabilitySHA256 string    `json:"capability_sha256"`
	ContentHash      string    `json:"content_hash"`
	Signature        string    `json:"signature"`
	VerifiedAt       time.Time `json:"verified_at"`
}

func (release VerifiedRelease) receipt() Receipt {
	return Receipt{
		SchemaVersion: 1, ContentHash: release.ContentHash,
		CapabilityHash: release.CapabilitySHA256,
		Generation:     release.Generation, ReviewedAt: release.VerifiedAt,
		Trust: TrustSignedRegistry, Version: release.Version,
		Publisher: release.Publisher, ArtifactHash: release.ArtifactSHA256,
		ManifestHash: release.ManifestSHA256, Signature: release.Signature,
	}
}

func (release VerifiedRelease) registryRelease() RegistryRelease {
	return RegistryRelease{
		SchemaVersion: 1, Name: release.Name, Version: release.Version,
		Generation: release.Generation, Publisher: release.Publisher,
		Artifact: "verified", ArtifactSHA256: release.ArtifactSHA256,
		ManifestSHA256:   release.ManifestSHA256,
		CapabilitySHA256: release.CapabilitySHA256,
		Signature:        release.Signature,
	}
}

type ActivationRecord struct {
	SchemaVersion      int               `json:"schema_version"`
	Active             VerifiedRelease   `json:"active"`
	Previous           []VerifiedRelease `json:"previous,omitempty"`
	MaxIndexGeneration uint64            `json:"max_index_generation"`
	MaxGeneration      uint64            `json:"max_generation"`
	MaxVersion         string            `json:"max_version"`
	Action             string            `json:"action"`
	ChangedAt          time.Time         `json:"changed_at"`
}

type DistributionConfig struct {
	Source         RegistrySource
	Publishers     map[string]ed25519.PublicKey
	CacheRoot      string
	Stager         *Stager
	RuntimeVersion string
	Now            func() time.Time
}

// Distributor verifies Registry releases and atomically publishes an
// activation pointer to immutable staged content.
type Distributor struct {
	config DistributionConfig
	cache  *sandbox.Workspace
	mu     sync.Mutex
}

func NewDistributor(config DistributionConfig) (*Distributor, error) {
	if len(config.Publishers) == 0 {
		return nil, errors.New("plugin publisher allowlist is empty")
	}
	if config.Stager == nil {
		return nil, errors.New("plugin distributor requires a stager")
	}
	if strings.TrimSpace(config.CacheRoot) == "" {
		return nil, errors.New("plugin artifact cache root is required")
	}
	if err := os.MkdirAll(config.CacheRoot, 0o700); err != nil {
		return nil, err
	}
	cacheRoot, err := safeDirectory(config.CacheRoot, false)
	if err != nil {
		return nil, fmt.Errorf("validate plugin artifact cache: %w", err)
	}
	cache, err := sandbox.NewWorkspace(cacheRoot)
	if err != nil {
		return nil, err
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	config.CacheRoot = cacheRoot
	publishers := make(map[string]ed25519.PublicKey, len(config.Publishers))
	for name, key := range config.Publishers {
		if err := validatePublisher(name); err != nil ||
			len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("plugin publisher %q is invalid", name)
		}
		publishers[name] = append(ed25519.PublicKey(nil), key...)
	}
	config.Publishers = publishers
	return &Distributor{config: config, cache: cache}, nil
}

func (d *Distributor) ResolveAndStage(
	ctx context.Context,
	name, version string,
) (VerifiedRelease, error) {
	if d == nil {
		return VerifiedRelease{}, errors.New("plugin distributor is required")
	}
	if ctx == nil {
		return VerifiedRelease{}, errors.New("plugin distribution context is required")
	}
	if strings.TrimSpace(d.config.Source.URL) == "" {
		return VerifiedRelease{}, errors.New("plugin Registry URL is required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return VerifiedRelease{}, err
	}
	registry, err := d.config.Source.fetchIndex(ctx)
	if err != nil {
		return VerifiedRelease{}, err
	}
	for _, indexed := range registry.Index.Releases {
		if err := VerifyRegistryRelease(indexed, d.config.Publishers); err != nil {
			return VerifiedRelease{}, err
		}
	}
	release, err := registry.selectRelease(name, version)
	if err != nil {
		return VerifiedRelease{}, err
	}
	if err := VerifyRegistryRelease(release, d.config.Publishers); err != nil {
		return VerifiedRelease{}, err
	}
	artifactURL, err := registry.artifactURL(release.Artifact)
	if err != nil {
		return VerifiedRelease{}, err
	}
	artifact, cached, err := d.cachedArtifact(release.ArtifactSHA256)
	if err != nil {
		return VerifiedRelease{}, err
	}
	if !cached {
		artifact, err = d.config.Source.read(ctx, artifactURL, maxCompressedArtifact)
		if err != nil {
			return VerifiedRelease{}, err
		}
	}
	if err := ctx.Err(); err != nil {
		return VerifiedRelease{}, err
	}
	if !equalHash(hashBytes(artifact), release.ArtifactSHA256) {
		return VerifiedRelease{}, fmt.Errorf(
			"%w: plugin artifact digest does not match Registry", ErrDigestMismatch,
		)
	}
	if !cached {
		if err := d.cacheArtifact(release.ArtifactSHA256, artifact); err != nil {
			return VerifiedRelease{}, err
		}
	}
	extracted, err := os.MkdirTemp(d.config.CacheRoot, ".extract-*")
	if err != nil {
		return VerifiedRelease{}, err
	}
	defer os.RemoveAll(extracted)
	if err := extractPluginArtifact(artifact, extracted); err != nil {
		return VerifiedRelease{}, err
	}
	manifest, rawManifest, err := readManifestWithRaw(extracted)
	if err != nil {
		return VerifiedRelease{}, err
	}
	if manifest.Version == "" || manifest.Publisher == "" || manifest.CodeHelper == "" {
		return VerifiedRelease{}, errors.New("Registry plugin manifest is not governed")
	}
	if manifest.Name != release.Name || manifest.Version != release.Version ||
		manifest.Publisher != release.Publisher ||
		manifest.Generation != release.Generation {
		return VerifiedRelease{}, errors.New("plugin manifest identity does not match Registry")
	}
	if err := checkCompatibility(manifest.CodeHelper, d.config.RuntimeVersion); err != nil {
		return VerifiedRelease{}, err
	}
	if !equalHash(hashBytes(rawManifest), release.ManifestSHA256) {
		return VerifiedRelease{}, fmt.Errorf(
			"%w: plugin manifest digest does not match Registry", ErrDigestMismatch,
		)
	}
	capabilityHash, err := ManifestCapabilityHash(manifest)
	if err != nil {
		return VerifiedRelease{}, err
	}
	if !equalHash(capabilityHash, release.CapabilitySHA256) {
		return VerifiedRelease{}, fmt.Errorf(
			"%w: plugin capability digest does not match Registry", ErrDigestMismatch,
		)
	}
	staged, err := d.config.Stager.Stage(extracted)
	if err != nil {
		return VerifiedRelease{}, err
	}
	return VerifiedRelease{
		Name: release.Name, Version: release.Version,
		Publisher: release.Publisher, Generation: release.Generation,
		IndexGeneration:  registry.Index.Generation,
		ArtifactSHA256:   release.ArtifactSHA256,
		ManifestSHA256:   release.ManifestSHA256,
		CapabilitySHA256: release.CapabilitySHA256,
		ContentHash:      staged.ContentHash, Signature: release.Signature,
		VerifiedAt: d.config.Now().UTC(),
	}, nil
}

func (d *Distributor) Activate(
	release VerifiedRelease,
	action string,
	current *ActivationRecord,
) (ActivationRecord, error) {
	if d == nil {
		return ActivationRecord{}, errors.New("plugin distributor is required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if action != "install" && action != "update" {
		return ActivationRecord{}, errors.New("plugin activation action is invalid")
	}
	if err := d.verifyStaged(release); err != nil {
		return ActivationRecord{}, err
	}
	if current != nil {
		if err := validateActivationRecord(*current); err != nil {
			return ActivationRecord{}, err
		}
		if release.IndexGeneration < current.MaxIndexGeneration ||
			release.Generation <= current.MaxGeneration {
			return ActivationRecord{}, ErrRegistryReplay
		}
		if compare, err := compareVersions(release.Version, current.MaxVersion); err != nil {
			return ActivationRecord{}, err
		} else if compare <= 0 {
			return ActivationRecord{}, ErrPluginDowngrade
		}
		if release.Publisher != current.Active.Publisher {
			return ActivationRecord{}, errors.New("plugin publisher cannot change during update")
		}
	} else if action == "update" {
		return ActivationRecord{}, errors.New("plugin is not installed")
	}
	record := ActivationRecord{
		SchemaVersion: activationSchemaV1, Active: release,
		MaxIndexGeneration: release.IndexGeneration,
		MaxGeneration:      release.Generation, MaxVersion: release.Version,
		Action: action, ChangedAt: d.config.Now().UTC(),
	}
	if current != nil {
		record.MaxIndexGeneration = current.MaxIndexGeneration
		record.MaxGeneration = current.MaxGeneration
		record.MaxVersion = current.MaxVersion
		if release.IndexGeneration > record.MaxIndexGeneration {
			record.MaxIndexGeneration = release.IndexGeneration
		}
		if release.Generation > record.MaxGeneration {
			record.MaxGeneration = release.Generation
		}
		record.MaxVersion = release.Version
		record.Previous = append(record.Previous, current.Active)
		record.Previous = append(record.Previous, current.Previous...)
		if len(record.Previous) > maxActivationHistory {
			record.Previous = record.Previous[:maxActivationHistory]
		}
	}
	return record, nil
}

func (d *Distributor) Rollback(current ActivationRecord) (ActivationRecord, error) {
	if d == nil {
		return ActivationRecord{}, errors.New("plugin distributor is required")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := validateActivationRecord(current); err != nil {
		return ActivationRecord{}, err
	}
	if len(current.Previous) == 0 {
		return ActivationRecord{}, errors.New("plugin has no verified rollback target")
	}
	target := current.Previous[0]
	if err := d.verifyStaged(target); err != nil {
		return ActivationRecord{}, fmt.Errorf("verify plugin rollback target: %w", err)
	}
	record := ActivationRecord{
		SchemaVersion: activationSchemaV1, Active: target,
		Previous:           append([]VerifiedRelease{current.Active}, current.Previous[1:]...),
		MaxIndexGeneration: current.MaxIndexGeneration,
		MaxGeneration:      current.MaxGeneration, MaxVersion: current.MaxVersion,
		Action: "rollback", ChangedAt: d.config.Now().UTC(),
	}
	if len(record.Previous) > maxActivationHistory {
		record.Previous = record.Previous[:maxActivationHistory]
	}
	return record, nil
}

func (d *Distributor) cacheArtifact(digest string, artifact []byte) error {
	if !validContentAddress(digest) {
		return errors.New("plugin artifact cache address is invalid")
	}
	if existing, err := d.cache.OpenFile(digest + ".tar.gz"); err == nil {
		data, readErr := io.ReadAll(io.LimitReader(existing, maxCompressedArtifact+1))
		closeErr := existing.Close()
		if err := errors.Join(readErr, closeErr); err != nil {
			return err
		}
		if !equalHash(hashBytes(data), digest) {
			return fmt.Errorf("%w: plugin artifact cache is corrupt", ErrDigestMismatch)
		}
		return nil
	}
	return d.cache.AtomicWrite(digest+".tar.gz", artifact, 0o600)
}

func (d *Distributor) cachedArtifact(digest string) ([]byte, bool, error) {
	if !validContentAddress(digest) {
		return nil, false, errors.New("plugin artifact cache address is invalid")
	}
	path := filepath.Join(d.config.CacheRoot, digest+".tar.gz")
	data, err := readLimitedFile(path, maxCompressedArtifact)
	if errors.Is(err, os.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !equalHash(hashBytes(data), digest) {
		return nil, false, fmt.Errorf(
			"%w: plugin artifact cache is corrupt", ErrDigestMismatch,
		)
	}
	return data, true, nil
}

func (d *Distributor) verifyStaged(release VerifiedRelease) error {
	if err := validateVerifiedRelease(release); err != nil {
		return err
	}
	if err := VerifyRegistryRelease(
		release.registryRelease(), d.config.Publishers,
	); err != nil {
		return err
	}
	path := filepath.Join(d.config.Stager.Root(), release.ContentHash)
	actual, err := HashBundle(path)
	if err != nil || !equalHash(actual, release.ContentHash) {
		return fmt.Errorf(
			"%w: plugin staged release is missing or corrupt", ErrDigestMismatch,
		)
	}
	manifest, err := ReadManifest(path)
	if err != nil {
		return err
	}
	if manifest.Name != release.Name || manifest.Version != release.Version ||
		manifest.Publisher != release.Publisher ||
		manifest.Generation != release.Generation {
		return errors.New("plugin staged manifest identity changed")
	}
	return nil
}

func validateActivationRecord(record ActivationRecord) error {
	if record.SchemaVersion != activationSchemaV1 || record.ChangedAt.IsZero() {
		return errors.New("plugin activation is missing or unsupported")
	}
	if record.Action != "install" && record.Action != "update" &&
		record.Action != "rollback" {
		return errors.New("plugin activation action is invalid")
	}
	if err := validateVerifiedRelease(record.Active); err != nil {
		return err
	}
	if record.MaxIndexGeneration < record.Active.IndexGeneration ||
		record.MaxGeneration < record.Active.Generation {
		return errors.New("plugin activation generation watermark is invalid")
	}
	if compare, err := compareVersions(record.MaxVersion, record.Active.Version); err != nil ||
		compare < 0 {
		return errors.New("plugin activation version watermark is invalid")
	}
	if len(record.Previous) > maxActivationHistory {
		return errors.New("plugin activation history exceeds limit")
	}
	for _, previous := range record.Previous {
		if err := validateVerifiedRelease(previous); err != nil {
			return err
		}
		if previous.Name != record.Active.Name ||
			previous.Publisher != record.Active.Publisher {
			return errors.New("plugin activation history identity mismatch")
		}
		if previous.IndexGeneration > record.MaxIndexGeneration ||
			previous.Generation > record.MaxGeneration {
			return errors.New("plugin activation history exceeds generation watermark")
		}
		if compare, err := compareVersions(record.MaxVersion, previous.Version); err != nil ||
			compare < 0 {
			return errors.New("plugin activation history exceeds version watermark")
		}
	}
	return nil
}

func validateVerifiedRelease(release VerifiedRelease) error {
	if err := validateRegistryRelease(release.registryRelease(), true); err != nil {
		return err
	}
	if release.IndexGeneration == 0 || !validContentAddress(release.ContentHash) ||
		release.VerifiedAt.IsZero() {
		return errors.New("verified plugin release is incomplete")
	}
	if err := validateReceipt(release.receipt()); err != nil {
		return err
	}
	return nil
}

func extractPluginArtifact(artifact []byte, destination string) error {
	if len(artifact) == 0 || len(artifact) > maxCompressedArtifact {
		return errors.New("plugin artifact exceeds compressed size limit")
	}
	compressed, err := gzip.NewReader(bytes.NewReader(artifact))
	if err != nil {
		return fmt.Errorf("open plugin artifact: %w", err)
	}
	defer compressed.Close()
	archive := tar.NewReader(compressed)
	seen := make(map[string]struct{})
	var total int64
	entries := 0
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("read plugin artifact: %w", err)
		}
		entries++
		if entries > maxBundleFiles {
			return errors.New("plugin artifact exceeds entry limit")
		}
		name, err := safeArchivePath(header.Name)
		if err != nil {
			return err
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("plugin artifact path %q is duplicated", name)
		}
		seen[name] = struct{}{}
		target := filepath.Join(destination, filepath.FromSlash(name))
		switch header.Typeflag {
		case tar.TypeDir:
			if header.Size != 0 {
				return errors.New("plugin artifact directory has data")
			}
			if err := os.MkdirAll(target, 0o700); err != nil {
				return err
			}
		case tar.TypeReg, 0:
			if header.Size < 0 || header.Size > maxBundleBytes ||
				total+header.Size > maxBundleBytes {
				return errors.New("plugin artifact exceeds extracted size limit")
			}
			total += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			mode := os.FileMode(0o600)
			if header.Mode&0o111 != 0 {
				mode = 0o700
			}
			output, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
			if err != nil {
				return err
			}
			written, copyErr := io.CopyN(output, archive, header.Size)
			syncErr := output.Sync()
			closeErr := output.Close()
			if err := errors.Join(copyErr, syncErr, closeErr); err != nil {
				return err
			}
			if written != header.Size {
				return errors.New("plugin artifact file is truncated")
			}
		default:
			return fmt.Errorf(
				"plugin artifact path %q uses forbidden entry type %d",
				name, header.Typeflag,
			)
		}
	}
	return syncTree(destination)
}

func safeArchivePath(name string) (string, error) {
	if name == "" || strings.ContainsRune(name, 0) ||
		strings.Contains(name, `\`) || strings.HasPrefix(name, "/") {
		return "", errors.New("plugin artifact path is invalid")
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(name)))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") ||
		clean != strings.TrimSuffix(name, "/") {
		return "", fmt.Errorf("plugin artifact path %q is unsafe", name)
	}
	return clean, nil
}

func readManifestWithRaw(root string) (Manifest, []byte, error) {
	var selected string
	for _, name := range []string{TOMLManifestName, ManifestName} {
		if _, err := os.Lstat(filepath.Join(root, name)); err == nil {
			if selected != "" {
				return Manifest{}, nil, errors.New(
					"plugin bundle contains both plugin.toml and plugin.json",
				)
			}
			selected = name
		} else if !errors.Is(err, os.ErrNotExist) {
			return Manifest{}, nil, err
		}
	}
	if selected == "" {
		return Manifest{}, nil, errors.New("plugin manifest is missing")
	}
	raw, err := readLimitedFile(filepath.Join(root, selected), maxManifestBytes)
	if err != nil {
		return Manifest{}, nil, err
	}
	manifest, err := ReadManifest(root)
	return manifest, raw, err
}
