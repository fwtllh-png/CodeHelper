package policy

import (
	"net"
	"net/url"
	"strings"

	"github.com/fwtllh-png/CodeHelper/internal/adapter/tool"
)

type NetworkApprovalMode string

const (
	NetworkImmediate NetworkApprovalMode = "immediate"
	NetworkDeferred  NetworkApprovalMode = "deferred"
)

type NetworkTarget struct {
	Host     string
	Protocol string
}

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
	if net.ParseIP(host) == nil && host != "localhost" && !strings.Contains(host, ".") {
		return NetworkTarget{}, false
	}
	return NetworkTarget{Host: normalizeHost(host), Protocol: "https"}, true
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func HostResource(target NetworkTarget, access tool.AccessMode) tool.Resource {
	return tool.Resource{Kind: "host", ID: target.Host, Access: access}
}
