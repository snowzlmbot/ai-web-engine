#!/system/bin/sh
# Idempotent Android Root bootstrap for ai-web-engine.
# This script is executed on the phone, not on Termux or a desktop host.
set -u

BASE=${AI_WEB_ENGINE_BASE:-/data/local/ai-instruction}
RAW_BASE_URL=${AI_WEB_ENGINE_RAW_BASE_URL:-https://raw.githubusercontent.com/snowzlmbot/ai-web-engine/main}
RELEASE_BASE_URL=${AI_WEB_ENGINE_RELEASE_BASE_URL:-https://github.com/snowzlmbot/ai-web-engine/releases/download}
SKILLS_URL=${AI_WEB_ENGINE_SKILLS_URL:-https://raw.githubusercontent.com/snowzlmbot/ShortX-Files/main/skills/shortx-rule-creator.zip}

log() { printf '%s\n' "$1"; }
fail() { log "[ERROR] $1"; exit 1; }

binary_valid() {
  candidate=$1
  [ -s "$candidate" ] || return 1
  [ -x "$candidate" ] || return 1
  command -v od >/dev/null 2>&1 || return 1
  magic=$(od -An -tx1 -N4 "$candidate" 2>/dev/null | tr -d ' \n\t')
  elf_class=$(od -An -tx1 -j4 -N1 "$candidate" 2>/dev/null | tr -d ' \n\t')
  machine=$(od -An -tx1 -j18 -N2 "$candidate" 2>/dev/null | tr -d ' \n\t')
  [ "$magic" = "7f454c46" ] || return 1
  [ "$elf_class" = "02" ] || return 1
  [ "$machine" = "b700" ] || return 1
  return 0
}

skills_valid() {
  root=$1
  [ -s "$root/SKILL.md" ] || return 1
  [ -d "$root/references" ] || return 1
  for name in actions advanced conditions examples triggers variables; do
    [ -s "$root/references/$name.md" ] || return 1
  done
  return 0
}

safe_zip_entries() {
  archive=$1
  command -v unzip >/dev/null 2>&1 || return 1
  entries=$(unzip -Z1 "$archive" 2>/dev/null) || return 1
  [ -n "$entries" ] || return 1
  bad_prefix=$(printf '%s\n' "$entries" | grep -v '^shortx-rule-creator/' || true)
  [ -z "$bad_prefix" ] || return 1
  bad_path=$(printf '%s\n' "$entries" | grep -E '(^/|(^|/)\.\.(/|$))' || true)
  [ -z "$bad_path" ] || return 1
  return 0
}

[ "$(id -u)" = 0 ] || fail "必须在 Android Root shell 中运行"
command -v getprop >/dev/null 2>&1 || fail "不是可识别的 Android 环境：缺少 getprop"
command -v uname >/dev/null 2>&1 || fail "缺少 uname"
command -v curl >/dev/null 2>&1 || fail "缺少 curl"
command -v unzip >/dev/null 2>&1 || fail "缺少 unzip"
command -v sed >/dev/null 2>&1 || fail "缺少 sed"
command -v tr >/dev/null 2>&1 || fail "缺少 tr"
command -v grep >/dev/null 2>&1 || fail "缺少 grep"
command -v od >/dev/null 2>&1 || fail "缺少 od"
command -v head >/dev/null 2>&1 || fail "缺少 head"
command -v wc >/dev/null 2>&1 || fail "缺少 wc"
command -v readlink >/dev/null 2>&1 || fail "缺少 readlink"
command -v pgrep >/dev/null 2>&1 || fail "缺少 pgrep"
command -v am >/dev/null 2>&1 || fail "缺少 Android am"

ABI=$(getprop ro.product.cpu.abi 2>/dev/null || true)
MACHINE=$(uname -m 2>/dev/null || true)
case "$ABI:$MACHINE" in
  arm64-v8a:*|*:aarch64) ;;
  *) fail "设备不是 Android ARM64：abi=$ABI machine=$MACHINE" ;;
esac

mkdir -p "$BASE/bin" "$BASE/scripts" "$BASE/config" "$BASE/skills" "$BASE/sessions" "$BASE/logs" || fail "创建 Android 引擎目录失败"
chmod 700 "$BASE" "$BASE/bin" "$BASE/scripts" "$BASE/config" "$BASE/skills" "$BASE/sessions" "$BASE/logs" 2>/dev/null || true

CONFIG="$BASE/config/model_config.json"
if [ ! -f "$CONFIG" ]; then
  printf '%s\n' '{"provider":"","endpoint":"","protocol":"openai","apiKey":"","defaultModelId":"","models":[],"reasoningLevel":"medium"}' > "$CONFIG" || fail "创建模型配置模板失败"
  chmod 600 "$CONFIG" || fail "设置模型配置权限失败"
  log "[INIT] 已创建 model_config.json"
else
  chmod 600 "$CONFIG" 2>/dev/null || true
fi

MASTER_KEY="$BASE/config/master.key"
if [ ! -f "$MASTER_KEY" ]; then
  KEY_NEW="$MASTER_KEY.new"
  rm -f "$KEY_NEW"
  head -c 32 /dev/urandom > "$KEY_NEW" 2>/dev/null || fail "无法生成 master.key"
  [ "$(wc -c < "$KEY_NEW" | tr -d ' \n\t')" = "32" ] || { rm -f "$KEY_NEW"; fail "master.key 长度校验失败"; }
  chmod 600 "$KEY_NEW" || { rm -f "$KEY_NEW"; fail "设置 master.key 权限失败"; }
  mv "$KEY_NEW" "$MASTER_KEY" || { rm -f "$KEY_NEW"; fail "安装 master.key 失败"; }
else
  [ "$(wc -c < "$MASTER_KEY" | tr -d ' \n\t')" = "32" ] || fail "已有 master.key 长度不是 32 字节"
  chmod 600 "$MASTER_KEY" 2>/dev/null || true
fi

REMOTE_VERSION=$(curl -fsSL --retry 2 --connect-timeout 15 "$RAW_BASE_URL/version.json" 2>/dev/null | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([0-9][0-9A-Za-z._-]*\)".*/\1/p')
[ -n "$REMOTE_VERSION" ] || fail "无法读取远端版本"
LOCAL_VERSION=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$BASE/config/version.json" 2>/dev/null || true)
BINARY="$BASE/bin/ai-web-engine"
SKILL_DIR="$BASE/skills/shortx-rule-creator"

if [ "$REMOTE_VERSION" = "$LOCAL_VERSION" ] && binary_valid "$BINARY" && skills_valid "$SKILL_DIR" && [ "$(wc -c < "$MASTER_KEY" | tr -d ' \n\t')" = "32" ]; then
  log "[SKIP] Android 环境已初始化且版本 $REMOTE_VERSION 校验通过"
  exit 0
fi

BIN_NEW="$BASE/bin/ai-web-engine.new"
SKILL_NEW="$BASE/skills/shortx-rule-creator.zip.new"
STAGE="$BASE/skills/.shortx-rule-creator.staging"
BIN_OLD="$BASE/bin/ai-web-engine.old"
SKILL_OLD="$BASE/skills/shortx-rule-creator.old"
rm -f "$BIN_NEW" "$SKILL_NEW" "$BIN_OLD"
rm -rf "$STAGE" "$SKILL_OLD"

log "[FETCH] 下载 Android ARM64 引擎 $REMOTE_VERSION"
if ! curl -fsSL --retry 2 --connect-timeout 15 "$RELEASE_BASE_URL/v$REMOTE_VERSION/ai-web-engine-android-arm64" -o "$BIN_NEW" || ! chmod 755 "$BIN_NEW" || ! binary_valid "$BIN_NEW"; then
  rm -f "$BIN_NEW"
  fail "Android ARM64 引擎下载或 ELF/AArch64 校验失败；旧引擎保持不变"
fi

log "[FETCH] 下载 shortx-rule-creator skills"
if ! curl -fsSL --retry 2 --connect-timeout 15 "$SKILLS_URL" -o "$SKILL_NEW" || [ ! -s "$SKILL_NEW" ] || ! safe_zip_entries "$SKILL_NEW"; then
  rm -f "$BIN_NEW" "$SKILL_NEW"
  fail "skills 下载或 ZIP 安全校验失败；旧 skills 保持不变"
fi
mkdir -p "$STAGE" || fail "创建 skills staging 目录失败"
if ! unzip -oq "$SKILL_NEW" -d "$STAGE" || ! skills_valid "$STAGE/shortx-rule-creator"; then
  rm -f "$BIN_NEW" "$SKILL_NEW"
  rm -rf "$STAGE"
  fail "skills 解压或完整性校验失败；旧 skills 保持不变"
fi

# Commit both new payloads only after all staging checks pass.
if [ -d "$SKILL_DIR" ]; then mv "$SKILL_DIR" "$SKILL_OLD" || fail "无法保护旧 skills"
fi
if [ -x "$BINARY" ]; then mv "$BINARY" "$BIN_OLD" || { [ -d "$SKILL_OLD" ] && mv "$SKILL_OLD" "$SKILL_DIR"; fail "无法保护旧引擎"; }
fi
if ! mv "$BIN_NEW" "$BINARY"; then
  [ -f "$BIN_OLD" ] && mv "$BIN_OLD" "$BINARY"
  [ -d "$SKILL_OLD" ] && mv "$SKILL_OLD" "$SKILL_DIR"
  rm -f "$SKILL_NEW"; rm -rf "$STAGE"
  fail "引擎安装失败，已恢复旧资源"
fi
if ! mv "$STAGE/shortx-rule-creator" "$SKILL_DIR"; then
  rm -f "$BINARY"
  [ -f "$BIN_OLD" ] && mv "$BIN_OLD" "$BINARY"
  [ -d "$SKILL_OLD" ] && mv "$SKILL_OLD" "$SKILL_DIR"
  rm -f "$SKILL_NEW"; rm -rf "$STAGE"
  fail "skills 安装失败，已恢复旧资源"
fi

chmod 755 "$BINARY" || fail "引擎权限校验失败"
chmod 700 "$SKILL_DIR" 2>/dev/null || true
binary_valid "$BINARY" || fail "安装后的 Android ARM64 引擎最终校验失败"
skills_valid "$SKILL_DIR" || fail "安装后的 skills 最终校验失败"
[ -s "$CONFIG" ] || fail "模型配置未落地"
[ "$(wc -c < "$MASTER_KEY" | tr -d ' \n\t')" = "32" ] || fail "master.key 最终校验失败"

rm -f "$BIN_OLD" "$BIN_NEW" "$SKILL_NEW"
rm -rf "$SKILL_OLD" "$STAGE"
VERSION_NEW="$BASE/config/version.json.new"
printf '{"version":"%s"}\n' "$REMOTE_VERSION" > "$VERSION_NEW" || fail "写入版本标记失败"
chmod 600 "$VERSION_NEW" || fail "设置版本标记权限失败"
mv "$VERSION_NEW" "$BASE/config/version.json" || fail "提交版本标记失败"

log "[DONE] Android 首次环境初始化完成：引擎、skills、配置、master.key 和目录均已就绪，版本 $REMOTE_VERSION"
exit 0
