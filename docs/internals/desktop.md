---
title: 桌面端架构
nav_title: 桌面端架构
description: Electron 主进程、Go Server Sidecar、Agent 会话与 IM Adapter 的进程边界。
order: 1
---

# 桌面端架构

桌面端不是把 CLI 直接嵌进 React，而是 React Renderer、Electron Host 和 Go sidecar 协作跑起来的。维护时踩的坑，多半来自跨过了本该关死的那道边界。

## 进程分布

生产入口的进程分布：

```text
Electron main
├── Chromium renderer（React UI）
├── claude-sidecar-go server
│   ├── Go net/http API 与 WebSocket 会话网关
│   └── 进程内 application.DesktopFactory / MainPrint Agent 会话
└── claude-sidecar-go adapters（按平台独立启动）
    ├── Telegram
    ├── Feishu
    ├── WeChat
    ├── DingTalk
    ├── WhatsApp
    ├── WeCom
    ├── QQ
    └── Slack
```

- **Electron main** 负责窗口、系统对话框、更新、终端、原生预览、宠物窗口和 Sidecar 生命周期。
- **Renderer** 只负责界面，通过 preload 暴露的 `window.desktopHost` 使用原生能力。
- **Server Sidecar** 是桌面端和 H5 共用的本地服务，提供 REST、WebSocket、Provider 代理和会话管理。
- **Go Agent 会话** 使用与终端 CLI 相同的执行图，执行模型请求、工具调用和 Agent 编排。
- **Adapter Sidecar** 把 IM 平台消息桥接到同一套 Server/CLI 会话。

`desktop/src-tauri/` 目前只是保留打包资源和历史代码的位置，不是桌面运行时。当前桌面 Host 是 Electron。

## 当前技术栈

| 层 | 主要技术 | 用途 |
|---|---|---|
| Renderer | React 18、Vite 8、TypeScript、Zustand 5 | 桌面 UI 与状态管理 |
| 样式与内容 | Tailwind CSS 4、Marked、DOMPurify、Shiki 4、Mermaid、KaTeX | 布局、Markdown、代码和图表 |
| Diff | `react-diff-viewer-continued` | 对话和工作区中的 Diff 展示 |
| Electron Host | Electron、electron-builder、electron-updater | 原生窗口、打包和更新 |
| 终端 | node-pty、xterm.js | 原生 PTY 与终端渲染 |
| 本地服务 | Go、`net/http`、WebSocket | HTTP API 与 WebSocket |

版本以 `desktop/package.json` 为准。本页只列会影响架构理解的主版本，避免复制完整依赖清单。

## Electron Host

核心入口与边界：

| 路径 | 职责 |
|---|---|
| `desktop/electron/main.ts` | Electron main 入口、窗口与 IPC 注册 |
| `desktop/electron/preload.ts` | 向主 renderer 暴露类型化 Host API |
| `desktop/electron/preview-preload.ts` | 原生网页预览的隔离桥接 |
| `desktop/electron/pet-preload.ts` | 宠物窗口的最小能力桥接 |
| `desktop/electron/ipc/` | IPC channel 注册和 payload 校验 |
| `desktop/electron/services/` | Sidecar、更新、终端、预览、窗口、代理等系统服务 |

Renderer 不应直接导入 Electron，也不应自行拼接任意 IPC channel。新增原生能力时，应同时更新 Host contract、main 侧校验和相关测试。

### 启动流程

1. Electron 解析默认或便携存储模式，并准备应用配置目录。
2. Host 选择可用端口，启动统一的 `claude-sidecar-go server` 二进制。
3. Go sidecar 从 `backend/cmd/claude-haha/main.go` 进入 `application.RunServer`，在同一 listener 上承载 HTTP 和 WebSocket。
4. 健康检查通过后，Renderer 获取 loopback Server URL 并加载会话与设置。
5. Host 根据已配置的平台启动对应 Adapter Sidecar。
6. Server 按需在进程内构造 Agent 会话；桌面会话和终端 CLI 共用 `MainPrint`。

Server 可绑定局域网可访问地址以支持 H5，但桌面 Renderer 使用 loopback 控制地址。H5、远程访问和宠物窗口分别经过其对应的 Token 与能力限制，不能因为本地 Server 已启动就假定所有来源都可信。

### Sidecar 入口

桌面构建脚本 `desktop/scripts/go-sidecar-build.ts` 编译 `backend/cmd/claude-haha`，按目标平台生成 `claude-sidecar-go-<target>`。同一个原生文件提供三种业务模式：

```text
claude-sidecar-go server   --app-root <path> --host <host> --port <port>
claude-sidecar-go cli      --app-root <path> [CLI arguments]
claude-sidecar-go adapters --app-root <path> --telegram|--feishu|--wechat|--dingtalk|--whatsapp|--wecom|--qq|--slack
```

`desktop/sidecars/claude-sidecar.ts` 是保留的 TypeScript Source 对照入口，不是桌面业务 sidecar 的构建目标。语音、Computer Use 和原生媒体资源由各自的构建步骤准备。

