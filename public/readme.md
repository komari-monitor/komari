# Frontend Build Instructions / 前端构建说明 / フロントエンド構築手順

## English

### Frontend Repository

- **Frontend project repository**: https://github.com/komari-monitor/komari-web

### Build Requirements

1. Clone the frontend repository and build the static files
2. Copy the generated static files to the `defaultTheme` folder in the Komari backend project root directory
3. Ensure the `defaultTheme/dist` folder contains `index.html`

### Important Note

⚠️ **The projects under Akizon77's personal repository are no longer maintained. Please use the projects under the organization (komari-monitor).**

---

## 中文

### 前端项目仓库

- **前端项目地址**: https://github.com/komari-monitor/komari-web

### 构建要求

1. 克隆前端仓库并构建静态文件
2. 将生成的静态文件复制到 Komari 后端项目根目录下的 `defaultTheme` 文件夹
3. 确保 `dist` 文件夹内包含 `index.html`

### 重要提醒

⚠️ **Akizon77 个人仓库的项目已经不再使用，请使用组织（komari-monitor）下的项目。**

---

## 日本語

### フロントエンドプロジェクトリポジトリ

- **フロントエンドプロジェクトアドレス**: https://github.com/komari-monitor/komari-web

### ビルド要件

1. フロントエンドリポジトリをクローンして静的ファイルをビルドする
2. 生成された静的ファイルを Komari バックエンドプロジェクトのルートディレクトリ下の `defaultTheme` フォルダーにコピーする
3. `dist` フォルダー内に `index.html` が含まれていることを確認する

### 重要な注意事項

⚠️ **Akizon77 の個人リポジトリのプロジェクトは使用されなくなりました。組織（komari-monitor）下のプロジェクトを使用してください。**

---

## Quick Setup / 快速设置 / クイックセットアップ

```bash
# Clone frontend repository / 克隆前端仓库 / フロントエンドリポジトリをクローン
git clone https://github.com/komari-monitor/komari-web
cd komari-web

# Install dependencies and build / 安装依赖并构建 / 依存関係をインストールしてビルド
npm install
npm run build

# Copy dist folder to Komari backend project / 将 dist 文件夹复制到 Komari 后端项目 / dist フォルダーを Komari バックエンドプロジェクトにコピー
cp -r dist /path/to/komari/public/defaultTheme
```
