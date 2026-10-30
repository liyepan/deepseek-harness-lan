#!/usr/bin/env bash
# enable.sh - 启用 dsh-lan 开机自启，并立即启动服务。
set -euo pipefail

SERVICE_NAME="dsh-lan"
DESTDIR="${DESTDIR:-}"

info() { printf '==> %s\n' "$*"; }
die()  { printf '错误: %s\n' "$*" >&2; exit 1; }

if [[ -z "${DESTDIR}" ]]; then
	[[ "$(id -u)" -eq 0 ]] || die "需要 root 权限，请使用：sudo ./enable.sh"
fi

if [[ -z "${DESTDIR}" && ! -f "/etc/systemd/system/${SERVICE_NAME}.service" ]]; then
	die "还没安装 ${SERVICE_NAME}，先执行：sudo ./install.sh"
fi

if [[ -n "${DESTDIR}" ]]; then
	info "DESTDIR 已设置，跳过 systemctl"
	exit 0
fi

info "启用并启动 ${SERVICE_NAME}"
systemctl enable --now "${SERVICE_NAME}"

sleep 1
systemctl --no-pager --lines=0 status "${SERVICE_NAME}" || true

echo
info "最近的启动输出"
journalctl -u "${SERVICE_NAME}" -n 40 --no-pager -o cat 2>/dev/null | sed -n "/局域网地址/,/安全提示/p" || true
