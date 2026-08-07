package cli_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/config"
	"github.com/fwtllh-png/CodeHelper/internal/host/cli"
)

func TestConfigProfileRendersLoadableTOML(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"config", "profile",
		"--profile", "recommended",
		"--workspace", "/workspace",
		"--data-dir", "/workspace/.codehelper",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	path := filepath.Join(t.TempDir(), "codehelper.toml")
	if err := os.WriteFile(path, stdout.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := config.Load(config.LoadOptions{
		Path: path,
		LookupEnv: func(string) (string, bool) {
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Config.Execution.Workspace != "/workspace" ||
		loaded.Config.Execution.Verify.Mode != "soft" {
		t.Fatalf("config=%+v", loaded.Config)
	}
}

func TestConfigExplainReturnsResolvedFieldMetadata(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codehelper.toml")
	if err := os.WriteFile(path, []byte("[execution]\nmax_steps = 9\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := cli.Run([]string{
		"config", "explain", "execution.max_steps",
		"--config", path,
		"--json",
	}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	var explanation config.FieldExplanation
	if err := json.Unmarshal(stdout.Bytes(), &explanation); err != nil {
		t.Fatal(err)
	}
	if explanation.Field != "execution.max_steps" ||
		explanation.Source != config.SourceFile ||
		explanation.Risk != "medium" {
		t.Fatalf("explanation=%+v", explanation)
	}
}
