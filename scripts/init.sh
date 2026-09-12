#!/system/bin/sh
# Idempotent cloud-managed bootstrap for ai-web-engine.
set -u

BASE=/data/local/ai-instruction
ROOT_URL=https://raw.githubusercontent.com/snowzlmbot/ai-web-engine/main
SKILLS_URL=https://raw.githubusercontent.com/snowzlmbot/ShortX-Files/main/skills/shortx-rule-creator.zip

log() { printf '%s\n' "$1"; }
fail() { log "[ERROR] $1"; exit 1; }

[ "$(id -u)" = 0 ] || fail "需要 root 权限"
command -v curl >/dev/null 2>&1 || fail "缺少 curl"
command -v unzip >/dev/null 2>&1 || fail "缺少 unzip"
command -v sed >/dev/null 2>&1 || fail "缺少 sed"

mkdir -p "$BASE/bin" "$BASE/scripts" "$BASE/config" "$BASE/skills" "$BASE/sessions" "$BASE/logs" || fail "创建目录失败"
chmod 700 "$BASE" "$BASE/bin" "$BASE/scripts" "$BASE/config" "$BASE/skills" "$BASE/sessions" "$BASE/logs" 2>/dev/null || true

CONFIG="$BASE/config/model_config.json"
if [ ! -f "$CONFIG" ]; then
  printf '%s\n' '{"provider":"","endpoint":"","protocol":"openai","apiKey":"","defaultModelId":"","models":[],"reasoningLevel":"medium"}' > "$CONFIG" || fail "创建配置失败"
  chmod 600 "$CONFIG"
  log "[INIT] 已创建模型配置模板"
else
  chmod 600 "$CONFIG" 2>/dev/null || true
fi

REMOTE_VERSION=$(curl -fsSL --retry 2 --connect-timeout 15 "$ROOT_URL/version.json" 2>/dev/null | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p')
[ -n "$REMOTE_VERSION" ] || fail "无法读取远端版本"
LOCAL_VERSION=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$BASE/config/version.json" 2>/dev/null || true)
BINARY="$BASE/bin/ai-web-engine"
SKILL_DIR="$BASE/skills/shortx-rule-creator"

if [ "$REMOTE_VERSION" = "$LOCAL_VERSION" ] && [ -x "$BINARY" ] && [ -f "$SKILL_DIR/SKILL.md" ]; then
  log "[SKIP] 已是最新版本"
  exit 0
fi

binary_ready=0
if [ -x "$BINARY" ] && [ "$REMOTE_VERSION" = "$LOCAL_VERSION" ]; then
  binary_ready=1
fi
if [ "$REMOTE_VERSION" != "$LOCAL_VERSION" ] || [ ! -x "$BINARY" ]; then
  BIN_NEW="$BINARY.new"
  rm -f "$BIN_NEW"
  if curl -fsSL --retry 2 --connect-timeout 15 "$ROOT_URL/releases/download/v$REMOTE_VERSION/ai-web-engine-android-arm64" -o "$BIN_NEW" && [ -s "$BIN_NEW" ] && chmod 755 "$BIN_NEW" && mv "$BIN_NEW" "$BINARY"; then
    binary_ready=1
    log "[UPDATE] 引擎已更新至 $REMOTE_VERSION"
  else
    rm -f "$BIN_NEW"
    log "[WARN] 引擎下载/替换失败，保留旧版本"
  fi
fi

skill_ready=0
[ -f "$SKILL_DIR/SKILL.md" ] && [ -d "$SKILL_DIR/references" ] && skill_ready=1
if [ ! -f "$SKILL_DIR/SKILL.md" ] || [ "$REMOTE_VERSION" != "$LOCAL_VERSION" ]; then
  SKILL_NEW="$BASE/skills/shortx-rule-creator.zip.new"
  STAGE="$BASE/skills/.shortx-rule-creator.staging"
  rm -f "$SKILL_NEW"
  rm -rf "$STAGE"
  if curl -fsSL --retry 2 --connect-timeout 15 "$SKILLS_URL" -o "$SKILL_NEW" && [ -s "$SKILL_NEW" ]; then
    mkdir -p "$STAGE"
    valid=1
    unzip -Z1 "$SKILL_NEW" 2>/dev/null | while IFS= read -r entry; do
      case "$entry" in shortx-rule-creator/*) ;; *) exit 1 ;; esac
      case "$entry" in /*|*../*) exit 1 ;; esac
    done || valid=0
    if [ "$valid" -eq 1 ] && unzip -oq "$SKILL_NEW" -d "$STAGE" && [ -f "$STAGE/shortx-rule-creator/SKILL.md" ] && [ -d "$STAGE/shortx-rule-creator/references" ]; then
      rm -rf "$SKILL_DIR"
      mv "$STAGE/shortx-rule-creator" "$SKILL_DIR" && skill_ready=1
      chmod 700 "$SKILL_DIR" 2>/dev/null || true
      log "[UPDATE] skills 已更新"
    else
      log "[WARN] skills 压缩包校验/解压失败，保留旧版本"
    fi
  else
    log "[WARN] skills 下载失败，保留旧版本"
  fi
  rm -f "$SKILL_NEW"
  rm -rf "$STAGE"
fi

if [ "$binary_ready" -eq 1 ] && [ "$skill_ready" -eq 1 ]; then
  printf '{"version":"%s"}\n' "$REMOTE_VERSION" > "$BASE/config/version.json"
  chmod 600 "$BASE/config/version.json"
  log "[DONE] 初始化完成，版本 $REMOTE_VERSION"
else
  log "[ERROR] 初始化未完成：旧引擎或 skills 不可用"
  exit 1
fi
