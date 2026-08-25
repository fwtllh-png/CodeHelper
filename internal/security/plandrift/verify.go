package plandrift

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func Verify(root string, document json.RawMessage) error {
	if len(document) == 0 {
		return nil
	}
	var projection struct {
		FileBaseline []struct {
			Path    string `json:"path"`
			Digest  string `json:"digest"`
			Missing bool   `json:"missing"`
		} `json:"file_baseline"`
	}
	if err := json.Unmarshal(document, &projection); err != nil {
		return fmt.Errorf("decode Plan baseline: %w", err)
	}
	if len(projection.FileBaseline) == 0 {
		return nil
	}
	workspace, err := sandbox.NewWorkspace(root)
	if err != nil {
		return err
	}
	for _, expected := range projection.FileBaseline {
		file, openErr := workspace.OpenFile(expected.Path)
		if errors.Is(openErr, os.ErrNotExist) {
			if expected.Missing {
				continue
			}
			return drift(expected.Path)
		}
		if openErr != nil {
			return fmt.Errorf("verify Plan baseline %q: %w", expected.Path, openErr)
		}
		if expected.Missing {
			_ = file.Close()
			return drift(expected.Path)
		}
		digest := sha256.New()
		_, copyErr := io.Copy(digest, file)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			return fmt.Errorf(
				"verify Plan baseline %q: %w",
				expected.Path,
				errors.Join(copyErr, closeErr),
			)
		}
		if hex.EncodeToString(digest.Sum(nil)) != expected.Digest {
			return drift(expected.Path)
		}
	}
	return nil
}

func drift(path string) error {
	return protocol.NewProblem(
		protocol.CodeConflict,
		fmt.Sprintf(
			"Plan workspace baseline changed at %s; generate a new Plan revision",
			path,
		),
		true,
		nil,
	)
}
