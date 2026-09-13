#!/system/bin/sh
# Idempotent Android Root bootstrap for ai-web-engine.
# This script is executed on the phone, not on Termux or a desktop host.
set -u

BASE=${AI_WEB_ENGINE_BASE:-/data/local/ai-instruction}
RAW_BASE_URL=${AI_WEB_ENGINE_RAW_BASE_URL:-https://raw.githubusercontent.com/snowzlmbot/ai-web-engine/main}
RELEASE_BASE_URL=${AI_WEB_ENGINE_RELEASE_BASE_URL:-https://github.com/snowzlmbot/ai-web-engine/releases/download}
SKILLS_URL=${AI_WEB_ENGINE_SKILLS_URL:-https://raw.githubusercontent.com/snowzlmbot/ShortX-Files/main/skills/shortx-rule-creator.zip}
START_URL=${AI_WEB_ENGINE_START_URL:-https://raw.githubusercontent.com/snowzlmbot/ai-web-engine/main/scripts/start.sh}
STOP_URL=${AI_WEB_ENGINE_STOP_URL:-https://raw.githubusercontent.com/snowzlmbot/ai-web-engine/main/scripts/stop.sh}

log() { printf '%s\n' "$1"; }
fail() { log "[ERROR] $1"; exit 1; }

binary_valid() {
  candidate=$1
  expected_class=$2
  expected_machine=$3
  [ -s "$candidate" ] || return 1
  [ -x "$candidate" ] || return 1
  command -v od >/dev/null 2>&1 || return 1
  magic=$(od -An -tx1 -N4 "$candidate" 2>/dev/null | tr -d ' \n\t')
  elf_class=$(od -An -tx1 -j4 -N1 "$candidate" 2>/dev/null | tr -d ' \n\t')
  machine=$(od -An -tx1 -j18 -N2 "$candidate" 2>/dev/null | tr -d ' \n\t')
  [ "$magic" = "7f454c46" ] || return 1
  [ "$elf_class" = "$expected_class" ] || return 1
  [ "$machine" = "$expected_machine" ] || return 1
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

script_valid() {
  candidate=$1
  [ -s "$candidate" ] || return 1
  sh -n "$candidate" >/dev/null 2>&1 || return 1
  grep -q 'ai-web-engine' "$candidate" || return 1
  return 0
}

config_valid() {
  candidate=$1
  [ -s "$candidate" ] || return 1
  grep -q '"reasoningLevel"' "$candidate" || return 1
  if grep -q '"providers"' "$candidate" && grep -q '"activeProviderId"' "$candidate"; then
    return 0
  fi
  grep -q '"provider"' "$candidate" || return 1
  grep -q '"endpoint"' "$candidate" || return 1
  grep -q '"protocol"' "$candidate" || return 1
  grep -q '"models"' "$candidate" || return 1
  return 0
}

safe_zip_entries() {
  archive=$1
  command -v unzip >/dev/null 2>&1 || return 1
  entries=$(unzip -Z1 "$archive" 2>/dev/null || true)
  if [ -z "$entries" ]; then
    # Android unzip builds commonly omit ZipInfo (-Z1). Parse the portable
    # `unzip -l` listing instead; the archive itself is tested separately.
    entries=$(unzip -l "$archive" 2>/dev/null | sed -n 's/^[[:space:]]*[0-9][0-9]*[[:space:]][[:space:]]*[0-9][0-9-]*[[:space:]][[:space:]]*[0-9:]*[[:space:]][[:space:]]*//p' | sed '/^[[:space:]]*$/d' | sed '/^[[:space:]]*[0-9][0-9]*[[:space:]]*$/d' || true)
  fi
  [ -n "$entries" ] || return 1
  bad_prefix=$(printf '%s\n' "$entries" | grep -v '^shortx-rule-creator/' || true)
  [ -z "$bad_prefix" ] || return 1
  bad_path=$(printf '%s\n' "$entries" | grep -E '(^/|(^|/)\.\.(/|$))' || true)
  [ -z "$bad_path" ] || return 1
  for required in shortx-rule-creator/SKILL.md shortx-rule-creator/references/actions.md shortx-rule-creator/references/advanced.md shortx-rule-creator/references/conditions.md shortx-rule-creator/references/examples.md shortx-rule-creator/references/triggers.md shortx-rule-creator/references/variables.md; do
    printf '%s\n' "$entries" | grep -F -x "$required" >/dev/null 2>&1 || return 1
  done
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
command -v date >/dev/null 2>&1 || fail "缺少 date"

ABI=$(getprop ro.product.cpu.abi 2>/dev/null || true)
MACHINE=$(uname -m 2>/dev/null || true)
case "$ABI" in
  arm64-v8a) ARCH_NAME=arm64; ASSET_SUFFIX=android-arm64; ELF_CLASS=02; ELF_MACHINE=b700 ;;
  armeabi-v7a|armeabi) ARCH_NAME=armv7; ASSET_SUFFIX=android-armv7; ELF_CLASS=01; ELF_MACHINE=2800 ;;
  x86_64) ARCH_NAME=x86_64; ASSET_SUFFIX=android-x86_64; ELF_CLASS=02; ELF_MACHINE=3e00 ;;
  x86) ARCH_NAME=x86; ASSET_SUFFIX=android-x86; ELF_CLASS=01; ELF_MACHINE=0300 ;;
  '')
    case "$MACHINE" in
      aarch64) ARCH_NAME=arm64; ASSET_SUFFIX=android-arm64; ELF_CLASS=02; ELF_MACHINE=b700 ;;
      armv7l|armv8l) ARCH_NAME=armv7; ASSET_SUFFIX=android-armv7; ELF_CLASS=01; ELF_MACHINE=2800 ;;
      x86_64) ARCH_NAME=x86_64; ASSET_SUFFIX=android-x86_64; ELF_CLASS=02; ELF_MACHINE=3e00 ;;
      i686) ARCH_NAME=x86; ASSET_SUFFIX=android-x86; ELF_CLASS=01; ELF_MACHINE=0300 ;;
      *) fail "不支持的 Android ABI：abi=$ABI machine=$MACHINE" ;;
    esac
    ;;
  *) fail "不支持的 Android ABI：abi=$ABI machine=$MACHINE" ;;
