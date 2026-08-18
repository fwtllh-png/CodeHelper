package config

import (
	"fmt"
	"strings"
)

// Profile controls how many fields are written explicitly. Omitted fields keep
// using Defaults, so profiles are presentation choices rather than new default
// sets.
type Profile string

const (
	ProfileMinimal     Profile = "minimal"
	ProfileRecommended Profile = "recommended"
	ProfileAdvanced    Profile = "advanced"
)

type ProfileOptions struct {
	Workspace string
	DataDir   string
}

func ParseProfile(value string) (Profile, error) {
	switch profile := Profile(strings.TrimSpace(value)); profile {
	case ProfileMinimal, ProfileRecommended, ProfileAdvanced:
		return profile, nil
	default:
		return "", fmt.Errorf(
			"invalid config profile %q (want minimal, recommended, or advanced)",
			value,
		)
	}
}

// RenderProfile writes a stable TOML projection of the selected profile.
func RenderProfile(profile Profile, options ProfileOptions) ([]byte, error) {
	if _, err := ParseProfile(string(profile)); err != nil {
		return nil, err
	}
	if strings.TrimSpace(options.Workspace) == "" {
		options.Workspace = "."
	}
	if strings.TrimSpace(options.DataDir) == "" {
		options.DataDir = ".codehelper"
	}
	base := fmt.Sprintf(`[execution]
workspace = %q
mode = "act"

[state]
data_dir = %q
`, options.Workspace, options.DataDir)
	if profile == ProfileMinimal {
		return []byte("# CodeHelper profile: minimal\n" + base), nil
	}
	recommended := base + `
[execution.verify]
mode = "soft"
scope = "diagnostics"
on_failure = "fail"
max_repair_steps = 1

[execution.journal]
durable = true
recover_on_start = true

[context.index]
enabled = true

[context.repo_map]
enabled = true

[context.working_set]
enabled = true

[context.evidence]
enabled = true
`
	if profile == ProfileRecommended {
		return []byte("# CodeHelper profile: recommended\n" + recommended), nil
	}
	advanced := fmt.Sprintf(`[runtime]
operation_buffer = 64
event_history = 256
subscriber_buffer = 64

[state]
data_dir = %q
busy_timeout = "5s"
event_retention = 1000000

[memory]
enabled = false

[telemetry]
log_level = "info"

[execution]
workspace = %q
mode = "act"
tools = false
max_output_tokens = 0
max_steps = 256
timeout = "2m"
idle_timeout = "1m"
max_concurrent = 8
rate_limit = 0
budget_tokens = 0
budget_usd = 0
native_search = false

[execution.verify]
mode = "soft"
scope = "diagnostics"
on_failure = "fail"
max_repair_steps = 1
timeout = "2m"

[execution.journal]
durable = true
recover_on_start = true

[execution.subagent]
delegation = "explicit"
max_depth = 5
max_parallel = 4
max_resident = 8
max_total = 16
max_steps = 24
max_tokens = 0
max_cost_usd = 0
wall_time = "5m"
workspace = "auto"

[execution.worker]
enabled = false
max_parallel = 2
max_attempts = 1
lease = "30s"
claim_interval = "1s"
automation_interval = "30s"
retry_backoff = "15s"
retry_backoff_max = "10m"
max_tokens = 0
max_cost_usd = 0

[context.index]
enabled = true
max_file_bytes = 1048576
max_files = 20000

[context.repo_map]
enabled = true
max_bytes = 8192
max_directories = 24

[context.working_set]
enabled = true
max_entries = 16
max_bytes = 8192

[context.evidence]
enabled = true
max_entries = 24
max_bytes = 4096

[context.coding_policy]
enabled = true

[context.compact]
auto_compact_tokens = 0
scope = "total"
summary_max_bytes = 8192
max_digest_entries = 120

[route]
lock = false
`, options.DataDir, options.Workspace)
	return []byte("# CodeHelper profile: advanced\n" + advanced), nil
}
