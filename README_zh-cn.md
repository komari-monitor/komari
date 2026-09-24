# SIXOSN Komari

[English](./README.md) | [简体中文](./README_zh-cn.md)

SIXOSN Komari 是一个注重安全、独立维护的服务器监控发行版。在保留轻量监控体验的基础上，提供专用默认主题、按节点设置的流量周期，并主动缩减远程控制攻击面。

> [!IMPORTANT]
> 本发行版只接受配套的 [`SIXOSN/komari-agent`](https://github.com/SIXOSN/komari-agent)，其他发行版的 Agent 会被主动拒绝。

## 项目关系说明

本仓库基于 [`komari-monitor/komari`](https://github.com/komari-monitor/komari) 开发，由 SIXOSN 独立维护，不属于上游官方发行版，也不应默认获得上游支持或兼容性保证。

服务端与 Agent 增加了 SIXOSN 发行版双向身份校验，因此兼容性有意区别于上游。源码许可证及依法需要保留的署名位于 [LICENSE](./LICENSE) 与 [NOTICE](./NOTICE)；本说明不能替代这些法律文件。

## 定制特性

- Glassmorphism 公开界面和管理前端源码均内置于本仓库。
- 每台服务器可分别设置流量重置日、重置时间及 IANA 时区。
- SIXOSN 服务端与 Agent 进行双向发行版身份校验。
- 移除远程命令、Web SSH、网页终端、文件管理和文件传输接口。
- 升级沿用现有数据库与服务器设置；新增流量周期字段默认关闭。
- 保留实时指标、历史记录、插件及自托管管理功能。

## 相关仓库

- 服务端：[`SIXOSN/komari`](https://github.com/SIXOSN/komari)
- Agent：[`SIXOSN/komari-agent`](https://github.com/SIXOSN/komari-agent)
- 前端源码：[`frontend/public`](./frontend/public) 和 [`frontend/admin`](./frontend/admin)

## Docker 部署

```bash
docker run -d \
  --name komari \
  --restart always \
  -p 25774:25774 \
  -v /path/to/komari-data:/app/data \
  ghcr.io/sixosn/komari:1.5.0-fix6
```

替换现有容器前，请先备份挂载的数据目录。

## Agent 安装

请在管理面板中创建或选择节点，并按照面板内显示的安装指引操作。本 README 不公开安装命令、凭据或兼容性实现细节。

## 安全范围

本项目仅应用于你拥有或获授权管理的系统。移除远程控制功能可以减少攻击面，但不能替代 TLS、访问控制、系统加固、数据备份和及时更新依赖。
