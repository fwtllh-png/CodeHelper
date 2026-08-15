package process

import (
	"errors"
	"os"
	"sort"
	"strings"
)

var allowedEnvironment = map[string]bool{
	"PATH": true, "HOME": true, "TMPDIR": true, "TMP": true, "TEMP": true,
	"LANG": true, "LC_ALL": true, "TERM": true, "COLORTERM": true,
	"USER": true, "LOGNAME": true, "SHELL": true,
	"GOROOT": true, "GOPATH": true, "GOCACHE": true, "GOMODCACHE": true,
	"GOTOOLCHAIN": true, "GOFLAGS": true, "GO111MODULE": true,
	"GOPROXY": true, "GOPRIVATE": true, "GONOPROXY": true,
	"GOSUMDB": true, "GONOSUMDB": true, "GOVCS": true,
	"HTTP_PROXY": true, "HTTPS_PROXY": true, "ALL_PROXY": true, "NO_PROXY": true,
	"http_proxy": true, "https_proxy": true, "all_proxy": true, "no_proxy": true,
	"OPENSSL_CONF": true,
	"SYSTEMROOT":   true, "COMSPEC": true, "PATHEXT": true, "WINDIR": true,
}

func SanitizedEnvironment(extra []string) ([]string, error) {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || !environmentAllowed(name) || SecretEnvironmentName(name) {
			continue
		}
		values[name] = value
	}
	for _, entry := range extra {
		name, value, ok := strings.Cut(entry, "=")
		if !ok || name == "" {
			return nil, errors.New("environment entries must use NAME=value")
		}
		if SecretEnvironmentName(name) {
			return nil, errors.New("secret environment variables cannot be passed to child processes")
		}
		if !environmentAllowed(name) {
			return nil, errors.New("environment variable is not in the child-process allow-list")
		}
		values[name] = value
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	sort.Strings(names)
	result := make([]string, 0, len(names))
	for _, name := range names {
		result = append(result, name+"="+values[name])
	}
	return result, nil
}

func SecretEnvironmentName(name string) bool {
	upper := strings.ToUpper(name)
	for _, marker := range []string{
		"API_KEY", "APIKEY", "TOKEN", "SECRET", "PASSWORD", "PASSWD",
		"CREDENTIAL", "AUTHORIZATION", "PRIVATE_KEY", "ACCESS_KEY", "COOKIE",
	} {
		if strings.Contains(upper, marker) {
			return true
		}
	}
	return false
}

func environmentAllowed(name string) bool {
	return allowedEnvironment[name] || strings.HasPrefix(name, "LC_")
}
