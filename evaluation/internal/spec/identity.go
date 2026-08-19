package spec

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
)

func DigestString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func BuildRunPartition(
	source SourceIdentity,
	artifacts ArtifactIdentity,
	seed int64,
	attempt int,
) (string, error) {
	if strings.TrimSpace(source.Commit) == "" ||
		!digestPattern.MatchString(source.DirtyDigest) {
		return "", fmt.Errorf("source identity is incomplete")
	}
	for name, value := range artifactDigests(artifacts) {
		if !digestPattern.MatchString(value) {
			return "", fmt.Errorf("artifact identity %s is invalid", name)
		}
	}
	if attempt < 1 {
		return "", fmt.Errorf("attempt must be positive")
	}
	digest := sha256.New()
	writeIdentityPart(digest, "schema", fmt.Sprint(SchemaVersion))
	writeIdentityPart(digest, "commit", source.Commit)
	writeIdentityPart(digest, "dirty", fmt.Sprint(source.Dirty))
	writeIdentityPart(digest, "dirty_digest", source.DirtyDigest)
	for _, name := range []string{
		"harness", "runtime", "host", "scenario", "fixture", "provider", "model", "config",
	} {
		writeIdentityPart(digest, name, artifactDigests(artifacts)[name])
	}
	writeIdentityPart(digest, "seed", fmt.Sprint(seed))
	writeIdentityPart(digest, "attempt", fmt.Sprint(attempt))
	return "sha256:" + hex.EncodeToString(digest.Sum(nil)), nil
}

func artifactDigests(identity ArtifactIdentity) map[string]string {
	return map[string]string{
		"harness":  identity.HarnessDigest,
		"runtime":  identity.RuntimeDigest,
		"host":     identity.HostDigest,
		"scenario": identity.ScenarioDigest,
		"fixture":  identity.FixtureDigest,
		"provider": identity.ProviderDigest,
		"model":    identity.ModelDigest,
		"config":   identity.ConfigDigest,
	}
}

func writeIdentityPart(writer io.Writer, name, value string) {
	_, _ = io.WriteString(writer, name)
	_, _ = writer.Write([]byte{0})
	_, _ = io.WriteString(writer, value)
	_, _ = writer.Write([]byte{0})
}
