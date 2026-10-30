#!/bin/sh
# install.sh - 把 dsh-lan 放到 /usr/bin，并生成 systemd 单元。
#
# 只做两件事：拷二进制、写 unit。不启动服务，不动 dsh 的任何配置。
# 纯 POSIX sh（sh / bash / dash 都行）。
#
# 用法：
#   sudo ./install.sh                监听 0.0.0.0:8080
#   sudo ./install.sh 9000           监听 0.0.0.0:9000
#   sudo ./install.sh 0.0.0.0:9000   直接给完整监听地址
#   sudo ./install.sh --port 9000    同上
#
# 可选环境变量：
#   DSH_LAN_USER     服务运行用户，默认 sudo 的调用者；装成 root 服务就传 root
#   DSH_LAN_LISTEN   监听地址（不传位置参数时生效），默认 0.0.0.0:8080
#   DSH_LAN_WORKDIR  服务工作目录，默认 ~（由 systemd 解析成服务用户的家目录）
#   DESTDIR          装到别的前缀（测试用，跳过 root 检查和 systemctl）
set -eu

SERVICE_NAME="dsh-lan"
UNIT_DIR="${DESTDIR:-}/etc/systemd/system"
UNIT_FILE="$UNIT_DIR/$SERVICE_NAME.service"
BIN_PATH="${DESTDIR:-}/usr/bin/$SERVICE_NAME"
WORKDIR="${DSH_LAN_WORKDIR:-~}"
SERVICE_USER="${DSH_LAN_USER:-${SUDO_USER:-}}"

info() { printf '==> %s\n' "$*"; }
die()  { printf '错误: %s\n' "$*" >&2; exit 1; }

# ── 监听地址：位置参数 > 环境变量 > 默认 ────────────────────────────────────
PORT_ARG="${1:-}"
case "$PORT_ARG" in
	--port|-p)
		PORT_ARG="${2:-}"
		EXTRA="${3:-}"
		;;
	"")
		EXTRA="${2:-}"
		;;
	*)
		EXTRA="${2:-}"
		;;
esac
[ -z "$EXTRA" ] || die "多余参数：$EXTRA（用法：sudo ./install.sh [端口]）"

if [ -n "$PORT_ARG" ]; then
	case "$PORT_ARG" in
		*:*) LISTEN="$PORT_ARG" ;;
		*)   LISTEN="0.0.0.0:$PORT_ARG" ;;
	esac
else
	LISTEN="${DSH_LAN_LISTEN:-0.0.0.0:8080}"
fi

LISTEN_PORT="${LISTEN##*:}"
case "$LISTEN_PORT" in
	''|*[!0-9]*) die "端口不合法：$LISTEN_PORT（要 1-65535，例如 9000 或 0.0.0.0:9000）" ;;
esac
if [ "$LISTEN_PORT" -lt 1 ] || [ "$LISTEN_PORT" -gt 65535 ]; then
	die "端口超出范围：$LISTEN_PORT（要 1-65535）"
fi

if [ -z "${DESTDIR:-}" ]; then
	[ "$(id -u)" -eq 0 ] || die "需要 root 权限，请使用：sudo ./install.sh [端口]"
	command -v systemctl >/dev/null 2>&1 || die "本机没有 systemctl"
fi

# ── 二进制 ──────────────────────────────────────────────────────────────────
REPO_DIR="$(cd "$(dirname "$0")" && pwd)"
SRC="$REPO_DIR/$SERVICE_NAME"
if [ ! -x "$SRC" ]; then
	command -v go >/dev/null 2>&1 || die "仓库里没有 $SERVICE_NAME，也没有 Go 工具链；先 make build"
	(cd "$REPO_DIR" && go build -trimpath -ldflags "-s -w" -o "$SERVICE_NAME" ./cmd/dsh-lan)
fi
install -Dm0755 "$SRC" "$BIN_PATH"
info "已安装 $BIN_PATH"

# ── unit ────────────────────────────────────────────────────────────────────
install -d -m0755 "$UNIT_DIR"
cat > "$UNIT_FILE" <<UNIT_EOF
[Unit]
Description=DeepSeek Harness LAN gateway (dsh-lan)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=$WORKDIR
StateDirectory=$SERVICE_NAME
StateDirectoryMode=0700
ExecStart=/usr/bin/$SERVICE_NAME --listen $LISTEN --state /var/lib/$SERVICE_NAME/state.json
Restart=on-failure
RestartSec=3
KillMode=mixed
TimeoutStopSec=20

[Install]
WantedBy=multi-user.target
UNIT_EOF

if [ -n "$SERVICE_USER" ]; then
	sed -i "/^\[Service\]/a User=$SERVICE_USER" "$UNIT_FILE"
fi

info "已生成 $UNIT_FILE"
if [ -z "${DESTDIR:-}" ]; then
	systemctl daemon-reload
fi

cat <<NEXT_EOF

安装完成。

  监听       $LISTEN
  运行用户   ${SERVICE_USER:-root}
  工作目录   $WORKDIR

下一步：

  sudo ./enable.sh

NEXT_EOF

if [ -z "${DESTDIR:-}" ] && systemctl is-active --quiet "$SERVICE_NAME" 2>/dev/null; then
	cat <<RESTART_EOF
服务正在运行。让新端口立刻生效：

  sudo systemctl restart $SERVICE_NAME

RESTART_EOF
fi
