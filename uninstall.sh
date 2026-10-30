#!/usr/bin/env bash
# uninstall.sh - 彻底移除 dsh-lan：停服务、删单元、删二进制。
#
#   sudo ./uninstall.sh            保留 /var/lib/dsh-lan 里的令牌缓存
#   sudo ./uninstall.sh --purge    连缓存一起删
set -euo pipefail

SERVICE_NAME="dsh-lan"
UNIT_NAME="${SERVICE_NAME}.service"
DESTDIR="${DESTDIR:-}"
UNIT_FILE="${DESTDIR}/etc/systemd/system/${UNIT_NAME}"
BIN_PATH="${DESTDIR}/usr/bin/${SERVICE_NAME}"
STATE_DIR="${DESTDIR}/var/lib/${SERVICE_NAME}"
PURGE=0

for arg in "$@"; do
	case "${arg}" in
		--purge) PURGE=1 ;;
		*) printf "用法: sudo ./uninstall.sh [--purge]\n" >&2; exit 2 ;;
	esac
done

info() { printf '==> %s\n' "$*"; }
die()  { printf '错误: %s\n' "$*" >&2; exit 1; }

if [[ -z "${DESTDIR}" ]]; then
	[[ "$(id -u)" -eq 0 ]] || die "需要 root 权限，请使用：sudo ./uninstall.sh"
fi

if [[ -z "${DESTDIR}" ]] && command -v systemctl >/dev/null 2>&1; then
	if systemctl list-unit-files "${UNIT_NAME}" >/dev/null 2>&1; then
		info "停止并禁用 ${SERVICE_NAME}"
		systemctl disable --now "${SERVICE_NAME}" >/dev/null 2>&1 || true
	fi
fi

if [[ -f "${UNIT_FILE}" ]]; then
	rm -f "${UNIT_FILE}"
	info "已删除 ${UNIT_FILE}"
fi

if [[ -z "${DESTDIR}" ]] && command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload
	systemctl reset-failed "${SERVICE_NAME}" >/dev/null 2>&1 || true
fi

if [[ -e "${BIN_PATH}" ]]; then
	rm -f "${BIN_PATH}"
	info "已删除 ${BIN_PATH}"
fi

if [[ "${PURGE}" -eq 1 && -d "${STATE_DIR}" ]]; then
	rm -rf "${STATE_DIR}"
	info "已删除 ${STATE_DIR}"
elif [[ -d "${STATE_DIR}" ]]; then
	info "保留 ${STATE_DIR}（含令牌缓存）；加 --purge 可一并删除"
fi

echo
info "卸载完成。dsh 本身未受影响。"
