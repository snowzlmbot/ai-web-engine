#!/system/bin/sh
# Start ai-web-engine and open the Android default browser only after health passes.
set -u

BASE=/data/local/ai-instruction
BINARY="$BASE/bin/ai-web-engine"
CONFIG="$BASE/config/model_config.json"
PIDFILE="$BINARY.pid"

if [ -s "$PIDFILE" ]; then
  PID=$(cat "$PIDFILE" 2>/dev/null || true)
  case "$PID" in
    ''|*[!0-9]*) ;;
    *)
      EXE=$(readlink "/proc/$PID/exe" 2>/dev/null || true)
      if [ "$EXE" = "$BINARY" ] && kill -0 "$PID" 2>/dev/null; then
        echo '[INFO] 已在运行'
        am start -a android.intent.action.VIEW -d http://127.0.0.1:6666 >/dev/null 2>&1 || true
        exit 0
      fi
      ;;
  esac
fi

[ -x "$BINARY" ] || { echo '[ERROR] 引擎不存在或不可执行，请先运行初始化'; exit 1; }
[ -f "$CONFIG" ] || { echo '[ERROR] 配置不存在，请先运行初始化'; exit 1; }
[ -s "$BASE/config/master.key" ] || { echo '[ERROR] master.key 不存在，请先运行初始化'; exit 1; }
[ -f "$BASE/skills/shortx-rule-creator/SKILL.md" ] || { echo '[ERROR] skills 不存在，请先运行初始化'; exit 1; }

cd "$BASE" || exit 1
nohup "$BINARY" --host 127.0.0.1 --port 6666 \
  --skills-dir "$BASE/skills" --config "$CONFIG" \
  --sessions-dir "$BASE/sessions" --log-dir "$BASE/logs" \
  >>"$BASE/logs/engine.log" 2>&1 &
PID=$!
printf '%s\n' "$PID" > "$PIDFILE"
chmod 600 "$PIDFILE" 2>/dev/null || true

sleep 2
STATUS=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:6666/health 2>/dev/null || printf '000')
echo "[INFO] health=$STATUS url=http://127.0.0.1:6666"
if [ "$STATUS" != 200 ]; then
  EXE=$(readlink "/proc/$PID/exe" 2>/dev/null || true)
  [ "$EXE" = "$BINARY" ] && kill -TERM "$PID" 2>/dev/null || true
  exit 1
fi

# Android default browser; do not use a fixed browser package.
am start -a android.intent.action.VIEW -d http://127.0.0.1:6666 >/dev/null 2>&1 || echo '[WARN] 默认浏览器打开失败，请手动访问 http://127.0.0.1:6666'
exit 0
