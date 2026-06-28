#!/bin/bash

# Color definitions for terminal output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m' # No Color

# Logging functions
log_info() {
    echo -e "$1"
}

log_success() {
    echo -e "${GREEN}$1${NC}"
}

log_error() {
    echo -e "${RED}$1${NC}"
}

log_step() {
    echo -e "${YELLOW}$1${NC}"
}


# Global variables
INSTALL_DIR="/opt/komari"
DATA_DIR="/opt/komari"
SERVICE_NAME="komari"
BINARY_PATH="$INSTALL_DIR/komari"
DEFAULT_PORT="25774"
LISTEN_PORT=""
REPO="komari-monitor/komari"
# 发布通道: stable（稳定版）或 snapshot（快照版）
CHANNEL="stable"
# TUI 工具: whiptail / dialog / 空（回退纯文本）
TUI_TOOL=""

# ==========================================================
# TUI / 交互层
# ==========================================================

# 检测可用的 TUI 工具
detect_tui() {
    if command -v whiptail >/dev/null 2>&1; then
        TUI_TOOL="whiptail"
    elif command -v dialog >/dev/null 2>&1; then
        TUI_TOOL="dialog"
    else
        TUI_TOOL=""
    fi
}

# 是否启用 TUI
tui_enabled() {
    [ -n "$TUI_TOOL" ]
}

# 菜单选择
# 用法: ui_menu "标题" "提示" tag1 "item1" tag2 "item2" ...
# 返回: 选中的 tag（输出到 stdout），取消返回非零
ui_menu() {
    local title="$1"; shift
    local prompt="$1"; shift

    if tui_enabled; then
        $TUI_TOOL --title "$title" --menu "$prompt" 20 70 10 "$@" 3>&1 1>&2 2>&3
        return $?
    fi

    # 纯文本回退
    {
        echo
        echo "=============================================================="
        echo "  $title"
        echo "=============================================================="
        echo "$prompt"
        echo
        local tag item
        local args=("$@")
        local i=0
        while [ $i -lt ${#args[@]} ]; do
            tag="${args[$i]}"
            item="${args[$((i + 1))]}"
            echo "  $tag) $item"
            i=$((i + 2))
        done
        echo
    } >&2
    local choice
    read -r -p "输入选项: " choice >&2
    echo "$choice"
}

# 输入框
# 用法: ui_input "标题" "提示" "默认值"
# 返回: 输入内容（输出到 stdout），取消返回非零
ui_input() {
    local title="$1"
    local prompt="$2"
    local default="$3"

    if tui_enabled; then
        $TUI_TOOL --title "$title" --inputbox "$prompt" 12 70 "$default" 3>&1 1>&2 2>&3
        return $?
    fi

    local input
    read -r -p "$prompt [默认: $default]: " input >&2
    if [ -z "$input" ]; then
        echo "$default"
    else
        echo "$input"
    fi
}

# 是/否确认
# 用法: ui_yesno "标题" "提示"
# 返回: 0 表示 是，1 表示 否
ui_yesno() {
    local title="$1"
    local prompt="$2"

    if tui_enabled; then
        $TUI_TOOL --title "$title" --yesno "$prompt" 12 70
        return $?
    fi

    local confirm
    read -r -p "$prompt (Y/n): " confirm >&2
    if [[ $confirm =~ ^[Nn]$ ]]; then
        return 1
    fi
    return 0
}

# 信息提示框
# 用法: ui_msgbox "标题" "内容"
ui_msgbox() {
    local title="$1"
    local content="$2"

    if tui_enabled; then
        $TUI_TOOL --title "$title" --msgbox "$content" 20 72
        return
    fi

    echo
    echo "=============================================================="
    echo "  $title"
    echo "=============================================================="
    echo -e "$content"
    echo "=============================================================="
    read -r -p "按回车键继续..." _
}

# 显示横幅（仅纯文本模式）
show_banner() {
    if tui_enabled; then
        return
    fi
    clear
    echo "=============================================================="
    echo "            Komari Monitoring System Installer"
    echo "       https://github.com/komari-monitor/komari"
    echo "=============================================================="
    echo
}

# 选择发布通道，结果写入全局变量 CHANNEL
select_channel() {
    local choice
    choice=$(ui_menu "选择发布通道" "请选择要使用的发布通道：" \
        "stable" "稳定版 (推荐)" \
        "snapshot" "快照版 (最新功能)")

    case "$choice" in
        snapshot|2)
            CHANNEL="snapshot"
            ;;
        stable|1|"")
            CHANNEL="stable"
            ;;
        *)
            CHANNEL="stable"
            ;;
    esac
    log_info "已选择通道: $CHANNEL"
}

