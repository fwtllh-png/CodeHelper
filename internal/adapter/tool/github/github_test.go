package github_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	githubtool "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/github"
	toolguard "github.com/fwtllh-png/CodeHelper/internal/adapter/tool/guard"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
	"github.com/fwtllh-png/CodeHelper/internal/security/sandbox"
)

func TestGitHubIssueAndPRContextReadonly(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method %s", r.Method)
		}
		switch r.URL.Path {
		case "/repos/acme/repo/issues/3":
			_, _ = w.Write([]byte(`{"number":3,"title":"bug","body":"details"}`))
		case "/repos/acme/repo/issues/3/comments":
			_, _ = w.Write([]byte(`[{"id":1,"body":"note"}]`))
		case "/repos/acme/repo/pulls/9":
			_, _ = w.Write([]byte(`{"number":9,"title":"feat"}`))
		case "/repos/acme/repo/pulls/9/reviews":
			_, _ = w.Write([]byte(`[{"id":2,"state":"APPROVED"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	registry := tool.NewRegistry(nil, nil)
	if err := githubtool.Register(registry, githubtool.Options{
		Workspace: t.TempDir(), Backend: passthroughBackend{},
		BaseURL: server.URL, Token: "tok", Client: server.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	issue := execute(t, registry, "github_issue_context", map[string]any{
		"provider": "github", "repository": "acme/repo", "number": 3,
	})
	if issue.IsError || !strings.Contains(issue.Content, `"title":"bug"`) {
		t.Fatalf("issue = %+v", issue)
	}
	pr := execute(t, registry, "github_pr_context", map[string]any{
		"provider": "github", "repository": "acme/repo", "number": 9,
	})
	if pr.IsError || !strings.Contains(pr.Content, `"state":"APPROVED"`) {
		t.Fatalf("pr = %+v", pr)
	}
}

func TestGitHubCommentAndCloseWritePath(t *testing.T) {
	root := initGitRepo(t)
	var posted, patched bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/repos/acme/repo/issues/4/comments":
			posted = true
			_, _ = w.Write([]byte(`{"id":11,"body":"hello"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/repo/issues/4":
			patched = true
			_, _ = w.Write([]byte(`{"number":4,"state":"closed"}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/repos/acme/repo/pulls/5":
			_, _ = w.Write([]byte(`{"number":5,"state":"closed"}`))
		default:
			http.Error(w, `{"message":"nope"}`, http.StatusNotFound)
		}
	}))
	defer server.Close()

	registry := tool.NewRegistry(nil, nil)
	if err := githubtool.Register(registry, githubtool.Options{
		Workspace: root, Backend: passthroughBackend{},
		BaseURL: server.URL, Token: "tok", Client: server.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	comment := execute(t, registry, "github_comment", map[string]any{
		"provider": "github", "repository": "acme/repo", "number": 4, "body": "hello",
	})
	if comment.IsError || !posted {
		t.Fatalf("comment = %+v posted=%v", comment, posted)
	}
	closed := execute(t, registry, "github_close_issue", map[string]any{
		"provider": "github", "repository": "acme/repo", "number": 4,
		"acceptance_criteria": []any{"tests pass"},
		"evidence":            []any{"task:task_1"},
	})
	if closed.IsError || !patched {
		t.Fatalf("close = %+v patched=%v", closed, patched)
	}

	// Dirty tree rejects close by default.
	if err := os.WriteFile(filepath.Join(root, "dirty.txt"), []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dirty, err := registry.Execute(t.Context(), tool.Call{
		Name: "github_close_pr", Authorized: true,
		Arguments: mustJSON(map[string]any{
			"provider": "github", "repository": "acme/repo", "number": 5,
			"acceptance_criteria": []any{"ok"}, "evidence": []any{"ev"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !dirty.IsError || dirty.Metadata["error_category"] != "dirty_worktree" {
		t.Fatalf("dirty = %+v", dirty)
	}
}

func TestGitHubWriteFailClosedWithoutApprovalHost(t *testing.T) {
	registry := tool.NewRegistry(nil, nil)
	if err := githubtool.Register(registry, githubtool.Options{
		Workspace: t.TempDir(), Backend: passthroughBackend{},
		BaseURL: "http://example.invalid", Token: "tok",
	}); err != nil {
		t.Fatal(err)
	}
	guard, err := toolguard.New(toolguard.Options{
		Registry: registry, Policy: policy.DefaultRuntime(policy.ModeAct, policy.PermissionAuto),
		Workspace: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = guard.Execute(t.Context(), "call-1", "github_comment", mustJSON(map[string]any{
		"provider": "github", "repository": "acme/repo", "number": 1, "body": "x",
	}))
	var decision *policy.DecisionError
	if !errors.As(err, &decision) || decision.Code != "approval_host_unavailable" {
		t.Fatalf("err = %v", err)
	}
}

func TestGitHubCommentFailureReceipt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
	}))
	defer server.Close()

	registry := tool.NewRegistry(nil, nil)
	if err := githubtool.Register(registry, githubtool.Options{
		Workspace: t.TempDir(), Backend: passthroughBackend{},
		BaseURL: server.URL, Token: "tok", Client: server.Client(),
	}); err != nil {
		t.Fatal(err)
	}
	comment := execute(t, registry, "github_comment", map[string]any{
		"provider": "github", "repository": "acme/repo", "number": 4, "body": "hello",
	})
	if !comment.IsError {
		t.Fatalf("expected error comment = %+v", comment)
	}
	if comment.Metadata["error_category"] == nil || comment.Metadata["status_code"] != 404 {
		t.Fatalf("failure receipt incomplete: %+v", comment.Metadata)
	}
}

func TestPRAttemptPreflightRejectReceipt(t *testing.T) {
	root := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "main.txt")
	runGit(t, root, "commit", "-qm", "base")

	registry := tool.NewRegistry(nil, nil)
	if err := githubtool.Register(registry, githubtool.Options{
		Workspace: root, Backend: passthroughBackend{},
	}); err != nil {
		t.Fatal(err)
	}
	// Patch that cannot apply against current tree.
	patch := "diff --git a/missing.txt b/missing.txt\n--- a/missing.txt\n+++ b/missing.txt\n@@ -1 +1 @@\n-old\n+new\n"
	recorded := execute(t, registry, "pr_attempt_record", map[string]any{
		"repository": "acme/repo", "title": "bad", "patch": patch, "task_id": "task_bad",
	})
	var body map[string]any
	_ = json.Unmarshal([]byte(recorded.Content), &body)
	attemptID, _ := body["attempt_id"].(string)
	preflight := execute(t, registry, "pr_attempt_preflight", map[string]any{
		"attempt_id": attemptID,
	})
	if !preflight.IsError || preflight.Metadata["ok"] != false || preflight.Metadata["mutated"] != false {
		t.Fatalf("preflight reject = %+v", preflight)
	}
}

func TestPRAttemptPreflightDoesNotMutate(t *testing.T) {
	root := initGitRepo(t)
	if err := os.WriteFile(filepath.Join(root, "main.txt"), []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "main.txt")
	runGit(t, root, "commit", "-qm", "base")

	registry := tool.NewRegistry(nil, nil)
	if err := githubtool.Register(registry, githubtool.Options{
		Workspace: root, Backend: passthroughBackend{},
	}); err != nil {
		t.Fatal(err)
	}
	patch := "diff --git a/main.txt b/main.txt\n--- a/main.txt\n+++ b/main.txt\n@@ -1 +1 @@\n-before\n+after\n"
	recorded := execute(t, registry, "pr_attempt_record", map[string]any{
		"repository": "acme/repo", "title": "change", "patch": patch, "task_id": "task_1",
	})
	var body map[string]any
	_ = json.Unmarshal([]byte(recorded.Content), &body)
	attemptID, _ := body["attempt_id"].(string)

	preflight := execute(t, registry, "pr_attempt_preflight", map[string]any{
		"attempt_id": attemptID,
	})
	if preflight.IsError || preflight.Metadata["mutated"] != false || preflight.Metadata["ok"] != true {
		t.Fatalf("preflight = %+v", preflight)
	}
	data, err := os.ReadFile(filepath.Join(root, "main.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "before\n" {
		t.Fatalf("worktree mutated: %q", data)
	}
	listed := execute(t, registry, "pr_attempt_list", map[string]any{})
	if listed.Metadata["count"] != 1 {
		t.Fatalf("list = %+v", listed)
	}
	read := execute(t, registry, "pr_attempt_read", map[string]any{"attempt_id": attemptID})
	if !strings.Contains(read.Content, attemptID) {
		t.Fatalf("read = %s", read.Content)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", "/dev/null")
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.email", "fixture@example.invalid")
	runGit(t, root, "config", "user.name", "Fixture")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("ok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, root, "add", "README")
	runGit(t, root, "commit", "-qm", "init")
	return root
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func execute(t *testing.T, registry *tool.Registry, name string, input map[string]any) tool.Result {
	t.Helper()
	result, err := registry.Execute(t.Context(), tool.Call{
		Name: name, Arguments: mustJSON(input), Authorized: true,
	})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return result
}

func mustJSON(value map[string]any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}

type passthroughBackend struct{}

func (passthroughBackend) Capability() sandbox.Capability {
	return sandbox.Capability{
		Platform: "fixture", Backend: "passthrough",
		Strength: sandbox.StrengthStrong, Available: true,
	}
}

func (passthroughBackend) Prepare(_ context.Context, command sandbox.Command) (sandbox.Command, error) {
	return command, nil
}
