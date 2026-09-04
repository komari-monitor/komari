#!/bin/bash

# Color definitions for terminal output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
BLUE='\033[0;34m'
MAGENTA='\033[0;35m'
CYAN='\033[0;36m'
WHITE='\033[0;37m'
BOLD='\033[1m'
DIM='\033[2m'
NC='\033[0m'
COLOR_ENABLED=0

# Logging functions
log_info() {
    if [ "$COLOR_ENABLED" -eq 1 ]; then
        printf '%b%s%b\n' "$CYAN" "$1" "$NC"
    else
        printf '%s\n' "$1"
    fi
}

log_success() {
    if [ "$COLOR_ENABLED" -eq 1 ]; then
        printf '%b%s%b\n' "$GREEN" "$1" "$NC"
    else
        printf '%s\n' "$1"
    fi
}

log_error() {
    if [ "$COLOR_ENABLED" -eq 1 ]; then
        printf '%b%s%b\n' "$RED" "$1" "$NC"
    else
        printf '%s\n' "$1"
    fi
}

log_step() {
    render_screen
    if [ "$COLOR_ENABLED" -eq 1 ]; then
        printf '%b%s%b\n' "$YELLOW" "$1" "$NC"
    else
        printf '%s\n' "$1"
    fi
}

# Global variables
INSTALL_DIR="/opt/komari"
DATA_DIR="/opt/komari"
SERVICE_NAME="komari"
BINARY_PATH="$INSTALL_DIR/komari"
BACKUP_DIR="$INSTALL_DIR/backup"
DATA_BACKUP_DIR="$DATA_DIR/data/backup"
DEFAULT_PORT="25774"
LISTEN_PORT=""
STANDARD_REPO="komari-monitor/komari"
LITE_REPO="nuomiiiii/komari"
REPO="$STANDARD_REPO"
# 发行版本: standard（标准版）或 lite（Lite 轻量版）
EDITION="standard"
EDITION_NAME=""
# 发布通道: stable（稳定版）或 snapshot（快照版）；Lite 仅支持 stable
CHANNEL="stable"
CHANNEL_NAME=""
# 语言: en（English）或 zh（简体中文）
LANGUAGE="zh"
# 非交互输出只绘制一次横幅，交互终端则在每个步骤前刷新屏幕。
SCREEN_DRAWN=0
PROGRESS_STEPS=()

# ==========================================================
# 本地化文案
# ==========================================================

