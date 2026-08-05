#!/usr/bin/env sh

set -eu

default_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
root=${BRAND_CHECK_ROOT:-$default_root}
forbidden=$(printf '%s%s' 'code' 'tui')
tmp=

cleanup() {
	[ -z "$tmp" ] || rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM

cd "$root"

scan_source() {
	if command -v rg >/dev/null 2>&1; then
		rg -n -i "$forbidden" \
			--glob '*.go' \
			--glob 'go.mod' \
			--glob 'go.sum' \
			--glob 'Makefile' \
			--glob '*.sh' \
			.
	else
		grep -R -n -i \
			--include='*.go' \
			--include='go.mod' \
			--include='go.sum' \
			--include='Makefile' \
			--include='*.sh' \
			"$forbidden" .
	fi
}

contains_forbidden() {
	if command -v rg >/dev/null 2>&1; then
		rg -i "$forbidden"
	else
		grep -i "$forbidden"
	fi
}

if scan_source; then
	echo "forbidden legacy brand found in source or build files" >&2
	exit 1
fi

scan_binary() {
	binary=$1
	if [ ! -f "$binary" ]; then
		echo "brand check binary is missing: $binary" >&2
		exit 1
	fi
	if strings "$binary" | contains_forbidden; then
		echo "forbidden legacy brand found in binary" >&2
		exit 1
	fi
}

scan_runtime() {
	binary=$1
	for command in help version 'version --json' 'config show'; do
		set -- $command
		if ! output=$("$binary" "$@" 2>&1); then
			echo "brand check runtime command failed: $command" >&2
			exit 1
		fi
		if printf '%s' "$output" | contains_forbidden; then
			echo "forbidden legacy brand found in runtime output: $command" >&2
			exit 1
		fi
	done
}

if [ -n "${BRAND_CHECK_BINARY:-}" ]; then
	binary=$BRAND_CHECK_BINARY
else
	tmp=$(mktemp -d)
	binary="$tmp/codehelper"
	go build -trimpath -o "$binary" ./cmd/codehelper
fi

scan_binary "$binary"
scan_runtime "$binary"
