#!/bin/bash
set -e

SERVER="root@115.29.194.105"
KEY="$HOME/poker.pem"
# ControlMaster: 复用同一条 SSH 连接，避免每个 rsync 都握手
SSH_OPTS="-i $KEY -o StrictHostKeyChecking=no -o ConnectTimeout=30 -o ControlMaster=auto -o ControlPath=/tmp/poker-deploy-%r@%h -o ControlPersist=120"
SSH="ssh $SSH_OPTS"
RSYNC="rsync -az -e \"ssh $SSH_OPTS\""   # 去掉 --checksum，用默认大小+时间判断
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== 部署 Texas Hold'em 到阿里云 ==="

# 预先建立 ControlMaster 连接
$SSH -N -f "$SERVER" 2>/dev/null || true

# 1. 按需编译：只有 .go 文件比 bin 新才重新编译
echo ""
BIN="$SCRIPT_DIR/backend/poker_server_linux"
NEED_BUILD=0
if [ ! -f "$BIN" ]; then
  NEED_BUILD=1
else
  for f in "$SCRIPT_DIR"/backend/**/*.go "$SCRIPT_DIR"/backend/*.go; do
    [ -f "$f" ] && [ "$f" -nt "$BIN" ] && NEED_BUILD=1 && break
  done
fi

if [ "$NEED_BUILD" = "1" ]; then
  echo ">>> [1/3] 编译 Linux 二进制..."
  cd "$SCRIPT_DIR/backend"
  GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags="-s -w" -o poker_server_linux .
  echo "  编译完成: $(du -sh poker_server_linux | cut -f1)"
  cd "$SCRIPT_DIR"
else
  echo ">>> [1/3] 二进制未变化，跳过编译"
fi

# 2. rsync 同步（并行，复用连接）
echo ""
echo ">>> [2/3] 同步文件到服务器..."
$SSH "$SERVER" "mkdir -p /opt/poker/backend /opt/poker/frontend /opt/poker/data"
eval $RSYNC "$BIN" "$SERVER:/opt/poker/backend/poker_server_linux" &
eval $RSYNC "$SCRIPT_DIR/frontend/" "$SERVER:/opt/poker/frontend/" &
eval $RSYNC --exclude='*.db' "$SCRIPT_DIR/data/" "$SERVER:/opt/poker/data/" &
eval $RSYNC "$SCRIPT_DIR/deploy/nginx-poker.conf" "$SERVER:/etc/nginx/conf.d/poker.conf" &
eval $RSYNC "$SCRIPT_DIR/deploy/poker.service" "$SERVER:/etc/systemd/system/poker.service" &
wait
echo "  同步完成"

# 3. 远程启动
echo ""
echo ">>> [3/3] 启动服务..."
$SSH "$SERVER" bash << 'REMOTE'
chmod +x /opt/poker/backend/poker_server_linux
rm -f /etc/nginx/conf.d/default.conf
systemctl daemon-reload
systemctl enable nginx poker
systemctl restart nginx
systemctl restart poker
sleep 2
echo "--- 服务状态 ---"
systemctl is-active nginx && echo "nginx: ok" || echo "nginx: FAILED"
systemctl is-active poker && echo "poker: ok" || echo "poker: FAILED"
ss -tlnp | grep -E ':80|:8080' || echo "端口未监听"
REMOTE

echo ""
echo "=== 完成！访问 http://115.29.194.105 ==="