# 输出当前语言的文案。参数用于替换文案中的 %s 占位符。
msg() {
    local key="$1"
    shift
    local en_text=""
    local zh_text=""

    case "$key" in
        input_option)
            en_text='Enter an option: '
            zh_text='输入选项： '
            ;;
        input_default)
            en_text=' [default: %s]: '
            zh_text=' [默认：%s]：'
            ;;
        press_enter)
            en_text='Press Enter to continue...'
            zh_text='按回车键继续...'
            ;;
        yes_no)
            en_text=' (Y/n): '
            zh_text=' (Y/n)：'
            ;;
        title_notice)
            en_text='Notice'
            zh_text='提示'
            ;;
        title_error)
            en_text='Error'
            zh_text='错误'
            ;;
        title_success)
            en_text='Success'
            zh_text='成功'
            ;;
        title_install_complete)
            en_text='Installation complete'
            zh_text='安装完成'
            ;;
        title_cleanup_complete)
            en_text='Cleanup complete'
            zh_text='清理完成'
            ;;
        title_upgrade_complete)
            en_text='Upgrade complete'
            zh_text='升级完成'
            ;;
        title_uninstall_complete)
            en_text='Uninstallation complete'
            zh_text='卸载完成'
            ;;
        sponsor_info)
            en_text='Sponsors:\n  AxisNow: Self-hosted private CDN with a flexible, modular network.\n  Dream Cloud: Cost-effective Asia-Pacific hosting with direct connectivity and DDoS protection.\n  Sharon Networks: China-optimized connectivity with low latency, high bandwidth, and Tbps-scale DDoS mitigation.'
            zh_text='赞助商：\n  AxisNow：自建私有部署 CDN，订阅式高仿 CDN，自主可控、灵活组合的 CDN 网络。\n  Dream Cloud：高性价比直连亚太高防，真高防，不虚标，打死退款。\n  Sharon Networks：亚太数据中心提供中国优化网络，低延时、高带宽，并提供 Tbps 级本地清洗高防。'
            ;;
        language_prompt)
            en_text='Select language / 请选择语言:'
            zh_text='Select language / 请选择语言:'
            ;;
        language_input)
            en_text='Select 1 or 2 [default 2] / 输入 1 或 2 [默认 2]: '
            zh_text='Select 1 or 2 [default 2] / 输入 1 或 2 [默认 2]: '
            ;;
        language_invalid)
            en_text='Invalid choice. Please enter 1 or 2.'
            zh_text='选项无效，请输入 1 或 2。'
            ;;
        root_required)
            en_text='Please run this script as root.'
            zh_text='请使用 root 权限运行此脚本。'
            ;;
        edition_title)
            en_text='Choose an edition'
            zh_text='选择安装版本'
            ;;
        edition_prompt)
            en_text='Komari has multiple editions with different features and performance profiles. Choose the one that fits your controller.\n\nChoose the edition to install [default 1]:'
            zh_text='Komari 目前提供多个版本，不同版本在功能和性能上有所差异，请根据主控配置选择。\n\n请选择安装的版本（默认 1）：'
            ;;
        edition_standard)
            en_text='Standard edition'
            zh_text='标准版本'
            ;;
        edition_lite)
            en_text='Lite edition - optimized for low-resource controllers with a streamlined feature set (maintained by @nuomiiiii)'
            zh_text='Lite 版本 - 改善低配置主控下的性能，精简复杂功能（由 @nuomiiiii 维护）'
            ;;
        edition_name_standard)
            en_text='Komari Standard'
            zh_text='Komari 标准版'
            ;;
        edition_name_lite)
            en_text='Komari Lite'
            zh_text='Komari Lite 轻量版'
            ;;
        selected_edition)
            en_text='Selected edition: %s'
            zh_text='已选择版本：%s'
            ;;
        channel_title)
            en_text='Choose a release channel'
            zh_text='选择发布通道'
            ;;
        channel_prompt)
            en_text='Choose the release channel [default 1]:'
            zh_text='请选择发布通道（默认 1）：'
            ;;
        channel_stable)
            en_text='Stable release (recommended)'
            zh_text='正式版（推荐）'
            ;;
        channel_snapshot)
            en_text='Snapshot release (latest changes)'
            zh_text='快照版（最新功能）'
            ;;
        channel_name_stable)
            en_text='stable'
            zh_text='正式版'
            ;;
        channel_name_snapshot)
            en_text='snapshot'
            zh_text='快照版'
            ;;
        selected_channel)
            en_text='Selected channel: %s'
            zh_text='已选择通道：%s'
            ;;
        progress_edition_standard)
            en_text='Standard edition'
            zh_text='标准版'
            ;;
        progress_edition_lite)
            en_text='Lite edition'
            zh_text='Lite 版本'
            ;;
        progress_download)
            en_text='Download Komari'
            zh_text='下载 Komari'
            ;;
        progress_service)
            en_text='Configure system service'
            zh_text='设置系统服务'
            ;;
        progress_backup)
            en_text='Back up current version'
            zh_text='备份当前版本'
            ;;
        progress_restart)
            en_text='Restart service'
            zh_text='重启服务'
            ;;
        progress_complete)
            en_text='Complete'
            zh_text='完成'
            ;;
        already_installed)
            en_text='Komari is already installed.\nUse the management menu to upgrade it.'
            zh_text='Komari 已安装。\n请使用管理菜单中的升级选项。'
            ;;
        install_cancelled)
            en_text='Installation cancelled.'
            zh_text='安装已取消。'
            ;;
        port_title)
            en_text='Listen port'
            zh_text='监听端口'
            ;;
        port_prompt)
            en_text='Enter the Komari listen port (1-65535)'
            zh_text='请输入 Komari 的监听端口（1-65535）'
            ;;
        invalid_port)
            en_text='Invalid port. Enter a number from 1 to 65535.'
            zh_text='端口号无效，请输入 1-65535 之间的数字。'
            ;;
        dependencies_start)
            en_text='Checking and installing dependencies...'
            zh_text='检查并安装依赖...'
            ;;
        dependencies_apt)
            en_text='Installing dependencies with apt...'
            zh_text='使用 apt 安装依赖...'
            ;;
        dependencies_yum)
            en_text='Installing dependencies with yum...'
            zh_text='使用 yum 安装依赖...'
            ;;
        dependencies_apk)
            en_text='Installing dependencies with apk...'
            zh_text='使用 apk 安装依赖...'
            ;;
        unsupported_package_manager)
            en_text='No supported package manager found (apt/yum/apk).'
            zh_text='未找到支持的包管理器（apt/yum/apk）。'
            ;;
        unsupported_arch)
            en_text='Unsupported architecture: %s'
            zh_text='不支持的架构：%s'
            ;;
        detected_arch)
            en_text='Detected architecture: %s'
            zh_text='检测到架构：%s'
            ;;
        create_install_dir)
            en_text='Creating installation directory: %s'
            zh_text='创建安装目录：%s'
            ;;
        create_data_dir)
            en_text='Creating data directory: %s'
            zh_text='创建数据目录：%s'
            ;;
        download_url_failed)
            en_text='Failed to get the download URL. Check your network connection and try again.'
            zh_text='获取下载链接失败，请检查网络连接后重试。'
            ;;
        download_binary)
            en_text='Downloading %s binary...'
            zh_text='正在下载 %s 二进制文件...'
            ;;
        download_url)
            en_text='Download URL: %s'
            zh_text='下载地址：%s'
            ;;
        download_size_unknown)
            en_text='unknown'
            zh_text='未知'
            ;;
        download_progress)
            en_text='Downloading %s %3s%% [%s%s] %s / %s'
            zh_text='正在下载 %s %3s%% [%s%s] %s / %s'
            ;;
        fetch_snapshot)
            en_text='Fetching the latest snapshot release...'
            zh_text='正在获取最新快照版本...'
            ;;
        snapshot_not_found)
            en_text='No snapshot release was found.'
            zh_text='未找到快照版本。'
            ;;
        snapshot_found)
            en_text='Latest snapshot: %s'
            zh_text='最新快照版本：%s'
            ;;
        download_failed)
            en_text='Download failed. Check your network connection.'
            zh_text='下载失败，请检查网络连接。'
            ;;
        binary_installed)
            en_text='%s binary installed at %s'
            zh_text='%s 二进制文件安装完成：%s'
            ;;
        no_systemd_manual)
            en_text='Warning: systemd was not found, so the service was not created.\n\nYou can start %s manually:\n    %s server -l 0.0.0.0:%s'
            zh_text='警告：未检测到 systemd，已跳过服务创建。\n\n您可以手动运行 %s：\n    %s server -l 0.0.0.0:%s'
            ;;
        service_started)
            en_text='Komari service started successfully.'
            zh_text='Komari 服务启动成功。'
            ;;
        service_start_failed)
            en_text='Komari service failed to start.\n\nView logs: journalctl -u %s -f'
            zh_text='Komari 服务启动失败。\n\n查看日志：journalctl -u %s -f'
            ;;
        systemd_start)
            en_text='Creating systemd service...'
            zh_text='创建 systemd 服务...'
            ;;
        systemd_created)
            en_text='systemd service file created.'
            zh_text='systemd 服务文件创建完成。'
            ;;
        access_info)
            en_text='Access URL:\n  http://%s:%s\n\nCreate the administrator account in your browser.\n\nService commands:\n  Status: systemctl status %s\n  Start: systemctl start %s\n  Stop: systemctl stop %s\n  Restart: systemctl restart %s\n  Logs: journalctl -u %s -f'
            zh_text='访问地址：\n  http://%s:%s\n\n请在浏览器中创建管理员账号。\n\n服务管理命令：\n  状态：systemctl status %s\n  启动：systemctl start %s\n  停止：systemctl stop %s\n  重启：systemctl restart %s\n  日志：journalctl -u %s -f'
            ;;
        cleanup_confirm)
            en_text='This will delete Komari binary upgrade backups and data upgrade archives.\n\nContinue?'
            zh_text='将删除 Komari 的二进制升级备份和数据升级压缩包。\n\n确定继续吗？'
            ;;
        cleanup_cancelled)
            en_text='Backup cleanup cancelled.'
            zh_text='备份清理已取消。'
            ;;
        cleanup_start)
            en_text='Cleaning upgrade history backups...'
            zh_text='清理升级历史备份...'
            ;;
        cleanup_complete)
            en_text='Removed files:\n  Binary backups: %s\n  Data archives: %s\n  Data archives: %s'
            zh_text='清理范围：\n  二进制备份：%s\n  数据压缩包：%s\n  数据压缩包：%s'
            ;;
        upgrade_start)
            en_text='Upgrading Komari...'
            zh_text='升级 Komari...'
            ;;
        not_installed)
            en_text='Komari is not installed. Install it first.'
            zh_text='Komari 未安装，请先安装。'
            ;;
        systemd_required)
            en_text='systemd was not found. The service cannot be managed.'
            zh_text='未检测到 systemd，无法管理服务。'
            ;;
        stopping_service)
            en_text='Stopping Komari service...'
            zh_text='停止 Komari 服务...'
            ;;
        clearing_backups)
            en_text='Removing old binary backups...'
            zh_text='清理旧的二进制备份...'
            ;;
        backing_up)
            en_text='Backing up the current binary...'
            zh_text='备份当前二进制文件...'
            ;;
        backup_failed_log)
            en_text='Failed to back up the current binary. Restarting the service.'
            zh_text='备份当前二进制文件失败，正在启动服务。'
            ;;
        backup_failed)
            en_text='The current version could not be backed up. Upgrade cancelled.'
            zh_text='备份当前版本失败，升级已取消。'
            ;;
        download_url_failed_log)
            en_text='Failed to get the download URL. Restoring the backup.'
            zh_text='获取下载链接失败，正在从备份恢复。'
            ;;
        download_url_failed_restore)
            en_text='Failed to get the download URL. The backup was restored.'
            zh_text='获取下载链接失败，已从备份恢复。'
            ;;
        downloading_latest)
            en_text='Downloading the latest %s...'
            zh_text='下载最新 %s...'
            ;;
        download_failed_log)
            en_text='Download failed. Restoring the backup.'
            zh_text='下载失败，正在从备份恢复。'
            ;;
        download_failed_restore)
            en_text='Download failed. The backup was restored.'
            zh_text='下载失败，已从备份恢复。'
            ;;
        upgrade_success)
            en_text='Version: %s\nChannel: %s'
            zh_text='版本：%s\n通道：%s'
            ;;
        upgrade_start_failed)
            en_text='The service failed to start after the upgrade. Check the logs.'
            zh_text='服务在升级后未能启动，请检查日志。'
            ;;
        uninstall_start)
            en_text='Uninstalling Komari...'
            zh_text='卸载 Komari...'
            ;;
        confirm_uninstall)
            en_text='This will remove the Komari binary and service.\n\nContinue?'
            zh_text='这将删除 Komari 二进制文件和服务。\n\n确定继续吗？'
            ;;
        uninstall_cancelled)
            en_text='Uninstallation cancelled.'
            zh_text='卸载已取消。'
            ;;
        stopping_disabling)
            en_text='Stopping and disabling the service...'
            zh_text='停止并禁用服务...'
            ;;
        systemd_removed)
            en_text='systemd service removed.'
            zh_text='systemd 服务已删除。'
            ;;
        deleting_binary)
            en_text='Removing the binary...'
            zh_text='删除二进制文件...'
            ;;
        data_dir_not_empty)
            en_text='Data directory %s is not empty; it was kept.'
            zh_text='数据目录 %s 不为空，未删除。'
            ;;
        binary_removed)
            en_text='Komari binary removed.'
            zh_text='Komari 二进制文件已删除。'
            ;;
        uninstall_complete)
            en_text='Data files were kept at %s.'
            zh_text='数据文件保留在 %s。'
            ;;
        service_status)
            en_text='Komari service status:'
            zh_text='Komari 服务状态：'
            ;;
        service_logs)
            en_text='View Komari service logs (press Ctrl+C to exit)...'
            zh_text='查看 Komari 服务日志（按 Ctrl+C 退出）...'
            ;;
        restart_start)
            en_text='Restarting Komari service...'
            zh_text='重启 Komari 服务...'
            ;;
        restart_success)
            en_text='The service is running again.'
            zh_text='服务已恢复运行。'
            ;;
        restart_failed)
            en_text='Service restart failed. Check the logs.'
            zh_text='服务重启失败，请检查日志。'
            ;;
        stop_start)
            en_text='Stopping Komari service...'
            zh_text='停止 Komari 服务...'
            ;;
        stop_success)
            en_text='The service is now stopped.'
            zh_text='服务当前已停止。'
            ;;
        main_title)
            en_text='Komari management menu'
            zh_text='Komari 管理菜单'
            ;;
        main_prompt)
            en_text='Select an action:'
            zh_text='请选择操作：'
            ;;
        main_install)
            en_text='Install Komari'
            zh_text='安装 Komari'
            ;;
        main_upgrade)
            en_text='Upgrade Komari'
            zh_text='升级 Komari'
            ;;
        main_uninstall)
            en_text='Uninstall Komari'
            zh_text='卸载 Komari'
            ;;
        main_status)
            en_text='View service status'
            zh_text='查看服务状态'
            ;;
        main_logs)
            en_text='View service logs'
            zh_text='查看服务日志'
            ;;
        main_restart)
            en_text='Restart service'
            zh_text='重启服务'
            ;;
        main_stop)
            en_text='Stop service'
            zh_text='停止服务'
            ;;
        main_cleanup)
            en_text='Clean upgrade backups'
            zh_text='清理升级历史备份'
            ;;
        main_exit)
            en_text='Exit'
            zh_text='退出'
            ;;
        invalid_option)
            en_text='Invalid option.'
            zh_text='无效选项。'
            ;;
    esac

    if [ "$LANGUAGE" = "en" ]; then
        printf "$en_text" "$@"
    else
        printf "$zh_text" "$@"
    fi
}

