#!/usr/bin/env bash
# Configure and install the local CodeHelper target VSIX into official VS Code.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

VSCODE_CLI="${VSCODE_CLI:-/Applications/Visual Studio Code.app/Contents/Resources/app/bin/code}"
SETTINGS_PATH="${CODEHELPER_VSCODE_SETTINGS_PATH:-$HOME/Library/Application Support/Code/User/settings.json}"
CONFIG_DIR="${CODEHELPER_CONFIG_DIR:-$HOME/.config/codehelper}"
CONFIG_PATH="$CONFIG_DIR/config.toml"
KEY_SOURCE="${CODEHELPER_DEEPSEEK_KEY_SOURCE:-$ROOT/docs/DEEPSEEK-LIVE.zh-CN.md}"
SKIP_BUILD=0
OPEN_WORKSPACE=1

usage() {
  cat <<'EOF'
Usage: ./scripts/setup-vscode-local.sh [--skip-build] [--no-open]

Build and install the target VSIX into official Visual Studio Code, configure
DeepSeek Responses, store its API key in macOS Keychain, and update User Settings.

Environment overrides:
  DEEPSEEK_API_KEY              API key; otherwise read from the local live guide
  VSCODE_CLI                    Official VS Code CLI path
  CODEHELPER_VSCODE_SETTINGS_PATH  VS Code User Settings path
  CODEHELPER_CONFIG_DIR            CodeHelper user configuration directory
  CODEHELPER_DEEPSEEK_KEY_SOURCE   Local file containing an export assignment
EOF
}

while (($# > 0)); do
  case "$1" in
    --skip-build) SKIP_BUILD=1 ;;
    --no-open) OPEN_WORKSPACE=0 ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
  shift
done

if [[ "$(uname -s)" != "Darwin" ]]; then
  echo "one-click setup currently supports macOS only" >&2
  exit 1
fi
case "$(uname -m)" in
  arm64) TARGET="darwin-arm64" ;;
  x86_64) TARGET="darwin-x64" ;;
  *)
    echo "unsupported macOS architecture: $(uname -m)" >&2
    exit 1
    ;;
esac
if [[ ! -x "$VSCODE_CLI" ]]; then
  echo "official VS Code CLI was not found: $VSCODE_CLI" >&2
  exit 1
fi
if pgrep -f "/Applications/Visual Studio Code.app/Contents/MacOS/Electron" >/dev/null 2>&1; then
  echo "quit all Visual Studio Code windows, then rerun this script" >&2
  exit 1
fi

if ((SKIP_BUILD == 0)); then
  make vscode-distribution
fi

ARTIFACT_DIR="$ROOT/extensions/vscode/dist/vscode-release/artifacts"
VSIX="$(find "$ARTIFACT_DIR" -maxdepth 1 -type f \
  -name "codehelper-vscode-*-${TARGET}.vsix" -print | sort | tail -1)"
if [[ -z "$VSIX" ]]; then
  echo "target VSIX was not found under $ARTIFACT_DIR" >&2
  exit 1
fi

api_key="${DEEPSEEK_API_KEY:-}"
if [[ -z "$api_key" && -f "$KEY_SOURCE" ]]; then
  api_key="$(awk -F"'" '/^export DEEPSEEK_API_KEY=/{print $2; exit}' "$KEY_SOURCE")"
fi
if [[ -z "$api_key" ]]; then
  read -r -s -p "DeepSeek API key: " api_key
  printf '\n'
fi
if [[ -z "$api_key" ]]; then
  echo "DeepSeek API key is required" >&2
  exit 1
fi
cleanup() {
  api_key=""
  unset DEEPSEEK_API_KEY
  if [[ -n "${TEMP_DIR:-}" ]]; then
    rm -rf "$TEMP_DIR"
  fi
}
trap cleanup EXIT

install -d -m 700 "$CONFIG_DIR"
install -m 600 "$ROOT/docs/examples/codehelper-vscode.toml" "$CONFIG_PATH"

TEMP_DIR="$(mktemp -d)"
unzip -qq "$VSIX" "extension/bin/${TARGET}/codehelper" -d "$TEMP_DIR"
RUNTIME="$TEMP_DIR/extension/bin/${TARGET}/codehelper"
chmod 700 "$RUNTIME"

DEEPSEEK_API_KEY="$api_key" "$RUNTIME" auth login \
  --config "$CONFIG_PATH" \
  --kind keyring \
  --name deepseek/default \
  --from-env DEEPSEEK_API_KEY >/dev/null

"$RUNTIME" config check --config "$CONFIG_PATH" >/dev/null
route="$("$RUNTIME" model resolve \
  --provider deepseek-v4-flash \
  --model deepseek-v4-flash \
  --json)"
if [[ "$route" != *'"protocol":"openai_responses"'* ||
  "$route" != *'"endpoint":"https://api.deepseek.com"'* ]]; then
  echo "bundled DeepSeek route verification failed" >&2
  exit 1
fi

node "$ROOT/extensions/vscode/scripts/local-settings.mjs" \
  "$SETTINGS_PATH" "$CONFIG_PATH"

if "$VSCODE_CLI" --list-extensions | grep -Fxq "codehelper.codehelper-vscode"; then
  "$VSCODE_CLI" --uninstall-extension codehelper.codehelper-vscode
fi
"$VSCODE_CLI" --install-extension "$VSIX"

echo "CodeHelper VS Code local setup completed."
echo "  target: $TARGET"
echo "  config: $CONFIG_PATH"
echo "  settings: $SETTINGS_PATH"
echo "  credential: macOS Keychain (deepseek/default)"
echo "  extension: codehelper.codehelper-vscode"

if ((OPEN_WORKSPACE == 1)); then
  "$VSCODE_CLI" "$ROOT/codehelper.code-workspace"
fi
