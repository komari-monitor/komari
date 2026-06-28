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

# Show banner
show_banner() {
    clear
    echo "=============================================================="
    echo "            Komari Monitoring System Installer"
    echo "       https://github.com/komari-monitor/komari"
    echo "=============================================================="
    echo
}

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
        log_info "获取最新 snapshot 版本..."
        local latest_snapshot=$(curl -s "https://api.github.com/repos/${REPO}/releases" | grep '"tag_name"' | grep 'Snapshot-' | head -1 | sed -e 's/.*"tag_name": *"//' -e 's/".*//')
        
        if [ -z "$latest_snapshot" ]; then
            log_error "未找到 snapshot 版本"
            return 1
        fi
        
        log_info "最新 snapshot 版本: $latest_snapshot"
        echo "https://github.com/${REPO}/releases/download/${latest_snapshot}/${file_name}"
    else
        # 稳定版：使用 latest
        echo "https://github.com/${REPO}/releases/latest/download/${file_name}"
    fi
}

# Binary installation
install_binary() {
    log_step "开始二进制安装..."

    if is_installed; then
        log_info "Komari 已安装。要升级，请使用升级选项。"
        return
    fi

    # 选择发布通道
    echo "请选择发布通道："
    echo "  1) stable (稳定版，推荐)"
    echo "  2) snapshot (快照版，最新功能)"
    read -p "请选择 [默认: 1]: " channel_choice
    
    case $channel_choice in
        2)
            CHANNEL="snapshot"
            log_info "已选择: snapshot (快照版)"
            ;;
        1|"")
            CHANNEL="stable"
            log_info "已选择: stable (稳定版)"
            ;;
        *)
            log_error "无效选项，使用默认 stable"
            CHANNEL="stable"
            ;;
    esac

    # 监听端口输入，校验范围 1-65535
    while true; do
        read -p "请输入监听端口 [默认: $DEFAULT_PORT]: " input_port
        if [[ -z "$input_port" ]]; then
            LISTEN_PORT="$DEFAULT_PORT"
            break
        elif [[ "$input_port" =~ ^[0-9]+$ ]] && (( input_port >= 1 && input_port <= 65535 )); then
            LISTEN_PORT="$input_port"
            break
        else
            log_error "端口号无效，请输入 1-65535 之间的数字。"
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
        log_error "获取下载链接失败"
        return 1
    fi

    log_step "下载 Komari 二进制文件..."
    log_info "URL: $download_url"

    if ! curl -L -o "$BINARY_PATH" "$download_url"; then
        log_error "下载失败"
        return 1
    fi

    chmod +x "$BINARY_PATH"
    log_success "Komari 二进制文件安装完成: $BINARY_PATH"

    if ! check_systemd; then
        log_step "警告：未检测到 systemd，跳过服务创建。"
        log_step "您可以从命令行手动运行 Komari："
        log_step "    $BINARY_PATH server -l 0.0.0.0:$LISTEN_PORT"
        echo
        log_success "安装完成！"
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
        log_error "Komari 服务启动失败"
        log_info "查看日志: journalctl -u ${SERVICE_NAME} -f"
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
    echo
    log_success "安装完成！"
    echo
    log_info "访问信息："
    log_info "  URL: http://$(hostname -I | awk '{print $1}'):${port}"
    if [ -n "$password" ]; then
        log_info "初始登录信息（仅显示一次）: $password"
    fi
    echo
    log_info "服务管理命令："
    log_info "  状态:  systemctl status $SERVICE_NAME"
    log_info "  启动:   systemctl start $SERVICE_NAME"
    log_info "  停止:    systemctl stop $SERVICE_NAME"
    log_info "  重启: systemctl restart $SERVICE_NAME"
    log_info "  日志:    journalctl -u $SERVICE_NAME -f"
}

