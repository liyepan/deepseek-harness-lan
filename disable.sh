#!/usr/bin/env bash
# disable.sh - 停止 dsh-lan 并取消开机自启（不删除任何文件）。
set -euo pipefail

SERVICE_NAME="dsh-lan"
DESTDIR="${DESTDIR:-}"

info() { printf '==> %s\n' "$*"; }
die()  { printf '错误: %s\n' "$*" >&2; exit 1; }

if [[ -z "${DESTDIR}" ]]; then
	[[ "$(id -u)" -eq 0 ]] || die "需要 root 权限，请使用：sudo ./disable.sh"
fi

if [[ -n "${DESTDIR}" ]]; then
	info "DESTDIR 已设置，跳过 systemctl"
	exit 0
fi

if ! systemctl list-unit-files "${SERVICE_NAME}.service" >/dev/null 2>&1; then
	info "${SERVICE_NAME} 没有安装，跳过"
	exit 0
fi

info "停止并禁用 ${SERVICE_NAME}"
systemctl disable --now "${SERVICE_NAME}" || true
systemctl --no-pager --lines=0 status "${SERVICE_NAME}" || true

echo
info "已停止。dsh 进程由本服务托管，会一并退出。"
