package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProfilesAreValidAndIncreaseOnlyExplicitSurface(t *testing.T) {
	lineCounts := make(map[Profile]int)
	for _, profile := range []Profile{
		ProfileMinimal,
		ProfileRecommended,
		ProfileAdvanced,
	} {
		t.Run(string(profile), func(t *testing.T) {
			rendered, err := RenderProfile(profile, ProfileOptions{
				Workspace: "/workspace",
				DataDir:   "/workspace/.codehelper",
			})
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "codehelper.toml")
			if err := os.WriteFile(path, rendered, 0o600); err != nil {
				t.Fatal(err)
			}
			snapshot, err := Load(LoadOptions{
				Path: path,
				LookupEnv: func(string) (string, bool) {
					return "", false
				},
			})
			if err != nil {
				t.Fatalf("profile is not loadable:\n%s\n%v", rendered, err)
			}
			if snapshot.Config.Execution.Workspace != "/workspace" ||
				snapshot.Config.State.DataDir != "/workspace/.codehelper" {
				t.Fatalf("profile paths=%+v", snapshot.Config)
			}
			lineCounts[profile] = strings.Count(string(rendered), "\n")
		})
	}
	if !(lineCounts[ProfileMinimal] < lineCounts[ProfileRecommended] &&
		lineCounts[ProfileRecommended] < lineCounts[ProfileAdvanced]) {
		t.Fatalf("profile line counts=%v", lineCounts)
	}
}

func TestExplainReportsCurrentDefaultSourceAndRisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codehelper.toml")
	if err := os.WriteFile(path, []byte(`
[execution]
max_steps = 12

[credential]
kind = "env"
name = "WORKSPACE_API_KEY"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Load(LoadOptions{
		Path: path,
		LookupEnv: func(string) (string, bool) {
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	explanation, err := snapshot.Explain("execution.max_steps")
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Current != float64(12) ||
		explanation.Default != float64(0) ||
		explanation.Source != SourceFile ||
		explanation.Risk != "medium" {
		t.Fatalf("explanation=%+v", explanation)
	}
	credential, err := snapshot.Explain("credential.name")
	if err != nil {
		t.Fatal(err)
	}
	if credential.Risk != "high" ||
		credential.Current != "WORKSPACE_API_KEY" {
		t.Fatalf("credential=%+v", credential)
	}
}

func TestExplainSupportsConfiguredPurposeRoutes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "codehelper.toml")
	if err := os.WriteFile(path, []byte(`
[route.plan]
provider = "openai"
model = "gpt-4.1"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Load(LoadOptions{
		Path: path,
		LookupEnv: func(string) (string, bool) {
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	explanation, err := snapshot.Explain("route.plan.provider")
	if err != nil {
		t.Fatal(err)
	}
	if explanation.Current != "openai" ||
		explanation.Source != SourceFile ||
		explanation.Risk != "medium" {
		t.Fatalf("explanation=%+v", explanation)
	}
}

func TestExplainRejectsUnknownFields(t *testing.T) {
	snapshot, err := Load(LoadOptions{
		LookupEnv: func(string) (string, bool) {
			return "", false
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.Explain("execution.not_real"); err == nil {
		t.Fatal("unknown field was explained")
	}
}
