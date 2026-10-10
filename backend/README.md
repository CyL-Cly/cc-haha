# Go CLI 与桌面后端

截至 2026-10-08，源码中的 CLI 和桌面本地服务入口已切换到 Go。桌面仍使用 React，通过 HTTP 与 WebSocket 连接 Go Server。启动器不回退到旧 TypeScript 后端；已安装的桌面应用需要重新构建或安装新版本才会更新。

**完整迁移尚未完成。** 普通默认工具目录已接共享 Harness 并通过分项真实模型任务；完整交互 TUI、全部接口分支和整个打包桌面的 E2E 仍未完成验收。已移植组件的测试通过不代表整个 Agent Harness 的行为已保持一致，真实 DeepSeek Benchmark 也尚未完成。

## 构建与启动

在仓库根目录执行。Go 版本以 `backend/go.mod` 为准，当前要求 Go 1.25.7；构建脚本还需要 Bun 和 Node.js。

```sh
bun run build:cli
./bin/claude-haha --version
```

构建在 `bin/native/` 生成本平台的 Go 文件。macOS 和 Linux 的公开启动器直接执行该文件；Windows 的 npm 启动器只转发参数和进程输入、输出。也可直接构建：

```sh
go -C backend build -o ../artifacts/claude-haha-go ./cmd/claude-haha
```

模型凭据通过运行环境或保存的服务商配置提供。当前程序不自动加载仓库 `.env`。普通入口会生成当前配置下的默认工具目录：

```sh
DISABLE_TELEMETRY=1 ./bin/claude-haha \
  -p '概括当前项目的目录结构'

DISABLE_TELEMETRY=1 ./bin/claude-haha
```

这些 Agent 命令会使用配置的模型服务并消耗其额度。默认目录随 Settings、Skills、MCP、Computer Use 等实际配置变化；正常入口可省略 `--tools`。仍未实现的特定能力会返回迁移错误，不能把默认目录接通当作完整斜杠命令、快捷键、附件和终端行为已验收。`DISABLE_TELEMETRY=1` 关闭尚未迁移的工具遥测分支。

## 桌面与 Server

`desktop/scripts/build-sidecars.ts` 构建 Go sidecar；Electron 的 `sidecarManager.ts` 固定启动 `claude-sidecar-go-*` 的 `server` 模式，并将 CLI 路径指向同一个 Go 可执行文件。旧后端选择和自动回退会报错。

独立开发服务可通过原生可执行文件启动：

```sh
DISABLE_TELEMETRY=1 ./bin/claude-haha server \
  --host 127.0.0.1 --port 3000 --app-root "$PWD"
```

CLI 与桌面会话共享 Go Controller、权限状态、工具和模型请求循环。确定性测试验证工具、权限、恢复、子 Agent 与文件差异。另有签名 Electron 的官方 DeepSeek 实测：旧会话冷启动恢复，普通输入框完成真实 Read → Write → 审批 → Read，核对工件和请求记录。每份证据对应具体二进制；最新 P250 记录见[当前验证记录](../.agents/plans/go-migration-current-validation.md)，它仍不代表整个桌面或配对 Benchmark 已通过。开发测试请使用临时 `HOME`、`CLAUDE_CONFIG_DIR` 和项目目录，保留真实用户数据。

## 文档与验收记录

- [CLI 构建、配置与使用](../docs/cli/index.md)
- [English CLI guide](../docs/en/cli/index.md)
- [迁移总览](../.agents/plans/go-migration-overview.md)
- [当前执行状态](../.agents/plans/go-cli-readiness.json)
- [公开原生入口](cmd/claude-haha/main.go)
- [共享 CLI / 桌面应用层](internal/application/)

Recovery 仍是独立精简模式，通过 `CLAUDE_CODE_FORCE_RECOVERY_CLI=1` 显式选择；它不执行完整 Agent 工具，也不是默认 CLI 或桌面后端。
