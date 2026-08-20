package admission

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type ResourceSnapshot struct {
	TemporaryDirectories []string `json:"temporary_directories"`
	RuntimeProcesses     []int    `json:"runtime_processes"`
}

func SnapshotOwnedResources(
	ctx context.Context,
	temporaryRoot, runtimePath string,
) (ResourceSnapshot, error) {
	return snapshotOwnedResources(
		ctx,
		temporaryRoot,
		runtimePath,
		[]string{"cdt-*"},
	)
}

func SnapshotH4Resources(
	ctx context.Context,
	temporaryRoot, packageBinary string,
) (ResourceSnapshot, error) {
	return snapshotOwnedResources(
		ctx,
		temporaryRoot,
		packageBinary,
		[]string{"codehelper-h4-canary-*"},
	)
}

func snapshotOwnedResources(
	ctx context.Context,
	temporaryRoot, runtimePath string,
	patterns []string,
) (ResourceSnapshot, error) {
	var directories []string
	for _, pattern := range patterns {
		matches, err := filepath.Glob(filepath.Join(temporaryRoot, pattern))
		if err != nil {
			return ResourceSnapshot{}, err
		}
		for _, match := range matches {
			directories = append(directories, filepath.Clean(match))
		}
	}
	sort.Strings(directories)
	absoluteRuntime, err := filepath.Abs(runtimePath)
	if err != nil {
		return ResourceSnapshot{}, err
	}
	output, err := exec.CommandContext(
		ctx,
		"ps",
		"-axo",
		"pid=,command=",
	).Output()
	if err != nil {
		return ResourceSnapshot{}, fmt.Errorf("list owned processes: %w", err)
	}
	var processes []int
	for _, line := range bytes.Split(output, []byte{'\n'}) {
		fields := strings.Fields(string(line))
		if len(fields) < 2 || !commandReferencesPath(fields[1:], absoluteRuntime) {
			continue
		}
		pid, parseErr := strconv.Atoi(fields[0])
		if parseErr != nil || pid < 1 {
			return ResourceSnapshot{}, errors.New("parse owned process inventory")
		}
		processes = append(processes, pid)
	}
	sort.Ints(processes)
	return ResourceSnapshot{
		TemporaryDirectories: directories,
		RuntimeProcesses:     processes,
	}, nil
}

func VerifyResourceCleanup(
	before, after ResourceSnapshot,
) (string, error) {
	remainingDirectories := addedStrings(
		before.TemporaryDirectories,
		after.TemporaryDirectories,
	)
	remainingProcesses := addedInts(
		before.RuntimeProcesses,
		after.RuntimeProcesses,
	)
	evidence := struct {
		Before               ResourceSnapshot `json:"before"`
		After                ResourceSnapshot `json:"after"`
		RemainingDirectories []string         `json:"remaining_directories"`
		RemainingProcesses   []int            `json:"remaining_processes"`
	}{
		Before: before, After: after,
		RemainingDirectories: remainingDirectories,
		RemainingProcesses:   remainingProcesses,
	}
	raw, err := json.Marshal(evidence)
	if err != nil {
		return "", err
	}
	digest := spec.DigestString(string(raw))
	if len(remainingDirectories) != 0 || len(remainingProcesses) != 0 {
		return digest, fmt.Errorf(
			"admission left %d temporary directories and %d Runtime processes",
			len(remainingDirectories),
			len(remainingProcesses),
		)
	}
	return digest, nil
}

func commandReferencesPath(arguments []string, path string) bool {
	for _, argument := range arguments {
		if filepath.Clean(argument) == path {
			return true
		}
	}
	return false
}

func addedStrings(before, after []string) []string {
	known := make(map[string]struct{}, len(before))
	for _, value := range before {
		known[value] = struct{}{}
	}
	var added []string
	for _, value := range after {
		if _, exists := known[value]; !exists {
			added = append(added, value)
		}
	}
	return added
}

func addedInts(before, after []int) []int {
	known := make(map[int]struct{}, len(before))
	for _, value := range before {
		known[value] = struct{}{}
	}
	var added []int
	for _, value := range after {
		if _, exists := known[value]; !exists {
			added = append(added, value)
		}
	}
	return added
}

func DefaultTemporaryRoot() string {
	return os.TempDir()
}
