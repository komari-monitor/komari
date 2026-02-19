# Komari 节点 IP 同步到 Git（社区脚本）

> 说明：这是社区提供的外部同步脚本，不是 Komari 内置核心功能。

该方案会从 `komari.db` 的 `clients` 表导出节点 IP 快照，按定时任务自动提交到 Git 仓库。

## 文件
- `komari-git-sync.sh`
- `komari-git-sync.env.example`
- `komari-git-sync.service`
- `komari-git-sync.timer`

## 默认行为
- 单向同步：服务器 -> Git
- 默认每 1 小时执行一次（可在 timer 中调整）

## 快速使用
1. 安装脚本与 systemd 文件
2. 编辑 `/etc/komari-git-sync.env`
3. 启动 timer

详见仓库内脚本注释。
