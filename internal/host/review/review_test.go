package review

import "testing"

func TestParseArgs(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		kind TargetKind
		ref  string
	}{
		{nil, KindUncommitted, ""},
		{[]string{"base", "develop"}, KindBaseBranch, "develop"},
		{[]string{"commit", "abc"}, KindCommit, "abc"},
		{[]string{"custom", "focus auth"}, KindCustom, "focus auth"},
	}
	for _, tc := range cases {
		got := ParseArgs(tc.args)
		if got.Kind != tc.kind || got.Ref != tc.ref {
			t.Fatalf("ParseArgs(%v) = %+v, want kind=%s ref=%s", tc.args, got, tc.kind, tc.ref)
		}
	}
}
