---
title: Project Structure
nav_title: Structure
description: Responsibility boundaries across the CLI, server, desktop app, adapters, and docs site.
order: 3
---

# Project Structure

The repository contains the CLI/TUI, local Server, Electron desktop app, IM adapters, and documentation site. This map lists stable responsibility boundaries rather than every file.

```text
.
├── backend/
│   ├── cmd/claude-haha/       # Go CLI / server / adapter / worker entry
│   └── internal/
│       ├── application/      # Production wiring and shared session factory
│       ├── server/           # REST, WebSocket, persistence, session lifecycle
│       ├── clibootstrap/     # Agent execution graph, tools, permissions
│       ├── tui/              # Native Go interactive terminal
│       ├── imruntime/        # Eight platforms and shared messages/attachments/bridge
│       └── providerproxy/    # Provider protocol conversion
├── bin/claude-haha           # Native Go binary resolution and exec
├── desktop/
│   ├── src/                  # React renderer: pages, components, stores, API client
│   ├── electron/             # Electron main/preload, IPC, native services
│   ├── scripts/              # Go sidecar build, native resources, packaging
│   ├── sidecars/             # Retained TypeScript reference sidecar entry
│   └── src-tauri/            # Native resources and compatibility entry
├── src/                     # TypeScript reference implementation and parity tests
├── adapters/                # TypeScript IM reference implementation and tests
├── runtime/                 # macOS / Windows Computer Use helper resources
├── scripts/                 # Builds, quality gates, parity, release, maintenance
├── tests/                   # Cross-boundary tests and fixtures
├── site/                    # React docs site, content index, static build
└── docs/                    # Chinese and English Markdown documentation
```
## Runtime boundaries

| Entry | Runtime | Responsibility |
|---|---|---|
| `backend/cmd/claude-haha/main.go` | Go | CLI, TUI, server, adapter modes |
| `desktop/electron/main.ts` | Electron | Native desktop host |
| `desktop/src/` | Chromium | React UI |
| `src/entrypoints/cli.tsx`, `src/server/index.ts` | Bun | Reference implementation and deterministic tests |

Put business APIs in `backend/internal/server/` and wire them in `application/`; platform differences belong in `imruntime/`, and native desktop capabilities in `desktop/electron/`. The renderer uses defined APIs to reach these capabilities.
