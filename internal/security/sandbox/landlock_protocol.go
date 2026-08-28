package sandbox

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"strings"
)

const (
	landlockHelperArgument = "__codehelper_internal_landlock_exec_v1"
	landlockSchemaVersion  = 1
	maxLandlockRequestSize = 1 << 20
)

type landlockRequest struct {
	SchemaVersion int      `json:"schema_version"`
	PolicyID      string   `json:"policy_id"`
	SyscallPolicy string   `json:"syscall_policy"`
	ReadOnly      []string `json:"read_only"`
	ReadWrite     []string `json:"read_write"`
	Executable    string   `json:"executable"`
	Arguments     []string `json:"arguments"`
}

func encodeLandlockRequest(request landlockRequest) ([]byte, error) {
	if err := validateLandlockRequest(request); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	if len(encoded) > maxLandlockRequestSize {
		return nil, errors.New("Landlock helper request exceeds 1 MiB")
	}
	return append(encoded, '\n'), nil
}

func decodeLandlockRequest(reader io.Reader) (landlockRequest, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxLandlockRequestSize+1))
	if err != nil {
		return landlockRequest{}, err
	}
	if len(data) > maxLandlockRequestSize {
		return landlockRequest{}, errors.New("Landlock helper request exceeds 1 MiB")
	}
	var request landlockRequest
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return landlockRequest{}, fmt.Errorf("decode Landlock helper request: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return landlockRequest{}, errors.New("Landlock helper request contains multiple JSON values")
		}
		return landlockRequest{}, fmt.Errorf("decode trailing Landlock helper data: %w", err)
	}
	if err := validateLandlockRequest(request); err != nil {
		return landlockRequest{}, err
	}
	return request, nil
}

func validateLandlockRequest(request landlockRequest) error {
	if request.SchemaVersion != landlockSchemaVersion {
		return errors.New("unsupported Landlock helper schema version")
	}
	if !strings.HasPrefix(request.PolicyID, "sandbox-v2-") {
		return errors.New("invalid Landlock helper policy identity")
	}
	if !validSyscallPolicy(request.SyscallPolicy) {
		return errors.New("invalid Linux syscall policy")
	}
	if err := validateLandlockPaths("read_only", request.ReadOnly); err != nil {
		return err
	}
	if err := validateLandlockPaths("read_write", request.ReadWrite); err != nil {
		return err
	}
	if request.Executable == "" || !filepath.IsAbs(request.Executable) ||
		filepath.Clean(request.Executable) != request.Executable ||
		request.Executable == string(filepath.Separator) ||
		strings.IndexByte(request.Executable, 0) >= 0 {
		return errors.New("Landlock helper executable must be a canonical absolute file")
	}
	if len(request.Arguments) == 0 || request.Arguments[0] != request.Executable {
		return errors.New("Landlock helper argv must begin with the executable literal")
	}
	for _, argument := range request.Arguments {
		if strings.IndexByte(argument, 0) >= 0 {
			return errors.New("Landlock helper argv contains NUL")
		}
	}
	return nil
}

const (
	syscallPolicyRestricted  = "restricted"
	syscallPolicyProxyRouted = "proxy_routed"
	syscallPolicyDirect      = "direct"
)

func validSyscallPolicy(value string) bool {
	switch value {
	case syscallPolicyRestricted, syscallPolicyProxyRouted, syscallPolicyDirect:
		return true
	default:
		return false
	}
}

func policySyscallMode(policy Policy) string {
	if policy.ManagedProxyPort != 0 {
		return syscallPolicyProxyRouted
	}
	if policy.AllowNetwork {
		return syscallPolicyDirect
	}
	return syscallPolicyRestricted
}

func validateLandlockPaths(field string, paths []string) error {
	if len(paths) == 0 {
		return fmt.Errorf("Landlock helper %s roots are required", field)
	}
	if !slices.IsSorted(paths) {
		return fmt.Errorf("Landlock helper %s roots must be sorted", field)
	}
	for index, path := range paths {
		if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path ||
			path == string(filepath.Separator) || strings.IndexByte(path, 0) >= 0 {
			return fmt.Errorf("Landlock helper %s root %d is not canonical", field, index)
		}
		if index > 0 && paths[index-1] == path {
			return fmt.Errorf("Landlock helper %s roots contain a duplicate", field)
		}
	}
	return nil
}

func landlockHelperArgs(helper, requestPath, policyID string) []string {
	return []string{
		helper, landlockHelperArgument,
		"--request", requestPath,
		"--policy-id", policyID,
	}
}

func parseLandlockHelperArguments(arguments []string) (string, string, error) {
	if len(arguments) != 4 || arguments[0] != "--request" || arguments[2] != "--policy-id" {
		return "", "", errors.New("invalid helper arguments")
	}
	requestPath := arguments[1]
	policyID := arguments[3]
	if requestPath == "" || !filepath.IsAbs(requestPath) ||
		filepath.Clean(requestPath) != requestPath ||
		policyID == "" || strings.IndexByte(policyID, 0) >= 0 {
		return "", "", errors.New("invalid helper request identity")
	}
	return requestPath, policyID, nil
}
