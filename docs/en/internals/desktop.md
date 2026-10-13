---
title: Desktop Architecture
nav_title: Desktop
description: Process boundaries between the Electron main process, Go server sidecar, Agent sessions, and IM adapters.
order: 1
---

# Desktop Architecture

The desktop app does not embed the CLI inside React. It is a React renderer, an Electron host, and Go sidecars cooperating, and most maintenance mistakes come from crossing a boundary that was meant to stay closed.

## Process map

```text
Electron main
├── Chromium renderer (React UI)
├── claude-sidecar-go server
│   ├── Go net/http API and WebSocket gateway
│   └── In-process application.DesktopFactory / MainPrint Agent sessions
└── claude-sidecar-go adapters (launched per platform)
    ├── Telegram
    ├── Feishu
    ├── WeChat
    ├── DingTalk
    ├── WhatsApp
    ├── WeCom
    ├── QQ
    └── Slack
```

- **Electron main** owns windows, native dialogs, updates, terminals, native preview, pet windows, and the sidecar lifecycle.
- **Renderer** owns the interface only. It reaches native capabilities through `window.desktopHost`, exposed by preload.
- **Server sidecar** is the local service shared by the desktop app and the H5 client: REST, WebSocket, provider proxy, and session management.
- **Go Agent sessions** execute model requests, tools, and orchestration with the same graph as the terminal CLI.
- **Adapter sidecar** bridges IM platform messages into the same server and Agent sessions.

`desktop/src-tauri/` now only holds packaging assets and historical code. It is not the desktop runtime; Electron is the current host.

## Current stack

| Layer | Main technologies | Purpose |
|---|---|---|
| Renderer | React 18, Vite 8, TypeScript, Zustand 5 | Desktop UI and state |
| Styling and content | Tailwind CSS 4, Marked, DOMPurify, Shiki 4, Mermaid, KaTeX | Layout, Markdown, code, diagrams |
| Diff | `react-diff-viewer-continued` | Diffs in chat and workspace |
| Electron host | Electron, electron-builder, electron-updater | Native windows, packaging, updates |
| Terminal | node-pty, xterm.js | Native PTY and terminal rendering |
| Local service | Go, `net/http`, WebSocket | HTTP API and WebSocket |

Versions follow `desktop/package.json`. This page lists only the majors that change how the architecture reads, not the full dependency list.

## Electron host

| Path | Responsibility |
|---|---|
| `desktop/electron/main.ts` | Electron main entry, windows, IPC registration |
| `desktop/electron/preload.ts` | Typed host API exposed to the main renderer |
| `desktop/electron/preview-preload.ts` | Isolated bridge for native web preview |
| `desktop/electron/pet-preload.ts` | Minimal bridge for pet windows |
| `desktop/electron/ipc/` | IPC channel registration and payload validation |
| `desktop/electron/services/` | Sidecar, update, terminal, preview, window, and proxy services |

The renderer must not import Electron directly, and must not assemble arbitrary IPC channel names. A new native capability means updating the host contract, the main-side validation, and the related tests together.

### Startup sequence

1. Electron resolves the default or portable storage mode and prepares the app config directory.
2. The host picks a free port and starts the unified `claude-sidecar-go server` binary.
3. The Go sidecar enters `application.RunServer` through `backend/cmd/claude-haha/main.go` and serves HTTP and WebSocket on one listener.
4. Once the health check passes, the renderer takes the loopback server URL and loads sessions and settings.
5. The host starts adapter sidecars for the platforms that are configured.
6. The server constructs Agent sessions in process; desktop sessions and the terminal CLI share `MainPrint`.

The server can bind a LAN-reachable address for H5, but the desktop renderer always talks to the loopback control address. H5, remote access, and pet windows each pass their own token and capability limits. A running local server does not make every caller trusted.

### Sidecar entry point

`desktop/scripts/go-sidecar-build.ts` compiles `backend/cmd/claude-haha` into the target platform's `claude-sidecar-go-<target>` executable. The same binary exposes three business modes:

```text
claude-sidecar-go server   --app-root <path> --host <host> --port <port>
claude-sidecar-go cli      --app-root <path> [CLI arguments]
claude-sidecar-go adapters --app-root <path> --telegram|--feishu|--wechat|--dingtalk|--whatsapp|--wecom|--qq|--slack
```

`desktop/sidecars/claude-sidecar.ts` is the retained TypeScript reference entry, rather than the desktop business sidecar build target. Separate build steps prepare speech, Computer Use, and native media resources.