# ==========================================================
# 基础检查
# ==========================================================

# Check if running as root
check_root() {
    if [ "$EUID" -ne 0 ]; then
        log_error "请使用 root 权限运行此脚本"
        exit 1
    fi
}

# Check for systemd
check_systemd() {
    if ! command -v systemctl >/dev/null 2>&1; then
        return 1
    else
        return 0
    fi
}

# Detect system architecture
detect_arch() {
    local arch=$(uname -m)
    case $arch in
        x86_64)
            echo "amd64"
            ;;
        aarch64)
            echo "arm64"
            ;;
        i386|i686)
            echo "386"
            ;;
        riscv64)
            echo "riscv64"
            ;;
        *)
            log_error "不支持的架构: $arch"
            exit 1
            ;;
    esac
}

# Check if Komari is already installed
is_installed() {
    if [ -f "$BINARY_PATH" ]; then
        return 0 # 0 means true in bash exit codes
    else
        return 1 # 1 means false
    fi
}

# Install dependencies
install_dependencies() {
    log_step "检查并安装依赖..."

    if ! command -v curl >/dev/null 2>&1; then
        if command -v apt >/dev/null 2>&1; then
            log_info "使用 apt 安装依赖..."
            apt update
            apt install -y curl
        elif command -v yum >/dev/null 2>&1; then
            log_info "使用 yum 安装依赖..."
            yum install -y curl
        elif command -v apk >/dev/null 2>&1; then
            log_info "使用 apk 安装依赖..."
            apk add curl
        else
            log_error "未找到支持的包管理器 (apt/yum/apk)"
            exit 1
        fi
    fi
}

# Get download URL based on channel
get_download_url() {
    local arch=$1
    local file_name="komari-linux-${arch}"

    if [ "$CHANNEL" = "snapshot" ]; then
        # 获取最新的 snapshot 预发布版本
        log_info "获取最新 snapshot 版本..." >&2
        local latest_snapshot=$(curl -s "https://api.github.com/repos/${REPO}/releases" | grep '"tag_name"' | grep 'Snapshot-' | head -1 | sed -e 's/.*"tag_name": *"//' -e 's/".*//')

        if [ -z "$latest_snapshot" ]; then
            log_error "未找到 snapshot 版本" >&2
            return 1
        fi

        log_info "最新 snapshot 版本: $latest_snapshot" >&2
        echo "https://github.com/${REPO}/releases/download/${latest_snapshot}/${file_name}"
    else
        # 稳定版：使用 latest
        echo "https://github.com/${REPO}/releases/latest/download/${file_name}"
    fi
}

# ==========================================================
# 业务操作
# ==========================================================

# Binary installation
install_binary() {
    log_step "开始二进制安装..."

    if is_installed; then
        ui_msgbox "提示" "Komari 已安装。\n如需升级，请使用主菜单中的升级选项。"
        return
    fi

    # 选择发布通道
    select_channel

    # 监听端口输入，校验范围 1-65535
    while true; do
        local input_port
        input_port=$(ui_input "监听端口" "请输入 Komari 的监听端口 (1-65535)：" "$DEFAULT_PORT")
        # 取消输入
        if [ $? -ne 0 ]; then
            log_info "安装已取消"
            return
        fi
        if [[ -z "$input_port" ]]; then
            LISTEN_PORT="$DEFAULT_PORT"
            break
        elif [[ "$input_port" =~ ^[0-9]+$ ]] && (( input_port >= 1 && input_port <= 65535 )); then
            LISTEN_PORT="$input_port"
            break
        else
            ui_msgbox "错误" "端口号无效，请输入 1-65535 之间的数字。"
        fi
    done

    install_dependencies

    local arch=$(detect_arch)
    log_info "检测到架构: $arch"

    log_step "创建安装目录: $INSTALL_DIR"
    mkdir -p "$INSTALL_DIR"

    log_step "创建数据目录: $DATA_DIR"
    mkdir -p "$DATA_DIR"

    local download_url=$(get_download_url "$arch")
    if [ $? -ne 0 ]; then
        ui_msgbox "错误" "获取下载链接失败，请检查网络连接或稍后重试。"
        return 1
    fi

    log_step "下载 Komari 二进制文件..."
    log_info "URL: $download_url"

    if ! curl -L -o "$BINARY_PATH" "$download_url"; then
        ui_msgbox "错误" "下载失败，请检查网络连接。"
        return 1
    fi

    chmod +x "$BINARY_PATH"
    log_success "Komari 二进制文件安装完成: $BINARY_PATH"

    if ! check_systemd; then
        ui_msgbox "安装完成" "警告：未检测到 systemd，已跳过服务创建。\n\n您可以手动运行 Komari：\n    $BINARY_PATH server -l 0.0.0.0:$LISTEN_PORT"
        return
    fi

    create_systemd_service "$LISTEN_PORT"

    systemctl daemon-reload
    systemctl enable ${SERVICE_NAME}.service
    systemctl start ${SERVICE_NAME}.service

    if systemctl is-active --quiet ${SERVICE_NAME}.service; then
        log_success "Komari 服务启动成功"

        log_step "正在获取初始密码..."
        sleep 5
        local password=$(journalctl -u ${SERVICE_NAME} --since "1 minute ago" | grep "admin account created." | tail -n 1 | sed -e 's/.*admin account created.//')
        if [ -z "$password" ]; then
            log_error "未能获取初始密码，请检查日志"
        fi
        show_access_info "$password" "$LISTEN_PORT"
    else
        ui_msgbox "错误" "Komari 服务启动失败。\n\n查看日志: journalctl -u ${SERVICE_NAME} -f"
        return 1
    fi
}

