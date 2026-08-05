package plugin

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Masterminds/semver/v3"
)

const (
	registryIndexSchemaV1 = 1
	maxRegistryIndexBytes = 4 << 20
)

var (
	ErrUnknownPublisher = errors.New("plugin publisher is not trusted")
	ErrInvalidSignature = errors.New("plugin registry signature is invalid")
	ErrRegistryReplay   = errors.New("plugin registry generation replay")
	ErrPluginDowngrade  = errors.New("plugin downgrade is not allowed")
)

// PublisherAllowlist is the strict on-disk trust root for Registry publishers.
type PublisherAllowlist struct {
	SchemaVersion int               `json:"schema_version"`
	Publishers    map[string]string `json:"publishers"`
}

func LoadPublisherAllowlist(path string) (map[string]ed25519.PublicKey, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errors.New("plugin publisher allowlist path is required")
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("plugin publisher allowlist is not a regular file")
	}
	if err := rejectMultiplyLinked(path, info); err != nil {
		return nil, err
	}
	data, err := readLimitedFile(path, maxRegistryIndexBytes)
	if err != nil {
		return nil, err
	}
	var wire PublisherAllowlist
	if err := decodeStrict(data, &wire); err != nil {
		return nil, fmt.Errorf("decode plugin publisher allowlist: %w", err)
	}
	if wire.SchemaVersion != 1 || wire.Publishers == nil {
		return nil, errors.New("plugin publisher allowlist is missing or unsupported")
	}
	result := make(map[string]ed25519.PublicKey, len(wire.Publishers))
	for publisher, encoded := range wire.Publishers {
		if err := validatePublisher(publisher); err != nil {
			return nil, fmt.Errorf("publisher %q: %w", publisher, err)
		}
		key, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("publisher %q has an invalid Ed25519 public key", publisher)
		}
		result[publisher] = ed25519.PublicKey(append([]byte(nil), key...))
	}
	return result, nil
}

// RegistryIndex is versioned metadata. Every release has its own detached
// publisher signature so one index can safely aggregate multiple publishers.
type RegistryIndex struct {
	SchemaVersion int               `json:"schema_version"`
	Generation    uint64            `json:"generation"`
	Releases      []RegistryRelease `json:"releases"`
}

