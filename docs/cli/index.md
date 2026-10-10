---
title: 安装与启动
nav_title: 安装与启动
description: 构建 Go CLI、配置模型服务，并从终端或桌面端启动。
order: 0
---

# 安装与启动

CLI 和桌面的本地服务使用同一套 Go Agent 实现，桌面界面仍使用 React。本文介绍当前源码的原生入口；已经安装的桌面应用需要重新构建或安装包含这些更改的版本才会更新。

当前 Go 实现仍在迁移。普通默认工具目录已接入，基本开发运行无需显式指定工具；部分条件功能和交互入口仍未完成，遇到未迁移能力会返回错误。下面的命令用于运行已实现的路径。

## 构建原生 CLI

先安装 [Git](https://git-scm.com/downloads)、[Bun](https://bun.sh)、Node.js 和 Go。Go 版本以仓库的 `backend/go.mod` 为准，当前要求 Go 1.25.7。

```bash
git clone https://github.com/NanmiCoder/cc-haha.git
cd cc-haha
bun install
bun run build:cli
./bin/claude-haha --version
```

构建会在 `bin/native/` 中生成当前平台的 Go 可执行文件。macOS 和 Linux 的 `bin/claude-haha` 直接启动它；npm 的入口仅负责定位原生文件和转发输入、输出及信号。缺少 Go 文件时，请先构建。

Windows PowerShell 可以启动同一原生实现：

```powershell
bun run build:cli
node scripts/cli/launch.cjs --version
```

交叉编译时显式指定目标，例如：

```bash
bun run build:cli -- --target windows/amd64
```

交叉编译只生成文件，不能替代目标系统上的运行验证。

## 配置模型服务

如果已经在桌面端保存了服务商，CLI 会读取同一份配置。也可以在启动进程的环境中设置认证方式、接口地址和准确的模型 ID：

```bash
export ANTHROPIC_AUTH_TOKEN='your-provider-token'
export ANTHROPIC_BASE_URL='https://provider.example.com/anthropic'
export ANTHROPIC_MODEL='provider-model'
```

原生入口不会自动加载仓库的 `.env`。请通过终端环境或进程管理器传入变量。认证头和接口配置说明见 [环境变量](./env.md)。

当前原生工具执行需要关闭尚未迁移的遥测路径：

```bash
export DISABLE_TELEMETRY=1
```

## 启动终端或脚本模式

```bash
./bin/claude-haha
./bin/claude-haha -p '概括当前项目的目录结构'
```

自定义接口请通过 `--model provider-model`、环境变量或保存的服务商配置指定模型。Go 会按原实现的优先级解析模型设置和别名。文件修改和命令执行遵循所选权限模式。

`-p` 不提供交互审批入口：未获准的工具调用会作为工具错误返回模型。脚本可以用 `--allowedTools` 预先授权所需工具；需要逐次确认时，使用上面的交互式终端入口。

Windows 使用相同参数：

```powershell
node scripts/cli/launch.cjs -p 'Summarize this project'
```

## 恢复与 fork 会话

在原工作目录和相同配置目录中，用 `--resume` 继续已有会话。加上 `--fork-session` 会继承历史并创建新的会话 ID，原会话日志保留。

```bash
./bin/claude-haha --resume SESSION_ID
./bin/claude-haha --resume SESSION_ID --fork-session -p '根据已有历史继续分析' --output-format json
```

将 `SESSION_ID` 换成实际 ID；JSON 结果的 `session_id` 是新 fork 的 ID，下一次使用它恢复。目前不要将 `--session-id` 和 `--resume` 一起使用。桌面消息的 fork/branch API 已接入同一历史恢复链，原生界面验收记录见迁移计划。测试历史副本时，应使用独立的 `HOME`、`CLAUDE_CONFIG_DIR` 和工作目录。

## 账号状态与 Agent 列表

这些命令读取当前 CLI 的配置和凭据，无需调用模型：

```bash
./bin/claude-haha auth status
./bin/claude-haha auth status --text
./bin/claude-haha auth status --openai
./bin/claude-haha agents
```

`auth status` 默认返回 JSON，已认证时退出码为 0，未认证时为 1。`--text` 返回可读文本。`agents` 列出当前配置中发现的 Agent，包括用户、项目、插件与内置定义。

退出账号时使用 `auth logout`；处理 OpenAI 凭据使用 `auth logout --openai`。退出命令会实际更新对应凭据和配置。CLI 的 `auth login` 浏览器登录与登录后安装流程仍在迁移，当前尚不可用。

## 安装到自己的 PATH

构建后，可以把原生文件复制到指定安装目录。安装脚本要求显式给出绝对路径：

```bash
bun run install:cli -- --prefix "$HOME/.local"
export PATH="$HOME/.local/bin:$PATH"
claude-haha --version
```

安装后的 `claude-haha` 是 Go 可执行文件，运行时无需 Bun 或 Node.js。它使用启动时的当前目录作为工作目录。

Windows 使用绝对安装目录，例如：

```powershell
bun run install:cli -- --prefix 'C:\Tools\cc-haha'
C:\Tools\cc-haha\bin\claude-haha.exe --version
```

## 与桌面端的连接

从当前源码构建的桌面端直接启动 Go 本地服务，React 页面通过 HTTP 和 WebSocket 连接该服务。桌面的 sidecar 构建负责生成并打包 Go 文件；只构建 CLI 不会替换已经安装的桌面应用。

CLI 和桌面共用配置路径。开发及测试时可用 `CLAUDE_CONFIG_DIR` 指向独立目录，避免影响日常会话。

桌面启动时还会准备 `claude-haha` 终端入口。在 macOS 和 Linux 上，它位于 `~/.local/bin/claude-haha`；Windows 使用 `~/.local/bin/claude-haha.cmd`。这个入口转发到桌面包内的同一个 Go sidecar，桌面更新后不需要再复制另一份 CLI。

在设置的终端页面查看安装和 PATH 状态。如果提示需要重启终端，请新开终端后执行 `claude-haha --version`。已有 shell 配置会保留；自动配置的 PATH 块只维护自己的内容。使用便携配置目录启动桌面时，终端入口也会沿用该配置目录。

## IM 适配器入口

桌面端在设置 → IM 中管理绑定、配对和适配器进程。当前 Go 原生消息运行时支持 Telegram、Slack、微信、企业微信、QQ、钉钉和飞书；这些入口通过同一个本地服务使用 Agent、会话恢复和工具权限。

需要单独调试进程时，构建原生 CLI 后执行：

```bash
./bin/claude-haha adapters --wechat
./bin/claude-haha adapters --wecom
./bin/claude-haha adapters --qq
./bin/claude-haha adapters --dingtalk
./bin/claude-haha adapters --feishu
```

入口读取 `CLAUDE_CONFIG_DIR/adapters.json` 中已有的绑定；微信凭据由扫码绑定流程保存。`ADAPTER_SERVER_URL` 可指定运行中的本地服务地址；开启本地访问认证时，还需通过 `CC_HAHA_LOCAL_ACCESS_TOKEN` 传入该服务的令牌。桌面自动启动适配器时会传递这些连接参数。发送 `/help` 查看会话选择、恢复、停止和权限审批命令，按 Ctrl+C 关闭单独启动的进程。

## 下一步

- [命令参考](./reference.md)：参数与协议；原生版本的可用范围以实际执行结果为准
- [环境变量](./env.md)：认证、模型与本地运行变量
- [架构总览](../internals/index.md)：CLI、Server、桌面壳与 Adapter 的分工
- [参与贡献与质量门禁](../internals/contributing.md)：构建与验证方法