# Create systemd service file
create_systemd_service() {
    local port="$1"
    log_step "创建 systemd 服务..."

    local service_file="/etc/systemd/system/${SERVICE_NAME}.service"
    cat > "$service_file" << EOF
[Unit]
Description=Komari Monitor Service
After=network.target

[Service]
Type=simple
ExecStart=${BINARY_PATH} server -l 0.0.0.0:${port}
WorkingDirectory=${DATA_DIR}
Restart=always
User=root

[Install]
WantedBy=multi-user.target
EOF

    log_success "systemd 服务文件创建完成"
}

# Show access information
show_access_info() {
    local password=$1
    local port=${2:-$DEFAULT_PORT}
    local ip=$(hostname -I | awk '{print $1}')

    local content="安装完成！\n\n"
    content+="访问信息：\n"
    content+="  URL: http://${ip}:${port}\n"
    if [ -n "$password" ]; then
        content+="  初始登录信息（仅显示一次）: ${password}\n"
    fi
    content+="\n服务管理命令：\n"
    content+="  状态: systemctl status $SERVICE_NAME\n"
    content+="  启动: systemctl start $SERVICE_NAME\n"
    content+="  停止: systemctl stop $SERVICE_NAME\n"
    content+="  重启: systemctl restart $SERVICE_NAME\n"
    content+="  日志: journalctl -u $SERVICE_NAME -f"

    ui_msgbox "安装完成" "$content"
}

# Upgrade function
upgrade_komari() {
    log_step "升级 Komari..."

    if ! is_installed; then
        ui_msgbox "错误" "Komari 未安装。请先安装它。"
        return 1
    fi

    if ! check_systemd; then
        ui_msgbox "错误" "未检测到 systemd。无法管理服务。"
        return 1
    fi

    # 选择发布通道
    select_channel

    log_step "停止 Komari 服务..."
    systemctl stop ${SERVICE_NAME}.service

    log_step "备份当前二进制文件..."
    cp "$BINARY_PATH" "${BINARY_PATH}.backup.$(date +%Y%m%d_%H%M%S)"

    local arch=$(detect_arch)
    local download_url=$(get_download_url "$arch")
    if [ $? -ne 0 ]; then
        log_error "获取下载链接失败，正在从备份恢复"
        mv "${BINARY_PATH}.backup."* "$BINARY_PATH"
        systemctl start ${SERVICE_NAME}.service
        ui_msgbox "错误" "获取下载链接失败，已从备份恢复。"
        return 1
    fi

    log_step "下载最新版本..."
    if ! curl -L -o "$BINARY_PATH" "$download_url"; then
        log_error "下载失败，正在从备份恢复"
        mv "${BINARY_PATH}.backup."* "$BINARY_PATH"
        systemctl start ${SERVICE_NAME}.service
        ui_msgbox "错误" "下载失败，已从备份恢复。"
        return 1
    fi

    chmod +x "$BINARY_PATH"

    log_step "重启 Komari 服务..."
    systemctl start ${SERVICE_NAME}.service

    if systemctl is-active --quiet ${SERVICE_NAME}.service; then
        ui_msgbox "升级完成" "Komari 升级成功 (通道: $CHANNEL)。"
    else
        ui_msgbox "错误" "服务在升级后未能启动，请检查日志。"
    fi
}

