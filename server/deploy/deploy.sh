#!/bin/sh
# LuminaGallery 部署 / 升级脚本（在路由器上执行）
#
#   sh deploy.sh /tmp/lumina-arm64
#
# 做四件事：停服 → 换二进制 → 装 procd 脚本并启用 → 自检 + 探活。
# 任何一步失败都会保留旧二进制并打印原因。

set -e

BIN="${1:-/tmp/lumina-arm64}"
DEST=/mnt/sda1/.lumina/bin/lumina
INIT=/etc/init.d/lumina
BACKUP=/mnt/sda1/.lumina/bin/lumina.bak

if [ ! -f "$BIN" ]; then
	echo "找不到二进制：$BIN"
	exit 1
fi

echo "== 1. 停止服务"
if [ -x "$INIT" ]; then
	"$INIT" stop 2>/dev/null || true
fi
kill "$(pidof lumina)" 2>/dev/null || true
sleep 1

echo "== 2. 备份旧版本并替换"
[ -f "$DEST" ] && cp "$DEST" "$BACKUP"
mkdir -p "$(dirname "$DEST")"
cp "$BIN" "$DEST"
chmod +x "$DEST"

echo "== 3. 安装 procd 脚本"
if [ -f /tmp/lumina.init ]; then
	cp /tmp/lumina.init "$INIT"
	chmod +x "$INIT"
	"$INIT" enable 2>/dev/null || ln -sf "$INIT" /etc/rc.d/S95lumina
	echo "   /etc/init.d/lumina 已安装并设为开机启动"
else
	echo "   跳过：未找到 /tmp/lumina.init"
fi

echo "== 4. 自检"
if ! "$DEST" -root /mnt/sda1 -doctor; then
	echo "!! 自检未通过，回滚"
	[ -f "$BACKUP" ] && cp "$BACKUP" "$DEST" && "$INIT" start
	exit 1
fi

echo "== 5. 启动"
if [ -x "$INIT" ]; then
	"$INIT" start
else
	cd /mnt/sda1/.lumina && nohup "$DEST" -root /mnt/sda1 -addr :8088 >/tmp/lumina.log 2>&1 &
fi
sleep 3

echo "== 6. 探活"
if curl -s --max-time 5 http://127.0.0.1:8088/api/health | grep -q '"ok":true'; then
	echo "   OK: $(curl -s http://127.0.0.1:8088/api/health)"
else
	echo "!! 启动后未响应，回滚"
	[ -f "$BACKUP" ] && cp "$BACKUP" "$DEST" && "$INIT" restart
	exit 1
fi

echo "完成。"
