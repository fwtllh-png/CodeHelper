package constitution_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
	"github.com/fwtllh-png/QCode/internal/security/constitution"
	"github.com/fwtllh-png/QCode/internal/security/policy"
)

func TestConstitutionHoldSurvivesBypass(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	repoPath := filepath.Join(workspace, ".qcode", "constitution.json")
	if err := os.MkdirAll(filepath.Dir(repoPath), 0o700); err != nil {
		t.Fatal(err)
	}
	doc := constitution.Document{
		Version: 1, DenyWriteGlobs: []string{"secrets/"},
		Prompt: "do not write secrets",
	}
	data, _ := json.Marshal(doc)
	if err := os.WriteFile(repoPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := constitution.Load(workspace, home)
	if err != nil {
		t.Fatal(err)
	}
	if !bundle.Status.Loaded || bundle.Status.RuleCount == 0 {
		t.Fatalf("status = %+v", bundle.Status)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionBypass)
	runtime.Repository = bundle.Rules
	decision := runtime.Evaluate(policy.Invocation{
		CallID: "c1", Tool: "file_write", Capability: policy.CapabilityWrite, Validated: true,
		Arguments: json.RawMessage(`{"path":"secrets/token"}`),
		Resources: []tool.Resource{{
			Kind: "file", Path: "secrets/token", Access: tool.AccessWrite,
		}},
	})
	if decision.Action != policy.ActionDeny ||
		!strings.Contains(decision.Code, "constitution_hold") {
		t.Fatalf("decision = %+v", decision)
	}
}

func TestRepoOverridesUserPrompt(t *testing.T) {
	workspace := t.TempDir()
	home := t.TempDir()
	writeDoc(t, filepath.Join(home, ".qcode", "constitution.json"), constitution.Document{
		Version: 1, Prompt: "user prompt", HoldTools: []string{"exec_command"},
	})
	writeDoc(t, filepath.Join(workspace, ".qcode", "constitution.json"), constitution.Document{
		Version: 1, Prompt: "repo prompt",
	})
	bundle, err := constitution.Load(workspace, home)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Prompt != "repo prompt" {
		t.Fatalf("prompt = %q", bundle.Prompt)
	}
}

func TestWriteTemplateIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "constitution.json")
	if err := constitution.WriteTemplate(path, false); err != nil {
		t.Fatal(err)
	}
	first, _ := os.ReadFile(path)
	if err := constitution.WriteTemplate(path, false); err != nil {
		t.Fatal(err)
	}
	second, _ := os.ReadFile(path)
	if string(first) != string(second) {
		t.Fatal("template rewritten without force")
	}
}

func writeDoc(t *testing.T, path string, doc constitution.Document) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}