esac

log "[INFO] Android ABI=$ABI machine=$MACHINE asset=$ASSET_SUFFIX"

mkdir -p "$BASE/bin" "$BASE/scripts" "$BASE/config" "$BASE/config/providers" "$BASE/skills" "$BASE/sessions" "$BASE/logs" || fail "创建 Android 引擎目录失败"
chmod 700 "$BASE" "$BASE/bin" "$BASE/scripts" "$BASE/config" "$BASE/config/providers" "$BASE/skills" "$BASE/sessions" "$BASE/logs" 2>/dev/null || true

CONFIG="$BASE/config/model_config.json"
if [ ! -f "$CONFIG" ]; then
  printf '%s\n' '{"provider":"","endpoint":"","protocol":"openai","apiKey":"","defaultModelId":"","models":[],"reasoningLevel":"xhigh"}' > "$CONFIG" || fail "创建模型配置模板失败"
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

INIT_CACHE_KEY=$(date +%s)
FORCE_UPDATE=${AI_WEB_ENGINE_FORCE_UPDATE:-0}
REMOTE_VERSION=$(curl -fsSL --retry 2 --connect-timeout 15 "$RAW_BASE_URL/version.json?ai_web_engine_init=$INIT_CACHE_KEY" 2>/dev/null | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([0-9][0-9A-Za-z._-]*\)".*/\1/p')
[ -n "$REMOTE_VERSION" ] || fail "无法读取远端版本"
resource_url() {
  base=$1
  case "$base" in
    *\?*) printf '%s&ai_web_engine_init=%s' "$base" "$INIT_CACHE_KEY" ;;
    *) printf '%s?ai_web_engine_init=%s' "$base" "$INIT_CACHE_KEY" ;;
  esac
}

