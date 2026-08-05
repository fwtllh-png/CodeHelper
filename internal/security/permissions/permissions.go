// Package permissions loads and persists workspace permissions.toml rules.
package permissions

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

const FileName = "permissions.toml"

// Entry is one deny/ask/allow stanza in permissions.toml.
type Entry struct {
	Tool          string `toml:"tool"`
	Resource      string `toml:"resource,omitempty"`
	CommandPrefix string `toml:"command_prefix,omitempty"`
	Code          string `toml:"code,omitempty"`
}

// Document is the on-disk permissions schema.
type Document struct {
	Deny  []Entry `toml:"deny,omitempty"`
	Ask   []Entry `toml:"ask,omitempty"`
	Allow []Entry `toml:"allow,omitempty"`
}

// Bundle is the compiled permissions file.
type Bundle struct {
	Path    string
	Present bool
	Rules   []policy.Rule
	Doc     Document
}

// Path returns `{workspace}/.codehelper/permissions.toml`.
func Path(workspace string) string {
	return filepath.Join(workspace, ".codehelper", FileName)
}

// Load reads permissions.toml. Missing file yields an empty bundle.
func Load(workspace string) (Bundle, error) {
	path := Path(workspace)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Bundle{Path: path}, nil
		}
		return Bundle{}, err
	}
	var doc Document
	if err := toml.Unmarshal(data, &doc); err != nil {
		return Bundle{}, fmt.Errorf("parse permissions.toml: %w", err)
	}
	rules, err := compile(doc)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{Path: path, Present: true, Rules: rules, Doc: doc}, nil
}

// Summary returns deny/ask/allow counts for TUI/CLI display.
func (b Bundle) Summary() (deny, ask, allow int) {
	return len(b.Doc.Deny), len(b.Doc.Ask), len(b.Doc.Allow)
}

// AppendAllow appends an allow rule and rewrites permissions.toml atomically.
func AppendAllow(workspace string, rule policy.Rule) (Bundle, error) {
	if rule.Tool == "" {
		return Bundle{}, errors.New("allow rule tool is required")
	}
	rule.Action = policy.ActionAllow
	bundle, err := Load(workspace)
	if err != nil {
		return Bundle{}, err
	}
	entry := Entry{
		Tool: rule.Tool, Resource: rule.Resource,
		CommandPrefix: rule.CommandPrefix, Code: rule.Code,
	}
	for _, existing := range bundle.Doc.Allow {
		if entryEqual(existing, entry) {
			return bundle, nil
		}
	}
	bundle.Doc.Allow = append(bundle.Doc.Allow, entry)
	if err := writeDocument(bundle.Path, bundle.Doc); err != nil {
		return Bundle{}, err
	}
	rules, err := compile(bundle.Doc)
	if err != nil {
		return Bundle{}, err
	}
	bundle.Present = true
	bundle.Rules = rules
	return bundle, nil
}

// RuleFromInvocation builds a durable allow rule for shell/file tools.
func RuleFromInvocation(invocation policy.Invocation) (policy.Rule, error) {
	toolName := strings.TrimSpace(invocation.Tool)
	if toolName == "" {
		return policy.Rule{}, errors.New("invocation tool is required")
	}
	switch {
	case isShellTool(toolName):
		prefix, err := commandPrefix(invocation.Arguments)
		if err != nil {
			return policy.Rule{}, err
		}
		return policy.Rule{
			Tool: toolName, CommandPrefix: prefix, Action: policy.ActionAllow,
			Code: "permissions_always_allow",
		}, nil
	case isFileWriteTool(toolName):
		resource, err := primaryWritePath(invocation)
		if err != nil {
			return policy.Rule{}, err
		}
		return policy.Rule{
			Tool: toolName, Resource: resource, Action: policy.ActionAllow,
			Code: "permissions_always_allow",
		}, nil
	default:
		resource := "*"
		for _, value := range invocation.Resources {
			if value.Kind == "host" && strings.TrimSpace(value.ID) != "" {
				resource = strings.ToLower(strings.TrimSpace(value.ID))
				break
			}
		}
		return policy.Rule{
			Tool: toolName, Resource: resource, Action: policy.ActionAllow,
			Code: "permissions_always_allow",
		}, nil
	}
}

func compile(doc Document) ([]policy.Rule, error) {
	var rules []policy.Rule
	add := func(entries []Entry, action policy.Action) error {
		for _, entry := range entries {
			if strings.TrimSpace(entry.Tool) == "" {
				return errors.New("permissions entry tool is required")
			}
			rules = append(rules, policy.Rule{
				Tool: entry.Tool, Resource: entry.Resource,
				CommandPrefix: entry.CommandPrefix, Action: action, Code: entry.Code,
			})
		}
		return nil
	}
	if err := add(doc.Deny, policy.ActionDeny); err != nil {
		return nil, err
	}
	if err := add(doc.Ask, policy.ActionAsk); err != nil {
		return nil, err
	}
	if err := add(doc.Allow, policy.ActionAllow); err != nil {
		return nil, err
	}
	return rules, nil
}

func writeDocument(path string, doc Document) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := toml.Marshal(doc)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func entryEqual(a, b Entry) bool {
	return a.Tool == b.Tool && a.Resource == b.Resource &&
		a.CommandPrefix == b.CommandPrefix && a.Code == b.Code
}

func isShellTool(name string) bool {
	switch name {
	case "shell_run", "shell_pty", "task_shell_start", "task_shell_wait":
		return true
	default:
		return strings.HasPrefix(name, "task_shell_")
	}
}

func isFileWriteTool(name string) bool {
	switch name {
	case "file_write", "file_edit", "file_patch":
		return true
	default:
		return false
	}
}

func commandPrefix(raw json.RawMessage) (string, error) {
	var input struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("shell arguments: %w", err)
	}
	fields := strings.Fields(strings.TrimSpace(input.Command))
	if len(fields) == 0 {
		return "", errors.New("shell command is empty")
	}
	return fields[0], nil
}

func primaryWritePath(invocation policy.Invocation) (string, error) {
	for _, resource := range invocation.Resources {
		if resource.Kind == "file" && resource.Path != "" {
			return filepath.ToSlash(filepath.Clean(resource.Path)), nil
		}
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := json.Unmarshal(invocation.Arguments, &input); err == nil && strings.TrimSpace(input.Path) != "" {
		return filepath.ToSlash(filepath.Clean(input.Path)), nil
	}
	return "", errors.New("write path is required for allow persistence")
}
