#!/bin/bash
set -e

SERVER="root@115.29.194.105"
KEY="$HOME/poker.pem"
SSH_OPTS="-i $KEY -o StrictHostKeyChecking=no -o ConnectTimeout=30 -o ControlMaster=auto -o ControlPath=/tmp/poker-deploy-%r@%h -o ControlPersist=120"
SSH="ssh $SSH_OPTS"
RSYNC="rsync -az --exclude='*.db' --exclude='poker_server*' --exclude='.git' -e \"ssh $SSH_OPTS\""
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

echo "=== 部署源码到阿里云（服务器端编译）==="

# 预建 ControlMaster
$SSH -N -f "$SERVER" 2>/dev/null || true

# 1. 同步源码 + 前端（排除二进制和数据库）
echo ""
echo ">>> [1/3] 上传源码..."
$SSH "$SERVER" "mkdir -p /opt/poker/backend /opt/poker/frontend /opt/poker/data"
eval $RSYNC "$SCRIPT_DIR/backend/" "$SERVER:/opt/poker/backend/" &
eval $RSYNC "$SCRIPT_DIR/frontend/" "$SERVER:/opt/poker/frontend/" &
eval $RSYNC "$SCRIPT_DIR/deploy/nginx-poker.conf" "$SERVER:/etc/nginx/conf.d/poker.conf" &
eval $RSYNC "$SCRIPT_DIR/deploy/poker.service" "$SERVER:/etc/systemd/system/poker.service" &
wait
echo "  上传完成"

# 2. 服务器端编译 + 重启
echo ""
echo ">>> [2/3] 服务器编译..."
$SSH "$SERVER" bash << 'REMOTE'
set -e
cd /opt/poker/backend

# 安装 Go（如果没有）
if ! command -v go &>/dev/null; then
  echo "  安装 Go..."
  curl -fsSL https://go.dev/dl/go1.23.4.linux-amd64.tar.gz | tar -C /usr/local -xz
  echo 'export PATH=$PATH:/usr/local/go/bin' >> /etc/profile
  export PATH=$PATH:/usr/local/go/bin
fi
export PATH=$PATH:/usr/local/go/bin

echo "  Go 版本: $(go version)"
GOPROXY=https://goproxy.cn,direct GONOSUMCHECK=* GOTOOLCHAIN=local go build -ldflags="-s -w" -o poker_server .
echo "  编译完成: $(du -sh poker_server | cut -f1)"
REMOTE

# 3. 重启服务
echo ""
echo ">>> [3/3] 重启服务..."
$SSH "$SERVER" bash << 'REMOTE'
rm -f /etc/nginx/conf.d/default.conf
systemctl daemon-reload
systemctl enable nginx poker
systemctl restart nginx
systemctl restart poker
sleep 2
systemctl is-active nginx && echo "nginx: ok" || echo "nginx: FAILED"
systemctl is-active poker && echo "poker: ok" || echo "poker: FAILED"
REMOTE

echo ""
echo "=== 完成！访问 http://115.29.194.105 ==="