init_colors() {
    if [ -t 2 ]; then
        COLOR_ENABLED=1
    fi
}

# ==========================================================
# 纯终端交互层
# ==========================================================

progress_reset() {
    PROGRESS_STEPS=()
}

progress_add() {
    PROGRESS_STEPS+=("$1")
}

show_progress() {
    if [ ${#PROGRESS_STEPS[@]} -eq 0 ]; then
        return
    fi

    printf '\n  '
    local step
    local separator=""
    local index=0
    local colors=("$CYAN" "$MAGENTA" "$YELLOW" "$BLUE" "$GREEN")
    for step in "${PROGRESS_STEPS[@]}"; do
        if [ -n "$separator" ]; then
            if [ "$COLOR_ENABLED" -eq 1 ]; then
                printf '%b / %b' "$DIM" "$NC"
            else
                printf ' / '
            fi
        fi
        if [ "$COLOR_ENABLED" -eq 1 ]; then
            printf '%b%s%b' "${colors[$((index % ${#colors[@]}))]}" "$step" "$NC"
        else
            printf '%s' "$step"
        fi
        separator=' / '
        index=$((index + 1))
    done
    printf '\n'
}

screen_is_interactive() {
    # 菜单通过命令替换捕获 stdout，交互界面统一输出到 stderr。
    [ -t 2 ]
}

render_screen() {
    if screen_is_interactive; then
        clear >&2 2>/dev/null || true
        show_banner >&2
    elif [ "$SCREEN_DRAWN" -ne 1 ]; then
        show_banner >&2
        SCREEN_DRAWN=1
    fi
}

ui_header() {
    if [ "$COLOR_ENABLED" -eq 1 ]; then
        printf '\n  %b%s%b\n' "$BOLD$BLUE" "$1" "$NC" >&2
    else
        printf '\n  %s\n' "$1" >&2
    fi
}

# 菜单选择。返回选中的 tag，输入结束时返回非零。
ui_menu() {
    local title="$1"
    local prompt="$2"
    shift 2

    render_screen
    ui_header "$title"
    if [ "$COLOR_ENABLED" -eq 1 ]; then
        printf '%b%s%b\n\n' "$WHITE" "$prompt" "$NC" >&2
    else
        printf '%s\n\n' "$prompt" >&2
    fi

    local tag
    local item
    local args=("$@")
    local i=0
    while [ $i -lt ${#args[@]} ]; do
        tag="${args[$i]}"
        item="${args[$((i + 1))]}"
        if [ "$COLOR_ENABLED" -eq 1 ]; then
            printf '  %b%s%b) %s\n' "$CYAN" "$tag" "$NC" "$item" >&2
        else
            printf '  %s) %s\n' "$tag" "$item" >&2
        fi
        i=$((i + 2))
    done

    printf '\n%s' "$(msg input_option)" >&2
    local choice
    if ! IFS= read -r choice; then
        return 1
    fi
    printf '%s\n' "$choice"
}

# 输入框。留空时返回默认值。
ui_input() {
    local title="$1"
    local prompt="$2"
    local default="$3"
    local input

    render_screen
    ui_header "$title"
    if [ "$COLOR_ENABLED" -eq 1 ]; then
        printf '%b%s%b' "$WHITE" "$prompt" "$NC" >&2
        printf '%b%s%b' "$DIM" "$(msg input_default "$default")" "$NC" >&2
    else
        printf '%s' "$prompt" >&2
        printf '%s' "$(msg input_default "$default")" >&2
    fi
    if ! IFS= read -r input; then
        return 1
    fi

    if [ -z "$input" ]; then
        printf '%s\n' "$default"
    else
        printf '%s\n' "$input"
    fi
}

# 是/否确认。空输入默认为是。
ui_yesno() {
    local title="$1"
    local prompt="$2"
    local confirm

    render_screen
    ui_header "$title"
    if [ "$COLOR_ENABLED" -eq 1 ]; then
        printf '%b%s%b' "$WHITE" "$prompt" "$NC" >&2
        printf '%b%s%b' "$YELLOW" "$(msg yes_no)" "$NC" >&2
    else
        printf '%s' "$prompt" >&2
        printf '%s' "$(msg yes_no)" >&2
    fi
    if ! IFS= read -r confirm; then
        return 1
    fi

    case "$confirm" in
        [Nn]|[Nn][Oo]|否|不)
            return 1
            ;;
        *)
            return 0
            ;;
    esac
}

# 信息提示框。
ui_msgbox() {
    local title="$1"
    local content="$2"

    render_screen
    ui_header "$title"
    printf '%s\n' "$content" >&2
    ui_pause
}

ui_pause() {
    printf '%s ' "$(msg press_enter)" >&2
    IFS= read -r _ || true
}

# 选择语言。语言菜单本身保持中英双语，默认简体中文。
select_language() {
    local choice

    while true; do
        printf '\n' >&2
        printf '  %s\n\n' "$(msg language_prompt)" >&2
        printf '  1) English\n' >&2
        printf '  2) 简体中文\n\n' >&2
        printf '%s' "$(msg language_input)" >&2

        if ! IFS= read -r choice; then
            exit 0
        fi

        case "$choice" in
            1)
                LANGUAGE="en"
                return 0
                ;;
            2|"")
                LANGUAGE="zh"
                return 0
                ;;
            *)
                log_error "$(msg language_invalid)"
                ;;
        esac
    done
}

