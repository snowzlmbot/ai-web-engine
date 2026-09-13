#!/system/bin/sh
# Start ai-web-engine on a browser-safe loopback port and open the Android default browser.
set -u

BASE=${AI_WEB_ENGINE_BASE:-/data/local/ai-instruction}
RAW_BASE_URL=${AI_WEB_ENGINE_RAW_BASE_URL:-https://raw.githubusercontent.com/snowzlmbot/ai-web-engine/main}
BINARY="$BASE/bin/ai-web-engine"
CONFIG="$BASE/config/model_config.json"
PIDFILE="$BINARY.pid"
VERSION_FILE="$BASE/config/version.json"
PORT=6688
URL="http://127.0.0.1:$PORT"
FORCE_UPDATE=0

remote_version() {
  curl -fsSL --retry 2 --connect-timeout 15 "$RAW_BASE_URL/version.json?start_version=$(date +%s)-$$" 2>/dev/null \
    | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([0-9][0-9A-Za-z._-]*\)".*/\1/p'
}

local_version() {
  sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$VERSION_FILE" 2>/dev/null || true
}

running_version() {
  curl -fsSL --connect-timeout 5 "$URL/health" 2>/dev/null \
    | sed -n 's/.*"version"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p'
}

update_init_script() {
  INIT_NEW="$BASE/scripts/init.sh.new"
  INIT_URL="$RAW_BASE_URL/scripts/init.sh?start_init=$(date +%s)-$$"
  rm -f "$INIT_NEW"
  if ! curl -fsSL --retry 2 --connect-timeout 15 "$INIT_URL" -o "$INIT_NEW"; then
    rm -f "$INIT_NEW"
    echo '[ERROR] 无法下载最新 init.sh，未更新引擎'
    return 1
  fi
  if [ ! -s "$INIT_NEW" ] || ! sh -n "$INIT_NEW" || \
     ! grep -q 'RELEASE_BASE_URL=' "$INIT_NEW" || \
     ! grep -q 'unzip -l' "$INIT_NEW" || ! grep -q 'unzip -t' "$INIT_NEW" || \
     ! grep -q 'start.sh' "$INIT_NEW" || ! grep -q 'stop.sh' "$INIT_NEW"; then
    rm -f "$INIT_NEW"
    echo '[ERROR] 最新 init.sh 校验失败，未更新引擎'
    return 1
  fi
  chmod 755 "$INIT_NEW" || { rm -f "$INIT_NEW"; return 1; }
  mv "$INIT_NEW" "$BASE/scripts/init.sh" || { rm -f "$INIT_NEW"; return 1; }
  return 0
}

ensure_latest() {
  [ -n "$REMOTE_VERSION" ] || return 0
  LOCAL_VERSION=$(local_version)
  [ "$FORCE_UPDATE" = 1 ] || [ "$LOCAL_VERSION" != "$REMOTE_VERSION" ] || return 0
  echo "[INFO] 本地引擎版本 ${LOCAL_VERSION:-unknown}，云端版本 $REMOTE_VERSION，正在安全更新"
  update_init_script || return 1
  sh "$BASE/scripts/init.sh" || return 1
  return 0
}

REMOTE_VERSION=$(remote_version || true)

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
      RUNNING_VERSION=$(running_version || true)
      if [ -z "$REMOTE_VERSION" ] || [ "$RUNNING_VERSION" = "$REMOTE_VERSION" ]; then
        echo "[INFO] 已在运行 version=${RUNNING_VERSION:-unknown} health=$STATUS url=$URL"
        am start -a android.intent.action.VIEW -d "$URL" >/dev/null 2>&1 || true
        exit 0
      fi
      echo "[INFO] 检测到旧版本引擎 running=${RUNNING_VERSION:-unknown}，正在安全迁移"
      FORCE_UPDATE=1
    else
      echo '[INFO] 已检测到本引擎但健康检查失败，正在安全重启'
    fi
    stop_owned_pid "$PID" || { echo '[ERROR] 旧引擎停止失败，未启动新版本'; exit 1; }
  elif owned_pid "$PID"; then
    echo '[INFO] 检测到旧端口引擎，正在安全迁移'
    FORCE_UPDATE=1
    stop_owned_pid "$PID" || { echo '[ERROR] 旧引擎停止失败，未启动新端口'; exit 1; }
  fi
fi

ensure_latest || { echo '[ERROR] 引擎自动更新失败，未启动新版本'; exit 1; }

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
