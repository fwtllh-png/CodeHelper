#!/usr/bin/env sh

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
binary=${1:-"$root/bin/codehelper"}
model_name=${LIVE_MODEL_NAME:-dola-seed-lite}
protocol=${LIVE_MODEL_PROTOCOL:-openai_chat}
context_tokens=${LIVE_MODEL_CONTEXT_TOKENS:-262144}
model_max_output_tokens=${LIVE_MODEL_MAX_OUTPUT_TOKENS:-131072}
model_capabilities=${LIVE_MODEL_CAPABILITIES:-streaming,reasoning,tool_calls}
input_price=${LIVE_MODEL_INPUT_PRICE_PER_MILLION:-0.25}
output_price=${LIVE_MODEL_OUTPUT_PRICE_PER_MILLION:-2.0}
pricing_currency=${LIVE_MODEL_PRICING_CURRENCY:-USD}

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

output=$(mktemp "${TMPDIR:-/tmp}/codehelper-live-model.XXXXXX")
trap 'rm -f "$output"; unset CODEHELPER_LIVE_API_KEY' EXIT HUP INT TERM

"$binary" exec \
	--provider "$model_name" \
	--model "$wire_model" \
	--base-url "$base_url" \
	--protocol "$protocol" \
	--api-key-env CODEHELPER_LIVE_API_KEY \
	--context-tokens "$context_tokens" \
	--model-max-output-tokens "$model_max_output_tokens" \
	--model-capabilities "$model_capabilities" \
	--input-price-per-million "$input_price" \
	--output-price-per-million "$output_price" \
	--pricing-currency "$pricing_currency" \
	--output-format stream-json \
	--max-output-tokens 64 \
	--budget-tokens 1000 \
	"Reply with exactly: codehelper-live-ok" >"$output"

ruby -rjson -rdigest -ruri -e '
	text = +""
	terminal = []
	File.foreach(ARGV[0]) do |line|
		event = JSON.parse(line)
		text << event.fetch("data", {}).fetch("text", "") if event["kind"] == "output.delta"
		terminal << event["kind"] if %w[turn.completed turn.failed turn.canceled].include?(event["kind"])
	end
	abort "unexpected live model text: #{text.inspect}" unless text.strip == "codehelper-live-ok"
	abort "live model did not produce one completed terminal: #{terminal.inspect}" unless terminal == ["turn.completed"]
	if (path = ENV["CODEHELPER_STAGE_EVIDENCE_PATH"]) && !path.empty?
		stage = ENV.fetch("CODEHELPER_STAGE")
		run_id = ENV.fetch("CODEHELPER_STAGE_RUN_ID")
		source_digest = ENV.fetch("CODEHELPER_STAGE_SOURCE_DIGEST")
		uri = URI.parse(ARGV[1])
		host = uri.host
		abort "live model base URL has no host" if host.nil? || host.empty?
		evidence = {
			schema_version: 1,
			stage: stage,
			run_id: run_id,
			source_digest: source_digest,
			kind: "live-model",
			endpoint_host_sha256: Digest::SHA256.hexdigest(host.downcase),
			model: ARGV[2],
			model_sha256: Digest::SHA256.hexdigest(ARGV[2]),
			provider: ARGV[3],
			terminal_event: terminal.fetch(0),
			terminal_count: terminal.length,
			text_assertion_sha256: Digest::SHA256.hexdigest(text.strip)
		}
		File.open(path, File::WRONLY | File::CREAT | File::TRUNC, 0o600) do |file|
			file.write(JSON.generate(evidence))
			file.write("\n")
		end
	end
' "$output" "$base_url" "$wire_model" "$model_name"

cat "$output"
