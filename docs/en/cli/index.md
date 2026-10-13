---
title: Install and Run
nav_title: Install and Run
description: Build the Go CLI, configure a model provider, and launch from a terminal or the desktop.
order: 0
---

# Install and Run

The CLI and desktop local server share the Go Agent implementation; the desktop interface still uses React. This page describes the native entry points in the current source. An installed desktop app needs a new build or release containing these changes before it updates.

The native CLI provides the interactive TUI, print mode, tools, sessions, skills, plugins, MCP, and account commands. Both entries construct the same Agent graph. A build, deterministic regression, and a real provider check establish different facts; source documentation does not certify a release or benchmark result.

## Build the native CLI

Install [Git](https://git-scm.com/downloads), [Bun](https://bun.sh), Node.js, and Go. Use the Go version declared in `backend/go.mod`, currently Go 1.25.7.

```bash
git clone https://github.com/NanmiCoder/cc-haha.git
cd cc-haha
bun install
bun run build:cli
./bin/claude-haha --version
```

The build creates a Go executable for the current platform in `bin/native/`. On macOS and Linux, `bin/claude-haha` executes it directly. The npm entry only locates the native file and forwards input, output, and signals. Build the Go file before launching.

Windows PowerShell launches the same native implementation:

```powershell
bun run build:cli
node scripts/cli/launch.cjs --version
```

Specify a target to cross-compile, for example:

```bash
bun run build:cli -- --target windows/amd64
```

Cross-compilation produces a file; it does not verify execution on the target system.

## Configure a model provider

The CLI reads the same saved provider configuration as the desktop. Alternatively, set credentials, the base URL, and an exact model ID in the launching process environment:

```bash
export ANTHROPIC_AUTH_TOKEN='your-provider-token'
export ANTHROPIC_BASE_URL='https://provider.example.com/anthropic'
export ANTHROPIC_MODEL='provider-model'
```

The native entry does not automatically load the repository `.env`. Pass variables through your terminal environment or process manager. See [Environment variables](./env.md) for authentication headers and endpoint configuration.

Use `DISABLE_TELEMETRY=1` or `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1` to disable non-essential traffic when needed.

## Run terminal or script mode

```bash
./bin/claude-haha
./bin/claude-haha -p 'Summarize this project'
```

For a custom endpoint, specify a model with `--model provider-model`, environment variables, or the saved provider configuration. Go resolves settings and aliases with the original priority order. File changes and command execution follow the selected permission mode.

`-p` has no interactive approval input: unapproved tool calls return to the model as tool errors. Scripts can grant the required tools with `--allowedTools`; use the interactive terminal above when each operation needs confirmation.

Use the same arguments on Windows:

```powershell
node scripts/cli/launch.cjs -p 'Summarize this project'
```

## Resume and fork sessions

Use `--resume` from the original working directory and the same configuration directory to continue a session. Adding `--fork-session` inherits its history under a new session ID and preserves the original transcript.

```bash
./bin/claude-haha --resume SESSION_ID
./bin/claude-haha --resume SESSION_ID --fork-session -p 'Continue the analysis using the existing history' --output-format json
```

Replace `SESSION_ID` with the actual ID. The JSON result's `session_id` identifies the new fork; resume that ID on the next run. Currently, do not combine `--session-id` with `--resume`. The desktop message fork/branch API uses the same history restoration chain; both entries use the same transcript restoration implementation. Use a separate `HOME`, `CLAUDE_CONFIG_DIR`, and workspace when testing copied histories.

## Account status and Agent listing

These commands read the current CLI configuration and credentials without making a model request:

```bash
./bin/claude-haha auth status
./bin/claude-haha auth status --text
./bin/claude-haha auth status --openai
./bin/claude-haha agents
```

`auth status` returns JSON by default and exits with 0 when authenticated or 1 otherwise. `--text` returns readable text. `agents` lists discovered user, project, plugin and built-in Agent definitions.

Use `auth logout` to clear the Anthropic account state, or `auth logout --openai` to handle OpenAI credentials. These commands update the corresponding stored credentials and configuration. Use `auth login` for the browser account flow; access to the selected account provider is required. Account tests use isolated loopback OAuth endpoints, not real accounts.

## Install on your PATH

After building, copy the native executable into an explicit installation directory. The installer requires an absolute prefix:

```bash
bun run install:cli -- --prefix "$HOME/.local"
export PATH="$HOME/.local/bin:$PATH"
claude-haha --version
```

The installed `claude-haha` is a Go executable and needs neither Bun nor Node.js to run. It uses the launching process's current directory as its working directory.

On Windows, use an absolute installation directory, for example:

```powershell
bun run install:cli -- --prefix 'C:\Tools\cc-haha'
C:\Tools\cc-haha\bin\claude-haha.exe --version
```

## Desktop connection

A desktop build from the current source launches the Go local server directly. React connects through HTTP and WebSocket. The desktop sidecar build generates and packages the Go executable; building the CLI alone does not replace an installed desktop app.

The CLI and desktop share configuration paths. Set `CLAUDE_CONFIG_DIR` to a separate directory for development and testing to avoid affecting everyday sessions.

Desktop startup also prepares the `claude-haha` terminal launcher. It lives at `~/.local/bin/claude-haha` on macOS and Linux, or `~/.local/bin/claude-haha.cmd` on Windows. The launcher forwards to the same Go sidecar inside the desktop package, so updating the desktop does not require copying another CLI executable.

Check installation and PATH status on the Terminal settings page. If a terminal restart is required, open a new terminal and run `claude-haha --version`. Existing shell configuration is preserved; automatic PATH setup maintains only its own block. A desktop started with a portable configuration directory also passes that directory to the terminal launcher.

## IM adapter entry points

The desktop manages bindings, pairing and adapter processes in Settings → IM. The native Go messaging runtimes currently support Telegram, Slack, WeChat, WeCom, QQ, DingTalk and Feishu. They use the same local server for the Agent, session recovery and tool approvals.

To debug a separate adapter process, build the native CLI and run:

```bash
./bin/claude-haha adapters --wechat
./bin/claude-haha adapters --wecom
./bin/claude-haha adapters --qq
./bin/claude-haha adapters --dingtalk
./bin/claude-haha adapters --feishu
```

The entry points read existing bindings from `CLAUDE_CONFIG_DIR/adapters.json`; the QR binding flow saves WeChat credentials. Set `ADAPTER_SERVER_URL` to the running local server address. If local access authentication is enabled, pass its token through `CC_HAHA_LOCAL_ACCESS_TOKEN` as well. The desktop supplies these connection parameters when starting adapters automatically. Send `/help` for session selection, recovery, stop and approval commands. Press Ctrl+C to close a separately started process.

## Next steps

- [Command reference](./reference.md): arguments and protocols; native availability follows actual execution
- [Environment variables](./env.md): authentication, models, and local runtime variables
- [Architecture overview](../internals/index.md): the CLI, server, desktop shell, and adapters
- [Contributing and quality gates](../internals/contributing.md): build and validation commands
