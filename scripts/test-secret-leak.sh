#!/usr/bin/env sh

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
binary=${1:-"$root/bin/codehelper"}
secret='codehelper-secret-leak-sentinel-9f3d1a'
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

if [ ! -x "$binary" ]; then
	echo "codehelper binary is required: $binary" >&2
	exit 2
fi

output=$(
	CODEHELPER_CREDENTIAL_KIND=env \
	CODEHELPER_CREDENTIAL_NAME=CODEHELPER_SECRET_LEAK_SENTINEL \
	CODEHELPER_SECRET_LEAK_SENTINEL="$secret" \
		"$binary" config show 2>&1
)

if printf '%s' "$output" | grep -F "$secret" >/dev/null; then
	echo "credential value leaked from config show" >&2
	exit 1
fi
if ! printf '%s' "$output" | grep -F 'CODEHELPER_SECRET_LEAK_SENTINEL' >/dev/null; then
	echo "credential reference missing from config show" >&2
	exit 1
fi

CODEHELPER_CREDENTIAL_KIND=env \
CODEHELPER_CREDENTIAL_NAME=CODEHELPER_SECRET_LEAK_SENTINEL \
CODEHELPER_SECRET_LEAK_SENTINEL="$secret" \
	"$binary" runtime-observe \
		--events 3 \
		--log-file "$tmp_dir/runtime.ndjson" \
		>"$tmp_dir/metrics.json"

if grep -F "$secret" "$tmp_dir/runtime.ndjson" "$tmp_dir/metrics.json" >/dev/null; then
	echo "credential value leaked from runtime observability output" >&2
	exit 1
fi
if ! grep -F '"operations_processed": 3' "$tmp_dir/metrics.json" >/dev/null; then
	echo "runtime metrics were not exported" >&2
	exit 1
fi

cd "$root"
go test ./internal/config ./internal/observability/telemetry -run 'Test(Secret|JSONLogger)'
