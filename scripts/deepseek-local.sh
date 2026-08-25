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
PROVIDER="deepseek-v4-flash"
MODEL="deepseek-v4-flash"
KEYRING_NAME="deepseek/default"

usage() {
  cat <<'EOF'
Usage: ./scripts/deepseek-local.sh COMMAND

Commands:
  init      build CodeHelper and install the local Web configuration
  web       run init, then launch the local Web workspace
  doc       create or refresh the ignored local runbook with the real API key
  check     validate the existing binary and config

Credential lookup order:
  1. DEEPSEEK_API_KEY
  2. docs/DEEPSEEK-LIVE.zh-CN.md (Git-ignored local runbook)
  3. macOS Keychain service codehelper, account deepseek/default
  4. legacy macOS Keychain service, account deepseek/default
  5. secure terminal prompt

Environment overrides:
  CODEHELPER_DEEPSEEK_LOCAL_DOC  local ignored runbook path
  CODEHELPER_CONFIG_DIR          config directory (default ~/.config/codehelper)
  CODEHELPER_LOCAL_WORKSPACE     Web workspace (default repository root)
EOF
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

完成 Go Binary 编译和 Web 配置安装。

## 一键启动 Web 工作区

```bash
make deepseek-web
```

脚本会构建 CodeHelper、配置 DeepSeek，并在本机 Loopback 地址启动 Web 工作区。

## Agent 执行入口

Agent 只需调用下列确定性命令，不应读取或输出本文件中的 Key：

```bash
make deepseek-init
make deepseek-web
```

API Key 只通过环境变量传给 Web 进程，不写入受 Git 跟踪的配置。

## 检查

```bash
./scripts/deepseek-local.sh check
./bin/codehelper --version
```
EOF
  } >"$LOCAL_DOC"
  chmod 600 "$LOCAL_DOC"
}

install_runtime_config() {
  make build
  install -d -m 700 "$CONFIG_DIR"
  install -m 600 "$ROOT/docs/examples/codehelper-deepseek.toml" "$CONFIG_PATH"
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
  "$ROOT/bin/codehelper" --version >/dev/null
  echo "DeepSeek local environment is ready."
  echo "  binary: $ROOT/bin/codehelper"
  echo "  config: $CONFIG_PATH"
  echo "  credential: injected into the Web process"
  echo "  workspace: $WORKSPACE"
}

command_name="${1:-}"
case "$command_name" in
  init)
    api_key="$(load_api_key)"
    trap 'api_key=""; unset DEEPSEEK_API_KEY' EXIT
    write_local_doc "$api_key"
    install_runtime_config
    check_environment
    ;;
  web)
    api_key="$(load_api_key)"
    trap 'api_key=""; unset DEEPSEEK_API_KEY' EXIT
    write_local_doc "$api_key"
    install_runtime_config
    check_environment
    DEEPSEEK_API_KEY="$api_key" exec "$ROOT/bin/codehelper" \
      --config "$CONFIG_PATH" \
      --workspace "$WORKSPACE" \
      --provider "$PROVIDER" \
      --model "$MODEL" \
      --api-key-env DEEPSEEK_API_KEY \
      --enable-tools \
      --posture suggest \
      --open
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
