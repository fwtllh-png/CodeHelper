package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestLoadMissingIsEmpty(t *testing.T) {
	root := t.TempDir()
	bundle, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if bundle.Present || len(bundle.Rules) != 0 {
		t.Fatalf("bundle = %+v", bundle)
	}
}

func TestLoadAndAppendAllowRoundTrip(t *testing.T) {
	root := t.TempDir()
	path := Path(root)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	content := `
[[deny]]
tool = "shell_run"
command_prefix = "rm"

[[ask]]
tool = "file_write"
resource = "secrets/"

[[allow]]
tool = "shell_run"
grant_key = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	bundle, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	deny, ask, allow := bundle.Summary()
	if !bundle.Present || deny != 1 || ask != 1 || allow != 1 || len(bundle.Rules) != 3 {
		t.Fatalf("bundle = %+v summary=%d/%d/%d", bundle, deny, ask, allow)
	}
	if bundle.Rules[0].Action != policy.ActionDeny || bundle.Rules[2].Action != policy.ActionAllow {
		t.Fatalf("rules = %+v", bundle.Rules)
	}

	updated, err := AppendAllow(root, policy.Rule{
		Tool: "file_write", GrantKey: strings.Repeat("b", 64), Action: policy.ActionAllow,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, allow = updated.Summary()
	if allow != 2 {
		t.Fatalf("allow count = %d", allow)
	}
	reloaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	_, _, allow = reloaded.Summary()
	if allow != 2 {
		t.Fatalf("reloaded allow = %d", allow)
	}
	// idempotent
	again, err := AppendAllow(root, policy.Rule{
		Tool: "file_write", GrantKey: strings.Repeat("b", 64), Action: policy.ActionAllow,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, allow = again.Summary()
	if allow != 2 {
		t.Fatalf("dedupe allow = %d", allow)
	}
}

func TestRuleFromInvocation(t *testing.T) {
	shell, err := RuleFromInvocation(policy.Invocation{
		Tool: "shell_run", Arguments: json.RawMessage(`{"command":"go test ./..."}`),
		Capability: tool.CapabilityProcess, Access: tool.AccessRead,
		Sandbox: tool.SandboxStrong,
	})
	if err != nil || len(shell.GrantKey) != 64 || shell.Action != policy.ActionAllow {
		t.Fatalf("shell = %+v err=%v", shell, err)
	}
	file, err := RuleFromInvocation(policy.Invocation{
		Tool: "file_write", Arguments: json.RawMessage(`{"path":"a.txt"}`),
		Resources:  []tool.Resource{{Kind: "file", Path: "a.txt", Access: tool.AccessWrite}},
		Capability: tool.CapabilityWrite, Access: tool.AccessWrite,
		Sandbox: tool.SandboxNone, Journaled: true,
	})
	if err != nil || len(file.GrantKey) != 64 {
		t.Fatalf("file = %+v err=%v", file, err)
	}
	host, err := RuleFromInvocation(policy.Invocation{
		Tool: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.com/a"}`),
		Resources:  []tool.Resource{{Kind: "host", ID: "example.com", Access: tool.AccessRead}},
		Capability: tool.CapabilityNetwork, Access: tool.AccessRead, Sandbox: tool.SandboxNone,
	})
	if err != nil || len(host.GrantKey) != 64 {
		t.Fatalf("host = %+v err=%v", host, err)
	}
}

func TestStoreSerializesConcurrentAllows(t *testing.T) {
	store, err := OpenStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	const count = 8
	var group sync.WaitGroup
	for index := range count {
		group.Add(1)
		go func() {
			defer group.Done()
			_, appendErr := store.AppendAllow(policy.Invocation{
				Tool:       "web_fetch",
				Capability: tool.CapabilityNetwork,
				Access:     tool.AccessRead, Sandbox: tool.SandboxNone,
				Resources: []tool.Resource{{
					Kind: "host", ID: fmt.Sprintf("host-%d.example", index),
					Access: tool.AccessRead,
				}},
			})
			if appendErr != nil {
				t.Error(appendErr)
			}
		}()
	}
	group.Wait()
	if rules := store.Rules(); len(rules) != count {
		t.Fatalf("rules = %d, want %d", len(rules), count)
	}
}

func TestPersistedGrantMatchesOnlySameTypedInvocation(t *testing.T) {
	root := t.TempDir()
	base := policy.Invocation{
		CallID: "one", Tool: "shell_run", Capability: tool.CapabilityProcess,
		Access: tool.AccessRead, Sandbox: tool.SandboxStrong, Validated: true,
		Arguments: json.RawMessage(`{"command":"go test ./...","cwd":"."}`),
		Resources: []tool.Resource{{
			Kind: "process", ID: "workspace", Access: tool.AccessRead, Tree: true,
		}, {
			Kind: "file", Path: "report.txt", Access: tool.AccessWrite,
		}},
	}
	rule, err := RuleFromInvocation(base)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AppendAllow(root, rule); err != nil {
		t.Fatal(err)
	}
	reloaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	runtime := policy.DefaultRuntime(policy.ModeAct, policy.PermissionSuggest)
	runtime.Repository = reloaded.Rules
	if decision := runtime.Evaluate(base); decision.Action != policy.ActionAllow {
		t.Fatalf("same invocation = %+v", decision)
	}
	changed := base
	changed.CallID = "two"
	changed.Arguments = json.RawMessage(`{"command":"go env","cwd":"."}`)
	if decision := runtime.Evaluate(changed); decision.Action != policy.ActionAsk {
		t.Fatalf("changed invocation = %+v", decision)
	}
}
