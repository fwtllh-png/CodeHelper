#!/usr/bin/env bash
# Build, configure, and run the local DeepSeek development environment.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

LOCAL_DOC="${CODEHELPER_DEEPSEEK_LOCAL_DOC:-$ROOT/docs/DEEPSEEK-LIVE.zh-CN.md}"
CONFIG_DIR="${CODEHELPER_CONFIG_DIR:-$HOME/.config/codehelper}"
CONFIG_PATH="$CONFIG_DIR/config.toml"
WORKSPACE="${CODEHELPER_LOCAL_WORKSPACE:-$ROOT}"
POSTURE="${CODEHELPER_LOCAL_POSTURE:-bypass}"
PROVIDER="deepseek-v4-flash"
MODEL="deepseek-v4-flash"
KEYRING_NAME="deepseek/default"

usage() {
  cat <<'EOF'
Usage: ./scripts/deepseek-local.sh COMMAND

Commands:
  init      build CodeHelper, install config, migrate the key to Keychain
  tui       run init, then launch the TUI with the DeepSeek model
  vscode    build/configure/install the extension and open official VS Code
  live-smoke
            run the real-provider single-turn release smoke
  multi-agent-smoke
            run the real-provider Multi-Agent release smoke
  doc       create or refresh the ignored local runbook with the real API key
  check     validate the existing binary, config, route, and credential

Credential lookup order:
  1. DEEPSEEK_API_KEY
  2. docs/DEEPSEEK-LIVE.zh-CN.md (Git-ignored local runbook)
  3. macOS Keychain service codehelper, account deepseek/default
  4. legacy macOS Keychain service, account deepseek/default
  5. secure terminal prompt

Environment overrides:
  CODEHELPER_DEEPSEEK_LOCAL_DOC  local ignored runbook path
  CODEHELPER_CONFIG_DIR          config directory (default ~/.config/codehelper)
  CODEHELPER_LOCAL_WORKSPACE     TUI workspace (default repository root)
  CODEHELPER_LOCAL_POSTURE       TUI posture (default bypass)
  VSCODE_CLI                     official VS Code CLI path
EOF
}

require_macos() {
  if [[ "$(uname -s)" != "Darwin" ]]; then
    echo "DeepSeek one-click setup currently requires macOS Keychain" >&2
    exit 1
  fi
}

decode_stored_key() {
  local stored=$1
  case "$stored" in
    go-keyring-base64:*)
      printf '%s' "${stored#go-keyring-base64:}" | /usr/bin/base64 -D
      ;;
    *)
      printf '%s' "$stored"
      ;;
  esac
}

read_key_from_doc() {
  [[ -f "$LOCAL_DOC" ]] || return 1
  local stored
  stored="$(sed -n "s/^export DEEPSEEK_API_KEY='\\([^']*\\)'$/\\1/p" "$LOCAL_DOC" | head -1)"
  [[ -n "$stored" ]] || return 1
  decode_stored_key "$stored"
}

read_key_from_keychain() {
  local service=$1
  local stored
  stored="$(security find-generic-password \
    -s "$service" -a "$KEYRING_NAME" -w 2>/dev/null)" || return 1
  decode_stored_key "$stored"
}

load_api_key() {
  local key="${DEEPSEEK_API_KEY:-}"
  if [[ -z "$key" ]]; then
    key="$(read_key_from_doc || true)"
  fi
  if [[ -z "$key" && "$(uname -s)" == "Darwin" ]]; then
    key="$(read_key_from_keychain codehelper || true)"
  fi
  if [[ -z "$key" && "$(uname -s)" == "Darwin" ]]; then
    local legacy_service="code""tui"
    key="$(read_key_from_keychain "$legacy_service" || true)"
  fi
  if [[ -z "$key" ]]; then
    read -r -s -p "DeepSeek API key: " key
    printf '\n'
  fi
  if [[ -z "$key" ]]; then
    echo "DeepSeek API key is required" >&2
    exit 1
  fi
  if [[ "$key" == *"'"* || "$key" == *$'\n'* ]]; then
    echo "DeepSeek API key contains unsupported quote or newline characters" >&2
    exit 1
  fi
  printf '%s' "$key"
}

write_local_doc() {
  local key=$1
  local local_doc_dir
  local_doc_dir="$(dirname "$LOCAL_DOC")"
  if [[ ! -d "$local_doc_dir" ]]; then
    install -d -m 700 "$local_doc_dir"
  fi
  umask 077
  {
    cat <<'EOF'
# CodeHelper 本机 DeepSeek 一键配置与运行

> 这是包含真实 API Key 的本机文档，已由仓库 `.gitignore` 明确忽略。
> 不要执行 `git add -f`，不要复制到 Issue、日志或对话中。

## 本机密钥

```bash
EOF
    printf "export DEEPSEEK_API_KEY='%s'\n" "$key"
    cat <<'EOF'
```

日常不需要手工 `source`，下面的 Make Target 会自动读取本文件。

## 一键初始化

```bash
make deepseek-init
```

完成 Go Binary 编译、配置安装、Keychain 写入、配置与模型路由检查。

## 一键启动 TUI

```bash
make deepseek-tui
```

默认使用当前仓库作为 Workspace，`act + bypass` 便于本机完整联调。切换为审批模式：

```bash
CODEHELPER_LOCAL_POSTURE=suggest make deepseek-tui
```

指定其他仓库：

```bash
CODEHELPER_LOCAL_WORKSPACE=/path/to/project make deepseek-tui
```

## 一键安装并启动 VS Code

先完全退出官方 VS Code，再执行：

```bash
make deepseek-vscode
```

脚本会构建 Target VSIX、配置 DeepSeek、写入官方 VS Code Settings、安装插件并打开
`codehelper.code-workspace`。

## Agent 执行入口

Agent 只需调用下列确定性命令，不应读取或输出本文件中的 Key：

```bash
make deepseek-init
CODEHELPER_LOCAL_POSTURE=suggest make deepseek-tui
make deepseek-vscode
```

如果 IDE Sandbox 拒绝写 macOS Keychain，Agent 应停止并请用户在普通 macOS Terminal
执行同一命令，不得把密钥降级写入受 Git 跟踪的配置。

## 检查

```bash
./scripts/deepseek-local.sh check
./bin/codehelper config show --config ~/.config/codehelper/config.toml
```
EOF
  } >"$LOCAL_DOC"
  chmod 600 "$LOCAL_DOC"
}

