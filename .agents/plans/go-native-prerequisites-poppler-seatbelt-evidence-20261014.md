# Poppler 与 macOS Seatbelt 原消费者依赖恢复

2026-10-14。冻结 Source `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`。这是当前 macOS 实物入口的有界证据，不是最终迁移验收。

## 准备与改动

- 当前宿主 macOS 26.3 / 25D125、arm64；真实 `/usr/bin/sandbox-exec` 的 allow-default preflight 返回 0。该二进制 SHA-256 为 `2f447acfae7a954ccb5a1bcf3a1955f69b1a8549c434ddff643df98fd3cb33ba`。原工具所需 ripgrep 15.1.0 已存在，SHA-256 为 `2fb61b6e5b3e2d89b115fe6c18fd8805670fdf4bdfde85954d40855a76830e5f`。没有修改系统安装或 OS 设置。
- 从 Codex primary runtime bundle 26.1011.11113 复制真实 Poppler 26.05.0 到本任务 ignored 私有 runtime，5,983 个普通文件/符号链接逐项校验与原 bundle 相同。两工具末端均为 arm64 Mach-O，私有路径下 `-v` 均返回 0。该校验是本机实际依赖复制证据，不声称上游 release digest。
- `native_pdf_read_physical_test.go` 只增加 `CC_HAHA_TEST_POPPLER_BIN` 的显式绝对目录发现及两处测试 PATH。保留原 Homebrew fallback 和全部 Read/Write/MCP/请求断言；显式依赖无效时失败。PDF 和 sandbox 生产实现未改变。
- 原六项 broker skip 为 clibootstrap 五项和 sandboxruntime 一项，准确 opt-in 为 `CC_HAHA_SANDBOX_PHYSICAL_BROKER=1`。仅在当前真实 Seatbelt 宿主 preflight 之后使用，没有 fake broker。

## 原入口执行结果

全部使用 Go 1.25.7、固定 Bun 1.3.14、临时 HOME/config、`test -p=2 -race -count=1 -json`。模型为已有脚本式 loopback HTTP peer，不消费 live quota，不执行真实 Claude OAuth。

| 原消费者 | 结果 |
| --- | --- |
| `sandboxruntime.TestNativeMacSeatbeltPhysicalAllowedWriteDenyProtectedAndBareGitScrub` | passed，2.149 秒，1 top-level；原 Source wrapper 与 Go wrapper 实际执行、允许私有写入、拒绝外部/危险写入和外网、批准 loopback、bare scrub、关机 |
| `TestNativeSandboxActualBackgroundTaskStopAndShutdownScrubJoin` | passed；真实后台进程 Stop/全局关闭、PID 消失、候选文件 scrub 与 proxy 回收 |
| `TestNativeSandboxActualMainPrintTerminalWrapPolicyAndShutdown` | passed；实际 MainPrint/Bash 文件与 policy 拒绝、两 proxy 关闭 |
| `TestNativeSandboxActualMainPrintNetworkPermissionUsesExistingOwnerFuture` | passed；原 synthetic permission 字段/等待、allow/deny settlement 与实际 loopback |
| `TestNativeSandboxDefaultMainPrintAutoAllowedBashActualToolReadPostAndCanonical` | passed；foreground 两配置、实际 Bash→Read→3 POST、canonical history、0审批 |
| `TestNativeSandboxStandaloneSDKStdinPermissionAnswerAndEOFRealBash` | passed；实际 stdin/control response 与 EOF 两配置、物理 network 与 SDK drain |
| 五项 Native sandbox 合计 | passed，8.758 秒，5 top-level / 11 test pass events，0 skip/fail |
| `TestActualMainPrintPDFReadNativeFilesPagesMessagesAndWriteGuard` | passed；原五场景、实际 pdfinfo/pdftoppm、下一模型 POST 的 document/image、Read 后 Write guard |
| `TestActualNativeMCPPDFReadBareAndOrdinaryFactories` | passed；真实 MCP tool context bare/ordinary 两 factory |
| 两项 PDF 合计 | passed，30.813 秒，2 top-level / 9 test pass events，0 skip/fail |

本任务 ignored `artifacts/go-migration-20261013/native-prerequisites-poppler-seatbelt-20261014/receipt.json` 保存完整命令环境、原日志及 SHA；`poppler-bin-path.txt` 保存私有 bin 指针，`private-runtime-restored-20261014/poppler-runtime-copy-sha256.json` 保存完整复制清单。`git diff --check` passed。

## 仍需独立验收

正式 immutable Go runner 需在实际依赖/宿主预检后显式转发两个 opt-in 环境值；仅设置 sourceEnvironment 不能保证通过 hermetic 环境过滤。Windows/Linux 实物 sandbox 不在本次结果内。完整 Go/覆盖率门禁、同候选桌面/Web/CLI 和 120 对 Benchmark 尚须由总体任务执行，不能以本次八个入口外推。