# Upgrade function
upgrade_komari() {
    log_step "升级 Komari..."

    if ! is_installed; then
        log_error "Komari 未安装。请先安装它。"
        return 1
    fi

    if ! check_systemd; then
        log_error "未检测到 systemd。无法管理服务。"
        return 1
    fi

    # 选择发布通道
    echo "请选择发布通道："
    echo "  1) stable (稳定版，推荐)"
    echo "  2) snapshot (快照版，最新功能)"
    read -p "请选择 [默认: 1]: " channel_choice
    
    case $channel_choice in
        2)
            CHANNEL="snapshot"
            log_info "已选择: snapshot (快照版)"
            ;;
        1|"")
            CHANNEL="stable"
            log_info "已选择: stable (稳定版)"
            ;;
        *)
            log_error "无效选项，使用默认 stable"
            CHANNEL="stable"
            ;;
    esac

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
        return 1
    fi

    log_step "下载最新版本..."
    if ! curl -L -o "$BINARY_PATH" "$download_url"; then
        log_error "下载失败，正在从备份恢复"
        mv "${BINARY_PATH}.backup."* "$BINARY_PATH"
        systemctl start ${SERVICE_NAME}.service
        return 1
    fi

    chmod +x "$BINARY_PATH"

    log_step "重启 Komari 服务..."
    systemctl start ${SERVICE_NAME}.service

    if systemctl is-active --quiet ${SERVICE_NAME}.service; then
        log_success "Komari 升级成功"
    else
        log_error "服务在升级后未能启动"
    fi
}

# Uninstall function
uninstall_komari() {
    log_step "卸载 Komari..."

    if ! is_installed; then
        log_info "Komari 未安装"
        return 0
    fi

    read -p "这将删除 Komari。您确定吗？(Y/n): " confirm
    if [[ $confirm =~ ^[Nn]$ ]]; then
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

    log_success "Komari 卸载完成"
    log_info "数据文件保留在 $DATA_DIR"
}

# Show service status
show_status() {
    if ! is_installed; then
        log_error "Komari 未安装"
        return
    fi
    if ! check_systemd; then
        log_error "未检测到 systemd。无法获取服务状态。"
        return
    fi
    log_step "Komari 服务状态:"
    systemctl status ${SERVICE_NAME}.service --no-pager -l
}

# Show service logs
show_logs() {
    if ! is_installed; then
        log_error "Komari 未安装"
        return
    fi
    if ! check_systemd; then
        log_error "未检测到 systemd。无法获取服务日志。"
        return
    fi
    log_step "查看 Komari 服务日志..."
    journalctl -u ${SERVICE_NAME} -f --no-pager
}

# Restart service
restart_service() {
    if ! is_installed; then
        log_error "Komari 未安装"
        return
    fi
    if ! check_systemd; then
        log_error "未检测到 systemd。无法重启服务。"
        return
    fi
    log_step "重启 Komari 服务..."
    systemctl restart ${SERVICE_NAME}.service
    if systemctl is-active --quiet ${SERVICE_NAME}.service; then
        log_success "服务重启成功"
    else
        log_error "服务重启失败"
    fi
}

# Stop service
stop_service() {
    if ! is_installed; then
        log_error "Komari 未安装"
        return
    fi
    if ! check_systemd; then
        log_error "未检测到 systemd。无法停止服务。"
        return
    fi
    log_step "停止 Komari 服务..."
    systemctl stop ${SERVICE_NAME}.service
    log_success "服务已停止"
}


# Main menu
main_menu() {
    show_banner
    echo "请选择操作："
    echo "  1) 安装 Komari"
    echo "  2) 升级 Komari"
    echo "  3) 卸载 Komari"
    echo "  4) 查看状态"
    echo "  5) 查看日志"
    echo "  6) 重启服务"
    echo "  7) 停止服务"
    echo "  8) 退出"
    echo

    read -p "输入选项 [1-8]: " choice

    case $choice in
        1) install_binary ;;
        2) upgrade_komari ;;
        3) uninstall_komari ;;
        4) show_status ;;
        5) show_logs ;;
        6) restart_service ;;
        7) stop_service ;;
        8) exit 0 ;;
        *) log_error "无效选项" ;;
    esac
}

# Main execution
check_root
main_menu
