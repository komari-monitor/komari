
# Komari 
![Badge](https://hitscounter.dev/api/hit?url=https%3A%2F%2Fgithub.com%2Fkomari-monitor%2Fkomari&label=&icon=github&color=%23a370f7&message=&style=flat&tz=UTC)

![komari](https://socialify.git.ci/komari-monitor/komari/image?description=1&font=Inter&forks=1&issues=1&language=1&logo=https%3A%2F%2Fraw.githubusercontent.com%2Fkomari-monitor%2Fkomari-web%2Fd54ce1288df41ead08aa19f8700186e68028a889%2Fpublic%2Ffavicon.png&name=1&owner=1&pattern=Plus&pulls=1&stargazers=1&theme=Auto)

Komari 是一款轻量级的自托管服务器监控工具，旨在提供简单、高效的服务器性能监控解决方案。它支持通过 Web 界面查看服务器状态，并通过轻量级 Agent 收集数据。

[文档](https://komari-document.pages.dev/) | [文档(镜像站 By Geekertao)](https://www.komari.wiki) | [Telegram 群组](https://t.me/komari_monitor)

## 特性
- **轻量高效**：低资源占用，适合各种规模的服务器。
- **自托管**：完全掌控数据隐私，部署简单。
- **Web 界面**：直观的监控仪表盘，易于使用。

## 快速开始

### 0. 容器云一键部署

- 雨云云应用 - CNY 4.5/月 

[![](https://rainyun-apps.cn-nb1.rains3.com/materials/deploy-on-rainyun-cn.svg)](https://app.rainyun.com/apps/rca/store/6780/NzYxNzAz_) 

### 1. 使用一键安装脚本
适用于使用了 systemd 的发行版（Ubuntu、Debian...）。
```bash
curl -fsSL https://raw.githubusercontent.com/komari-monitor/komari/main/install-komari.sh -o install-komari.sh
chmod +x install-komari.sh
sudo ./install-komari.sh
```

### 2. Docker 部署
1. 创建数据目录：
   ```bash
   mkdir -p ./data
   ```
2. 运行 Docker 容器：
   ```bash
   docker run -d \
     -p 25774:25774 \
     -v $(pwd)/data:/app/data \
     --name komari \
     ghcr.io/komari-monitor/komari:latest
   ```
3. 查看默认账号和密码：
   ```bash
   docker logs komari
   ```
4. 在浏览器中访问 `http://<your_server_ip>:25774`。

> [!NOTE]
> 你也可以通过环境变量 `ADMIN_USERNAME` 和 `ADMIN_PASSWORD` 自定义初始用户名和密码。

### 3. 二进制文件部署
1. 访问 Komari 的 [GitHub Release 页面](https://github.com/komari-monitor/komari/releases) 下载适用于你操作系统的最新二进制文件。
2. 运行 Komari：
   ```bash
   ./komari server -l 0.0.0.0:25774
   ```
3. 在浏览器中访问 `http://<your_server_ip>:25774`，默认监听 `25774` 端口。
4. 默认账号和密码可在启动日志中查看，或通过环境变量 `ADMIN_USERNAME` 和 `ADMIN_PASSWORD` 设置。

> [!NOTE]
> 确保二进制文件具有可执行权限（`chmod +x komari`）。数据将保存在运行目录下的 `data` 文件夹中。


### 手工构建

#### 依赖

- Go 1.18+ 和 Node.js 20+（手工构建）

1. 构建前端静态文件：
   ```bash
   git clone https://github.com/komari-monitor/komari-web
   cd komari-web
   npm install
   npm run build
   ```
2. 构建后端：
   ```bash
   git clone https://github.com/komari-monitor/komari
   cd komari
   ```
   将步骤1中生成的静态文件复制到 `komari` 项目根目录下的 `/public/dist` 文件夹。
   ```bash 
   go build -o komari
   ```
4. 运行：
   ```bash
   ./komari server -l 0.0.0.0:25774
   ```
   默认监听 `25774` 端口，访问 `http://localhost:25774`。

## 前端开发指南
[Komari 主题开发指南 | Komari](https://komari-document.pages.dev/dev/theme.html)

[在 Crowdin 上翻译 Komari](https://crowdin.com/project/komari/invite?h=cd051bf172c9a9f7f1360e87ffb521692507706)

## 客户端 Agent 开发指南
[Komari Agent 信息上报与事件处理文档](https://komari-document.pages.dev/dev/agent.html)

## 贡献
欢迎提交 Issue 或 Pull Request！

## 鸣谢

### DreamCloud

[DreamCloud - 极高性价比解锁直连亚太高防](https://as211392.com/)

### 🚀 由 SharonNetworks 赞助

[![Sharon Networks](https://raw.githubusercontent.com/komari-monitor/public/refs/heads/main/images/sharon-networks.webp)](https://sharon.io) 

SharonNetworks 为您的业务起飞保驾护航！

亚太数据中心提供顶级的中国优化网络接入 · 低延时 & 高带宽 & 提供 Tbps 级本地清洗高防服务，为您的业务保驾护航，为您的客户提供极致体验。加入社区 [Telegram 群组](https://t.me/SharonNetwork) 可参与公益募捐或群内抽奖免费使用。

### 开源社区

提交 PR、制作主题的各位开发者

—— 以及：感谢我自己能这么闲

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=komari-monitor/komari&type=Date)](https://www.star-history.com/#komari-monitor/komari&Date)
