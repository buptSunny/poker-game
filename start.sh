#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
BACKEND_DIR="$SCRIPT_DIR/backend"
LOG_CF=/tmp/poker_cf_tunnel.log
PORT=8080

echo "=== Texas Hold'em Online 启动 ==="

# 1. 杀掉旧进程
echo "[1/3] 清理旧进程..."
lsof -ti:$PORT | xargs kill -9 2>/dev/null || true
pkill -f poker_server 2>/dev/null || true
pkill cloudflared 2>/dev/null || true
sleep 1

# 2. 启动 Go 服务器
echo "[2/3] 启动游戏服务器 (port $PORT)..."
cd "$BACKEND_DIR"
if [ ! -f poker_server ]; then
  echo "  编译中..."
  go build -o poker_server .
fi
./poker_server &
SERVER_PID=$!
sleep 1

# 验证服务器启动
if ! curl -s "http://localhost:$PORT/rooms" > /dev/null; then
  echo "❌ 服务器启动失败"
  exit 1
fi
echo "  ✓ 服务器已启动 (PID=$SERVER_PID)"

# 3. 启动 Cloudflare Tunnel
echo "[3/3] 创建公网隧道..."
cloudflared tunnel --url "http://localhost:$PORT" > "$LOG_CF" 2>&1 &
CF_PID=$!

# 等待公网 URL
PUBLIC_URL=""
for i in $(seq 1 30); do
  PUBLIC_URL=$(grep -o 'https://[a-z0-9-]*\.trycloudflare\.com' "$LOG_CF" 2>/dev/null | head -1)
  [ -n "$PUBLIC_URL" ] && break
  sleep 1
done

if [ -z "$PUBLIC_URL" ]; then
  echo "⚠️  获取公网URL超时，请检查网络"
  echo "日志: $LOG_CF"
  # 仍然显示局域网地址
  LOCAL_IP=$(ipconfig getifaddr en0 2>/dev/null || ipconfig getifaddr en1 2>/dev/null || echo "unknown")
  echo ""
  echo "局域网地址: http://$LOCAL_IP:$PORT"
else
  echo ""
  echo "=================================================="
  echo "  ✅ 游戏已上线！把下面的链接发给朋友："
  echo ""
  echo "  🌐  $PUBLIC_URL"
  echo ""
  echo "  局域网备用: http://$(ipconfig getifaddr en0 2>/dev/null || echo localhost):$PORT"
  echo "=================================================="
fi

echo ""
echo "按 Ctrl+C 关闭服务器"

# 捕获退出信号，清理进程
cleanup() {
  echo ""
  echo "正在关闭..."
  kill $SERVER_PID $CF_PID 2>/dev/null || true
  exit 0
}
trap cleanup INT TERM

# 保持前台运行
wait $SERVER_PID
