#!/system/bin/sh
# Roll back ai-web-engine by exactly one formal GitHub Release.
# This script never accepts a caller-supplied version and never changes user data.
set -u

BASE=${AI_WEB_ENGINE_BASE:-/data/local/ai-instruction}
REPO=${AI_WEB_ENGINE_REPO:-snowzlmbot/ai-web-engine}
API_BASE=${AI_WEB_ENGINE_API_BASE:-https://api.github.com/repos/$REPO}
DOWNLOAD_BASE=${AI_WEB_ENGINE_DOWNLOAD_BASE:-https://github.com/$REPO/releases/download}
BINARY="$BASE/bin/ai-web-engine"
PIDFILE="$BINARY.pid"
VERSION_FILE="$BASE/config/version.json"
LOG="$BASE/logs/engine.log"
START_SCRIPT="$BASE/scripts/start.sh"
STOP_SCRIPT="$BASE/scripts/stop.sh"
MANIFEST_FILE="$BASE/scripts/manifest.json"
SIGNATURE_FILE="$BASE/scripts/manifest.sig"
PUBLIC_KEY_FILE="$BASE/scripts/manifest.pub"
PORT=6688
URL="http://127.0.0.1:$PORT"

log() { printf '%s\n' "$1"; }
fail() { log "[ERROR] $1"; exit 1; }

verify_scripts() {
  [ -x "$BINARY" ] && [ -s "$MANIFEST_FILE" ] && [ -s "$SIGNATURE_FILE" ] && [ -s "$PUBLIC_KEY_FILE" ] || return 1
  "$BINARY" verify-scripts --manifest "$MANIFEST_FILE" --signature "$SIGNATURE_FILE" --public-key "$PUBLIC_KEY_FILE" --scripts-dir "$BASE/scripts" --version "$(current_version)" >/dev/null 2>&1
}

binary_valid() {
  candidate=$1
  expected_class=$2
  expected_machine=$3
  [ -s "$candidate" ] || return 1
  [ -x "$candidate" ] || return 1
  magic=$(od -An -tx1 -N4 "$candidate" 2>/dev/null | tr -d ' \n\t')
  elf_class=$(od -An -tx1 -j4 -N1 "$candidate" 2>/dev/null | tr -d ' \n\t')
  machine=$(od -An -tx1 -j18 -N2 "$candidate" 2>/dev/null | tr -d ' \n\t')
  [ "$magic" = "7f454c46" ] || return 1
  [ "$elf_class" = "$expected_class" ] || return 1
  [ "$machine" = "$expected_machine" ] || return 1
  return 0
}

owned_pid() {
  candidate=$1
  case "$candidate" in ''|*[!0-9]*) return 1 ;; esac
  kill -0 "$candidate" 2>/dev/null || return 1
  exe=$(readlink "/proc/$candidate/exe" 2>/dev/null || true)
  if [ "$exe" = "$BINARY" ]; then return 0; fi
  cmdline=$(tr '\000' ' ' <"/proc/$candidate/cmdline" 2>/dev/null || true)
  case "$cmdline" in "$BINARY"|"$BINARY "*) return 0 ;; esac
  return 1
}

stop_owned() {
  [ -s "$PIDFILE" ] || return 0
  pid=$(cat "$PIDFILE" 2>/dev/null || true)
  owned_pid "$pid" || return 0
  kill -TERM "$pid" 2>/dev/null || true
  n=0
  while [ "$n" -lt 15 ]; do
    kill -0 "$pid" 2>/dev/null || return 0
    sleep 1
    n=$((n + 1))
  done
  owned_pid "$pid" && kill -KILL "$pid" 2>/dev/null || true
  kill -0 "$pid" 2>/dev/null && return 1
  return 0
}

start_without_update() {
  [ -x "$START_SCRIPT" ] || return 1
  AI_WEB_ENGINE_SKIP_UPDATE=1 sh "$START_SCRIPT" >/dev/null 2>&1
}

current_version() {
  sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([0-9][0-9A-Za-z._-]*\)".*/\1/p' "$VERSION_FILE" 2>/dev/null || true
}

