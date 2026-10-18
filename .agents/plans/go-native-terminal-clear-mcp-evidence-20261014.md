# /clear MCP 目录与物理连接：2026-10-14

当前冻结 Source MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，中间聚焦证据，不声明完整迁移或最终门禁通过。没有真实 Claude OAuth/刷新；所有目录、凭据和 transport 为 disposable HOME、fake account/loopback。

实际 Source `clear/conversation.ts:184-193` 清空 app MCP clients/tools/commands/resources，保留 pluginReconnectKey。`useManageMCPConnections.ts:676-757/763-927` 的 sessionId effects 重新加载配置与发布目录；旧 effect cleanup 仅使旧配置加载失效。`connectToServer` 与 fetch 的 memo 保留，清理并不无条件断开全部物理 peer。

新增 Source fixture 使用真实 React/Ink、原 AppStore/Hook、原 `/clear` command 与真实 HTTP MCP。connected 与 held tools/list 两个消费者均观察到四个 app catalog 清空、key19 保留，随后重发布；已连接 client 身份保持，未完成的原工具列表物理请求保留，initialize/tools/list/prompts/list 各一次。安全 guard 为0blocked。Source 初版 fixture 把 macOS /var alias 当启动 CWD，而原 clear 正常解析为 /private namespace，导致额外握手；v7 raw failure 保留，最终按实际 main 的 canonical CWD 初始化（v8 race passed3.289秒），没有改变生产 namespace 身份规则。

Go 新 `mcptools/terminal_clear.go` 清当前可见连接、失效旧配置发布 generation，再沿同 Service/Runtime BeginInteractiveDiscovery 异步发布。prompt/resource 元数据是内部 peer cache，公共 consumer 仅选择当前 connected peer；资源已缓存时复用实际 rows。扩大真实resources支持后暴露 held-tools 同期旧resources请求尚未整体安装而新effect重复resources/list；新增 Runtime 的原版20-entry资源Promise LRU，保留pending并发请求、通知失效、实际peer关闭清缓存、整个Runtime关闭join。v13错误期望仅inspect工具被实际Source的List/Read辅助工具反例修正，原raw保留；不改变Source辅助工具列表。`AgentResources` 从当前连接构造快照，防止 clear 到重新连接之间将内部缓存当公共资源传给新 Agent。borrowed namespace/child/foreign/nil Scope 均不允许清父 Runtime，旧 native binding 与取消/Close 合同保留。没有调用 ReloadComponents/重建 MainPrint。

`native_terminal_clear_mcp.go` 的 Owner helper 由已有 `TerminalClear` 在 session ID regenerate 后、SessionStart 前调用。清后的两种物理消费者均由同 MainPrint/Harness 执行下一条 human，真实模型 POST 收到实际恢复的 MCP tool catalog；initialize/tools/list/prompts/list 各一次。

新公开真实 PTY 从 `application.RunCommand` 普通 CLI 启动，human1 → `/clear` → catalog callback 的实际重发布 → human2 → 两次真实 loopback model POST → 普通退出。两个 POST 都携带 `mcp__owned-peer__inspect`，第二次不带旧 human；一个实际 initialize、一个 tools/list、一个 prompts/list；原 slave termios 完整恢复。observer marker 只观察原 catalog callback，不填 fake ready/catalog。首版缺 XNU 已有 FIONREAD pending-input observation，v11 termios RED 保留；使用项目已有不读取/丢弃输入、不修改 flags 的 probe 后 v12 passed。

`native-terminal-clear-mcp-source-physical-public-v12.log`：race passed application5.472s、clibootstrap4.729s、mcptools1.542s。选择新 Source、Go physical、公开 PTY 和既有 interactive startup/disable pending regressions。v10 broader pattern 额外选到旧 fake-account fixture，其新 release-notes startup HTTP 被私有 CONNECT guard 拒绝 raw.githubusercontent.com；这个独立隔离缺口交 lifecycle 修复，未放宽 external guard。

共享 Source fixture guard 的 exact absent path 原先只识别 string。现在 Buffer/fileURL 仅归一化到现有已明确声明的 absent 集合；不新增可访问路径。新 `client-options-absence.test.ts` 验证三种形式同ENOENT、未声明/非fileURL/escape/symlink仍拒绝、外部 sentinel 不变。`client-options.test.ts` 与新 guard test fresh22tests/94assertions/18.31s passed。MCP fixture 的已知缺失 ancestor.git 同时声明 /var 与 /private 两种路径，TTY 探测通过真实隔离的 COLUMNS/LINES 环境取宽高；没有额外允许真实 TTY、外部 shell 或 Keychain。

尚需最终 immutable snapshot 的全量 check:go/verify、90%门禁与三入口/Benchmark；此聚焦记录不能替代那些结果。

`native-terminal-clear-mcp-resource-memo-v14.log`：真实Source connected/held-tools 与 Go清理→下一model含resources服务均race passed（clibootstrap7.268s、mcptools3.830s、mcpruntime3.038s）；四类list/initialize均一次，cached/pending resources实际复用，原pool/disable/peer-close回归通过。

共享 mcptools/mcpruntime/cooperative 三包全量 race v15 passed（4.843s、7.249s、2.477s），包含新pending资源Promise cache与现有SDK namespace/关闭/通知/取消合同。

最终本区域五包 vet（cooperative/mcptools/mcpruntime/clibootstrap/application）v16 exit0 passed，当前共享 `git diff --check` passed；不是完整门禁声明。
