#!/usr/bin/env bash

set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
temporary="$(mktemp -d "${TMPDIR:-/tmp}/codehelper-live-smoke-test.XXXXXX")"
trap 'rm -rf "$temporary"' EXIT HUP INT TERM

fake="$temporary/codehelper"
cat >"$fake" <<'EOF'
#!/usr/bin/env sh
if [ "${FAKE_LIVE_FAILURE:-}" = "runtime_exit" ]; then
  exit 1
fi
if [ "${FAKE_LIVE_FAILURE:-}" = "event_parse" ]; then
  printf '%s\n' 'not-json'
fi
if [ "${LIVE_MODEL_MULTI_AGENT:-0}" = "1" ]; then
  text=codehelper-multi-agent-live-ok
  if [ "${FAKE_LIVE_FAILURE:-}" = "final_text" ]; then
    text=wrong-multi-agent-text
  fi
  printf '%s\n' \
    '{"kind":"turn.started","data":{"provider":"deepseek-v4-flash","model":"deepseek-v4-flash"}}' \
    '{"kind":"agent.spawned","data":{"agent_id":"agent-a"}}' \
    '{"kind":"agent.status","data":{"agent_id":"agent-a","status":"completed","detail":{"result":{}}}}'
  if [ "${FAKE_LIVE_FAILURE:-}" != "spawn_count" ]; then
    printf '%s\n' '{"kind":"agent.spawned","data":{"agent_id":"agent-b"}}'
    if [ "${FAKE_LIVE_FAILURE:-}" = "agent_failed" ]; then
      printf '%s\n' '{"kind":"agent.status","data":{"agent_id":"agent-b","status":"failed","detail":{"result":{}}}}'
    elif [ "${FAKE_LIVE_FAILURE:-}" != "terminal_count" ]; then
      printf '%s\n' '{"kind":"agent.status","data":{"agent_id":"agent-b","status":"completed","detail":{"result":{}}}}'
    fi
  fi
else
  text=codehelper-live-ok
  if [ "${FAKE_LIVE_FAILURE:-}" = "final_text" ]; then
    text=wrong-single-text
  fi
fi
printf '{"kind":"output.delta","data":{"text":"%s"}}\n' "$text"
if [ "${FAKE_LIVE_FAILURE:-}" != "usage_missing" ]; then
  cost_known=true
  if [ "${FAKE_LIVE_FAILURE:-}" = "cost_unknown" ]; then
    cost_known=false
  fi
  printf '{"kind":"usage","data":{"sample":1,"provider":"deepseek-v4-flash","model":"deepseek-v4-flash","input_tokens":100,"output_tokens":10,"reasoning_tokens":2,"cached_tokens":20,"cost_microunits":42,"cost_known":%s}}\n' "$cost_known"
fi
if [ "${FAKE_LIVE_FAILURE:-}" = "terminal_failed" ]; then
  printf '%s\n' '{"kind":"turn.failed","data":{}}'
else
  printf '%s\n' '{"kind":"turn.completed","data":{}}'
fi
EOF
chmod 700 "$fake"

run_case() {
  local scenario=$1
  local multi=$2
  local fake_failure=$3
  local expected_status=$4
  local expected_reason=$5
  local evidence="$temporary/$scenario.json"
  set +e
  LIVE_MODEL_BASE_URL=https://example.invalid \
    LIVE_MODEL_WIRE_MODEL=deepseek-v4-flash \
    LIVE_MODEL_API_KEY=test-only-key \
    LIVE_MODEL_NAME=deepseek-v4-flash \
    LIVE_MODEL_PROTOCOL=openai_responses \
    LIVE_MODEL_MULTI_AGENT="$multi" \
    FAKE_LIVE_FAILURE="$fake_failure" \
    CODEHELPER_STAGE=h2_live \
    CODEHELPER_STAGE_RUN_ID=h2-smoke-test \
    CODEHELPER_STAGE_SOURCE_DIGEST=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
    CODEHELPER_STAGE_LOCK_IDENTITY=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
    CODEHELPER_H2_SCENARIO_ID="$scenario" \
    CODEHELPER_H2_SAMPLE_INDEX=1 \
    CODEHELPER_STAGE_EVIDENCE_PATH="$evidence" \
    "$root/scripts/live-model-smoke.sh" "$fake" >/dev/null 2>"$temporary/$scenario.stderr"
  local command_status=$?
  set -e
  if [ "$expected_status" = "passed" ]; then
    [ "$command_status" -eq 0 ]
  else
    [ "$command_status" -ne 0 ]
  fi
  ruby -rjson -e '
    evidence = JSON.parse(File.read(ARGV[0]))
    abort "wrong stage" unless evidence["stage"] == "h2_live"
    abort "wrong status" unless evidence["status"] == ARGV[1]
    abort "wrong failure reason" unless evidence["failure_reason"] == ARGV[2]
    unless %w[usage_missing runtime_command_failed].include?(ARGV[2])
      abort "usage missing" unless evidence["usage_samples"] == 1 &&
        evidence["input_tokens"] == 100 &&
        evidence["output_tokens"] == 10 &&
        evidence["cost_microunits"] == 42
      expected_cost_known = ARGV[2] != "cost_unknown"
      abort "wrong cost-known state" unless evidence["cost_known"] == expected_cost_known
    end
    abort "duration missing" unless evidence["duration_ms"] >= 1
    abort "digest missing" unless %w[
      endpoint_host_sha256 config_sha256 text_assertion_sha256 event_shape_sha256
    ].all? { |key| evidence.fetch(key).match?(/\Asha256:[0-9a-f]{64}\z/) }
  ' "$evidence" "$expected_status" "$expected_reason"
  mode="$(stat -f '%Lp' "$evidence" 2>/dev/null || stat -c '%a' "$evidence")"
  [ "$mode" = "600" ]
}

run_case exact-response 0 "" passed none
run_case multi-agent 1 "" passed none
run_case multi-agent-failed 1 spawn_count failed spawn_count_mismatch
run_case agent-terminal-count 1 terminal_count failed agent_terminal_count_mismatch
run_case agent-not-completed 1 agent_failed failed agent_not_completed
run_case multi-final-text 1 final_text failed final_text_mismatch
run_case single-final-text 0 final_text failed final_text_mismatch
run_case runtime-command 0 runtime_exit failed runtime_command_failed
run_case event-parse 0 event_parse failed event_parse_failed
run_case terminal-contract 0 terminal_failed failed terminal_contract_mismatch
run_case usage-missing 0 usage_missing failed usage_missing
run_case cost-unknown 0 cost_unknown failed cost_unknown
