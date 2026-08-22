#!/bin/sh
set -eu

ref="${1:-}"
if [ -z "$ref" ]; then
  echo "previous release ref is required" >&2
  exit 2
fi

resolved="$(git rev-parse --verify "${ref}^{commit}" 2>/dev/null)" || {
  echo "previous release ref does not resolve to a commit: ${ref}" >&2
  exit 2
}

if printf '%s' "$ref" | grep -Eq '^[0-9a-f]{40}$'; then
  if [ "$resolved" != "$ref" ]; then
    echo "previous release commit is not canonical: ${ref}" >&2
    exit 2
  fi
elif ! git show-ref --verify --quiet "refs/tags/${ref}"; then
  echo "previous release ref must be a full commit SHA or local release tag: ${ref}" >&2
  exit 2
fi

printf '%s\n' "$resolved"