## Go server and Agent

| Path | Responsibility |
|---|---|
| `backend/cmd/claude-haha/main.go` | Native CLI, server, adapters, and isolated worker mode dispatch |
| `backend/internal/application/server.go` | Production Go server services and factory wiring |
| `backend/internal/server/` | REST, WebSocket, access control, session lifecycle |
| `backend/internal/application/desktop.go` | Desktop session construction, runtime selection, output projection |
| `backend/internal/clibootstrap/main_print.go` | Shared CLI and desktop Agent execution graph |
| `backend/internal/imruntime/` | Eight platforms' messages, attachments, and session bridges |

HTTP, chat WebSocket, internal SDK messages, OAuth callbacks, and H5 assets retain the client contracts. Each new API must still have a defined capability boundary, access-control tests, and truthful error propagation.

## WebSocket semantics

The renderer keeps one connection per session:

```text
ws://<server>/ws/<sessionId>
```

If the server has auth enabled, the client passes the token as a connection query parameter. After the connection opens, the server sends the current session identity, a snapshot of pending permission requests, and the running state.

### Heartbeat and reconnect

Current behavior in `desktop/src/api/websocket.ts`:

- Send `ping` every 30 seconds after connecting.
- If no `pong` arrives within 10 seconds, the client closes the connection and reconnects.
- Reconnect delay grows exponentially from 1 second, capped at 30 seconds.
- Automatic reconnect has no fixed retry ceiling; only closing the session stops it.
- Messages sent while offline are queued in memory.
- After reconnecting, queued messages are flushed first, then `sync_state` fetches the server's authoritative run state.

If you have read "at most 10 retries" somewhere, that wording is stale — the list above is current.

### A disconnected client does not stop the work

After the last client disconnects:

- Foreground turns and background tasks keep running.
- The idle grace period starts only after the work finishes.
- A client reconnecting inside the grace period cancels the cleanup.
- The server cleans up the Agent session only once the grace period passes with no clients.
- Sessions waiting on a permission request have their own bounded cleanup policy so they cannot hold a process forever.

That is why locking a phone, refreshing the renderer, or a brief network switch does not interrupt a running task.

## CLI and provider proxy

The server projects each Go Agent session’s output, permission requests, tool results, and background-task state into the existing WebSocket protocol. The server and Agent maintain provider, model, effort, and permission mode together; the renderer is not the single source of truth.

`backend/internal/providerproxy/` handles the supported provider protocols:

- Anthropic Messages
- OpenAI Chat Completions
- OpenAI Responses

Model mapping, authentication style, and context settings follow the actual provider configuration. Do not hardcode a vendor list in architecture docs.

## IM adapters

Each platform runs its own adapter sidecar, so one platform's bad credentials or failed startup cannot take the others down.

```text
IM platform → Go imruntime → shared WebSocket bridge → Go server → Go Agent session
```

`backend/internal/imruntime/` owns configuration, pairing, session mapping, queues, deduplication, and attachments. Telegram, Feishu, WeChat, DingTalk, WhatsApp, WeCom, QQ, and Slack run as separate adapter processes. Idle bridges release their WebSocket; foreground work, background work, and pending approval keep it bound. The next message restores the same session.

Storage relocation is coordinated between the desktop host and Go server through preview/quiesce/validate/activate/recover. Adapters acknowledge quiesced only after actual queue, attachment, and credential writes drain and their resources close.
## Persistence boundaries

Different data does not share one store:

| Data | Primary boundary |
|---|---|
| Renderer UI preferences and open tabs | Browser storage and its migrations |
| Sessions and messages | Local session data managed by the server and Agent |
| Provider, H5, Computer Use, and other settings | Config files managed by the server |
| Electron window and native state | Electron user data / app mode directory |
| IM configuration and pairing state | Adapter config and per-platform state directories |

Any change to a JSON shape, a `localStorage` key, or the app config layout needs a forward migration plus a regression test against old data. Never overwrite the user's shared Claude configuration, and never read the real user directory from tests.

## Build and verification

Desktop builds are orchestrated by scripts in `desktop/package.json`:

```bash
cd desktop
bun run build:sidecars
bun run build
bun run build:electron
```

`electron:package` runs electron-builder on top of that. Verifying a packaged artifact only proves the asset and installer structure is correct. User flows that touch windows, permissions, terminals, preview, updates, or Computer Use still need a real desktop smoke test.
