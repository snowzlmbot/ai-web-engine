#!/system/bin/sh
# Start ai-web-engine on a browser-safe loopback port and open the Android default browser.
set -u

BASE=${AI_WEB_ENGINE_BASE:-/data/local/ai-instruction}
BINARY="$BASE/bin/ai-web-engine"
CONFIG="$BASE/config/model_config.json"
PIDFILE="$BINARY.pid"
PORT=6688
URL="http://127.0.0.1:$PORT"

owned_pid() {
  candidate=$1
  case "$candidate" in
    ''|*[!0-9]*) return 1 ;;
  esac
  kill -0 "$candidate" 2>/dev/null || return 1
  EXE=$(readlink "/proc/$candidate/exe" 2>/dev/null || true)
  if [ "$EXE" = "$BINARY" ]; then
    return 0
  fi
  CMDLINE=$(tr '\000' ' ' <"/proc/$candidate/cmdline" 2>/dev/null || true)
  case "$CMDLINE" in
    "$BINARY"|"$BINARY "*) return 0 ;;
    *) return 1 ;;
  esac
}

engine_uses_port() {
  candidate=$1
  CMDLINE=$(tr '\000' ' ' <"/proc/$candidate/cmdline" 2>/dev/null || true)
  case " $CMDLINE " in
    *" --port $PORT "*) return 0 ;;
    *) return 1 ;;
  esac
}

stop_owned_pid() {
  candidate=$1
  owned_pid "$candidate" || return 0
  kill -TERM "$candidate" 2>/dev/null || true
  n=0
  while [ "$n" -lt 10 ]; do
    kill -0 "$candidate" 2>/dev/null || return 0
    sleep 1
    n=$((n + 1))
  done
  # Re-verify the same PID and executable before escalation.
  if owned_pid "$candidate"; then
    kill -KILL "$candidate" 2>/dev/null || true
  fi
  kill -0 "$candidate" 2>/dev/null && return 1
  return 0
}

# If the exact engine PID is already serving the new port, only reopen the UI.
# If it is an older instance (for example the former 6666 listener), migrate it
# safely before starting the browser-safe port.
if [ -s "$PIDFILE" ]; then
  PID=$(cat "$PIDFILE" 2>/dev/null || true)
  if owned_pid "$PID" && engine_uses_port "$PID"; then
    STATUS=$(curl -s -o /dev/null -w '%{http_code}' "$URL/health" 2>/dev/null || printf '000')
    if [ "$STATUS" = 200 ]; then
      echo "[INFO] 已在运行 health=$STATUS url=$URL"
      am start -a android.intent.action.VIEW -d "$URL" >/dev/null 2>&1 || true
      exit 0
    fi
    echo '[INFO] 已检测到本引擎但健康检查失败，正在安全重启'
    stop_owned_pid "$PID" || { echo '[ERROR] 旧引擎停止失败，未启动新端口'; exit 1; }
  elif owned_pid "$PID"; then
    echo '[INFO] 检测到旧端口引擎，正在安全迁移'
    stop_owned_pid "$PID" || { echo '[ERROR] 旧引擎停止失败，未启动新端口'; exit 1; }
  fi
fi

[ -x "$BINARY" ] || { echo '[ERROR] 引擎不存在或不可执行，请先运行初始化'; exit 1; }
[ -f "$CONFIG" ] || { echo '[ERROR] 配置不存在，请先运行初始化'; exit 1; }
[ -s "$BASE/config/master.key" ] || { echo '[ERROR] master.key 不存在，请先运行初始化'; exit 1; }
[ -f "$BASE/skills/shortx-rule-creator/SKILL.md" ] || { echo '[ERROR] skills 不存在，请先运行初始化'; exit 1; }
[ "$PORT" != 6666 ] || { echo '[ERROR] 禁止使用浏览器危险端口'; exit 1; }

cd "$BASE" || exit 1
nohup "$BINARY" --host 127.0.0.1 --port "$PORT" \
  --skills-dir "$BASE/skills" --config "$CONFIG" \
  --sessions-dir "$BASE/sessions" --log-dir "$BASE/logs" \
  >>"$BASE/logs/engine.log" 2>&1 &
PID=$!
printf '%s\n' "$PID" > "$PIDFILE"
chmod 600 "$PIDFILE" 2>/dev/null || true

STATUS=000
n=0
while [ "$n" -lt 10 ]; do
  STATUS=$(curl -s -o /dev/null -w '%{http_code}' "$URL/health" 2>/dev/null || printf '000')
  [ "$STATUS" = 200 ] && break
  sleep 1
  n=$((n + 1))
done
echo "[INFO] health=$STATUS url=$URL"
if [ "$STATUS" != 200 ]; then
  owned_pid "$PID" && kill -TERM "$PID" 2>/dev/null || true
  exit 1
fi

# Android default browser; do not use a fixed browser package.
am start -a android.intent.action.VIEW -d "$URL" >/dev/null 2>&1 || echo "[WARN] 默认浏览器打开失败，请手动访问 $URL"
exit 0
