#!/usr/bin/env sh

set -eu

root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
check="$root/scripts/check-brand.sh"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT HUP INT TERM

mkdir -p "$tmp/bin"
printf 'package clean\n' >"$tmp/clean.go"
printf '#!/usr/bin/env sh\nexit 0\n' >"$tmp/bin/clean"
chmod +x "$tmp/bin/clean"
BRAND_CHECK_ROOT="$tmp" BRAND_CHECK_BINARY="$tmp/bin/clean" "$check"

forbidden=$(printf '%s%s' 'code' 'tui')
printf 'package dirty\n// %s\n' "$forbidden" >"$tmp/dirty.go"

if BRAND_CHECK_ROOT="$tmp" BRAND_CHECK_BINARY="$tmp/bin/clean" "$check" \
	>"$tmp/stdout" 2>"$tmp/stderr"; then
	echo "brand check accepted a forbidden source marker" >&2
	exit 1
fi

if ! grep -q "forbidden legacy brand" "$tmp/stderr"; then
	echo "brand check failed without a useful diagnostic" >&2
	exit 1
fi

rm "$tmp/dirty.go"
printf '%s\n' "$forbidden" >"$tmp/bin/dirty"
chmod +x "$tmp/bin/dirty"
if BRAND_CHECK_ROOT="$tmp" BRAND_CHECK_BINARY="$tmp/bin/dirty" "$check" \
	>"$tmp/stdout" 2>"$tmp/stderr"; then
	echo "brand check accepted a forbidden binary marker" >&2
	exit 1
fi
if ! grep -q "forbidden legacy brand found in binary" "$tmp/stderr"; then
	echo "binary brand check failed without a useful diagnostic" >&2
	exit 1
fi

printf '#!/usr/bin/env sh\nprintf "%%s%%s\\n" "code" "tui"\n' >"$tmp/bin/runtime-dirty"
chmod +x "$tmp/bin/runtime-dirty"
if BRAND_CHECK_ROOT="$tmp" BRAND_CHECK_BINARY="$tmp/bin/runtime-dirty" "$check" \
	>"$tmp/stdout" 2>"$tmp/stderr"; then
	echo "brand check accepted a forbidden runtime marker" >&2
	exit 1
fi
if ! grep -q "forbidden legacy brand found in runtime output" "$tmp/stderr"; then
	echo "runtime brand check failed without a useful diagnostic" >&2
	exit 1
fi
