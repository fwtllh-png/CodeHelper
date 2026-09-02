package policy

import (
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/fwtllh-png/QCode/internal/adapter/tool"
)

type NetworkApprovalMode string

const (
	NetworkImmediate NetworkApprovalMode = "immediate"
	NetworkDeferred  NetworkApprovalMode = "deferred"
)

type NetworkTarget struct {
	Host     string
	Protocol string
	Port     uint16
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
		port := defaultNetworkPort(protocol)
		if parsed.Port() != "" {
			value, parseErr := strconv.ParseUint(parsed.Port(), 10, 16)
			if parseErr != nil || value == 0 {
				return NetworkTarget{}, false
			}
			port = uint16(value)
		}
		return NetworkTarget{
			Host: normalizeHost(parsed.Hostname()), Protocol: protocol, Port: port,
		}, true
	}
	if strings.ContainsAny(value, " \t\r\n/?#") {
		return NetworkTarget{}, false
	}
	host := value
	port := uint16(443)
	if h, rawPort, err := net.SplitHostPort(value); err == nil {
		host = h
		parsed, parseErr := strconv.ParseUint(rawPort, 10, 16)
		if parseErr != nil || parsed == 0 {
			return NetworkTarget{}, false
		}
		port = uint16(parsed)
	}
	host = strings.Trim(host, "[]")
	if host == "" {
		return NetworkTarget{}, false
	}
	if net.ParseIP(host) == nil && host != "localhost" && !strings.Contains(host, ".") {
		return NetworkTarget{}, false
	}
	return NetworkTarget{
		Host: normalizeHost(host), Protocol: "https", Port: port,
	}, true
}

func normalizeHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func HostResource(target NetworkTarget, access tool.AccessMode) tool.Resource {
	return tool.Resource{
		Kind: "host", ID: target.Host, Access: access,
		Protocol: target.Protocol, Port: target.Port,
	}
}

func defaultNetworkPort(protocol string) uint16 {
	if protocol == "http" {
		return 80
	}
	return 443
}
