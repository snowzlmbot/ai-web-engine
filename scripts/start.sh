#!/system/bin/sh
# Start one ai-web-engine instance without touching unrelated processes.
set -u

BASE=/data/local/ai-instruction
BINARY="$BASE/bin/ai-web-engine"
CONFIG="$BASE/config/model_config.json"
PIDFILE="$BINARY.pid"

if [ -s "$PIDFILE" ]; then
  PID=$(cat "$PIDFILE" 2>/dev/null || true)
  case "$PID" in
    ''|*[!0-9]*) rm -f "$PIDFILE" ;;
    *)
      EXE=''
      if command -v readlink >/dev/null 2>&1; then
        EXE=$(readlink "/proc/$PID/exe" 2>/dev/null || true)
      fi
      if [ "$EXE" = "$BINARY" ] && kill -0 "$PID" 2>/dev/null; then
        echo '[INFO] 已在运行'
        exit 0
      fi
      rm -f "$PIDFILE"
      ;;
  esac
fi

[ -x "$BINARY" ] || { echo '[ERROR] 引擎不存在或不可执行'; exit 1; }
[ -f "$CONFIG" ] || { echo '[ERROR] 配置不存在'; exit 1; }
[ -f "$BASE/skills/shortx-rule-creator/SKILL.md" ] || { echo '[ERROR] skills 不存在'; exit 1; }

# ShortX expands %模型key% in the wrapper and exports it as an inherited
# process environment variable. This script never prints or persists it.
cd "$BASE" || exit 1
nohup "$BINARY" \
  --host 127.0.0.1 \
  --port 6666 \
  --skills-dir "$BASE/skills" \
  --config "$CONFIG" \
  --sessions-dir "$BASE/sessions" \
  --log-dir "$BASE/logs" \
  >>"$BASE/logs/engine.log" 2>&1 &
PID=$!
printf '%s\n' "$PID" > "$PIDFILE"
chmod 600 "$PIDFILE" 2>/dev/null || true
sleep 2
STATUS=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:6666/health 2>/dev/null || printf '000')
echo "[INFO] health=$STATUS url=http://127.0.0.1:6666"
if [ "$STATUS" != 200 ]; then
  # Only clean up the PID we just started, after verifying its executable.
  EXE=''
  if command -v readlink >/dev/null 2>&1; then
    EXE=$(readlink "/proc/$PID/exe" 2>/dev/null || true)
  fi
  [ "$EXE" = "$BINARY" ] && kill -TERM "$PID" 2>/dev/null || true
  rm -f "$PIDFILE"
  exit 1
fi
exit 0
