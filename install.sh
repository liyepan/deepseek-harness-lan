#!/usr/bin/env bash
# install.sh - 安装 dsh-lan 到 /usr/bin，并生成 systemd 单元。
#
# 只做两件事：放二进制、写单元文件。不会启动服务，也不会碰 dsh 的任何配置。
# 启用/停用分别用 enable.sh / disable.sh，卸载用 uninstall.sh。
#
# 环境变量（都可选）：
#   DSH_LAN_USER     服务运行用户，默认取 sudo 的调用者
#   DSH_LAN_LISTEN   监听地址，默认 0.0.0.0:8080
#   DSH_LAN_WORKDIR  服务工作目录，默认目标用户的家目录
#   DESTDIR          安装到别的前缀（测试用，跳过 root 检查和 systemctl）
set -euo pipefail

SERVICE_NAME="dsh-lan"
UNIT_NAME="${SERVICE_NAME}.service"
DESTDIR="${DESTDIR:-}"
UNIT_DIR="${DESTDIR}/etc/systemd/system"
UNIT_FILE="${UNIT_DIR}/${UNIT_NAME}"
BIN_PATH="${DESTDIR}/usr/bin/${SERVICE_NAME}"
# The unit always points at the real install path; DESTDIR only stages files.
EXEC_PATH="/usr/bin/${SERVICE_NAME}"
STATE_DIR="${DESTDIR}/var/lib/${SERVICE_NAME}"
LISTEN="${DSH_LAN_LISTEN:-0.0.0.0:8080}"

info() { printf '==> %s\n' "$*"; }
warn() { printf '警告: %s\n' "$*" >&2; }
die()  { printf '错误: %s\n' "$*" >&2; exit 1; }

if [[ -z "${DESTDIR}" ]]; then
	[[ "$(id -u)" -eq 0 ]] || die "需要 root 权限，请使用：sudo ./install.sh"
	command -v systemctl >/dev/null 2>&1 || die "本机没有 systemctl，这套脚本依赖 systemd"
fi

# ── 目标用户 ────────────────────────────────────────────────────────────────
TARGET_USER="${DSH_LAN_USER:-${SUDO_USER:-}}"
if [[ -z "${TARGET_USER}" ]]; then
	if [[ -n "${DESTDIR}" ]]; then
		TARGET_USER="$(id -un)"
	else
		die "无法确定服务以哪个用户运行，请显式指定：DSH_LAN_USER=<用户名> sudo ./install.sh"
	fi
fi
id -u "${TARGET_USER}" >/dev/null 2>&1 || die "用户 ${TARGET_USER} 不存在"
TARGET_GROUP="$(id -gn "${TARGET_USER}")"
TARGET_HOME="$(getent passwd "${TARGET_USER}" | cut -d: -f6)"
[[ -n "${TARGET_HOME}" && -d "${TARGET_HOME}" ]] || die "无法确定 ${TARGET_USER} 的家目录"
WORKDIR="${DSH_LAN_WORKDIR:-${TARGET_HOME}}"

# dsh 的位置不写进任何配置：服务启动时由 dsh-lan 用目标用户的登录 shell
# 解析（和你在终端里敲命令得到的结果一致）。这里只做一次提醒。
if ! runuser -u "${TARGET_USER}" -- bash -lc "command -v dsh" >/dev/null 2>&1; then
	warn "在 ${TARGET_USER} 的登录环境里没找到 dsh；装好之后重启服务即可"
fi

# ── 二进制 ──────────────────────────────────────────────────────────────────
REPO_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
SRC="${REPO_DIR}/${SERVICE_NAME}"
if [[ ! -x "${SRC}" ]]; then
	command -v go >/dev/null 2>&1 || die "仓库里还没有 ${SERVICE_NAME}，也没有 Go 工具链；先执行 make build"
	info "编译 ${SERVICE_NAME}"
	(cd "${REPO_DIR}" && go build -trimpath -ldflags "-s -w" -o "${SERVICE_NAME}" ./cmd/dsh-lan)
fi
install -Dm0755 "${SRC}" "${BIN_PATH}"
info "已安装 ${BIN_PATH}"

# ── 状态目录 ────────────────────────────────────────────────────────────────
install -d -m0700 "${STATE_DIR}"
if [[ -z "${DESTDIR}" ]]; then
	chown "${TARGET_USER}:${TARGET_GROUP}" "${STATE_DIR}"
fi

# ── systemd 单元 ────────────────────────────────────────────────────────────
install -d -m0755 "${UNIT_DIR}"
cat > "${UNIT_FILE}" <<UNIT_EOF
[Unit]
Description=DeepSeek Harness LAN gateway (dsh-lan)
Documentation=https://git.linuxacme.com/liyepan/deepseek-harness-lan
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${TARGET_USER}
Group=${TARGET_GROUP}
WorkingDirectory=${WORKDIR}
Environment=HOME=${TARGET_HOME}
StateDirectory=${SERVICE_NAME}
StateDirectoryMode=0700
ExecStart=${EXEC_PATH} --listen ${LISTEN} --state /var/lib/${SERVICE_NAME}/state.json
Restart=on-failure
RestartSec=3
KillMode=mixed
TimeoutStopSec=20

[Install]
WantedBy=multi-user.target
UNIT_EOF
info "已生成 ${UNIT_FILE}"

if [[ -z "${DESTDIR}" ]]; then
	systemctl daemon-reload
fi

cat <<NEXT_EOF

安装完成。

  运行用户   ${TARGET_USER}
  监听       ${LISTEN}
  工作目录   ${WORKDIR}

下一步：

  sudo ./enable.sh      启用并立即启动
  systemctl status dsh-lan --no-pager
  journalctl -u dsh-lan -f

服务启动后，日志里会打印局域网访问地址；手机连同一个 Wi-Fi 直接打开即可。

NEXT_EOF
