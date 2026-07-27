<div align="center">
  <img src="./build/appicon.png" width="120" alt="樱花猫 Logo" />
  <h1>樱花猫 · Sakura Cat</h1>
  <p>Telegram 媒体库 · 基于 MTProto 协议的桌面端播放与下载工具</p>
</div>

![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)
![Vue](https://img.shields.io/badge/Vue-3-42b883?logo=vuedotjs&logoColor=white)
![Wails](https://img.shields.io/badge/Wails-v3_alpha-F48041)
![License](https://img.shields.io/badge/License-AGPL--3.0-blue.svg)
![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-lightgrey)

樱花猫是一款浏览 Telegram 频道与对话、流畅下载媒体资源、边下边播并支持「一起看」同步放映的桌面应用。基于 Wails3 + Vue3 构建,数据全部存储在本地。

---

## 目录

- [特性](#特性)
- [技术栈](#技术栈)
- [环境要求](#环境要求)
- [快速开始](#快速开始)
- [项目结构](#项目结构)
- [开发文档](#开发文档)
- [下载](#下载)
- [贡献](#贡献)
- [协议](#协议)
- [致谢](#致谢)

---

## 特性

### 📥 下载

- **MTProto 原生接入** —— 基于 [gotd/td](https://github.com/gotd/td) 直接对接 Telegram 官方协议,无需第三方 API 中转
- **自适应限流** —— 自动应对 `FLOOD_WAIT`:冷却 → 降速 → 逐步加速,在合规前提下尽量利用并发
- **全局并发调度** —— 全局最多 10 个文件并发,任务级 worker pool 可配置
- **限速与重试** —— 全局速率限制、失败自动重试(最多 5 次)、支持暂停 / 恢复
- **断点续传** —— 下载进度持久化到本地 SQLite,中断后可继续

### ▶️ 播放

- **边下边播** —— 独立分集流式 HTTP 服务,绕过全量缓冲,不落盘即可播放
- **弹幕** —— 内置弹幕引擎
- **一起看** —— 本地同步放映房间,生成邀请二维码,观看端浏览器扫码即可加入

### 🎨 体验

- **多窗口** —— 单例 / 多实例 / 主窗口三级模型,自定义顶栏,支持拖拽与最大化联动
- **暗色模式** —— 跟随系统或手动切换
- **命名模板** —— 灵活的文件命名与任务模板
- **代理配置** —— 支持 SOCKS / HTTP 代理
- **纯本地存储** —— SQLite 数据库,无外部依赖,数据完全留在你的机器上

## 技术栈

| 层 | 技术 |
|---|---|
| 桌面框架 | Wails v3(alpha) |
| 后端 | Go 1.25 · gotd/td(MTProto) · modernc.org/sqlite(纯 Go SQLite,无 CGO) · golang.org/x/time |
| 前端 | Vue 3 · Naive UI · Pinia · Vue Router · Vite 8 · TypeScript |
| 实时通信 | WebSocket(gorilla/websocket · coder/websocket) |
| 其他 | danmaku 弹幕引擎 |

## 环境要求

- **Go** >= 1.25
- **Node.js** >= 20(含 npm)
- **Wails3 CLI**(见下文安装)
- 平台依赖:
  - **Windows**:WebView2 Runtime(Win11 已预装)
  - **macOS**:Xcode Command Line Tools
  - **Linux**:webkit2gtk-4.1 等,详见 [Wails 文档](https://v3.wails.io/)

## 快速开始

### 1. 安装 Wails3 CLI

```bash
go install github.com/wailsapp/wails/v3/cmd/wails3@latest
```

### 2. 克隆仓库

```bash
git clone https://github.com/<your-name>/<your-repo>.git
cd <your-repo>
```

### 3. 安装前端依赖

```bash
cd frontend
npm install
cd ..
```

### 4. 开发模式运行

```bash
wails3 dev
```

开发模式下前端热更新,后端 Go 文件变更自动重编译。

### 5. 构建

```bash
wails3 build
```

构建产物位于 `build/bin/`。

## 项目结构

```
tg-download/
├── main.go                 # 应用入口:初始化 DB / 服务 / 窗口
├── internal/
│   ├── telegram/           # MTProto 客户端、鉴权、频道、消息、代理
│   ├── download/           # 下载调度、扫描、监听、自适应限流、存储
│   ├── events/             # Wails 事件分发
│   ├── settings/           # 应用设置
│   ├── template/           # 命名与任务模板
│   └── db/                 # SQLite schema
├── services/               # Wails 绑定服务层(鉴权 / 下载 / 视频流 / 一起看 / …)
├── frontend/               # Vue3 前端
│   └── src/
│       ├── views/          # 页面:发现 / 分类 / 下载 / 设置 / 预览 / 一起看
│       ├── components/     # 通用与业务组件
│       ├── composables/    # 组合式函数
│       ├── stores/         # Pinia 状态
│       ├── services/       # 后端绑定封装
│       └── layouts/        # 布局:主 / 空白 / 预览
├── build/                  # 各平台构建资源(Windows / macOS / Linux / iOS / Android)
└── Taskfile.yml
```

## 开发文档

前端与后端的窗口管理、路由守卫、多窗口生命周期等交互机制详见 [FRONTEND.md](./FRONTEND.md)。

## 下载

预编译安装包请前往 [Releases](https://github.com/<your-name>/<your-repo>/releases) 页面下载。

支持平台:

- Windows(x64)
- macOS(Apple Silicon / Intel)
- Linux

## 贡献

欢迎通过 Issue 反馈问题或提出建议;Pull Request 请先在 Issue 中讨论。

提交前请确保:

- `wails3 dev` 可正常启动
- 无新增敏感信息(密钥、Token、个人配置等)
- 代码风格与现有目录约定保持一致

## 协议

本项目基于 [AGPL-3.0](./LICENSE) 协议开源。

这意味着:任何对本项目的修改与分发,以及将其作为网络服务对外提供(即 SaaS 场景),都必须以 AGPL-3.0 协议开源完整源代码。

## 致谢

- [Wails](https://wails.io/) —— Go + Web 桌面应用框架
- [gotd/td](https://github.com/gotd/td) —— Telegram MTProto 客户端库
- [Naive UI](https://www.naiveui.com/) —— Vue 3 组件库
- [modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite) —— 纯 Go SQLite 驱动
- 所有为开源生态做出贡献的开发者

---

> 本项目仅供学习与个人使用,请遵守所在地区法律法规及 Telegram 服务条款。
