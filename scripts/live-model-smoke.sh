#!/usr/bin/env sh

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
binary=${1:-"$root/bin/codehelper"}
model_name=${LIVE_MODEL_NAME:-dola-seed-lite}
protocol=${LIVE_MODEL_PROTOCOL:-openai_chat}
context_tokens=${LIVE_MODEL_CONTEXT_TOKENS:-1048576}
model_max_output_tokens=${LIVE_MODEL_MAX_OUTPUT_TOKENS:-393216}
model_capabilities=${LIVE_MODEL_CAPABILITIES:-streaming,reasoning,tool_calls}
utc_hour=$(date -u +%H)
case "$utc_hour" in
	01|02|03|06|07|08|09)
		pricing_window=peak
		default_input_price=0.44
		default_cached_input_price=0.014
		default_output_price=1.32
		;;
	*)
		pricing_window=off_peak
		default_input_price=0.22
		default_cached_input_price=0.007
		default_output_price=0.66
		;;
esac
input_price=${LIVE_MODEL_INPUT_PRICE_PER_MILLION:-$default_input_price}
cached_input_price=${LIVE_MODEL_CACHED_INPUT_PRICE_PER_MILLION:-$default_cached_input_price}
output_price=${LIVE_MODEL_OUTPUT_PRICE_PER_MILLION:-$default_output_price}
pricing_currency=${LIVE_MODEL_PRICING_CURRENCY:-USD}
multi_agent=${LIVE_MODEL_MULTI_AGENT:-0}

[ -x "$binary" ] || {
	echo "codehelper binary is required: $binary" >&2
	exit 2
}

if [ -n "${LIVE_MODEL_BASE_URL:-}" ] ||
	[ -n "${LIVE_MODEL_WIRE_MODEL:-}" ] ||
	[ -n "${LIVE_MODEL_API_KEY:-}" ]; then
	[ -n "${LIVE_MODEL_BASE_URL:-}" ] &&
		[ -n "${LIVE_MODEL_WIRE_MODEL:-}" ] &&
		[ -n "${LIVE_MODEL_API_KEY:-}" ] || {
		echo "LIVE_MODEL_BASE_URL, LIVE_MODEL_WIRE_MODEL, and LIVE_MODEL_API_KEY must be provided together" >&2
		exit 2
	}
	base_url=$LIVE_MODEL_BASE_URL
	wire_model=$LIVE_MODEL_WIRE_MODEL
	CODEHELPER_LIVE_API_KEY=$LIVE_MODEL_API_KEY
elif [ -n "${LIVE_MODEL_CONFIG:-}" ]; then
	config=$LIVE_MODEL_CONFIG
	command -v ruby >/dev/null 2>&1 || {
		echo "ruby is required to read LIVE_MODEL_CONFIG" >&2
		exit 2
	}
	[ -f "$config" ] || {
		echo "live model credentials require LIVE_MODEL_BASE_URL, LIVE_MODEL_WIRE_MODEL, and LIVE_MODEL_API_KEY or LIVE_MODEL_CONFIG" >&2
		exit 2
	}
	read_model_field() {
		field=$1
		ruby -ryaml -e '
			config = YAML.safe_load(File.read(ARGV[0]), aliases: true)
			model = config.fetch("models").find { |item| item["name"] == ARGV[1] }
			abort "live model not found: #{ARGV[1]}" unless model
			value = model.fetch(ARGV[2])
			abort "live model field contains a newline" if value.to_s.include?("\n")
			print value
		' "$config" "$model_name" "$field"
	}
	base_url=$(read_model_field base_url)
	wire_model=$(read_model_field model)
	CODEHELPER_LIVE_API_KEY=$(read_model_field api_key)
else
	echo "live model credentials require explicit LIVE_MODEL_BASE_URL/LIVE_MODEL_WIRE_MODEL/LIVE_MODEL_API_KEY or explicit LIVE_MODEL_CONFIG" >&2
	exit 2
fi
export CODEHELPER_LIVE_API_KEY

smoke_dir=$(mktemp -d "${TMPDIR:-/tmp}/codehelper-live-model.XXXXXX")
output="$smoke_dir/events.ndjson"
model_metadata="$smoke_dir/model-metadata.json"
trap 'rm -rf "$smoke_dir"; unset CODEHELPER_LIVE_API_KEY' EXIT HUP INT TERM
ruby -rjson -e '
	value = {
		canonical_id: ARGV[1],
		wire_id: ARGV[1],
		context_tokens: Integer(ARGV[2], 10),
		max_output_tokens: Integer(ARGV[3], 10),
		capabilities: {
			streaming: true,
			reasoning: true,
			reasoning_efforts: %w[off low medium high max],
			tool_calls: true,
			native_search: false,
			vision: false,
			image_input: false,
			prompt_cache: true
		},
		pricing: {
			input_per_million: Float(ARGV[4]),
			cached_input_per_million: Float(ARGV[5]),
			output_per_million: Float(ARGV[6]),
			currency: ARGV[7]
		}
	}
	File.write(ARGV[0], JSON.generate(value), mode: "w", perm: 0o600)
