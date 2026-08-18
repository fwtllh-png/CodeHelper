package acp

import "github.com/fwtllh-png/CodeHelper/internal/runtime/protocol"

func acpCodeForProblem(code protocol.ErrorCode) int {
	switch code {
	case protocol.CodeInvalidArgument:
		return codeInvalidParams
	case protocol.CodeConflict, protocol.CodeResourceExhausted:
		return codeConflict
	case protocol.CodeUnavailable, protocol.CodeDeadlineExceeded:
		return codeUnavailable
	default:
		return codeInternalError
	}
}
