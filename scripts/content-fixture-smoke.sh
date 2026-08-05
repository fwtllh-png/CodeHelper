#!/usr/bin/env bash
# Optional non-hermetic smoke: proves contentdeps Probe + diagnostics report
# fixture binaries via CODEHELPER_*_BINARY. Not part of default `make verify`.
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
FIXTURE="$(mktemp -d "${TMPDIR:-/tmp}/codehelper-content-smoke.XXXXXX")"
cleanup() { rm -rf "$FIXTURE"; }
trap cleanup EXIT

cat >"$FIXTURE/tesseract" <<'EOF'
#!/usr/bin/env bash
echo "fixture-ocr-ok"
EOF
chmod +x "$FIXTURE/tesseract"

export CODEHELPER_TESSERACT_BINARY="$FIXTURE/tesseract"
export CODEHELPER_SPEECH_BINARY="$FIXTURE/missing-speech"
cd "$ROOT"
OUT="$(go run ./cmd/codehelper diagnostics --json --workspace "$FIXTURE")"
echo "$OUT" | python3 -c '
import json,sys
payload=json.load(sys.stdin)
content=payload.get("content") or {}
assert content.get("ocr")=="ready", content
assert content.get("speech")=="unavailable", content
print("content-fixture-smoke ok")
'
