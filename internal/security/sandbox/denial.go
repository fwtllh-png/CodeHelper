package sandbox

import (
	"errors"
	"fmt"
	"strings"
)

type DenialOperation string

const (
	DenialRead    DenialOperation = "read"
	DenialWrite   DenialOperation = "write"
	DenialNetwork DenialOperation = "network"
	DenialProcess DenialOperation = "process"
)

const (
	ReasonPathReadNotAuthorized  = "path_read_not_authorized"
	ReasonPathWriteNotAuthorized = "path_write_not_authorized"
	ReasonNetworkNotAuthorized   = "network_target_not_authorized"
	ReasonProcessNotAuthorized   = "process_capability_not_authorized"
	ReasonWorkspaceTreeDenied    = "workspace_tree_write_denied"
	ReasonEnforcementMismatch    = "enforcement_mismatch"
	ReasonBackendUnavailable     = "backend_unavailable"
	ReasonAuthorityUnverified    = "authority_unverified"
	ReasonRestrictionUnenforced  = "restriction_unenforced"
)

type Denial struct {
	Backend    string          `json:"backend"`
	Operation  DenialOperation `json:"operation"`
	Resource   string          `json:"resource"`
	ReasonCode string          `json:"reason_code"`
	Protocol   string          `json:"protocol,omitempty"`
	Port       uint16          `json:"port,omitempty"`
}

func (d Denial) Validate() error {
	if d.Operation == "" || strings.TrimSpace(d.Resource) == "" ||
		strings.TrimSpace(d.ReasonCode) == "" {
		return errors.New("sandbox denial requires operation, resource, and reason")
	}
	switch d.Operation {
	case DenialRead, DenialWrite, DenialNetwork, DenialProcess:
		return nil
	default:
		return errors.New("sandbox denial operation is invalid")
	}
}

func (d Denial) Amendable() bool {
	switch d.ReasonCode {
	case ReasonPathReadNotAuthorized,
		ReasonPathWriteNotAuthorized,
		ReasonNetworkNotAuthorized,
		ReasonProcessNotAuthorized:
		return true
	default:
		return false
	}
}

type DenialError struct {
	Denial Denial
	Cause  error
}

func (e *DenialError) Error() string {
	if e == nil {
		return "sandbox denied"
	}
	message := fmt.Sprintf(
		"sandbox denied %s on %s (%s)",
		e.Denial.Operation,
		e.Denial.Resource,
		e.Denial.ReasonCode,
	)
	if e.Cause != nil {
		return message + ": " + e.Cause.Error()
	}
	return message
}

func (e *DenialError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

func Denied(denial Denial, cause error) error {
	if err := denial.Validate(); err != nil {
		return errors.Join(err, cause)
	}
	return &DenialError{Denial: denial, Cause: cause}
}

func DenialFromError(err error) (Denial, bool) {
	var denied *DenialError
	if !errors.As(err, &denied) || denied == nil ||
		denied.Denial.Validate() != nil {
		return Denial{}, false
	}
	return denied.Denial, true
}

func WithDenialBackend(err error, backend string) error {
	denial, ok := DenialFromError(err)
	if !ok {
		return err
	}
	if denial.Backend == "" {
		denial.Backend = strings.TrimSpace(backend)
	}
	var typed *DenialError
	_ = errors.As(err, &typed)
	return &DenialError{Denial: denial, Cause: typed.Cause}
}
