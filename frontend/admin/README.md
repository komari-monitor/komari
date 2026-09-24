# SIXOSN Komari Web

This directory is the integrated administration frontend for [`SIXOSN/komari`](https://github.com/SIXOSN/komari). It is built together with the Glassmorphism public frontend in `../public`.

## 项目关系说明

本仓库基于 [`komari-monitor/komari-web`](https://github.com/komari-monitor/komari-web) 开发，由 SIXOSN 独立维护，不属于上游官方发行版。依法需要保留的许可证与 Git 历史署名不受本说明影响。

本前端只面向 SIXOSN 定制服务端：增加按节点设置的流量周期与重置时区，安装命令指向 SIXOSN Agent，并移除远程命令、Web SSH、网页终端、文件管理和文件传输界面。

## 主要变更

- 在“服务器 → 编辑 → 流量阈值”中设置每台服务器的重置日、时间和 IANA 时区。
- Agent 安装地址切换为 `SIXOSN/komari-agent`。
- Docker Agent 使用 `ghcr.io/sixosn/komari-agent:snapshot`。
- 删除远程控制相关路由、菜单、依赖与构建产物。
- 管理界面与 Glassmorphism 一并由主仓构建；页面参数在“系统 → 页面设置”管理。

## 开发环境

建议使用 Node.js 22 或更高版本。

```bash
npm install
```

复制 `.env.example` 为 `.env.development`，并设置开发后端：

```env
VITE_API_TARGET=http://127.0.0.1:25774
```

启动与构建：

```bash
npm run dev
npm run build
```

同步翻译检查：

```bash
npm run i18n:sync:dry
```

## 配套仓库

- 服务端：[`SIXOSN/komari`](https://github.com/SIXOSN/komari)
- Agent：[`SIXOSN/komari-agent`](https://github.com/SIXOSN/komari-agent)
- 默认主题：[`SIXOSN/komari-theme-Glassmorphism`](https://github.com/SIXOSN/komari-theme-Glassmorphism)
