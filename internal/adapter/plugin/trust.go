package plugin

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

const (
	maxBundleFiles = 4096
	maxBundleBytes = 64 << 20

	TrustUnsignedLocal  = "unsigned-local"
	TrustSignedRegistry = "signed-registry"
)

type Receipt struct {
	SchemaVersion  int       `json:"schema_version"`
	ContentHash    string    `json:"content_hash"`
	CapabilityHash string    `json:"capability_hash"`
	Generation     uint64    `json:"generation"`
	ReviewedAt     time.Time `json:"reviewed_at"`
	Trust          string    `json:"trust,omitempty"`
	Version        string    `json:"version,omitempty"`
	Publisher      string    `json:"publisher,omitempty"`
	ArtifactHash   string    `json:"artifact_sha256,omitempty"`
	ManifestHash   string    `json:"manifest_sha256,omitempty"`
	Signature      string    `json:"signature,omitempty"`
}

type CapabilityInventory struct {
	Tools           []string `json:"tools" toml:"tools"`
	FilesystemRoots []string `json:"filesystem_roots" toml:"filesystem_roots"`
	NetworkHosts    []string `json:"network_hosts" toml:"network_hosts"`
	AllowProcess    bool     `json:"allow_process" toml:"allow_process"`
}

func Review(
	bundleRoot string,
	capabilities CapabilityInventory,
	generation uint64,
	now time.Time,
) (Receipt, error) {
	if generation == 0 {
		return Receipt{}, errors.New("plugin generation must be positive")
	}
	contentHash, err := HashBundle(bundleRoot)
	if err != nil {
		return Receipt{}, err
	}
	capabilityHash, err := HashCapabilities(capabilities)
	if err != nil {
		return Receipt{}, err
	}
	return Receipt{
		SchemaVersion: 1, ContentHash: contentHash,
		CapabilityHash: capabilityHash, Generation: generation,
		ReviewedAt: now.UTC(), Trust: TrustUnsignedLocal,
	}, nil
}

func Verify(
	bundleRoot string,
	capabilities CapabilityInventory,
	generation uint64,
	receipt Receipt,
) error {
	if receipt.SchemaVersion != 1 ||
		receipt.ContentHash == "" ||
		receipt.CapabilityHash == "" ||
		receipt.Generation == 0 {
		return errors.New("plugin trust receipt is missing or unsupported")
	}
	if generation != receipt.Generation {
		return errors.New("plugin trust generation changed")
	}
	contentHash, err := HashBundle(bundleRoot)
	if err != nil {
		return err
	}
	capabilityHash, err := HashCapabilities(capabilities)
	if err != nil {
		return err
	}
	if !equalHash(contentHash, receipt.ContentHash) {
		return errors.New("plugin content changed after review")
	}
	if !equalHash(capabilityHash, receipt.CapabilityHash) {
		return errors.New("plugin capabilities changed after review")
	}
	return nil
}

