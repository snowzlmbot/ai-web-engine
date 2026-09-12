#!/system/bin/sh
set -u
BASE=/data/local/ai-instruction
BINARY="$BASE/bin/ai-web-engine"
CONFIG="$BASE/config/model_config.json"

if pgrep -f 'ai-web-engine' >/dev/null 2>&1; then
  echo '[INFO] 已在运行'
  exit 0
fi
[ -x "$BINARY" ] || { echo '[ERROR] 引擎不存在或不可执行'; exit 1; }
[ -f "$CONFIG" ] || { echo '[ERROR] 配置不存在'; exit 1; }
[ -f "$BASE/skills/shortx-rule-creator/SKILL.md" ] || { echo '[ERROR] skills 不存在'; exit 1; }

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
printf '%s\n' "$PID" > "$BASE/bin/ai-web-engine.pid"
sleep 2
STATUS=$(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:6666/health 2>/dev/null || printf '000')
echo "[INFO] health=$STATUS url=http://127.0.0.1:6666"
[ "$STATUS" = 200 ]
