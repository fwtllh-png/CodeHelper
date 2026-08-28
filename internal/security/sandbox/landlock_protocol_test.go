//go:build !windows

package sandbox

import (
	"bytes"
	"slices"
	"strings"
	"testing"
)

func TestLandlockHelperProtocolRoundTripAndSecretIsolation(t *testing.T) {
	secret := "fixture-secret-must-not-enter-helper-argv"
	request := landlockRequest{
		SchemaVersion: landlockSchemaVersion,
		PolicyID:      "sandbox-v2-0123456789abcdef",
		SyscallPolicy: syscallPolicyRestricted,
		ReadOnly:      []string{"/bin", "/usr"},
		ReadWrite:     []string{"/private/tmp", "/workspace"},
		Executable:    "/bin/sh",
		Arguments:     []string{"/bin/sh", "-c", "printf " + secret},
	}
	encoded, err := encodeLandlockRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeLandlockRequest(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(decoded.Arguments, request.Arguments) ||
		decoded.PolicyID != request.PolicyID {
		t.Fatalf("decoded request = %+v", decoded)
	}
	helperArgs := landlockHelperArgs(
		"/helper", "/private/tmp/request", request.PolicyID,
	)
	if strings.Contains(strings.Join(helperArgs, "\x00"), secret) {
		t.Fatalf("secret entered helper argv: %q", helperArgs)
	}
}

func TestLandlockHelperProtocolRejectsUnknownAndMalformedInput(t *testing.T) {
	valid := `{
		"schema_version":1,
		"policy_id":"sandbox-v2-0123456789abcdef",
		"syscall_policy":"restricted",
		"read_only":["/bin"],
		"read_write":["/workspace"],
		"executable":"/bin/sh",
		"arguments":["/bin/sh"]
	}`
	for name, input := range map[string]string{
		"unknown":  strings.Replace(valid, `"arguments"`, `"unknown":true,"arguments"`, 1),
		"trailing": valid + `{}`,
		"root":     strings.Replace(valid, `["/bin"]`, `["/"]`, 1),
		"unsorted": strings.Replace(valid, `["/bin"]`, `["/usr","/bin"]`, 1),
		"argv":     strings.Replace(valid, `["/bin/sh"]`, `["sh"]`, 1),
		"syscalls": strings.Replace(valid, `"restricted"`, `"unknown"`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeLandlockRequest(strings.NewReader(input)); err == nil {
				t.Fatalf("malformed helper request was accepted: %s", input)
			}
		})
	}
}

func TestPolicySyscallModeIsMonotonic(t *testing.T) {
	for _, test := range []struct {
		policy Policy
		want   string
	}{
		{policy: Policy{}, want: syscallPolicyRestricted},
		{policy: Policy{ManagedProxyPort: 43128}, want: syscallPolicyProxyRouted},
		{policy: Policy{AllowNetwork: true}, want: syscallPolicyDirect},
	} {
		if got := policySyscallMode(test.policy); got != test.want {
			t.Fatalf("policySyscallMode(%+v) = %q, want %q", test.policy, got, test.want)
		}
	}
}

func TestLandlockHelperArgumentsRejectUnknownOrSecretBearingParameters(t *testing.T) {
	valid := []string{"--request", "/private/tmp/request", "--policy-id", "sandbox-v2-id"}
	if path, policyID, err := parseLandlockHelperArguments(valid); err != nil ||
		path != valid[1] || policyID != valid[3] {
		t.Fatalf("parse valid helper arguments = %q, %q, %v", path, policyID, err)
	}
	for _, arguments := range [][]string{
		{"--request", "relative", "--policy-id", "sandbox-v2-id"},
		{"--request", "/private/tmp/request", "--unknown", "value"},
		append(append([]string{}, valid...), "--secret", "fixture-secret"),
	} {
		if _, _, err := parseLandlockHelperArguments(arguments); err == nil {
			t.Fatalf("helper arguments were accepted: %q", arguments)
		}
	}
}
