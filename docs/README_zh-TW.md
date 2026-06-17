# Komari

![Badge](https://hitscounter.dev/api/hit?url=https%3A%2F%2Fgithub.com%2Fkomari-monitor%2Fkomari&label=&icon=github&color=%23a370f7&message=&style=flat&tz=UTC)

![komari](https://socialify.git.ci/komari-monitor/komari/image?description=1&font=Inter&forks=1&issues=1&language=1&logo=https%3A%2F%2Fraw.githubusercontent.com%2Fkomari-monitor%2Fkomari-web%2Fd54ce1288df41ead08aa19f8700186e68028a889%2Fpublic%2Ffavicon.png&name=1&owner=1&pattern=Plus&pulls=1&stargazers=1&theme=Auto)

Komari 是一款輕量級的自託管伺服器監控工具，旨在提供簡單、高效的伺服器性能監控解決方案。它支援透過 Web 介面查看伺服器狀態，並透過輕量級 Agent 收集數據。

> [!WARNING]
> Komari 是一款自託管的監控/控制程式，僅應部署在你擁有或已獲授權管理的系統上。請勿將 Komari 武器化，或在未獲授權的情況下部署、存取、持久化、執行命令及從事其他濫用行為。關於現實中的濫用風險，可參考 Huntress 的分析：[Komari C2 agent abuse](https://www.huntress.com/blog/komari-c2-agent-abuse)。
> 使用者需要自行承擔部署和使用 Komari 的責任。開發者不對未經授權或濫用行為及其後果承擔責任。
> 在 Windows 端啟用遠端控制後，客戶端會在每次使用者登入時透過 Windows 通知提醒使用者 Komari 是一款遠端控制軟體。

[文檔](https://komari-document.pages.dev/) | [Telegram 群組](https://t.me/komari_monitor)

## 特性

- **輕量高效**：低資源佔用，適合各種規模的伺服器。
- **自託管**：完全掌控數據隱私，部署簡單。
- **Web 介面**：直觀的監控儀表盤，易於使用。

## 快速開始

### 0. 容器雲一鍵部署

- 雨雲雲應用 - CNY 4.5/月

[![](https://rainyun-apps.cn-nb1.rains3.com/materials/deploy-on-rainyun-cn.svg)](https://app.rainyun.com/apps/rca/store/6780/NzYxNzAz_)

- 1Panel 應用商店

已上架 1Panel 應用商店，應用商店-實用工具-Komari 即可安裝

### 1. 使用一鍵安裝腳本

適用於使用了 systemd 的發行版（Ubuntu、Debian...）。

```bash
curl -fsSL https://raw.githubusercontent.com/komari-monitor/komari/main/install-komari.sh -o install-komari.sh
chmod +x install-komari.sh
sudo ./install-komari.sh
```

### 2. Docker 部署

1. 建立資料目錄：
   ```bash
   mkdir -p ./data
   ```
2. 執行 Docker 容器：
   ```bash
   docker run -d \
     -p 25774:25774 \
     -v $(pwd)/data:/app/data \
     --name komari \
     ghcr.io/komari-monitor/komari:latest
   ```
3. 查看預設帳號和密碼：
   ```bash
   docker logs komari
   ```
4. 在瀏覽器中存取 `http://<your_server_ip>:25774`。

> [!NOTE]
> 你也可以透過環境變數 `ADMIN_USERNAME` 和 `ADMIN_PASSWORD` 自訂初始使用者名稱和密碼。

### 3. 二進位檔案部署

1. 存取 Komari 的 [GitHub Release 頁面](https://github.com/komari-monitor/komari/releases) 下載適用於你作業系統的最新二進位檔案。
2. 執行 Komari：
   ```bash
   ./komari server -l 0.0.0.0:25774
   ```
3. 在瀏覽器中存取 `http://<your_server_ip>:25774`，預設監聽 `25774` 連接埠。
4. 預設帳號和密碼可在啟動日誌中查看，或透過環境變數 `ADMIN_USERNAME` 和 `ADMIN_PASSWORD` 設定。

> [!NOTE]
> 確保二進位檔案具有可執行權限（`chmod +x komari`）。資料將保存在執行目錄下的 `data` 資料夾中。

### 手工建置

#### 依賴

- Go 1.18+ 和 Node.js 20+（手工建置）

1. 建置前端靜態檔案：
   ```bash
   git clone https://github.com/komari-monitor/komari-web
   cd komari-web
   npm install
   npm run build
   ```
2. 建置後端：
   ```bash
   git clone https://github.com/komari-monitor/komari
   cd komari
   ```
   將步驟1中產生的靜態檔案複製到 `komari` 專案根目錄下的 `/web/public/defaultTheme/dist` 資料夾，並將 `komari-theme.json` 與 `preview.png`/`perview.png` 複製到 `/web/public/defaultTheme`。
   ```bash
   go build -o komari
   ```
3. 執行：
   ```bash
   ./komari server -l 0.0.0.0:25774
   ```
   預設監聽 `25774` 連接埠，存取 `http://localhost:25774`。

## 前端開發指南

[Komari 主題開發指南 | Komari](https://komari-document.pages.dev/dev/theme.html)

[在 Crowdin 上翻譯 Komari](https://crowdin.com/project/komari/invite?h=cd051bf172c9a9f7f1360e87ffb521692507706)

## 客戶端 Agent 開發指南

[Komari Agent 資訊上報與事件處理文檔](https://komari-document.pages.dev/dev/agent.html)

## 貢獻

歡迎提交 Issue 或 Pull Request！

## 鳴謝

### 破碎工坊雲

[破碎工坊雲 - 專業雲計算服務平台，提供高效、穩定、安全的高防伺服器與CDN解決方案](https://www.crash.work/)

### DreamCloud

[DreamCloud - 極高性價比解鎖直連亞太高防](https://as211392.com/)

### 🚀 由 SharonNetworks 贊助

[![Sharon Networks](https://raw.githubusercontent.com/komari-monitor/public/refs/heads/main/images/sharon-networks.webp)](https://sharon.io)

SharonNetworks 為您的業務起飛保駕護航！

亞太資料中心提供頂級的中國優化網路接入 · 低延遲 & 高頻寬 & 提供 Tbps 級本地清洗高防服務，為您的業務保駕護航，為您的客戶提供極致體驗。加入社群 [Telegram 群組](https://t.me/SharonNetwork) 可參與公益募捐或群內抽獎免費使用。

### 開源社群

提交 PR、製作主題的各位開發者

—— 以及：感謝我自己能這麼閒

## Star History

[![Star History Chart](https://api.star-history.com/svg?repos=komari-monitor/komari&type=Date)](https://www.star-history.com/#komari-monitor/komari&Date)
