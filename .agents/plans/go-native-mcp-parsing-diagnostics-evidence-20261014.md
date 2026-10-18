# MCP 配置诊断的实际作用域与入口证据

2026-10-14；完整迁移仍未完成，未切分支／提交／推送／合入 main。

普通 `/mcp` 的配置错误不同于连接 failed 状态。新增 `mcpconfig.Diagnostics` 按原版组件的 user／project／local／enterprise 顺序调用实际 `Scope`，保留 fatal／warning 分类、完整错误字段、原版作用域标题与文件位置。不能用合并配置、managed 优先级或批准状态隐藏坏文件。`MainPrint.TerminalMCPParsingDiagnostics` 通过同一 Owner 暴露只读快照；组件 mount 的缓存由实际 TUI 消费者管理，不新增持久化 shape。

`native-terminal-mcp-diagnostics.fixture.ts` 运行原版完整 `getMcpConfigsByScope`，实际私有配置文件，以及完整 `McpParsingWarnings` Ink 组件。四组 profile 覆盖无诊断、父目录 missing-env warning 加当前目录 JSON fatal、四作用域同时出现、禁用三作用域但 enterprise 仍显示。返回的全部 scope／path／label／errors／warnings 深比较一致，能力 guard 零 blocked。Source 标题／错误／警告确实渲染；长私有路径由原版 Ink 实际换行，路径本身另按原始快照严格比较。

- `mcp-parsing-diagnostics-native-v8.log`：mcpconfig race 5.806s、bootstrap race 2.480s passed。实际 MainPrint 的私有配置字节不变，真实 loopback MCP／模型端点请求为零，私有 CONNECT guard 外部请求为零；保留 scoped read error／cancel 原错误身份，headless／closed／nil producer 被拒绝。
- `mcp-parsing-diagnostics-source-v4.log`：独立完整原版组件和 scope 对照 race 6.187s passed。
- `tui-mcp-diagnostics-public-before-render-v2-red.log`：真实 Installed／stdio／模型链完成后普通 `/mcp` 只显示 peer failed，缺失原版配置错误／警告，真实产品 RED。中央 UI 修复与公开终端复验仍由对应实现继续，不以只读 API 通过代替 UI。
- v1 的 cwd guard、v2 的依赖编译器探测及已物理确认缺失的祖先 `.git`、v3 的原版空输出 newline／长路径断言、v5 的并发接线缺字段编译、v6 的 `/var` 别名预期均保留日志。这些不计为正确产品反例。

命令、输入与日志 SHA 保存在 ignored `artifacts/go-migration-20261013/mcp-parsing-diagnostics-evidence-v1.json`。当前完整 mcpconfig 包 race 8.863s passed，15 test events、0 skipped／0 failed（`mcp-config-all-current-v1.jsonl`）；公开 UI positive 继续执行。此有限证据不证明整个 MCP、最新全量门禁、同版本桌面／Web／CLI、真实 DeepSeek 或 120 对 Benchmark 已通过。Claude 真实 OAuth 按用户最新要求不执行。