# 显示 ASCII 横幅。
show_banner() {
    if [ "$COLOR_ENABLED" -eq 1 ]; then
        printf '%b' "$CYAN"
    fi
    while IFS= read -r line; do
        printf '%s\n' "$line"
    done <<'ASCII_ART'
<-.(`-')            <-. (`-')   (`-')  _    (`-')   _
 __( OO)      .->      \(OO )_  (OO ).-/ <-.(OO )  (_)
'-'. ,--.(`-')----. ,--./  ,-.) / ,---.  ,------,) ,-(`-')
|  .'   /( OO).-.  '|   `.'   | | \ /`.\ |   /`. ' | ( OO)
|      /)( _) | |  ||  |'.'|  | '-'|_.' ||  |_.' | |  |  )
|  .   '  \|  |)|  ||  |   |  |(|  .-.  ||  .   .'(|  |_/
|  |\   \  '  '-'  '|  |   |  | |  | |  ||  |\  \  |  |'->
    `--' '--'   `-----' `--'   `--' `--' `--'`--' '--' `--'
ASCII_ART
    if [ "$COLOR_ENABLED" -eq 1 ]; then
        printf '%b' "$NC"
    fi
    show_progress
}


# 设置发行版本，结果写入全局变量 EDITION / REPO。
select_edition() {
    local choice
    choice=$(ui_menu "$(msg edition_title)" "$(msg edition_prompt)" \
        "1" "$(msg edition_standard)" \
        "2" "$(msg edition_lite)")

    case "$choice" in
        lite|2)
            EDITION="lite"
            EDITION_NAME="$(msg edition_name_lite)"
            REPO="$LITE_REPO"
            ;;
        standard|1|"")
            EDITION="standard"
            EDITION_NAME="$(msg edition_name_standard)"
            REPO="$STANDARD_REPO"
            ;;
        *)
            EDITION="standard"
            EDITION_NAME="$(msg edition_name_standard)"
            REPO="$STANDARD_REPO"
            ;;
    esac
    if [ "$EDITION" = "lite" ]; then
        progress_add "$(msg progress_edition_lite)"
    else
        progress_add "$(msg progress_edition_standard)"
    fi
    log_info "$(msg selected_edition "$EDITION_NAME")"
}