type RegistryRelease struct {
	SchemaVersion    int    `json:"schema_version"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	Generation       uint64 `json:"generation"`
	Publisher        string `json:"publisher"`
	Artifact         string `json:"artifact"`
	ArtifactSHA256   string `json:"artifact_sha256"`
	ManifestSHA256   string `json:"manifest_sha256"`
	CapabilitySHA256 string `json:"capability_sha256"`
	Signature        string `json:"signature"`
}

type releasePayload struct {
	SchemaVersion    int    `json:"schema_version"`
	Name             string `json:"name"`
	Version          string `json:"version"`
	Generation       uint64 `json:"generation"`
	Publisher        string `json:"publisher"`
	ArtifactSHA256   string `json:"artifact_sha256"`
	ManifestSHA256   string `json:"manifest_sha256"`
	CapabilitySHA256 string `json:"capability_sha256"`
}

func (release RegistryRelease) payload() releasePayload {
	return releasePayload{
		SchemaVersion: release.SchemaVersion, Name: release.Name,
		Version: release.Version, Generation: release.Generation,
		Publisher: release.Publisher, ArtifactSHA256: release.ArtifactSHA256,
		ManifestSHA256:   release.ManifestSHA256,
		CapabilitySHA256: release.CapabilitySHA256,
	}
}

func (release RegistryRelease) canonicalPayload() ([]byte, error) {
	return json.Marshal(release.payload())
}

func SignRegistryRelease(release RegistryRelease, key ed25519.PrivateKey) (string, error) {
	if err := validateRegistryRelease(release, false); err != nil {
		return "", err
	}
	if len(key) != ed25519.PrivateKeySize {
		return "", errors.New("plugin publisher private key is invalid")
	}
	payload, err := release.canonicalPayload()
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ed25519.Sign(key, payload)), nil
}

func VerifyRegistryRelease(
	release RegistryRelease,
	publishers map[string]ed25519.PublicKey,
) error {
	if err := validateRegistryRelease(release, true); err != nil {
		return err
	}
	key, ok := publishers[release.Publisher]
	if !ok {
		return fmt.Errorf("%w: %s", ErrUnknownPublisher, release.Publisher)
	}
	signature, err := base64.StdEncoding.DecodeString(release.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return ErrInvalidSignature
	}
	payload, err := release.canonicalPayload()
	if err != nil {
		return err
	}
	if !ed25519.Verify(key, payload, signature) {
		return ErrInvalidSignature
	}
	return nil
}

func validateRegistryRelease(release RegistryRelease, requireSignature bool) error {
	if release.SchemaVersion != registryIndexSchemaV1 {
		return errors.New("plugin release schema_version must be 1")
	}
	if err := validatePluginName(release.Name); err != nil {
		return err
	}
	if _, err := semver.StrictNewVersion(release.Version); err != nil {
		return errors.New("plugin release version must be strict SemVer")
	}
	if release.Generation == 0 {
		return errors.New("plugin release generation must be positive")
	}
	if err := validatePublisher(release.Publisher); err != nil {
		return err
	}
	if strings.TrimSpace(release.Artifact) == "" ||
		strings.ContainsAny(release.Artifact, "\x00\r\n") {
		return errors.New("plugin release artifact is invalid")
	}
	for _, value := range []string{
		release.ArtifactSHA256, release.ManifestSHA256, release.CapabilitySHA256,
	} {
		if !validContentAddress(value) {
			return errors.New("plugin release contains an invalid SHA-256 hash")
		}
	}
	if requireSignature && release.Signature == "" {
		return ErrInvalidSignature
	}
	return nil
}

type RegistrySource struct {
	URL        string
	HTTPClient *http.Client
}

type resolvedRegistry struct {
	IndexURL *url.URL
	Index    RegistryIndex
}

func (source RegistrySource) fetchIndex(ctx context.Context) (resolvedRegistry, error) {
	target, err := url.Parse(strings.TrimSpace(source.URL))
	if err != nil {
		return resolvedRegistry{}, err
	}
	if target.Scheme != "https" && target.Scheme != "file" {
		return resolvedRegistry{}, errors.New("plugin Registry URL must use https:// or file://")
	}
	data, err := source.read(ctx, target, maxRegistryIndexBytes)
	if err != nil {
		return resolvedRegistry{}, err
	}
	var index RegistryIndex
	if err := decodeStrict(data, &index); err != nil {
		return resolvedRegistry{}, fmt.Errorf("decode plugin Registry index: %w", err)
	}
	if index.SchemaVersion != registryIndexSchemaV1 || index.Generation == 0 ||
		index.Releases == nil {
		return resolvedRegistry{}, errors.New("plugin Registry index is missing or unsupported")
	}
	seen := make(map[string]struct{}, len(index.Releases))
	var signedGeneration uint64
	for _, release := range index.Releases {
		if err := validateRegistryRelease(release, true); err != nil {
			return resolvedRegistry{}, err
		}
		key := release.Name + "\x00" + release.Version
		if _, duplicate := seen[key]; duplicate {
			return resolvedRegistry{}, fmt.Errorf(
				"plugin Registry has duplicate release %s@%s", release.Name, release.Version,
			)
		}
		seen[key] = struct{}{}
		if release.Generation > signedGeneration {
			signedGeneration = release.Generation
		}
	}
	if signedGeneration == 0 || index.Generation != signedGeneration {
		return resolvedRegistry{}, errors.New(
			"plugin Registry generation does not match signed releases",
		)
	}
	return resolvedRegistry{IndexURL: target, Index: index}, nil
}

func (source RegistrySource) read(
	ctx context.Context,
	target *url.URL,
	limit int64,
) ([]byte, error) {
	switch target.Scheme {
	case "file":
		path, err := fileURLPath(target)
		if err != nil {
			return nil, err
		}
		return readLimitedFile(path, limit)
	case "https":
		if source.HTTPClient == nil {
			return nil, errors.New("HTTPS Plugin Registry requires an injected gated HTTP client")
		}
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
		if err != nil {
			return nil, err
		}
		response, err := source.HTTPClient.Do(request)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode < 200 || response.StatusCode >= 300 {
			return nil, fmt.Errorf("plugin Registry returned HTTP %d", response.StatusCode)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
		if err != nil {
			return nil, err
		}
		if int64(len(data)) > limit {
			return nil, errors.New("plugin Registry response exceeds size limit")
		}
		return data, nil
	default:
		return nil, errors.New("unsupported plugin Registry URL")
	}
}

func (registry resolvedRegistry) selectRelease(name, version string) (RegistryRelease, error) {
	if err := validatePluginName(name); err != nil {
		return RegistryRelease{}, err
	}
	if _, err := semver.StrictNewVersion(version); err != nil {
		return RegistryRelease{}, errors.New("plugin install version must be strict SemVer")
	}
	for _, release := range registry.Index.Releases {
		if release.Name == name && release.Version == version {
			return release, nil
		}
	}
	return RegistryRelease{}, fmt.Errorf("plugin release %s@%s was not found", name, version)
}

func (registry resolvedRegistry) artifactURL(reference string) (*url.URL, error) {
	value, err := url.Parse(reference)
	if err != nil {
		return nil, err
	}
	target := registry.IndexURL.ResolveReference(value)
	if target.Scheme != registry.IndexURL.Scheme {
		return nil, errors.New("plugin artifact changes Registry URL scheme")
	}
	if target.Scheme == "https" &&
		!strings.EqualFold(target.Host, registry.IndexURL.Host) {
		return nil, errors.New("plugin artifact changes Registry origin")
	}
	if target.Scheme == "file" {
		indexPath, err := fileURLPath(registry.IndexURL)
		if err != nil {
			return nil, err
		}
		artifactPath, err := fileURLPath(target)
		if err != nil {
			return nil, err
		}
		base := filepath.Dir(indexPath)
		relative, err := filepath.Rel(base, artifactPath)
		if err != nil || relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil, errors.New("plugin artifact escapes file Registry root")
		}
		resolvedBase, err := filepath.EvalSymlinks(base)
		if err != nil {
			return nil, err
		}
		resolvedArtifact, err := filepath.EvalSymlinks(artifactPath)
		if err != nil {
			return nil, err
		}
		resolvedRelative, err := filepath.Rel(resolvedBase, resolvedArtifact)
		if err != nil || resolvedRelative == ".." ||
			strings.HasPrefix(resolvedRelative, ".."+string(filepath.Separator)) {
			return nil, errors.New("plugin artifact escapes resolved file Registry root")
		}
	}
	return target, nil
}

func fileURLPath(target *url.URL) (string, error) {
	if target == nil || target.Scheme != "file" || target.Host != "" {
		return "", errors.New("file Registry URL must be an absolute local file URL")
	}
	path, err := url.PathUnescape(target.Path)
	if err != nil || !filepath.IsAbs(path) {
		return "", errors.New("file Registry URL path is invalid")
	}
	return filepath.Clean(filepath.FromSlash(path)), nil
}

func readLimitedFile(path string, limit int64) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("plugin Registry file is not a regular file")
	}
	if err := rejectMultiplyLinked(path, info); err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(file, limit+1))
	closeErr := file.Close()
	if err := errors.Join(readErr, closeErr); err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errors.New("plugin Registry file exceeds size limit")
	}
	return data, nil
}

func hashBytes(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}

func compareVersions(left, right string) (int, error) {
	leftVersion, err := semver.StrictNewVersion(left)
	if err != nil {
		return 0, err
	}
	rightVersion, err := semver.StrictNewVersion(right)
	if err != nil {
		return 0, err
	}
	return leftVersion.Compare(rightVersion), nil
}