## Go Server 与 Agent

| 路径 | 职责 |
|---|---|
| `backend/cmd/claude-haha/main.go` | 原生 CLI、Server、Adapter 和隔离 worker 模式分发 |
| `backend/internal/application/server.go` | Go Server 生产服务和工厂接线 |
| `backend/internal/server/` | REST、WebSocket、访问控制与会话生命周期 |
| `backend/internal/application/desktop.go` | 桌面会话构造与配置、消息投影 |
| `backend/internal/clibootstrap/main_print.go` | CLI / 桌面 Agent 共用主执行图 |
| `backend/internal/imruntime/` | 八个平台的消息、附件与会话桥 |

HTTP、聊天 WebSocket、内部 SDK 消息、OAuth 回调和 H5 静态资源保持原有客户端契约。新增 API 时仍须确定它属于本地桌面、H5、宠物、内部 SDK 还是公开资源，并覆盖访问控制和错误传播。

## WebSocket 语义

Renderer 为每个会话维护一条连接：

```text
ws://<server>/ws/<sessionId>
```

如果 Server 启用了认证，客户端会把 Token 放在连接查询参数中。连接成功后，Server 会发送当前会话标识、尚未处理的权限请求快照和运行状态。

### 心跳与重连

当前 `desktop/src/api/websocket.ts` 的行为是：

- 连接后每 30 秒发送一次 `ping`。
- 10 秒内没有收到 `pong`，客户端主动关闭连接并进入重连。
- 重连延迟从 1 秒开始指数增长，最高封顶 30 秒。
- 自动重连没有固定的最大尝试次数；显式关闭会话才停止。
- 断线期间发送的消息进入内存队列。
- 重连成功后先发送队列中的消息，再发送 `sync_state` 获取 Server 的权威运行状态。

如果你在别处读到过“最多重试 10 次”，那是过期说法，以上面这几条为准。

### 客户端断开不等于停止任务

最后一个客户端断开后：

- 正在运行的前台回合或后台任务继续执行。
- 工作结束后才进入空闲宽限期。
- 客户端在宽限期内重连会取消清理。
- 超过宽限期且没有客户端，Server 才清理对应 Agent 会话。
- 等待权限的会话有独立的有界清理策略，避免永久占用运行时。

这让手机锁屏、Renderer 刷新或短暂网络切换不会直接中断正在运行的任务。

## CLI 与 Provider 代理

Server 将同一 Go Agent 会话的输出、权限请求、工具结果和后台任务状态投影到原有 WebSocket 协议。会话的 Provider、模型、effort 和权限模式由 Server 和 Agent 共同维护，Renderer 不是唯一真相来源。

Go `backend/internal/providerproxy/` 处理支持的 Provider 协议：

- Anthropic Messages
- OpenAI Chat Completions
- OpenAI Responses

Provider 的模型映射、认证方式和上下文设置以实际 Provider 配置为准，不应在架构文档中硬编码厂商清单。

## IM Adapter

每个平台使用独立 Adapter Sidecar，避免一个平台的凭据或启动失败拖垮其他平台。

```text
IM 平台 → Go imruntime → 共享 WebSocket bridge → Go Server → Go Agent 会话
```

`backend/internal/imruntime/` 负责配置、配对、会话映射、消息队列、去重和附件。Telegram、飞书、微信、钉钉、WhatsApp、企业微信、QQ 和 Slack 分别运行独立的 Adapter 进程。空闲桥会释放 WebSocket；有前台、后台任务或待审批动作时保留连接，下一条消息仍恢复同一 session。

存储迁移由桌面 Host 和 Go Server 的 preview/quiesce/validate/activate/recover 协议协调；Adapter 必须完成队列、附件和凭据写入并真实关闭后才能确认 quiesced。
## 持久化边界

不同数据不共享同一个存储：

| 数据 | 主要边界 |
|---|---|
| Renderer UI 偏好与打开标签 | 浏览器存储及其迁移 |
| 会话与消息 | Server/CLI 管理的本地会话数据 |
| Provider、H5、Computer Use 等设置 | Server 管理的配置文件 |
| Electron 窗口和原生状态 | Electron user data / 应用模式目录 |
| IM 配置和配对状态 | Adapter 配置与各平台状态目录 |

任何 JSON、`localStorage` 或应用配置形状变化都必须带前向迁移和旧数据回归测试。不要直接覆盖用户的共享 Claude 配置，也不要在测试中读取真实用户目录。

## 构建与验证

桌面构建由 `desktop/package.json` 的脚本编排：

```bash
cd desktop
bun run build:sidecars
bun run build
bun run build:electron
```

`electron:package` 在此基础上运行 electron-builder。打包产物验证只能证明资源和安装包结构正确；涉及窗口、权限、终端、预览、更新或 Computer Use 的用户流程，还需要真实桌面 Smoke 验证。
