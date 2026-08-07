package engine

import (
	"testing"

	"github.com/fwtllh-png/CodeHelper/internal/security/policy"
)

func TestEffectiveProfilePermissionPreservesHostReadOnlyCeiling(t *testing.T) {
	for _, requested := range []policy.Permission{
		policy.PermissionSuggest,
		policy.PermissionAuto,
		policy.PermissionBypass,
	} {
		if got := effectiveProfilePermission(true, requested); got != policy.PermissionNever {
			t.Fatalf("read-only permission for %s = %s", requested, got)
		}
	}
	if got := effectiveProfilePermission(
		false,
		policy.PermissionBypass,
	); got != policy.PermissionBypass {
		t.Fatalf("trusted permission = %s", got)
	}
}

func TestProfilePermissionCeilingDistinguishesHostFromSessionNever(t *testing.T) {
	sessionNever := Options{
		Security:                 policy.DefaultRuntime(policy.ModeAct, policy.PermissionNever),
		ProfilePermissionCeiling: policy.PermissionSuggest,
	}
	if profileReadOnlyFromOptions(sessionNever) {
		t.Fatal("session-selected never became a Host read-only ceiling")
	}
	hostNever := sessionNever
	hostNever.ProfilePermissionCeiling = policy.PermissionNever
	if !profileReadOnlyFromOptions(hostNever) {
		t.Fatal("Host read-only ceiling was not retained")
	}
}
