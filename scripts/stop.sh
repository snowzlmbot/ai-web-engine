#!/system/bin/sh
# Safely stop only the ai-web-engine process started by start.sh.
set -u

BASE=/data/local/ai-instruction
BINARY="$BASE/bin/ai-web-engine"
PIDFILE="$BINARY.pid"
MANIFEST_FILE="$BASE/scripts/manifest.json"
SIGNATURE_FILE="$BASE/scripts/manifest.sig"
PUBLIC_KEY_FILE="$BASE/scripts/manifest.pub"
LOG="$BASE/logs/engine.log"

log() { printf '%s\n' "$1"; }

verify_scripts() {
  [ -x "$BINARY" ] && [ -s "$MANIFEST_FILE" ] && [ -s "$SIGNATURE_FILE" ] && [ -s "$PUBLIC_KEY_FILE" ] || return 1
  "$BINARY" verify-scripts --manifest "$MANIFEST_FILE" --signature "$SIGNATURE_FILE" --public-key "$PUBLIC_KEY_FILE" --scripts-dir "$BASE/scripts" >/dev/null 2>&1
}

if ! verify_scripts; then
  log '[ERROR] scripts 完整性校验失败，拒绝停止引擎'
  exit 1
fi

if [ ! -s "$PIDFILE" ]; then
  log '[INFO] 未运行'
  exit 0
fi

PID=$(cat "$PIDFILE" 2>/dev/null || true)
case "$PID" in
  ''|*[!0-9]*)
  log '[INFO] PID 文件无效，未停止任何进程'
  exit 0
  ;;
esac

# Verify the exact executable before sending any signal. Never use a broad
# process-name match: the device may have unrelated processes with similar
# command lines.
EXE=''
if command -v readlink >/dev/null 2>&1; then
  EXE=$(readlink "/proc/$PID/exe" 2>/dev/null || true)
fi
if [ "$EXE" != "$BINARY" ]; then
  CMDLINE=$(tr '\000' ' ' <"/proc/$PID/cmdline" 2>/dev/null || true)
  case "$CMDLINE" in
    "$BINARY"|"$BINARY "*) ;;
    *)
      log '[INFO] PID 不属于 ai-web-engine，未停止任何进程'
      exit 0
      ;;
  esac
fi

kill -TERM "$PID" 2>/dev/null || true
n=0
while [ "$n" -lt 10 ]; do
  if ! kill -0 "$PID" 2>/dev/null; then
    sync
    log '[DONE] 引擎已停止，所有数据和 PID 记录保留'
    exit 0
  fi
  sleep 1
  n=$((n + 1))
done

# Re-check the same PID and executable immediately before escalation.
EXE=''
if command -v readlink >/dev/null 2>&1; then
  EXE=$(readlink "/proc/$PID/exe" 2>/dev/null || true)
fi
if [ "$EXE" = "$BINARY" ]; then
  kill -KILL "$PID" 2>/dev/null || true
fi
sync
printf '%s [INFO] stopped pid=%s\n' "$(date -u +%FT%TZ)" "$PID" >>"$LOG" 2>/dev/null || true
log '[DONE] 引擎已停止，所有数据和 PID 记录保留'
exit 0