# 设置发布通道，结果写入全局变量 CHANNEL。
select_channel() {
    local choice

    if [ "$EDITION" = "lite" ]; then
        CHANNEL="stable"
        CHANNEL_NAME="$(msg channel_name_stable)"
        progress_add "$CHANNEL_NAME"
        log_info "$(msg selected_channel "$CHANNEL_NAME")"
        return 0
    fi

    choice=$(ui_menu "$(msg channel_title)" "$(msg channel_prompt)" \
        "1" "$(msg channel_stable)" \
        "2" "$(msg channel_snapshot)")

    case "$choice" in
        snapshot|2)
            CHANNEL="snapshot"
            CHANNEL_NAME="$(msg channel_name_snapshot)"
            ;;
        stable|1|"")
            CHANNEL="stable"
            CHANNEL_NAME="$(msg channel_name_stable)"
            ;;
        *)
            CHANNEL="stable"
            CHANNEL_NAME="$(msg channel_name_stable)"
            ;;
    esac
    progress_add "$CHANNEL_NAME"
    log_info "$(msg selected_channel "$CHANNEL_NAME")"
}

# ==========================================================
# 基础检查
# ==========================================================

# Check if running as root
check_root() {
    if [ "$EUID" -ne 0 ]; then
        log_error "$(msg root_required)"
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
        loongarch64|loong64)
            echo "loong64"
            ;;
        *)
            log_error "$(msg unsupported_arch "$arch")"
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
    log_step "$(msg dependencies_start)"

    if ! command -v curl >/dev/null 2>&1; then
        if command -v apt >/dev/null 2>&1; then
            log_info "$(msg dependencies_apt)"
            apt update
            apt install -y curl
        elif command -v yum >/dev/null 2>&1; then
            log_info "$(msg dependencies_yum)"
            yum install -y curl
        elif command -v apk >/dev/null 2>&1; then
            log_info "$(msg dependencies_apk)"
            apk add curl
        else
            log_error "$(msg unsupported_package_manager)"
            exit 1
        fi
    fi
}