install_runtime_config() {
  local key=$1
  require_macos
  make build
  install -d -m 700 "$CONFIG_DIR"
  install -m 600 "$ROOT/docs/examples/codehelper-vscode.toml" "$CONFIG_PATH"
  if ! DEEPSEEK_API_KEY="$key" "$ROOT/bin/codehelper" auth login \
      --config "$CONFIG_PATH" \
      --kind keyring \
      --name "$KEYRING_NAME" \
      --from-env DEEPSEEK_API_KEY >/dev/null; then
    echo "macOS Keychain write failed; rerun this command in a normal Terminal" >&2
    exit 1
  fi
}

check_environment() {
  [[ -x "$ROOT/bin/codehelper" ]] || {
    echo "CodeHelper binary is missing; run make deepseek-init" >&2
    exit 1
  }
  [[ -f "$CONFIG_PATH" ]] || {
    echo "CodeHelper config is missing: $CONFIG_PATH" >&2
    exit 1
  }
  "$ROOT/bin/codehelper" config check --config "$CONFIG_PATH" >/dev/null
  "$ROOT/bin/codehelper" auth status --config "$CONFIG_PATH" >/dev/null
  local route
  route="$("$ROOT/bin/codehelper" model resolve \
    --provider "$PROVIDER" --model "$MODEL" --json)"
  if [[ "$route" != *'"protocol":"openai_responses"'* ||
    "$route" != *'"endpoint":"https://api.deepseek.com"'* ]]; then
    echo "bundled DeepSeek route verification failed" >&2
    exit 1
  fi
  echo "DeepSeek local environment is ready."
  echo "  binary: $ROOT/bin/codehelper"
  echo "  config: $CONFIG_PATH"
  echo "  credential: macOS Keychain ($KEYRING_NAME)"
  echo "  workspace: $WORKSPACE"
}

command_name="${1:-}"
case "$command_name" in
  init)
    api_key="$(load_api_key)"
    trap 'api_key=""; unset DEEPSEEK_API_KEY' EXIT
    write_local_doc "$api_key"
    install_runtime_config "$api_key"
    check_environment
    ;;
  tui)
    api_key="$(load_api_key)"
    trap 'api_key=""; unset DEEPSEEK_API_KEY' EXIT
    write_local_doc "$api_key"
    install_runtime_config "$api_key"
    check_environment
    exec "$ROOT/bin/codehelper" tui \
      --config "$CONFIG_PATH" \
      --workspace "$WORKSPACE" \
      --provider "$PROVIDER" \
      --model "$MODEL" \
      --protocol openai_responses \
      --enable-tools \
      --mode act \
      --posture "$POSTURE"
    ;;
  vscode)
    api_key="$(load_api_key)"
    trap 'api_key=""; unset DEEPSEEK_API_KEY' EXIT
    write_local_doc "$api_key"
    DEEPSEEK_API_KEY="$api_key" "$ROOT/scripts/setup-vscode-local.sh"
    ;;
  live-smoke|multi-agent-smoke)
    api_key="$(load_api_key)"
    trap 'api_key=""; unset DEEPSEEK_API_KEY LIVE_MODEL_API_KEY' EXIT
    live_binary="${CODEHELPER_LIVE_BINARY:-$ROOT/bin/codehelper}"
    if [[ -z "${CODEHELPER_LIVE_BINARY:-}" ]]; then
      make build
    fi
    [[ -x "$live_binary" ]] || {
      echo "CodeHelper live binary is missing: $live_binary" >&2
      exit 1
    }
    multi_agent=0
    if [[ "$command_name" == "multi-agent-smoke" ]]; then
      multi_agent=1
    fi
    LIVE_MODEL_MULTI_AGENT="$multi_agent" \
      LIVE_MODEL_NAME="$PROVIDER" \
      LIVE_MODEL_BASE_URL="https://api.deepseek.com" \
      LIVE_MODEL_WIRE_MODEL="$MODEL" \
      LIVE_MODEL_API_KEY="$api_key" \
      LIVE_MODEL_PROTOCOL="openai_responses" \
      "$ROOT/scripts/live-model-smoke.sh" "$live_binary"
    ;;
  doc)
    api_key="$(load_api_key)"
    trap 'api_key=""; unset DEEPSEEK_API_KEY' EXIT
    write_local_doc "$api_key"
    echo "wrote ignored local runbook: $LOCAL_DOC"
    ;;
  check)
    check_environment
    ;;
  -h|--help|help)
    usage
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac
