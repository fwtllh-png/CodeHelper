package wire

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestLoadRepositoryRules(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repository-rules.json")
	data := `[{"tool":"exec_command","command_prefix":"rm","action":"deny"}]`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	rules, err := loadRepositoryRules(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].CommandPrefix != "rm" ||
		rules[0].Action != policy.ActionDeny {
		t.Fatalf("rules = %+v", rules)
	}
	if err := os.WriteFile(path, []byte(`[{"tool":"exec_command","action":"permit"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadRepositoryRules(path); err == nil {
		t.Fatal("invalid repository action succeeded")
	}
}
