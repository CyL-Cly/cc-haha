---
title: 项目结构
nav_title: 项目结构
description: 仓库的目录职责边界：CLI、Server、桌面端、Adapter 与文档站。
order: 3
---

# 项目结构

本项目包含 CLI/TUI、本地 Server、Electron 桌面端、IM Adapter 和文档站。下面只列稳定的职责边界；具体文件以当前源码为准。

```text
.
├── backend/
│   ├── cmd/claude-haha/       # Go CLI / Server / Adapter / worker 统一入口
│   └── internal/
│       ├── application/      # 生产服务接线、CLI / 桌面会话工厂
│       ├── server/           # REST、WebSocket、持久化与会话生命周期
│       ├── clibootstrap/     # Agent 主执行图、工具和权限接线
│       ├── tui/              # Go 交互式终端
│       ├── imruntime/        # 八个平台与共享 IM 消息、附件和会话桥
│       └── providerproxy/    # Provider 协议转换
├── bin/claude-haha           # 原生 Go CLI 文件定位与 exec
├── desktop/
│   ├── src/                  # React Renderer：页面、组件、stores、API 客户端
│   ├── electron/             # Electron main / preload、IPC 和系统服务
│   ├── scripts/              # Go sidecar 构建、原生资源与桌面打包
│   ├── sidecars/             # 保留的 TypeScript Source sidecar 对照入口
│   └── src-tauri/            # 原生资源及兼容入口
├── src/                     # TypeScript 原实现、Source 差分对照与测试
├── adapters/                # TypeScript IM Source 对照与测试
├── runtime/                 # macOS / Windows Computer Use helper 资源
├── scripts/                 # 构建、质量门禁、差分评测、发布和维护
├── tests/                   # 跨边界测试与资源
├── site/                    # React 文档站、索引和静态构建
└── docs/                    # 中英文 Markdown 文档
```
## 运行边界

| 入口 | 运行时 | 说明 |
|---|---|---|
| `backend/cmd/claude-haha/main.go` | Go | CLI、TUI、Server 和 Adapter 模式 |
| `desktop/electron/main.ts` | Electron | 原生桌面 Host |
| `desktop/src/` | Chromium | React UI |
| `src/entrypoints/cli.tsx`、`src/server/index.ts` | Bun | Source 对照和确定性测试 |

新增业务 API 放进 `backend/internal/server/` 并在 `application/` 接线；平台差异放进 `imruntime/`，原生桌面能力放进 `desktop/electron/`。Renderer 通过已定义的 API 使用这些能力。
