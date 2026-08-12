package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
command_prefix = "go"
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
		Tool: "file_write", Resource: "notes.txt", Action: policy.ActionAllow,
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
		Tool: "file_write", Resource: "notes.txt", Action: policy.ActionAllow,
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
	})
	if err != nil || shell.CommandPrefix != "go" || shell.Action != policy.ActionAllow {
		t.Fatalf("shell = %+v err=%v", shell, err)
	}
	file, err := RuleFromInvocation(policy.Invocation{
		Tool: "file_write", Arguments: json.RawMessage(`{"path":"a.txt"}`),
		Resources: []tool.Resource{{Kind: "file", Path: "a.txt", Access: tool.AccessWrite}},
	})
	if err != nil || file.Resource != "a.txt" {
		t.Fatalf("file = %+v err=%v", file, err)
	}
	host, err := RuleFromInvocation(policy.Invocation{
		Tool: "web_fetch", Arguments: json.RawMessage(`{"url":"https://example.com/a"}`),
		Resources: []tool.Resource{{Kind: "host", ID: "example.com", Access: tool.AccessRead}},
	})
	if err != nil || host.Resource != "example.com" {
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
				Tool: "web_fetch",
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