# Get download URL based on channel
get_download_url() {
    local arch=$1
    local file_name="komari-linux-${arch}"

    # Lite 仓库没有 snapshot 发布，始终使用正式版下载地址。
    if [ "$EDITION" = "lite" ]; then
        CHANNEL="stable"
    fi

    if [ "$CHANNEL" = "snapshot" ]; then
        # 获取最新的 snapshot 预发布版本
        log_info "$(msg fetch_snapshot)" >&2
        local latest_snapshot=$(curl -s "https://api.github.com/repos/${REPO}/releases" | grep '"tag_name"' | grep 'Snapshot-' | head -1 | sed -e 's/.*"tag_name": *"//' -e 's/".*//')

        if [ -z "$latest_snapshot" ]; then
            log_error "$(msg snapshot_not_found)" >&2
            return 1
        fi

        log_info "$(msg snapshot_found "$latest_snapshot")" >&2
        echo "https://github.com/${REPO}/releases/download/${latest_snapshot}/${file_name}"
    else
        # 稳定版：使用 latest
        echo "https://github.com/${REPO}/releases/latest/download/${file_name}"
    fi
}

# Format bytes with a compact, human-readable unit.
format_bytes() {
    local bytes="${1:-0}"
    if ! [[ "$bytes" =~ ^[0-9]+$ ]]; then
        bytes=0
    fi

    awk -v bytes="$bytes" 'BEGIN {
        split("B KiB MiB GiB TiB", units, " ")
        value = bytes
        unit = 1
        while (value >= 1024 && unit < 5) {
            value = value / 1024
            unit++
        }
        if (unit == 1) {
            printf "%.0f %s", value, units[unit]
        } else {
            printf "%.1f %s", value, units[unit]
        }
    }'
}

# Read the final Content-Length after following redirects. A zero result means
# the server did not provide a usable total size.
get_remote_size() {
    local url="$1"
    curl -fsSLI --max-time 30 "$url" 2>/dev/null | awk '
        BEGIN { IGNORECASE = 1 }
        /^content-length:/ {
            value = $2
            gsub(/\r/, "", value)
            if (value ~ /^[0-9]+$/) {
                size = value
            }
        }
        END {
            print size + 0
        }
    '
}

