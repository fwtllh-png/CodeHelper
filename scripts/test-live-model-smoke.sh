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
  set +e
  LIVE_MODEL_BASE_URL=https://example.invalid \
    LIVE_MODEL_WIRE_MODEL=deepseek-v4-flash \
    LIVE_MODEL_API_KEY=test-only-key \
    LIVE_MODEL_NAME=deepseek-v4-flash \
    LIVE_MODEL_PROTOCOL=openai_responses \
    LIVE_MODEL_MULTI_AGENT="$multi" \
    FAKE_LIVE_FAILURE="$fake_failure" \
    "$root/scripts/live-model-smoke.sh" "$fake" >/dev/null 2>"$temporary/$scenario.stderr"
  local command_status=$?
  set -e
  if [ "$expected_status" = "passed" ]; then
    [ "$command_status" -eq 0 ]
  else
    [ "$command_status" -ne 0 ]
    grep -q "failure_reason=$expected_reason" "$temporary/$scenario.stderr"
  fi
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
