package policy

import (
	"encoding/json"
	"net"
	"net/url"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

// NetworkApprovalMode distinguishes when host approval is collected.
// Immediate: ask before Dial (web/MCP). Deferred: ask on first dial while a
// process may already be running (shell managed-egress; future).
type NetworkApprovalMode string

const (
	NetworkImmediate NetworkApprovalMode = "immediate"
	NetworkDeferred  NetworkApprovalMode = "deferred"
)

// NetworkTarget is a normalized network endpoint for host-scoped approvals.
type NetworkTarget struct {
	Host     string
	Protocol string
}

// ParseNetworkTarget extracts host+protocol from a URL or bare host:port.
func ParseNetworkTarget(raw string) (NetworkTarget, bool) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return NetworkTarget{}, false
	}
	if strings.Contains(value, "://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.Hostname() == "" {
			return NetworkTarget{}, false
		}
		protocol := strings.ToLower(parsed.Scheme)
		if protocol == "" {
			protocol = "https"
		}
		return NetworkTarget{Host: normalizeHost(parsed.Hostname()), Protocol: protocol}, true
	}
	// Bare host or host:port (no spaces — reject search queries).
	if strings.ContainsAny(value, " \t\r\n/?#") {
		return NetworkTarget{}, false
	}
	host := value
	if h, _, err := net.SplitHostPort(value); err == nil {
		host = h
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return NetworkTarget{}, false
	}
	// Allow IPs, localhost, and dotted names; reject single tokens like "hello".
	if net.ParseIP(host) == nil && host != "localhost" && !strings.Contains(host, ".") {
		return NetworkTarget{}, false
	}
	return NetworkTarget{Host: normalizeHost(host), Protocol: "https"}, true
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

// HostScopedInvocation rebuilds an invocation fingerprinted by tool + host
// resources only (empty args), so session grants reuse across URLs on one host.
func HostScopedInvocation(invocation Invocation) (Invocation, bool) {
	var hosts []tool.Resource
	for _, resource := range invocation.Resources {
		if resource.Kind != "host" || strings.TrimSpace(resource.ID) == "" {
			continue
		}
		hosts = append(hosts, tool.Resource{
			Kind: "host", ID: normalizeHost(resource.ID), Access: resource.Access, Tree: resource.Tree,
		})
	}
	if len(hosts) == 0 {
		return Invocation{}, false
	}
	return Invocation{
		CallID: invocation.CallID, Tool: invocation.Tool, Arguments: json.RawMessage(`{}`),
		Resources: hosts, Capability: invocation.Capability, Validated: true,
	}, true
}

// HostResource builds a canonical host resource for Guard/policy.
func HostResource(target NetworkTarget, access tool.AccessMode) tool.Resource {
	return tool.Resource{Kind: "host", ID: target.Host, Access: access}
}
