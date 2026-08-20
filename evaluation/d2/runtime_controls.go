package d2

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/fwtllh-png/CodeHelper/evaluation/internal/runner"
	"github.com/fwtllh-png/CodeHelper/evaluation/internal/spec"
)

type SyntheticRepositoryEvidence struct {
	CaseID         string `json:"case_id"`
	Seed           uint64 `json:"seed"`
	Files          int    `json:"files"`
	Bytes          int64  `json:"bytes"`
	EvidenceDigest string `json:"evidence_digest"`
}

func MaterializeSyntheticRepository(
	root string,
	generated GeneratedCase,
) (SyntheticRepositoryEvidence, error) {
	if err := generated.Validate(); err != nil {
		return SyntheticRepositoryEvidence{}, err
	}
	if entries, err := os.ReadDir(root); err != nil {
		return SyntheticRepositoryEvidence{}, err
	} else if len(entries) != 0 {
		return SyntheticRepositoryEvidence{}, errors.New(
			"D2 synthetic repository destination is not empty",
		)
	}
	digestInput := strings.Builder{}
	var bytesWritten int64
	for index := 0; index < generated.Workload.Files; index++ {
		language := []string{"go", "ts", "py"}[index%3]
		relative := filepath.Join(
			fmt.Sprintf("module-%03d", index%17),
			fmt.Sprintf("file-%05d.%s", index, language),
		)
		content := fmt.Sprintf(
			"// d2 synthetic case=%s seed=%d index=%d\n",
			generated.ID,
			generated.Seed,
			index,
		)
		path := filepath.Join(root, relative)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return SyntheticRepositoryEvidence{}, err
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			return SyntheticRepositoryEvidence{}, err
		}
		digestInput.WriteString(filepath.ToSlash(relative))
		digestInput.WriteByte(0)
		digestInput.WriteString(content)
		digestInput.WriteByte(0)
		bytesWritten += int64(len(content))
	}
	evidence := SyntheticRepositoryEvidence{
		CaseID: generated.ID,
		Seed:   generated.Seed,
		Files:  generated.Workload.Files,
		Bytes:  bytesWritten,
	}
	evidence.EvidenceDigest = spec.DigestString(digestInput.String())
	raw, err := json.Marshal(evidence)
	if err != nil {
		return SyntheticRepositoryEvidence{}, err
	}
	if err := os.WriteFile(
		filepath.Join(root, ".d2-synthetic-manifest.json"),
		append(raw, '\n'),
		0o600,
	); err != nil {
		return SyntheticRepositoryEvidence{}, err
	}
	return evidence, nil
}

func runFaultControlProbes(ctx context.Context, root string) (map[string]int, error) {
	temporary, err := os.MkdirTemp("", "codehelper-d2-faults-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	probes := []struct {
		id  string
		run func(context.Context) error
	}{
		{"provider_disconnect", probeProviderDisconnect},
		{"process_crash", probeProcessCrash},
		{"persistence_contention", func(context.Context) error {
			return probePersistenceContention(temporary)
		}},
		{"filesystem_pressure", func(context.Context) error {
			return probeFilesystemPressure(temporary)
		}},
		{"mcp_disconnect", probeMCPDisconnect},
		{"tool_timeout", func(probeCtx context.Context) error {
			return probeToolTimeout(probeCtx, root)
		}},
	}
	hits := make(map[string]int, len(probes))
	var failures []error
	for _, probe := range probes {
		probeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := probe.run(probeCtx)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", probe.id, err))
			continue
		}
		hits[probe.id]++
	}
	if err := errors.Join(failures...); err != nil {
		return hits, err
	}
	return hits, nil
}

func probeProviderDisconnect(context.Context) error {
	server, client := net.Pipe()
	defer client.Close()
	if err := server.Close(); err != nil {
		return err
	}
	buffer := make([]byte, 1)
	if _, err := client.Read(buffer); !errors.Is(err, io.EOF) {
		return errors.New("provider disconnect did not produce EOF")
	}
	return nil
}

func probeProcessCrash(ctx context.Context) error {
	command := exec.CommandContext(ctx, "sleep", "30")
	if err := command.Start(); err != nil {
		return err
	}
	if err := command.Process.Kill(); err != nil {
		return err
	}
	err := command.Wait()
	var exitError *exec.ExitError
	if !errors.As(err, &exitError) {
		return errors.New("owned process termination was not observed")
	}
	return nil
}

func probePersistenceContention(root string) error {
	path := filepath.Join(root, "exclusive.lock")
	first, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer first.Close()
	second, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if second != nil {
		second.Close()
	}
	if !errors.Is(err, os.ErrExist) {
		return errors.New("persistence contention did not reject the second owner")
	}
	return nil
}

func probeFilesystemPressure(root string) error {
	path := filepath.Join(root, "bounded-output")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	writer := &boundedFileWriter{writer: file, remaining: 32}
	if _, err := writer.Write(bytes.Repeat([]byte("a"), 32)); err != nil {
		return err
	}
	if _, err := writer.Write([]byte("overflow")); !errors.Is(err, errFileBudget) {
		return errors.New("filesystem pressure did not enforce the byte budget")
	}
	return nil
}

func probeMCPDisconnect(context.Context) error {
	reader, writer := io.Pipe()
	if err := writer.Close(); err != nil {
		return err
	}
	buffer := make([]byte, 1)
	if _, err := reader.Read(buffer); !errors.Is(err, io.EOF) {
		return errors.New("MCP stdio disconnect did not produce EOF")
	}
	return reader.Close()
}

func probeToolTimeout(ctx context.Context, root string) error {
	timeout, cancel := context.WithTimeout(ctx, 10*time.Millisecond)
	defer cancel()
	result, err := runner.RunOwnedCommand(
		timeout,
		root,
		[]string{"sleep", "30"},
		nil,
		1024,
	)
	if err == nil || !result.TimedOut {
		return errors.New("guarded Tool timeout control did not trigger")
	}
	return nil
}

var errFileBudget = errors.New("D2 filesystem byte budget exhausted")

type boundedFileWriter struct {
	writer    io.Writer
	remaining int
}

func (w *boundedFileWriter) Write(value []byte) (int, error) {
	if len(value) > w.remaining {
		return 0, errFileBudget
	}
	written, err := w.writer.Write(value)
	w.remaining -= written
	return written, err
}
