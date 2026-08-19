#!/usr/bin/env bash

set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
temporary="$(mktemp -d "${TMPDIR:-/tmp}/codehelper-live-smoke-test.XXXXXX")"
trap 'rm -rf "$temporary"' EXIT HUP INT TERM

fake="$temporary/codehelper"
cat >"$fake" <<'EOF'
#!/usr/bin/env sh
if [ "${LIVE_MODEL_MULTI_AGENT:-0}" = "1" ]; then
  text=codehelper-multi-agent-live-ok
  printf '%s\n' \
    '{"kind":"turn.started","data":{"provider":"deepseek-v4-flash","model":"deepseek-v4-flash"}}' \
    '{"kind":"agent.spawned","data":{"agent_id":"agent-a"}}' \
    '{"kind":"agent.spawned","data":{"agent_id":"agent-b"}}' \
    '{"kind":"agent.status","data":{"agent_id":"agent-a","status":"completed","detail":{"result":{}}}}' \
    '{"kind":"agent.status","data":{"agent_id":"agent-b","status":"completed","detail":{"result":{}}}}'
else
  text=codehelper-live-ok
fi
printf '{"kind":"output.delta","data":{"text":"%s"}}\n' "$text"
printf '%s\n' \
  '{"kind":"usage","data":{"sample":1,"provider":"deepseek-v4-flash","model":"deepseek-v4-flash","input_tokens":100,"output_tokens":10,"reasoning_tokens":2,"cached_tokens":20,"cost_microunits":42,"cost_known":true}}' \
  '{"kind":"turn.completed","data":{}}'
EOF
chmod 700 "$fake"

run_case() {
  local scenario=$1
  local multi=$2
  local evidence="$temporary/$scenario.json"
  LIVE_MODEL_BASE_URL=https://example.invalid \
    LIVE_MODEL_WIRE_MODEL=deepseek-v4-flash \
    LIVE_MODEL_API_KEY=test-only-key \
    LIVE_MODEL_NAME=deepseek-v4-flash \
    LIVE_MODEL_PROTOCOL=openai_responses \
    LIVE_MODEL_MULTI_AGENT="$multi" \
    CODEHELPER_STAGE=h2_live \
    CODEHELPER_STAGE_RUN_ID=h2-smoke-test \
    CODEHELPER_STAGE_SOURCE_DIGEST=sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
    CODEHELPER_STAGE_LOCK_IDENTITY=sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb \
    CODEHELPER_H2_SCENARIO_ID="$scenario" \
    CODEHELPER_H2_SAMPLE_INDEX=1 \
    CODEHELPER_STAGE_EVIDENCE_PATH="$evidence" \
    "$root/scripts/live-model-smoke.sh" "$fake" >/dev/null
  ruby -rjson -e '
    evidence = JSON.parse(File.read(ARGV[0]))
    abort "wrong stage" unless evidence["stage"] == "h2_live"
    abort "usage missing" unless evidence["usage_samples"] == 1 &&
      evidence["input_tokens"] == 100 &&
      evidence["output_tokens"] == 10 &&
      evidence["cost_microunits"] == 42 &&
      evidence["cost_known"] == true
    abort "duration missing" unless evidence["duration_ms"] >= 1
    abort "digest missing" unless %w[
      endpoint_host_sha256 config_sha256 text_assertion_sha256 event_shape_sha256
    ].all? { |key| evidence.fetch(key).match?(/\Asha256:[0-9a-f]{64}\z/) }
  ' "$evidence"
  mode="$(stat -f '%Lp' "$evidence" 2>/dev/null || stat -c '%a' "$evidence")"
  [ "$mode" = "600" ]
}

run_case exact-response 0
run_case multi-agent 1
