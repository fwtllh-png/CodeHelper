package policy

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type Grant struct {
	Kind    string `json:"kind"`
	Key     string `json:"key"`
	Summary string `json:"summary"`
}

func GrantForInvocation(call Invocation) (Grant, bool) {
	resources, kinds := normalizedGrantResources(call.Resources, false)
	kind, summary := "", ""
	hash := sha256.New()
	writeFingerprintField(hash, call.Tool)
	switch {
	case call.Capability == tool.CapabilityProcess:
		var input struct{ Command, CWD string }
		if json.Unmarshal(call.Arguments, &input) != nil {
			return Grant{}, false
		}
		input.Command = strings.TrimSpace(input.Command)
		if input.Command == "" && kinds&4 == 0 {
			return Grant{}, false
		}
		commandIdentity := input.Command
		if input.Command != "" {
			var ok bool
			commandIdentity, ok = commandGrantIdentity(input.Command)
			if !ok {
				return Grant{}, false
			}
		}
		kind, summary = "shell", "command: "+input.Command
		if input.Command == "" {
			kind, summary = "sandbox", "sandbox escalation: "+strings.Join(resources, ", ")
		}
		writeFingerprintField(hash, commandIdentity)
		writeFingerprintField(hash, cleanGrantPath(input.CWD))
	case call.Journaled && len(resources) != 0:
		kind, summary = "file", "workspace paths: "+strings.Join(resources, ", ")
	case call.Capability == tool.CapabilityNetwork:
		resources, _ = normalizedGrantResources(call.Resources, true)
		if len(resources) == 0 {
			return Grant{}, false
		}
		kind, summary = "network", "network endpoints: "+strings.Join(resources, ", ")
	case kinds&2 != 0 && len(resources) != 0:
		kind, summary = "agent", call.Tool+": "+strings.Join(resources, ", ")
	default:
		return Grant{}, false
	}
	for _, resource := range resources {
		writeFingerprintField(hash, resource)
	}
	return Grant{kind, hex.EncodeToString(hash.Sum(nil)), summary}, true
}

func normalizedGrantResources(
	resources []tool.Resource, network bool,
) ([]string, uint8) {
	var values []string
	var kinds uint8
	for _, resource := range resources {
		switch resource.Kind {
		case "agent":
			kinds |= 2
		case "sandbox":
			kinds |= 4
		}
		value := resource.Path
		if value == "" {
			value = resource.ID
		}
		if value == "" || resource.Kind == "parallel" {
			continue
		}
		if network {
			target, ok := ParseNetworkTarget(value)
			if resource.Kind != "host" && resource.Kind != "url" || !ok {
				continue
			}
			value = target.Protocol + "://" + target.Host
		} else {
			value = resource.Kind + ":" + cleanGrantPath(value) + ":" + string(resource.Access)
		}
		values = append(values, value)
	}
	sort.Strings(values)
	result := values[:0]
	for _, value := range values {
		if len(result) == 0 || result[len(result)-1] != value {
			result = append(result, value)
		}
	}
	return result, kinds
}

func cleanGrantPath(value string) string {
	if value = strings.TrimSpace(value); value == "" {
		return "."
	}
	return filepath.ToSlash(filepath.Clean(value))
}