health_version() {
  curl -fsSL --connect-timeout 5 "$URL/health" 2>/dev/null \
    | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

if [ "$(id -u)" = 0 ]; then
  :
else
  fail "必须在 Android Root shell 中运行"
fi
if ! verify_scripts; then
  fail "scripts 完整性校验失败，拒绝执行回退"
fi
for command in curl sed od tr sha256sum readlink kill sleep getprop uname cp mv rm chmod date cat; do
  command -v "$command" >/dev/null 2>&1 || fail "缺少命令：$command"
done
[ -x "$BINARY" ] || fail "当前引擎不存在：$BINARY"
[ -x "$START_SCRIPT" ] || fail "start.sh 不存在或不可执行"
[ -x "$STOP_SCRIPT" ] || fail "stop.sh 不存在或不可执行"

ABI=$(getprop ro.product.cpu.abi 2>/dev/null || true)
MACHINE=$(uname -m 2>/dev/null || true)
case "$ABI" in
  arm64-v8a) ASSET=ai-web-engine-android-arm64; ELF_CLASS=02; ELF_MACHINE=b700 ;;
  armeabi-v7a|armeabi) ASSET=ai-web-engine-android-armv7; ELF_CLASS=01; ELF_MACHINE=2800 ;;
  x86_64) ASSET=ai-web-engine-android-x86_64; ELF_CLASS=02; ELF_MACHINE=3e00 ;;
  x86) ASSET=ai-web-engine-android-x86; ELF_CLASS=01; ELF_MACHINE=0300 ;;
  '')
    case "$MACHINE" in
      aarch64) ASSET=ai-web-engine-android-arm64; ELF_CLASS=02; ELF_MACHINE=b700 ;;
      armv7l|armv8l) ASSET=ai-web-engine-android-armv7; ELF_CLASS=01; ELF_MACHINE=2800 ;;
      x86_64) ASSET=ai-web-engine-android-x86_64; ELF_CLASS=02; ELF_MACHINE=3e00 ;;
      i686) ASSET=ai-web-engine-android-x86; ELF_CLASS=01; ELF_MACHINE=0300 ;;
      *) fail "不支持的 ABI：abi=$ABI machine=$MACHINE" ;;
    esac
    ;;
  *) fail "不支持的 ABI：abi=$ABI machine=$MACHINE" ;;
esac

CURRENT=$(current_version)
[ -n "$CURRENT" ] || fail "无法读取当前版本"
JSON="$BASE/.ai-web-engine-releases.json.new"
BIN_NEW="$BASE/bin/ai-web-engine.rollback.new"
SUM_NEW="$BASE/bin/SHA256SUMS.rollback.new"
BIN_BACKUP="$BASE/bin/ai-web-engine.rollback.current"
VERSION_BACKUP="$BASE/config/version.json.rollback.current"
SCRIPT_BACKUP_DIR="$BASE/scripts/.rollback-current"
TARGET_STAGE="$BASE/scripts/.rollback-target"
TARGET_MANIFEST="$TARGET_STAGE/manifest.json"
TARGET_SIGNATURE="$TARGET_STAGE/manifest.sig"
TARGET_PUBLIC_KEY="$TARGET_STAGE/manifest.pub"
TARGET_INIT="$TARGET_STAGE/init.sh"
TARGET_START="$TARGET_STAGE/start.sh"
TARGET_STOP="$TARGET_STAGE/stop.sh"
TARGET_ROLLBACK="$TARGET_STAGE/rollback.sh"
HAD_VERSION=0
[ -s "$VERSION_FILE" ] && HAD_VERSION=1
rm -f "$JSON" "$BIN_NEW" "$SUM_NEW" "$BIN_BACKUP" "$VERSION_BACKUP"
rm -rf "$SCRIPT_BACKUP_DIR" "$TARGET_STAGE"
mkdir -p "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR" || fail "无法创建回退 staging 目录"

curl -fsSL --retry 2 --connect-timeout 15 -H 'Accept: application/vnd.github+json' \
  "$API_BASE/releases?per_page=10&rollback=$(date +%s)-$$" -o "$JSON" || {
  rm -f "$JSON"
  fail "无法读取 GitHub Release 列表"
}
release_tags() {
  tr -d '\r\n' < "$JSON" | sed 's/},[[:space:]]*{/}\n{/g' |
    while IFS= read -r record; do
      case "$record" in
        *'"draft":true'*|*'"prerelease":true'*) continue ;;
      esac
      printf '%s\n' "$record" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\(v[0-9][0-9A-Za-z._-]*\)".*/\1/p'
    done
}
TAGS=$(release_tags)
LATEST=$(printf '%s\n' "$TAGS" | sed -n '1p')
PREVIOUS=$(printf '%s\n' "$TAGS" | sed -n '2p')
rm -f "$JSON"
case "$LATEST" in v[0-9]*) ;; *) fail "Release 列表中没有可识别的最新正式版本" ;; esac
case "$PREVIOUS" in v[0-9]*) ;; *) fail "没有可回退的上一个正式版本" ;; esac
[ "v$CURRENT" = "$LATEST" ] || fail "当前版本 $CURRENT 不是最新版本 $LATEST，拒绝跨版本或重复回退"
TARGET=${PREVIOUS#v}

case "$ASSET" in
  ai-web-engine-android-arm64) EXPECTED_CLASS=02; EXPECTED_MACHINE=b700 ;;
  ai-web-engine-android-armv7) EXPECTED_CLASS=01; EXPECTED_MACHINE=2800 ;;
  ai-web-engine-android-x86_64) EXPECTED_CLASS=02; EXPECTED_MACHINE=3e00 ;;
  ai-web-engine-android-x86) EXPECTED_CLASS=01; EXPECTED_MACHINE=0300 ;;