func HashBundle(bundleRoot string) (string, error) {
	bundleRoot, err := safeDirectory(bundleRoot, false)
	if err != nil {
		return "", fmt.Errorf("validate plugin bundle root: %w", err)
	}
	workspace, err := sandbox.NewWorkspace(bundleRoot)
	if err != nil {
		return "", fmt.Errorf("validate plugin bundle: %w", err)
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("codehelper-plugin-content-v2\x00"))
	var total int64
	entries := 0
	var walk func(string) error
	walk = func(relativeDirectory string) error {
		directoryName := relativeDirectory
		if directoryName == "" {
			directoryName = "."
		}
		directory, err := workspace.OpenDirectory(filepath.FromSlash(directoryName))
		if err != nil {
			return err
		}
		children, err := directory.ReadDir(-1)
		closeErr := directory.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		sort.Slice(children, func(i, j int) bool {
			return children[i].Name() < children[j].Name()
		})
		for _, child := range children {
			relative := filepath.ToSlash(filepath.Join(relativeDirectory, child.Name()))
			entries++
			if entries > maxBundleFiles {
				return errors.New("plugin bundle exceeds entry limit")
			}
			info, err := child.Info()
			if err != nil {
				return err
			}
			if info.IsDir() {
				writeHashField(hash, []byte("directory"))
				writeHashField(hash, []byte(relative))
				if err := walk(relative); err != nil {
					return err
				}
				continue
			}
			if !info.Mode().IsRegular() {
				return fmt.Errorf("plugin bundle path %q is not a regular file", relative)
			}
			file, err := workspace.OpenFile(filepath.FromSlash(relative))
			if err != nil {
				return err
			}
			openedInfo, err := file.Stat()
			if err != nil {
				file.Close()
				return err
			}
			total += openedInfo.Size()
			if total > maxBundleBytes {
				file.Close()
				return errors.New("plugin bundle exceeds byte limit")
			}
			writeHashField(hash, []byte("file"))
			writeHashField(hash, []byte(relative))
			executable := byte(0)
			if openedInfo.Mode().Perm()&0o111 != 0 {
				executable = 1
			}
			writeHashField(hash, []byte{executable})
			writeHashSize(hash, uint64(openedInfo.Size()))
			_, copyErr := io.CopyN(hash, file, openedInfo.Size())
			after, statErr := file.Stat()
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if statErr != nil {
				return statErr
			}
			if closeErr != nil {
				return closeErr
			}
			if openedInfo.Size() != after.Size() ||
				!openedInfo.ModTime().Equal(after.ModTime()) {
				return fmt.Errorf("plugin bundle path %q changed while hashing", relative)
			}
		}
		return nil
	}
	if err := walk(""); err != nil {
		return "", fmt.Errorf("hash plugin bundle: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func HashCapabilities(capabilities CapabilityInventory) (string, error) {
	normalized, err := normalizeCapabilities(capabilities)
	if err != nil {
		return "", err
	}
	canonical, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	hash := sha256.New()
	_, _ = hash.Write([]byte("codehelper-plugin-capabilities-v1\x00"))
	writeHashField(hash, canonical)
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func normalizeCapabilities(value CapabilityInventory) (CapabilityInventory, error) {
	normalized := CapabilityInventory{
		Tools:           append([]string(nil), value.Tools...),
		FilesystemRoots: append([]string(nil), value.FilesystemRoots...),
		NetworkHosts:    append([]string(nil), value.NetworkHosts...),
		AllowProcess:    value.AllowProcess,
	}
	for _, field := range []struct {
		name   string
		values *[]string
	}{
		{name: "tools", values: &normalized.Tools},
		{name: "filesystem_roots", values: &normalized.FilesystemRoots},
		{name: "network_hosts", values: &normalized.NetworkHosts},
	} {
		for index, item := range *field.values {
			item = strings.TrimSpace(item)
			if item == "" || item == "*" {
				return CapabilityInventory{}, fmt.Errorf(
					"plugin capability %s[%d] must be explicit", field.name, index,
				)
			}
			(*field.values)[index] = item
		}
		sort.Strings(*field.values)
		*field.values = compactStrings(*field.values)
	}
	return normalized, nil
}

func compactStrings(values []string) []string {
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result
}

func writeHashField(writer io.Writer, value []byte) {
	writeHashSize(writer, uint64(len(value)))
	_, _ = writer.Write(value)
}

func writeHashSize(writer io.Writer, value uint64) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], value)
	_, _ = writer.Write(size[:])
}

func equalHash(left, right string) bool {
	leftBytes, leftErr := hex.DecodeString(left)
	rightBytes, rightErr := hex.DecodeString(right)
	return leftErr == nil && rightErr == nil &&
		len(leftBytes) == sha256.Size &&
		len(rightBytes) == sha256.Size &&
		subtle.ConstantTimeCompare(leftBytes, rightBytes) == 1
}
