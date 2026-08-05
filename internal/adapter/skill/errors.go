package skill

import "errors"

var (
	ErrDependencyConflict    = errors.New("skill dependency conflict")
	ErrDependencyCycle       = errors.New("skill dependency cycle")
	ErrCompatibilityMismatch = errors.New("skill compatibility mismatch")
	ErrLockDrift             = errors.New("skill lock drift")
)

const (
	ErrorCategoryDependencyConflict    = "dependency_conflict"
	ErrorCategoryDependencyCycle       = "dependency_cycle"
	ErrorCategoryCompatibilityMismatch = "compatibility_mismatch"
	ErrorCategoryLockDrift             = "skill_lock_drift"
)

func ErrorCategory(err error) string {
	switch {
	case errors.Is(err, ErrDependencyConflict):
		return ErrorCategoryDependencyConflict
	case errors.Is(err, ErrDependencyCycle):
		return ErrorCategoryDependencyCycle
	case errors.Is(err, ErrCompatibilityMismatch):
		return ErrorCategoryCompatibilityMismatch
	case errors.Is(err, ErrLockDrift):
		return ErrorCategoryLockDrift
	default:
		return ""
	}
}