esac

log "[FETCH] 准备从 $LATEST 回退到 $PREVIOUS（仅一版）"
DOWNLOAD_URL="$DOWNLOAD_BASE/$PREVIOUS/$ASSET"
SUM_URL="$DOWNLOAD_BASE/$PREVIOUS/SHA256SUMS"
curl -fsSL --retry 2 --connect-timeout 15 "$DOWNLOAD_URL" -o "$BIN_NEW" || { rm -f "$BIN_NEW"; fail "目标引擎下载失败，当前版本保持不变"; }
curl -fsSL --retry 2 --connect-timeout 15 "$SUM_URL" -o "$SUM_NEW" || { rm -f "$BIN_NEW" "$SUM_NEW"; fail "SHA256SUMS 下载失败，当前版本保持不变"; }
chmod 755 "$BIN_NEW" || { rm -f "$BIN_NEW" "$SUM_NEW"; fail "目标引擎权限设置失败"; }
EXPECTED_HASH=$(sed -n "s/^\([0-9A-Fa-f][0-9A-Fa-f]*\)[[:space:]][[:space:]]*$ASSET$/\1/p" "$SUM_NEW")
[ -n "$EXPECTED_HASH" ] || { rm -f "$BIN_NEW" "$SUM_NEW"; fail "SHA256SUMS 中没有当前 ABI 资产"; }
ACTUAL_HASH=$(sha256sum "$BIN_NEW" | tr -s ' ' | sed 's/[[:space:]].*//')
[ "$ACTUAL_HASH" = "$EXPECTED_HASH" ] || { rm -f "$BIN_NEW" "$SUM_NEW"; fail "目标引擎 SHA256 校验失败"; }
binary_valid "$BIN_NEW" "$ELF_CLASS" "$ELF_MACHINE" || { rm -f "$BIN_NEW" "$SUM_NEW"; rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"; fail "目标引擎 ELF/ABI 校验失败"; }

for pair in \
  "manifest.json:manifest.json" \
  "manifest.sig:manifest.sig" \
  "manifest.pub:manifest.pub" \
  "ai-web-engine-script-init.sh:init.sh" \
  "ai-web-engine-script-start.sh:start.sh" \
  "ai-web-engine-script-stop.sh:stop.sh" \
  "ai-web-engine-script-rollback.sh:rollback.sh"; do
  remote=${pair%%:*}
  local_name=${pair#*:}
  curl -fsSL --retry 2 --connect-timeout 15 "$DOWNLOAD_BASE/$PREVIOUS/$remote" -o "$TARGET_STAGE/$local_name" || { rm -f "$BIN_NEW" "$SUM_NEW"; rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"; fail "目标版本完整性资产下载失败：$remote"; }
  [ -s "$TARGET_STAGE/$local_name" ] || { rm -f "$BIN_NEW" "$SUM_NEW"; rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"; fail "目标版本完整性资产为空：$remote"; }
done
for pair in "manifest.json:manifest.json" "manifest.sig:manifest.sig" "manifest.pub:manifest.pub" "ai-web-engine-script-init.sh:init.sh" "ai-web-engine-script-start.sh:start.sh" "ai-web-engine-script-stop.sh:stop.sh" "ai-web-engine-script-rollback.sh:rollback.sh"; do
  remote=${pair%%:*}
  local_name=${pair#*:}
  expected=$(sed -n "s/^\\([0-9A-Fa-f]\\{64\\}\\)[[:space:]][[:space:]]*$remote$/\\1/p" "$SUM_NEW")
  actual=$(sha256sum "$TARGET_STAGE/$local_name" | tr -s ' ' | sed 's/[[:space:]].*//')
  [ -n "$expected" ] && [ "$actual" = "$expected" ] || { rm -f "$BIN_NEW" "$SUM_NEW"; rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"; fail "目标版本完整性资产 SHA-256 校验失败：$remote"; }
done
chmod 755 "$TARGET_STAGE"/*.sh
if ! "$BIN_NEW" verify-scripts --manifest "$TARGET_MANIFEST" --signature "$TARGET_SIGNATURE" --public-key "$TARGET_PUBLIC_KEY" --scripts-dir "$TARGET_STAGE" --version "$TARGET" >/dev/null 2>&1; then
  rm -f "$BIN_NEW" "$SUM_NEW"
  rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"
  fail "目标版本脚本签名校验失败，未停止当前引擎"
fi

cp "$VERSION_FILE" "$VERSION_BACKUP" 2>/dev/null || true
for file in init.sh start.sh stop.sh rollback.sh manifest.json manifest.sig manifest.pub; do
  cp "$BASE/scripts/$file" "$SCRIPT_BACKUP_DIR/$file" || {
    rm -f "$BIN_NEW" "$SUM_NEW" "$BIN_BACKUP" "$VERSION_BACKUP"
    rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"
    fail "无法创建当前脚本完整性材料备份：$file"
  }
done
if ! stop_owned; then
  rm -f "$BIN_NEW" "$SUM_NEW" "$BIN_BACKUP" "$VERSION_BACKUP"
  rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"
  fail "当前引擎未能安全停止，未执行回退"
fi
restore_rollback_payloads() {
  rm -f "$BINARY" "$VERSION_FILE" "$BASE/scripts/init.sh" "$BASE/scripts/start.sh" "$BASE/scripts/stop.sh" "$BASE/scripts/rollback.sh" "$MANIFEST_FILE" "$SIGNATURE_FILE" "$PUBLIC_KEY_FILE"
  [ -f "$BIN_BACKUP" ] && mv "$BIN_BACKUP" "$BINARY"
  [ -f "$VERSION_BACKUP" ] && mv "$VERSION_BACKUP" "$VERSION_FILE"
  for file in init.sh start.sh stop.sh rollback.sh manifest.json manifest.sig manifest.pub; do
    [ -f "$SCRIPT_BACKUP_DIR/$file" ] && mv "$SCRIPT_BACKUP_DIR/$file" "$BASE/scripts/$file"
  done
}

VERSION_NEW="$BASE/config/version.json.rollback.new"
rm -f "$VERSION_NEW"
if ! mv "$BIN_NEW" "$BINARY" || ! printf '{"version":"%s","abi":"%s"}\n' "$TARGET" "${ABI:-$MACHINE}" >"$VERSION_NEW" || ! chmod 600 "$VERSION_NEW" || ! mv "$VERSION_NEW" "$VERSION_FILE" || ! chmod 755 "$BINARY"; then
  restore_rollback_payloads
  rm -f "$SUM_NEW"
  rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"
  start_without_update || true
  fail "安装目标版本失败，已恢复当前版本"
fi
for file in init.sh start.sh stop.sh rollback.sh manifest.json manifest.sig manifest.pub; do
  cp "$TARGET_STAGE/$file" "$BASE/scripts/$file.rollback.new" || {
    restore_rollback_payloads
    rm -f "$VERSION_NEW" "$SUM_NEW"
    rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"
    start_without_update || true
    fail "安装目标完整性材料失败，已恢复当前版本：$file"
  }
done
chmod 755 "$BASE/scripts/init.sh.rollback.new" "$BASE/scripts/start.sh.rollback.new" "$BASE/scripts/stop.sh.rollback.new" "$BASE/scripts/rollback.sh.rollback.new"
chmod 644 "$BASE/scripts/manifest.json.rollback.new" "$BASE/scripts/manifest.sig.rollback.new" "$BASE/scripts/manifest.pub.rollback.new"
for file in init.sh start.sh stop.sh rollback.sh manifest.json manifest.sig manifest.pub; do
  mv "$BASE/scripts/$file.rollback.new" "$BASE/scripts/$file" || {
    restore_rollback_payloads
    rm -f "$SUM_NEW"
    rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"
    start_without_update || true
    fail "提交目标完整性材料失败，已恢复当前版本：$file"
  }
done
rm -f "$SUM_NEW"
if ! start_without_update; then
  stop_owned || true
  restore_rollback_payloads
  rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"
  start_without_update || true
  fail "目标版本启动失败，已恢复当前版本"
fi
n=0
while [ "$n" -lt 15 ]; do
  RUNNING=$(health_version || true)
  [ "$RUNNING" = "$TARGET" ] && break
  sleep 1
  n=$((n + 1))
done
if [ "$RUNNING" != "$TARGET" ]; then
  stop_owned || true
  restore_rollback_payloads
  rm -rf "$TARGET_STAGE" "$SCRIPT_BACKUP_DIR"
  start_without_update || true
  fail "目标版本健康检查失败，已恢复当前版本"
fi
rm -f "$BIN_BACKUP" "$VERSION_BACKUP"
printf '%s [INFO] rollback from=%s to=%s asset=%s\n' "$(date -u +%FT%TZ)" "$CURRENT" "$TARGET" "$ASSET" >>"$LOG" 2>/dev/null || true
log "[DONE] 已安全回退到上一版本 $TARGET；配置、Key、会话、skills 和日志均保留"
exit 0
