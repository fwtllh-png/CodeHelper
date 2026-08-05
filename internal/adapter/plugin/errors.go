package plugin

import "errors"

var ErrDigestMismatch = errors.New("plugin extension digest mismatch")

const (
	ErrorCategorySignatureInvalid = "extension_signature_invalid"
	ErrorCategoryDigestMismatch   = "extension_digest_mismatch"
)

// ErrorCategory maps Registry verification failures onto stable machine
// categories without exposing remote or filesystem error strings.
func ErrorCategory(err error) string {
	switch {
	case errors.Is(err, ErrUnknownPublisher), errors.Is(err, ErrInvalidSignature):
		return ErrorCategorySignatureInvalid
	case errors.Is(err, ErrDigestMismatch):
		return ErrorCategoryDigestMismatch
	default:
		return ""
	}
}
