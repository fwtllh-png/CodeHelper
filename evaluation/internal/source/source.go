package source

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

func Resolve(ctx context.Context, root string) (spec.SourceIdentity, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return spec.SourceIdentity{}, fmt.Errorf("resolve repository root: %w", err)
	}
	commitRaw, err := git(ctx, root, "rev-parse", "HEAD")
	if err != nil {
		return spec.SourceIdentity{}, err
	}
	commit := strings.TrimSpace(string(commitRaw))
	if commit == "" {
		return spec.SourceIdentity{}, fmt.Errorf("git rev-parse returned an empty commit")
	}
	diff, err := git(ctx, root, "diff", "--binary", "--no-ext-diff", "HEAD", "--")
	if err != nil {
		return spec.SourceIdentity{}, err
	}
	untrackedRaw, err := git(
		ctx,
		root,
		"ls-files",
		"--others",
		"--exclude-standard",
		"-z",
	)
	if err != nil {
		return spec.SourceIdentity{}, err
	}
	untracked := splitNUL(untrackedRaw)
	slices.Sort(untracked)

	digest := sha256.New()
	writePart(digest, "commit", []byte(commit))
	writePart(digest, "diff", diff)
	for _, relative := range untracked {
		absolute, err := safePath(root, relative)
		if err != nil {
			return spec.SourceIdentity{}, err
		}
		info, err := os.Lstat(absolute)
		if err != nil {
			return spec.SourceIdentity{}, fmt.Errorf(
				"inspect untracked path %q: %w",
				relative,
				err,
			)
		}
		var content []byte
		switch {
		case info.Mode().IsRegular():
			content, err = os.ReadFile(absolute)
		case info.Mode()&os.ModeSymlink != 0:
			var target string
			target, err = os.Readlink(absolute)
			content = []byte(target)
		default:
			continue
		}
		if err != nil {
			return spec.SourceIdentity{}, fmt.Errorf(
				"read untracked path %q: %w",
				relative,
				err,
			)
		}
		writePart(digest, "untracked-path", []byte(filepath.ToSlash(relative)))
		writePart(digest, "untracked-mode", []byte(info.Mode().String()))
		writePart(digest, "untracked-content", content)
	}
	return spec.SourceIdentity{
		Commit:      commit,
		Dirty:       len(diff) != 0 || len(untracked) != 0,
		DirtyDigest: "sha256:" + hex.EncodeToString(digest.Sum(nil)),
	}, nil
}

func Environment(host string) spec.Environment {
	if strings.TrimSpace(host) == "" {
		host, _ = os.Hostname()
	}
	if strings.TrimSpace(host) == "" {
		host = "unknown"
	}
	host = "host-" + strings.TrimPrefix(spec.DigestString(host), "sha256:")[:12]
	return spec.Environment{
		Host:      host,
		OS:        runtime.GOOS,
		Arch:      runtime.GOARCH,
		GoVersion: runtime.Version(),
	}
}

func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", args...)
	command.Dir = root
	output, err := command.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf(
			"git %s: %w: %s",
			strings.Join(args, " "),
			err,
			strings.TrimSpace(string(output)),
		)
	}
	return output, nil
}

func splitNUL(data []byte) []string {
	var values []string
	for len(data) != 0 {
		index := bytes.IndexByte(data, 0)
		if index < 0 {
			if value := string(data); value != "" {
				values = append(values, value)
			}
			break
		}
		if index > 0 {
			values = append(values, string(data[:index]))
		}
		data = data[index+1:]
	}
	return values
}

func safePath(root, relative string) (string, error) {
	if filepath.IsAbs(relative) {
		return "", fmt.Errorf("untracked path %q is absolute", relative)
	}
	absolute := filepath.Join(root, filepath.FromSlash(relative))
	back, err := filepath.Rel(root, absolute)
	if err != nil || back == ".." ||
		strings.HasPrefix(filepath.ToSlash(back), "../") {
		return "", fmt.Errorf("untracked path %q escapes repository", relative)
	}
	return absolute, nil
}

func writePart(writer io.Writer, name string, value []byte) {
	_, _ = io.WriteString(writer, name)
	_, _ = writer.Write([]byte{0})
	_, _ = writer.Write(value)
	_, _ = writer.Write([]byte{0})
}