# Draw a single-line progress meter. The final argument controls whether the
# line is terminated; non-interactive output only prints the final result.
print_download_progress() {
    local label="$1"
    local downloaded_bytes="${2:-0}"
    local total_bytes="${3:-0}"
    local is_final="${4:-0}"
    local percent_label="--"
    local total_label
    local downloaded_label
    local bar_width=24
    local filled=0
    local empty=$bar_width

    if ! [[ "$downloaded_bytes" =~ ^[0-9]+$ ]]; then
        downloaded_bytes=0
    fi
    if ! [[ "$total_bytes" =~ ^[0-9]+$ ]]; then
        total_bytes=0
    fi

    if [ "$total_bytes" -gt 0 ]; then
        local percent=$((downloaded_bytes * 100 / total_bytes))
        if [ "$percent" -gt 100 ]; then
            percent=100
        fi
        percent_label="$percent"
        filled=$((percent * bar_width / 100))
        empty=$((bar_width - filled))
        total_label=$(format_bytes "$total_bytes")
    else
        total_label="$(msg download_size_unknown)"
    fi

    downloaded_label=$(format_bytes "$downloaded_bytes")

    local filled_bar=""
    local empty_bar=""
    if [ "$filled" -gt 0 ]; then
        printf -v filled_bar '%*s' "$filled" ''
        filled_bar=${filled_bar// /#}
    fi
    if [ "$empty" -gt 0 ]; then
        printf -v empty_bar '%*s' "$empty" ''
        empty_bar=${empty_bar// /-}
    fi

    local line
    line=$(msg download_progress \
        "$label" "$percent_label" "$filled_bar" "$empty_bar" \
        "$downloaded_label" "$total_label")

    if screen_is_interactive; then
        if [ "$is_final" -eq 1 ]; then
            printf '\r\033[K%s\n' "$line" >&2
        else
            printf '\r\033[K%s' "$line" >&2
        fi
    elif [ "$is_final" -eq 1 ]; then
        printf '%s\n' "$line"
    fi
}

# Download silently with curl while the shell owns the visible progress line.
download_file() {
    local url="$1"
    local target="$2"
    local label="$3"
    local total_bytes
    total_bytes=$(get_remote_size "$url")
    if ! [[ "$total_bytes" =~ ^[0-9]+$ ]]; then
        total_bytes=0
    fi

    : > "$target" || return 1
    curl -fsSL -o "$target" "$url" &
    local download_pid=$!
    local downloaded_bytes=0

    while kill -0 "$download_pid" 2>/dev/null; do
        if [ -f "$target" ]; then
            downloaded_bytes=$(stat -c '%s' "$target" 2>/dev/null || printf '0')
        fi
        print_download_progress "$label" "$downloaded_bytes" "$total_bytes"
        sleep 0.2
    done

    wait "$download_pid"
    local download_status=$?
    if [ -f "$target" ]; then
        downloaded_bytes=$(stat -c '%s' "$target" 2>/dev/null || printf '0')
    fi
    print_download_progress "$label" "$downloaded_bytes" "$total_bytes" 1
    return "$download_status"
}

# ==========================================================
# 业务操作
# ==========================================================

# Binary installation
install_binary() {
    progress_reset

    if is_installed; then
        ui_msgbox "$(msg title_notice)" "$(msg already_installed)"
        return
    fi

    # 选择发行版本和发布通道
    select_edition
    select_channel

    # 监听端口输入，校验范围 1-65535
    while true; do
        local input_port
        if ! input_port=$(ui_input "$(msg port_title)" "$(msg port_prompt)" "$DEFAULT_PORT"); then
            log_info "$(msg install_cancelled)"
            return
        fi
        if [[ -z "$input_port" ]]; then
            LISTEN_PORT="$DEFAULT_PORT"
            break
        elif [[ "$input_port" =~ ^[0-9]+$ ]] && (( input_port >= 1 && input_port <= 65535 )); then
            LISTEN_PORT="$input_port"
            break
        else
            ui_msgbox "$(msg title_error)" "$(msg invalid_port)"
        fi
    done

    install_dependencies

    local arch=$(detect_arch)
    log_info "$(msg detected_arch "$arch")"

    log_step "$(msg create_install_dir "$INSTALL_DIR")"
    mkdir -p "$INSTALL_DIR"

    log_step "$(msg create_data_dir "$DATA_DIR")"
    mkdir -p "$DATA_DIR"

    local download_url=$(get_download_url "$arch")
    if [ $? -ne 0 ]; then
        ui_msgbox "$(msg title_error)" "$(msg download_url_failed)"
        return 1
    fi

    progress_add "$(msg progress_download)"
    log_step "$(msg download_binary "$EDITION_NAME")"
    log_info "$(msg download_url "$download_url")"

    if ! download_file "$download_url" "$BINARY_PATH" "$EDITION_NAME"; then
        ui_msgbox "$(msg title_error)" "$(msg download_failed)"
        return 1
    fi

    chmod +x "$BINARY_PATH"
    log_success "$(msg binary_installed "$EDITION_NAME" "$BINARY_PATH")"

    if ! check_systemd; then
        progress_add "$(msg progress_complete)"
        local content
        content=$(msg no_systemd_manual "$EDITION_NAME" "$BINARY_PATH" "$LISTEN_PORT")
        content="$(printf '%s\n\n%s' "$(msg sponsor_info)" "$content")"
        ui_msgbox "$(msg title_install_complete)" "$content"
        return
    fi

    progress_add "$(msg progress_service)"
    create_systemd_service "$LISTEN_PORT"

    systemctl daemon-reload
    systemctl enable ${SERVICE_NAME}.service
    systemctl start ${SERVICE_NAME}.service

    if systemctl is-active --quiet ${SERVICE_NAME}.service; then
        log_success "$(msg service_started)"

        progress_add "$(msg progress_complete)"
        show_access_info "$LISTEN_PORT"
    else
        ui_msgbox "$(msg title_error)" "$(msg service_start_failed "$SERVICE_NAME")"
        return 1
    fi
}

# Create systemd service file
create_systemd_service() {
    local port="$1"
    log_step "$(msg systemd_start)"

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

    log_success "$(msg systemd_created)"
}

# Show access information
show_access_info() {
    local port=${1:-$DEFAULT_PORT}
    local ip=$(hostname -I | awk '{print $1}')
    local content

    content=$(msg access_info \
        "$ip" "$port" \
        "$SERVICE_NAME" "$SERVICE_NAME" "$SERVICE_NAME" "$SERVICE_NAME" "$SERVICE_NAME")
    content="$(printf '%s\n\n%s' "$(msg sponsor_info)" "$content")"

    ui_msgbox "$(msg title_install_complete)" "$content"
}

# Remove historical upgrade backups
cleanup_backups() {
    progress_reset

    if ! ui_yesno "$(msg title_notice)" "$(msg cleanup_confirm)"; then
        log_info "$(msg cleanup_cancelled)"
        return 0
    fi

    log_step "$(msg cleanup_start)"
    rm -f -- "${BINARY_PATH}.backup."*

    local backup_dir
    for backup_dir in "$BACKUP_DIR" "$DATA_BACKUP_DIR"; do
        if [ -d "$backup_dir" ]; then
            find "$backup_dir" -maxdepth 1 -type f -name '*.zip' -delete
        fi
    done

    ui_msgbox "$(msg title_cleanup_complete)" "$(msg cleanup_complete "${BINARY_PATH}.backup.*" "$BACKUP_DIR" "$DATA_BACKUP_DIR")"
}

# Upgrade function
upgrade_komari() {
    progress_reset
    log_step "$(msg upgrade_start)"

    if ! is_installed; then
        ui_msgbox "$(msg title_error)" "$(msg not_installed)"
        return 1
    fi

    if ! check_systemd; then
        ui_msgbox "$(msg title_error)" "$(msg systemd_required)"
        return 1
    fi

    # 选择发行版本和发布通道
    select_edition
    select_channel

    log_step "$(msg stopping_service)"
    systemctl stop ${SERVICE_NAME}.service

    log_step "$(msg clearing_backups)"
    rm -f -- "${BINARY_PATH}.backup."*

    local backup_path="${BINARY_PATH}.backup.$(date +%Y%m%d_%H%M%S)"
    progress_add "$(msg progress_backup)"
    log_step "$(msg backing_up)"
    if ! cp "$BINARY_PATH" "$backup_path"; then
        log_error "$(msg backup_failed_log)"
        systemctl start ${SERVICE_NAME}.service
        ui_msgbox "$(msg title_error)" "$(msg backup_failed)"
        return 1
    fi

    local arch=$(detect_arch)
    local download_url=$(get_download_url "$arch")
    if [ $? -ne 0 ]; then
        log_error "$(msg download_url_failed_log)"
        mv "$backup_path" "$BINARY_PATH"
        systemctl start ${SERVICE_NAME}.service
        ui_msgbox "$(msg title_error)" "$(msg download_url_failed_restore)"
        return 1
    fi

    progress_add "$(msg progress_download)"
    log_step "$(msg downloading_latest "$EDITION_NAME")"
    if ! download_file "$download_url" "$BINARY_PATH" "$EDITION_NAME"; then
        log_error "$(msg download_failed_log)"
        mv "$backup_path" "$BINARY_PATH"
        systemctl start ${SERVICE_NAME}.service
        ui_msgbox "$(msg title_error)" "$(msg download_failed_restore)"
        return 1
    fi

    chmod +x "$BINARY_PATH"

    progress_add "$(msg progress_restart)"
    log_step "$(msg restart_start)"
    systemctl start ${SERVICE_NAME}.service

    if systemctl is-active --quiet ${SERVICE_NAME}.service; then
        progress_add "$(msg progress_complete)"
        ui_msgbox "$(msg title_upgrade_complete)" "$(msg upgrade_success "$EDITION_NAME" "$CHANNEL_NAME")"
    else
        ui_msgbox "$(msg title_error)" "$(msg upgrade_start_failed)"
    fi
}

# Uninstall function
uninstall_komari() {
    progress_reset
    log_step "$(msg uninstall_start)"

    if ! is_installed; then
        ui_msgbox "$(msg title_notice)" "$(msg not_installed)"
        return 0
    fi

    if ! ui_yesno "$(msg title_notice)" "$(msg confirm_uninstall)"; then
        log_info "$(msg uninstall_cancelled)"
        return 0
    fi

    if check_systemd; then
        log_step "$(msg stopping_disabling)"
        systemctl stop ${SERVICE_NAME}.service >/dev/null 2>&1
        systemctl disable ${SERVICE_NAME}.service >/dev/null 2>&1
        rm -f "/etc/systemd/system/${SERVICE_NAME}.service"
        systemctl daemon-reload
        log_success "$(msg systemd_removed)"
    fi

    log_step "$(msg deleting_binary)"
    rm -f "$BINARY_PATH"
    # 尝试在目录为空时删除该目录
    rmdir "$INSTALL_DIR" 2>/dev/null || log_info "$(msg data_dir_not_empty "$INSTALL_DIR")"
    log_success "$(msg binary_removed)"

    ui_msgbox "$(msg title_uninstall_complete)" "$(msg uninstall_complete "$DATA_DIR")"
}

# Show service status
show_status() {
    progress_reset

    if ! is_installed; then
        ui_msgbox "$(msg title_error)" "$(msg not_installed)"
        return
    fi
    if ! check_systemd; then
        ui_msgbox "$(msg title_error)" "$(msg systemd_required)"
        return
    fi
    log_step "$(msg service_status)"
    systemctl status ${SERVICE_NAME}.service --no-pager -l
    ui_pause
}

# Show service logs
show_logs() {
    progress_reset

    if ! is_installed; then
        ui_msgbox "$(msg title_error)" "$(msg not_installed)"
        return
    fi
    if ! check_systemd; then
        ui_msgbox "$(msg title_error)" "$(msg systemd_required)"
        return
    fi
    # 日志为实时流，直接在终端显示。
    log_step "$(msg service_logs)"
    journalctl -u ${SERVICE_NAME} -f --no-pager
}

# Restart service
restart_service() {
    progress_reset

    if ! is_installed; then
        ui_msgbox "$(msg title_error)" "$(msg not_installed)"
        return
    fi
    if ! check_systemd; then
        ui_msgbox "$(msg title_error)" "$(msg systemd_required)"
        return
    fi
    log_step "$(msg restart_start)"
    systemctl restart ${SERVICE_NAME}.service
    if systemctl is-active --quiet ${SERVICE_NAME}.service; then
        ui_msgbox "$(msg title_success)" "$(msg restart_success)"
    else
        ui_msgbox "$(msg title_error)" "$(msg restart_failed)"
    fi
}

# Stop service
stop_service() {
    progress_reset

    if ! is_installed; then
        ui_msgbox "$(msg title_error)" "$(msg not_installed)"
        return
    fi
    if ! check_systemd; then
        ui_msgbox "$(msg title_error)" "$(msg systemd_required)"
        return
    fi
    log_step "$(msg stop_start)"
    systemctl stop ${SERVICE_NAME}.service
    ui_msgbox "$(msg title_success)" "$(msg stop_success)"
}


# Main menu
main_menu() {
    if ! is_installed; then
        install_binary
        return
    fi

    while true; do
        local choice
        if ! choice=$(ui_menu "$(msg main_title)" "$(msg main_prompt)" \
            "1" "$(msg main_install)" \
            "2" "$(msg main_upgrade)" \
            "3" "$(msg main_uninstall)" \
            "4" "$(msg main_status)" \
            "5" "$(msg main_logs)" \
            "6" "$(msg main_restart)" \
            "7" "$(msg main_stop)" \
            "8" "$(msg main_cleanup)" \
            "9" "$(msg main_exit)"); then
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
            8) cleanup_backups ;;
            9) exit 0 ;;
            *) ui_msgbox "$(msg title_error)" "$(msg invalid_option)" ;;
        esac
        progress_reset
    done
}

# Main execution
check_root
init_colors
select_language
main_menu
