package protocol

import (
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

func (c Case) Validate() error {
	if err := validateIdentity(c.Version, c.ID, c.Revision, c.Digest); err != nil {
		return fmt.Errorf("evaluation case: %w", err)
	}
	if strings.TrimSpace(c.Category) == "" || strings.TrimSpace(c.Prompt) == "" {
		return errors.New("evaluation case category and prompt are required")
	}
	if c.Execution.MaxSteps < 0 || c.Execution.TimeoutMS < 0 {
		return errors.New("evaluation case execution limits must not be negative")
	}
	if !c.Expectation.Terminal.valid() {
		return fmt.Errorf(
			"evaluation case terminal status %q is invalid",
			c.Expectation.Terminal,
		)
	}
	return nil
}

func (r Result) Validate() error {
	if err := validateIdentity(r.Version, r.ID, r.Revision, r.Digest); err != nil {
		return fmt.Errorf("evaluation result: %w", err)
	}
	if strings.TrimSpace(r.CaseID) == "" || r.CaseRevision == 0 ||
		!validDigest(r.CaseDigest) {
		return errors.New("evaluation result case identity is incomplete")
	}
	if !r.Status.valid() {
		return fmt.Errorf("evaluation result status %q is invalid", r.Status)
	}
	if r.Status == ResultUnavailable {
		if r.Terminal != "" {
			return errors.New("unavailable evaluation result must not have a terminal status")
		}
	} else if !r.Terminal.valid() {
		return fmt.Errorf("evaluation result terminal status %q is invalid", r.Terminal)
	}
	if r.RetryAttempts < 0 || r.Usage.CostMicrounits < 0 ||
		r.Usage.Calls < 0 || r.Usage.UnpricedCalls < 0 ||
		r.Usage.UnpricedCalls > r.Usage.Calls {
		return errors.New("evaluation result counters are invalid")
	}
	if r.Verification != nil {
		if r.Verification.RepairSteps < 0 ||
			!validVerificationStatus(r.Verification.Status) ||
			!validVerificationAction(r.Verification.Action) {
			return errors.New("evaluation result verification is invalid")
		}
	}
	return nil
}

func validateIdentity(version int, id string, revision uint64, digest string) error {
	if version != Version {
		return fmt.Errorf("version %d is unsupported", version)
	}
	if strings.TrimSpace(id) == "" {
		return errors.New("id is required")
	}
	if revision == 0 {
		return errors.New("revision is required")
	}
	if !validDigest(digest) {
		return errors.New("digest must be a sha256 value")
	}
	return nil
}

func validDigest(value string) bool {
	const prefix = "sha256:"
	if !strings.HasPrefix(value, prefix) {
		return false
	}
	encoded := value[len(prefix):]
	if len(encoded) != 64 {
		return false
	}
	_, err := hex.DecodeString(encoded)
	return err == nil && encoded == strings.ToLower(encoded)
}

func (s ResultStatus) valid() bool {
	return s == ResultPassed || s == ResultFailed || s == ResultUnavailable
}

func (s TerminalStatus) valid() bool {
	return s == TerminalCompleted || s == TerminalFailed ||
		s == TerminalCanceled || s == TerminalIncomplete
}

func validVerificationStatus(value string) bool {
	switch value {
	case "passed", "failed", "unavailable", "not_evaluated":
		return true
	default:
		return false
	}
}

func validVerificationAction(value string) bool {
	switch value {
	case "passed", "repair", "reported", "blocked", "failed", "reverted", "skipped":
		return true
	default:
		return false
	}
}
