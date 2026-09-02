package permissions

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	toml "github.com/pelletier/go-toml/v2"

	"github.com/fwtllh-png/QCode/internal/security/policy"
)

const FileName = "permissions.toml"

type Entry struct {
	Tool          string `toml:"tool"`
	Resource      string `toml:"resource,omitempty"`
	CommandPrefix string `toml:"command_prefix,omitempty"`
	GrantKey      string `toml:"grant_key,omitempty"`
	Code          string `toml:"code,omitempty"`
}
type Document struct {
	Deny  []Entry `toml:"deny,omitempty"`
	Ask   []Entry `toml:"ask,omitempty"`
	Allow []Entry `toml:"allow,omitempty"`
}
type Bundle struct {
	Path    string
	Present bool
	Rules   []policy.Rule
	Doc     Document
}

func Load(path string) (Bundle, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Bundle{Path: path}, nil
		}
		return Bundle{}, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return Bundle{}, err
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return Bundle{}, errors.New("permissions authority must be a regular file")
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
func (b Bundle) Summary() (deny, ask, allow int) {
	return len(b.Doc.Deny), len(b.Doc.Ask), len(b.Doc.Allow)
}
func AppendAllow(path string, rule policy.Rule) (Bundle, error) {
	if rule.Tool == "" {
		return Bundle{}, errors.New("allow rule tool is required")
	}
	if len(rule.GrantKey) != 64 {
		return Bundle{}, errors.New("allow rule grant_key must be a SHA-256")
	}
	rule.Action = policy.ActionAllow
	bundle, err := Load(path)
	if err != nil {
		return Bundle{}, err
	}
	entry := Entry{
		Tool: rule.Tool, Resource: rule.Resource,
		CommandPrefix: rule.CommandPrefix, GrantKey: rule.GrantKey, Code: rule.Code,
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
func RuleFromInvocation(invocation policy.Invocation) (policy.Rule, error) {
	grant, ok := policy.GrantForInvocation(invocation)
	if !ok {
		return policy.Rule{}, errors.New("invocation has no persistable typed grant")
	}
	return policy.Rule{
		Tool: invocation.Tool, GrantKey: grant.Key, Action: policy.ActionAllow,
		Code: "permissions_always_allow",
	}, nil
}
func compile(doc Document) ([]policy.Rule, error) {
	var rules []policy.Rule
	add := func(entries []Entry, action policy.Action) error {
		for _, entry := range entries {
			if action == policy.ActionAllow && len(entry.GrantKey) != 64 {
				return errors.New("permissions allow entry requires a SHA-256 grant_key")
			}
			rules = append(rules, policy.Rule{
				Tool: entry.Tool, Resource: entry.Resource,
				CommandPrefix: entry.CommandPrefix, GrantKey: entry.GrantKey,
				Action: action, Code: entry.Code,
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
	if err := policy.ValidateRules(policy.SourceUser, rules); err != nil {
		return nil, err
	}
	return rules, nil
}
func writeDocument(path string, doc Document) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.Chmod(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
			return errors.New("permissions authority must be a regular file")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	data, err := toml.Marshal(doc)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".permissions-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err = tmp.Chmod(0o600); err == nil {
		_, err = tmp.Write(data)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}
func entryEqual(a, b Entry) bool {
	return a.Tool == b.Tool && a.Resource == b.Resource &&
		a.CommandPrefix == b.CommandPrefix && a.GrantKey == b.GrantKey &&
		a.Code == b.Code
}