LOCAL_VERSION=$(sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$BASE/config/version.json" 2>/dev/null || true)
LOCAL_ABI=$(sed -n 's/.*"abi"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$BASE/config/version.json" 2>/dev/null || true)
BINARY="$BASE/bin/ai-web-engine"
SKILL_DIR="$BASE/skills/shortx-rule-creator"
START_SCRIPT="$BASE/scripts/start.sh"
STOP_SCRIPT="$BASE/scripts/stop.sh"

if [ "$FORCE_UPDATE" != 1 ] && [ "$REMOTE_VERSION" = "$LOCAL_VERSION" ] && [ "$LOCAL_ABI" = "$ARCH_NAME" ] && binary_valid "$BINARY" "$ELF_CLASS" "$ELF_MACHINE" && skills_valid "$SKILL_DIR" && script_valid "$START_SCRIPT" && script_valid "$STOP_SCRIPT" && config_valid "$CONFIG" && [ "$(wc -c < "$MASTER_KEY" | tr -d ' \n\t')" = "32" ]; then
  log "[SKIP] Android 环境已初始化且版本 $REMOTE_VERSION 校验通过"
  exit 0
fi

BIN_NEW="$BASE/bin/ai-web-engine.new"
SKILL_NEW="$BASE/skills/shortx-rule-creator.zip.new"
START_NEW="$BASE/scripts/start.sh.new"
STOP_NEW="$BASE/scripts/stop.sh.new"
STAGE="$BASE/skills/.shortx-rule-creator.staging"
BIN_OLD="$BASE/bin/ai-web-engine.old"
SKILL_OLD="$BASE/skills/shortx-rule-creator.old"
START_OLD="$BASE/scripts/start.sh.old"
STOP_OLD="$BASE/scripts/stop.sh.old"
rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW" "$BIN_OLD" "$START_OLD" "$STOP_OLD"
rm -rf "$STAGE" "$SKILL_OLD"

log "[FETCH] 下载 Android $ARCH_NAME 引擎 $REMOTE_VERSION"
if ! curl -fsSL --retry 2 --connect-timeout 15 "$(resource_url "$RELEASE_BASE_URL/v$REMOTE_VERSION/ai-web-engine-$ASSET_SUFFIX")" -o "$BIN_NEW" || ! chmod 755 "$BIN_NEW" || ! binary_valid "$BIN_NEW" "$ELF_CLASS" "$ELF_MACHINE"; then
  rm -f "$BIN_NEW"
  fail "Android $ARCH_NAME 引擎下载或 ELF/架构校验失败；旧引擎保持不变"
fi

log "[FETCH] 下载 shortx-rule-creator skills"
if curl -fsSL --retry 2 --connect-timeout 15 "$(resource_url "$SKILLS_URL")" -o "$SKILL_NEW"; then
  [ -s "$SKILL_NEW" ] || { rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"; fail "skills 下载结果为空；旧 skills 保持不变"; }
  unzip -t "$SKILL_NEW" >/dev/null 2>&1 || { rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"; fail "skills ZIP 压缩数据损坏；旧 skills 保持不变"; }
  safe_zip_entries "$SKILL_NEW" || { rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"; fail "skills ZIP 路径校验失败；旧 skills 保持不变"; }
else
  rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"
  fail "skills 下载失败；旧 skills 保持不变"
fi
log "[FETCH] 下载启动和停止脚本"
if ! curl -fsSL --retry 2 --connect-timeout 15 "$(resource_url "$START_URL")" -o "$START_NEW" || ! chmod 755 "$START_NEW" || ! script_valid "$START_NEW" || ! grep -q 'PORT=6688' "$START_NEW"; then
  rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW"
  fail "start.sh 下载或校验失败；旧启动环境保持不变"
fi
if ! curl -fsSL --retry 2 --connect-timeout 15 "$(resource_url "$STOP_URL")" -o "$STOP_NEW" || ! chmod 755 "$STOP_NEW" || ! script_valid "$STOP_NEW"; then
  rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"
  fail "stop.sh 下载或校验失败；旧启动环境保持不变"
fi
mkdir -p "$STAGE" || {
  rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"
  rm -rf "$STAGE"
  fail "创建 skills staging 目录失败；旧资源保持不变"
}
if ! unzip -o "$SKILL_NEW" -d "$STAGE" >/dev/null 2>&1 || ! skills_valid "$STAGE/shortx-rule-creator"; then
  rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"
  rm -rf "$STAGE"
  fail "skills 解压或完整性校验失败；旧 skills 保持不变"
fi

# Commit all new payloads only after every download and staging check passed.
# Preserve old payloads until the complete new set has been installed.
if [ -d "$SKILL_DIR" ]; then
  if ! mv "$SKILL_DIR" "$SKILL_OLD"; then
    rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"
    rm -rf "$STAGE"
    fail "无法保护旧 skills；未替换任何资源"
  fi
fi
if [ -e "$BINARY" ]; then
  if ! mv "$BINARY" "$BIN_OLD"; then
    [ -d "$SKILL_OLD" ] && mv "$SKILL_OLD" "$SKILL_DIR"
    rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"
    rm -rf "$STAGE"
    fail "无法保护旧引擎；未替换任何资源"
  fi
fi
if [ -e "$START_SCRIPT" ]; then
  if ! mv "$START_SCRIPT" "$START_OLD"; then
    [ -f "$BIN_OLD" ] && mv "$BIN_OLD" "$BINARY"
    [ -d "$SKILL_OLD" ] && mv "$SKILL_OLD" "$SKILL_DIR"
    rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"
    rm -rf "$STAGE"
    fail "无法保护旧启动脚本；未替换任何资源"
  fi
fi
if [ -e "$STOP_SCRIPT" ]; then
  if ! mv "$STOP_SCRIPT" "$STOP_OLD"; then
    [ -f "$START_OLD" ] && mv "$START_OLD" "$START_SCRIPT"
    [ -f "$BIN_OLD" ] && mv "$BIN_OLD" "$BINARY"
    [ -d "$SKILL_OLD" ] && mv "$SKILL_OLD" "$SKILL_DIR"
    rm -f "$BIN_NEW" "$SKILL_NEW" "$START_NEW" "$STOP_NEW"
    rm -rf "$STAGE"
    fail "无法保护旧停止脚本；未替换任何资源"
  fi
fi

restore_old_payloads() {
  rm -f "$BINARY" "$START_SCRIPT" "$STOP_SCRIPT"
  [ -f "$BIN_OLD" ] && mv "$BIN_OLD" "$BINARY"
  [ -d "$SKILL_OLD" ] && mv "$SKILL_OLD" "$SKILL_DIR"
  [ -f "$START_OLD" ] && mv "$START_OLD" "$START_SCRIPT"
  [ -f "$STOP_OLD" ] && mv "$STOP_OLD" "$STOP_SCRIPT"
}

if ! mv "$BIN_NEW" "$BINARY"; then
  restore_old_payloads
  rm -f "$SKILL_NEW" "$START_NEW" "$STOP_NEW"
  rm -rf "$STAGE"
  fail "引擎安装失败，已恢复旧资源"
fi
if ! mv "$STAGE/shortx-rule-creator" "$SKILL_DIR"; then
  restore_old_payloads
  rm -f "$SKILL_NEW" "$START_NEW" "$STOP_NEW"
  rm -rf "$STAGE"
  fail "skills 安装失败，已恢复旧资源"
fi
if ! mv "$START_NEW" "$START_SCRIPT"; then
  restore_old_payloads
  rm -f "$SKILL_NEW" "$STOP_NEW"
  rm -rf "$STAGE"
  fail "start.sh 安装失败，已恢复旧资源"
fi
if ! mv "$STOP_NEW" "$STOP_SCRIPT"; then
  restore_old_payloads
  rm -f "$SKILL_NEW"
  rm -rf "$STAGE"
  fail "stop.sh 安装失败，已恢复旧资源"
fi

chmod 755 "$BINARY" "$START_SCRIPT" "$STOP_SCRIPT" || {
  restore_old_payloads
  rm -f "$SKILL_NEW"
  rm -rf "$STAGE"
  fail "启动资源权限设置失败，已恢复旧资源"
}
chmod 700 "$SKILL_DIR" 2>/dev/null || true
if ! binary_valid "$BINARY" "$ELF_CLASS" "$ELF_MACHINE" || ! skills_valid "$SKILL_DIR" || ! script_valid "$START_SCRIPT" || ! script_valid "$STOP_SCRIPT" || ! config_valid "$CONFIG" || [ "$(wc -c < "$MASTER_KEY" | tr -d ' \n\t')" != "32" ]; then
  restore_old_payloads
  rm -f "$SKILL_NEW"
  rm -rf "$STAGE"
  fail "安装后完整性校验失败，已恢复旧资源且未写入完成标记"
fi

VERSION_NEW="$BASE/config/version.json.new"
printf '{"version":"%s","abi":"%s"}\n' "$REMOTE_VERSION" "$ARCH_NAME" > "$VERSION_NEW" || {
  restore_old_payloads
  rm -f "$VERSION_NEW" "$SKILL_NEW"
  fail "写入版本标记失败，已恢复旧资源"
}
chmod 600 "$VERSION_NEW" || {
  restore_old_payloads
  rm -f "$VERSION_NEW" "$SKILL_NEW"
  fail "设置版本标记权限失败，已恢复旧资源"
}
if ! mv "$VERSION_NEW" "$BASE/config/version.json"; then
  restore_old_payloads
  rm -f "$VERSION_NEW"
  fail "提交版本标记失败，已恢复旧资源"
fi
rm -f "$BIN_OLD" "$BIN_NEW" "$SKILL_NEW" "$START_OLD" "$STOP_OLD"
rm -rf "$SKILL_OLD" "$STAGE"

log "[DONE] Android 首次环境初始化完成：ABI=$ARCH_NAME、引擎、skills、启动脚本、停止脚本、配置、master.key 和目录均已就绪，版本 $REMOTE_VERSION"
exit 0