# Uninstall function
uninstall_komari() {
    log_step "卸载 Komari..."

    if ! is_installed; then
        ui_msgbox "提示" "Komari 未安装。"
        return 0
    fi

    if ! ui_yesno "确认卸载" "这将删除 Komari 二进制文件和服务。\n\n您确定要继续吗？"; then
        log_info "卸载已取消"
        return 0
    fi

    if check_systemd; then
        log_step "停止并禁用服务..."
        systemctl stop ${SERVICE_NAME}.service >/dev/null 2>&1
        systemctl disable ${SERVICE_NAME}.service >/dev/null 2>&1
        rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
        systemctl daemon-reload
        log_success "systemd 服务已删除"
    fi

    log_step "删除二进制文件..."
    rm -f "$BINARY_PATH"
    # 尝试在目录为空时删除该目录
    rmdir "$INSTALL_DIR" 2>/dev/null || log_info "数据目录 $INSTALL_DIR 不为空，未删除"
    log_success "Komari 二进制文件已删除"

    ui_msgbox "卸载完成" "Komari 卸载完成。\n\n数据文件保留在 $DATA_DIR"
}

# Show service status
show_status() {
    if ! is_installed; then
        ui_msgbox "错误" "Komari 未安装。"
        return
    fi
    if ! check_systemd; then
        ui_msgbox "错误" "未检测到 systemd。无法获取服务状态。"
        return
    fi
    if tui_enabled; then
        local status_output
        status_output=$(systemctl status ${SERVICE_NAME}.service --no-pager -l 2>&1)
        ui_msgbox "服务状态" "$status_output"
    else
        log_step "Komari 服务状态:"
        systemctl status ${SERVICE_NAME}.service --no-pager -l
        read -r -p "按回车键继续..." _
    fi
}

# Show service logs
show_logs() {
    if ! is_installed; then
        ui_msgbox "错误" "Komari 未安装。"
        return
    fi
    if ! check_systemd; then
        ui_msgbox "错误" "未检测到 systemd。无法获取服务日志。"
        return
    fi
    # 日志为实时流，直接在终端显示
    if tui_enabled; then
        clear
    fi
    log_step "查看 Komari 服务日志 (按 Ctrl+C 退出)..."
    journalctl -u ${SERVICE_NAME} -f --no-pager
}

# Restart service
restart_service() {
    if ! is_installed; then
        ui_msgbox "错误" "Komari 未安装。"
        return
    fi
    if ! check_systemd; then
        ui_msgbox "错误" "未检测到 systemd。无法重启服务。"
        return
    fi
    log_step "重启 Komari 服务..."
    systemctl restart ${SERVICE_NAME}.service
    if systemctl is-active --quiet ${SERVICE_NAME}.service; then
        ui_msgbox "成功" "服务重启成功。"
    else
        ui_msgbox "错误" "服务重启失败，请检查日志。"
    fi
}

# Stop service
stop_service() {
    if ! is_installed; then
        ui_msgbox "错误" "Komari 未安装。"
        return
    fi
    if ! check_systemd; then
        ui_msgbox "错误" "未检测到 systemd。无法停止服务。"
        return
    fi
    log_step "停止 Komari 服务..."
    systemctl stop ${SERVICE_NAME}.service
    ui_msgbox "成功" "服务已停止。"
}


# Main menu
main_menu() {
    while true; do
        show_banner

        local choice
        choice=$(ui_menu "Komari 监控系统安装器" "请选择操作：" \
            "1" "安装 Komari" \
            "2" "升级 Komari" \
            "3" "卸载 Komari" \
            "4" "查看状态" \
            "5" "查看日志" \
            "6" "重启服务" \
            "7" "停止服务" \
            "8" "退出")

        # 用户在 TUI 中取消（ESC/Cancel）则退出
        if [ $? -ne 0 ] && tui_enabled; then
            clear
            exit 0
        fi

        case $choice in
            1) install_binary ;;
            2) upgrade_komari ;;
            3) uninstall_komari ;;
            4) show_status ;;
            5) show_logs ;;
            6) restart_service ;;
            7) stop_service ;;
            8) 
                tui_enabled && clear
                exit 0 
                ;;
            *) ui_msgbox "错误" "无效选项" ;;
        esac

        # 纯文本模式下单次执行后退出循环（保持原有行为，避免输出被覆盖）
        if ! tui_enabled; then
            break
        fi
    done
}

# Main execution
check_root
detect_tui
main_menu