' "$model_metadata" "$wire_model" "$context_tokens" \
	"$model_max_output_tokens" "$input_price" "$cached_input_price" \
	"$output_price" "$pricing_currency"
command_status=0

if [ "$multi_agent" = "1" ]; then
	config="$smoke_dir/multi-agent.toml"
	cat >"$config" <<'EOF'
[execution.subagent]
delegation = "explicit"
max_depth = 1
max_parallel = 4
max_resident = 4
max_total = 4
max_steps = 12
wall_time = "5m"
workspace = "read_only"
EOF
	if "$binary" exec \
		--config "$config" \
		--data-dir "$smoke_dir/state" \
		--provider "$model_name" \
		--model "$wire_model" \
		--base-url "$base_url" \
		--protocol "$protocol" \
		--api-key-env CODEHELPER_LIVE_API_KEY \
		--model-metadata "$model_metadata" \
		--output-format stream-json \
		--enable-tools \
		--workspace "$root" \
		--mode act \
		--posture bypass \
		--max-steps 12 \
		--timeout 5m \
		--max-output-tokens 16384 \
		--budget-tokens 500000 \
		"In one model step, call spawn_agent exactly twice with context_mode=fresh and never call it again. task_name=live_alpha with role=explore has objective: without using tools or delegating, return alpha-live-ok. task_name=live_beta with role=explore has objective: without using tools or delegating, return beta-live-ok. Then call wait_agent for both terminal results and end with exactly codehelper-multi-agent-live-ok." >"$output"; then
		command_status=0
	else
		command_status=$?
	fi
else
	if "$binary" exec \
		--provider "$model_name" \
		--model "$wire_model" \
		--base-url "$base_url" \
		--protocol "$protocol" \
		--api-key-env CODEHELPER_LIVE_API_KEY \
		--model-metadata "$model_metadata" \
		--output-format stream-json \
		--max-output-tokens 64 \
		--budget-tokens 4096 \
		"Reply with exactly: codehelper-live-ok" >"$output"; then
		command_status=0
	else
		command_status=$?
	fi
fi
ruby -rjson -e '
	text = +""
	terminal = []
	spawned = []
	agent_terminal = {}
	usage_by_sample = {}
	parse_failed = false
	File.foreach(ARGV[0]) do |line|
		begin
			event = JSON.parse(line)
		rescue JSON::ParserError
			parse_failed = true
			next
		end
		text << event.fetch("data", {}).fetch("text", "") if event["kind"] == "output.delta"
		terminal << event["kind"] if %w[turn.completed turn.failed turn.canceled].include?(event["kind"])
		if event["kind"] == "usage"
			data = event.fetch("data")
			usage_by_sample[data.fetch("sample", 0)] = data
		end
		if event["kind"] == "agent.spawned"
			spawned << event.fetch("data").fetch("agent_id")
		elsif event["kind"] == "agent.status"
			data = event.fetch("data")
			if %w[completed failed interrupted].include?(data["status"])
				detail = data["detail"].is_a?(Hash) ? data["detail"] : {}
				result = detail["result"].is_a?(Hash) ? detail["result"] : {}
				agent_terminal[data.fetch("agent_id")] = {
					status: data["status"],
					message: data.fetch("message", "").slice(0, 300),
					unresolved: Array(result["unresolved"]).map { |item| item.to_s.slice(0, 300) }
				}
			end
		end
	end
	multi_agent = ENV["LIVE_MODEL_MULTI_AGENT"] == "1"
	command_status = Integer(ARGV[1], 10)
	failure_reason =
		if parse_failed
			"event_parse_failed"
		elsif command_status != 0 && terminal.empty?
			"runtime_command_failed"
		elsif terminal != ["turn.completed"]
			"terminal_contract_mismatch"
		elsif multi_agent && spawned.uniq.length != 2
			"spawn_count_mismatch"
		elsif multi_agent && agent_terminal.length != 2
			"agent_terminal_count_mismatch"
		elsif multi_agent && !agent_terminal.values.all? { |item| item.fetch(:status) == "completed" }
			"agent_not_completed"
		elsif multi_agent && text.strip != "codehelper-multi-agent-live-ok"
			"final_text_mismatch"
		elsif !multi_agent && text.strip != "codehelper-live-ok"
			"final_text_mismatch"
		elsif command_status != 0
			"runtime_command_failed"
		elsif usage_by_sample.empty?
			"usage_missing"
		elsif !usage_by_sample.values.all? { |sample| sample["cost_known"] == true }
			"cost_unknown"
		else
			"none"
		end
	warn "live model failure_reason=#{failure_reason}" unless failure_reason == "none"
	exit 1 unless failure_reason == "none"
' "$output" "$command_status"

cat "$output"
