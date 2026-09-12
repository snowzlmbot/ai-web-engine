#!/system/bin/sh
set -u
BASE=/data/local/ai-instruction
if ! pgrep -f 'ai-web-engine' >/dev/null 2>&1; then
  echo '[INFO] 未运行'
  exit 0
fi
pkill -TERM -f 'ai-web-engine' 2>/dev/null || true
sleep 1
if pgrep -f 'ai-web-engine' >/dev/null 2>&1; then
  pkill -9 -f 'ai-web-engine' 2>/dev/null || true
fi
rm -f "$BASE/bin/ai-web-engine.pid"
sync
echo '[DONE] 引擎已停止，数据保留'
