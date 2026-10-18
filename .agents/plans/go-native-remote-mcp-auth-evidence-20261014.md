# Go Remote 与终端 MCP 认证消费者增量证据

2026-10-14。冻结 Source `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`；当前分支继续工作，不提交、不推送、不合入 main。本记录只描述聚焦证据，**不代表最终迁移、质量门禁、覆盖率或 Benchmark 已通过**。

## 隔离与原版边界

- 全部账户、模型和凭据为临时 HOME/config 下的 fake；HTTP/WS/TLS 只连私有 loopback。真实 Claude OAuth 登录、刷新及云账号验收未执行，遵守用户本轮禁止该操作的指令。
- MCP 浏览器程序为明确的私有可执行文件，只记录真实生产者生成的 URL；Claude 连接器流程未访问真实 Claude 网站。
- 使用固定 Bun 1.3.14、Go 1.25.7、原版本全模块业务代码。替换的是外部 OS、HTTP 和浏览器 primitive，不向 Go 输入 Source 预处理结果，不启动 TS CLI/Server 回退。
- 日志位于 ignored `artifacts/go-migration-20261013/`；新机器缺少这些文件时应重跑，不能按本文转抄通过。

## `--remote` 真 gate 下的普通 CLI

`application/terminal_remote_session.go` 包装同一个实际 Session/MainPrint，普通远端输入发送 CCR sessions HTTP/WS。远端输入不进入本地模型 ASK；保留 Source 的本地安全命令集合、权限响应、interrupt、关闭、真实媒体 producer 和终端恢复。

`ccrsessions.Manager` 的 frontend subscription 与 `--sdk-url` ingress 分离；使用实际 WS `/v1/sessions/ws/{id}/subscribe` 和 HTTP events/control 生命周期。加载/权限/结果状态由真实服务事件推进。忙碌时的下一输入照原版立即发送，Bash 输入不附加本地展示前缀。关闭取消并 join HTTP、WS、timer 和控制请求。

| 已执行消费者 | 观测结果 | 证据 |
| --- | --- | --- |
| 原版 RemoteSessionManager + SessionsWebSocket、HTTP、权限、interrupt、permanent close 对照 | passed | `cli-flags/native-remote-focused-aggregate-v20.log` |
| 公开 Parse → RunCommand → 真 PTY，初始描述/空描述两个 profile | passed；7 次真实 CCR 输入，批准产生真实 shell 文件，deny/server-cancel/interrupt、忙碌第二输入、Bash display、正常退出和精确 termios | 同上 |
| 实际 Source gate memo/env/global override 与 external 默认 false | passed；9 个 profile；没有强开普通公共默认可用性 | 同上 |
| 原版标题清理、UTF-16、宽度 fallback | passed；19 个实际 Source case | 同上 |
| canonical `generate_session_title` purpose、schema、system prompt、独立模型调用与父历史保持 | passed；fake loopback 模型 | 同上 |

该 aggregate 使用七个准确 selector，application 11.122s、clibootstrap 5.823s、ccrsessions 2.183s、sessiontitle 2.337s；cooperative 仅 compile/no selected tests。旧 v18 因 selector 未命中不能作为功能证据。

冻结的 `ant + sonnet` profile 中，原版 queryHaiku/title 路径有真实 ReferenceError，生成器捕获后走初始描述 fallback、模型 HTTP 为零。Go 对照严格断言创建 POST 的原版 fallback title；不能外推为默认/custom/internal title provider 支持或真实模型验收。原错误期待 `modelRequests=1` 的 RED 和原版异常收据保留。

## `/mcp` generic OAuth 与 Claude 连接器

普通 HTTP/SSE 认证复用现有 `mcpauth.Service`、shared secure store、同一 Runtime 和 native Owner。原 MCPSettings 先读取实际 provider `tokens()` 产生 `isAuthenticated`；ManagePlugins 不提供该字段。仅实际 caller 投射为 true 时执行前置 revoke，不能按 token 存在自行推断。

URL、PKCE、手动 callback、state 验证、credential 写入、重连与工具目录更新均为真实消费者。取消不只停止 Owner 等待，还等待实际 HTTP/监听端口关闭；修复前的 listener-survived RED 保留。

Claude 连接器使用真实 cached account/organization、原 oauth origin 和 `mcprs`→`mcpsrv` URL 规则；私有 opener + Enter 后才重连。清除是原版两步确认和 needs-auth/catalog 移除，不撤销 cached Claude token。generic clear 则真实 revoke access/refresh 并变为 failed。两种文案和状态分别沿 Source，未合并为一个假流程。

| 消费者 | 结果与范围 | 最新证据 |
| --- | --- | --- |
| 完整原 MCPSettings/ConnectionManager/RemoteMenu/Ink/default bindings → manual callback → PKCE HTTP → 重连/clear/unmount | passed；真实 callback port 关闭、零 capability 逃逸 | `cli-flags/native-mcp-auth-whole-source-ui-v7.log` |
| generic 物理 callback、取消、held token、state mismatch、nil/foreign/retained/borrowed/child/closed guards | passed | `cli-flags/native-mcp-auth-proxy-physical-v3.log`；后续 v6 aggregate |
| 实际 provider.tokens 的存在语义 | passed；5 个实际 Source profile，含 expired/no-refresh、future 空 access object、partial registration | `cli-flags/native-mcp-auth-tool-guards-presence-cache-v7.log` |
| generic 公开 PTY | passed；真实 browser/manual PKCE、真实 MCP tool、clear 两次 revoke、下一 ASK、held token Esc join、pending listener 全局退出、termios | `tui-mcp-auth-public-v4.log`，peer evidence，application 9.236s |
| Claude proxy 实际受信 catalog/private official TLS → 私有 browser → RPC → tool → clear/cancel → 下一 Harness | passed；不含真实 Claude 网站或账号 OAuth | `cli-flags/native-mcp-auth-proxy-public-pty-v3.log`；后续 v6 aggregate |
| 公开 proxy PTY | passed；4 个模型 POST、catalog/真实 tool 变化、两步 clear、Esc 保持、全局退出和 termios | 同上，application 11.070s / PTY 8.96s |
| 07 旧 held fake catalog shutdown 选择器 | passed；同版本暖缓存、私有外连 guard | `cli-flags/native-mcp-held-fake-account-shutdown-current-v1.log`，2.921s；后续 v6 aggregate |

proxy public v1 的 loopback model 未配置 NO_PROXY、v2 的 proxy clear 文案与 Source 不同均保留；v3 才是修复后通过。没有靠改变产品 gate 或发送面板命令给模型替代消费者。

## 模型 `authenticate` 工具

初始 discovery 的 needs-auth 目录现在提供 Source `mcp__<server>__authenticate`；空 object schema strip、描述/prompt、非并发、10000 结果阈值、默认 allow（保留全局权限规则优先级）和 message-only tool_result 映射沿原工具。proxy/non-http/sse 返回原版 unsupported 指引。

HTTP/SSE 工具调用只保留到 URL 返回的 native reference，剩余 OAuth 有单独受控 lifetime。它独立于普通回合取消，成功后在同 Owner 上真实重连并更新工具/命令目录。全局关闭、headless EOF 取消并 join；PermissionView 共享 lifetime 指针，避免复制 Mutex/WaitGroup。

真实反例证明：headless OAuth 已成功但下一模型仍只有 authenticate。现只给该原版认证成功 producer 绑定 `OnAuthenticationChanged`，刷新现有目录；没有对所有 SDK settings 变化开放 admission，也没有宽泛 reload。

| 消费者 | 结果 | 证据 |
| --- | --- | --- |
| 完整原版 manager 发布实际 createMcpAuthTool → URL 提前返回 → 真实 callback/后台 catalog | passed；严格比较描述/schema/权限/映射和 unsupported 文案 | `cli-flags/native-mcp-auth-tool-source-harness-v3.log` |
| Source pending callback 自然进程 EOF | passed；没有 `process.exit`，真实 unref listener/timer 不阻止 Bun 退出，0 token/0 connect | `cli-flags/native-mcp-auth-tool-source-pending-v1.log`，4.451s |
| normal、held model 取消、headless 三路真实 Harness | passed；model2 收到授权 URL，callback 前无 browser/token；随后真实工具和 model4 结果 | `cli-flags/native-mcp-auth-tool-headless-public-session-v5.log` |
| 公开 `--print` | passed；Parse/RunCommand、default permissions、下一 HTTP 收到真实授权链接、EOF 不等五分钟并关闭 listener | 同上 |
| 完整 Session.Close held metadata | passed；先取消 ASK，早期 auth close 取消实际 metadata HTTP 并 join，0 late model/token | 同上 |
| nil/foreign/retained/cancelled/closed call context | passed；不产生模型/OAuth工作 | `cli-flags/native-mcp-auth-tool-guards-presence-cache-v7.log` |

v6 精确旧/新消费者 aggregate：clibootstrap 16.584s、application 11.825s；其他三个包仅 compile/no selected tests。AuthCache 接线后的最新真实消费者 aggregate `native-mcp-auth-cache-auth-tool-consumers-race-v9.log` passed：clibootstrap 15.676s、application 10.521s，覆盖 Source/UI/AuthTool/pending、generic/proxy physical/public、headless、Session.Close 与准确 held fake catalog shutdown selector。不能把未命中的单测当通过。

## 认证失败缓存的实际消费者

原 `client.ts` 的 `mcp-needs-auth-cache.json` 是原有 Source 格式。本次没有改变 JSON shape 或加 schema 标记。Native batch 专用方法遵守 await writeChain → reset read promise；只在真实 discovery batch 里跳过最近 15 分钟失败或 http/sse discovery-without-token。显式 reconnect 保持真实连接。HTTP/SSE 成功清除失败 discovery（保留 registration/secret/未知项）并等待 targeted cache clear；authenticate 工具成功先排入 clear-all。

当前聚焦证据：

- `native-mcp-auth-cache-source-physical-v5.log`：11 个完整 Source manager/client + Native loopback profile，-race passed 12.674s。包含 TTL 正好边界、未来、字符串/数组数值、无效/null/缺 timestamp、discovery 无 token、真实 401。比较实际 status/catalog/file/probe；不是只测一个 TTL 函数。
- `native-mcp-auth-cache-source-followup-v7.log`：外部清除后新 batch、显式成功覆盖旧失败与 registration 保持、显式失败移除旧 pseudo，原版真实 reconnect/batch + Go 物理消费者，-race passed 5.244s。
- `native-mcp-auth-cache-physical-writechain-v6.log`：真实私有文件、阻塞中的旧 401 write、targeted clear、后续 write、每 batch memo 与外部文件重载、clear-all 实际 unlink，-race passed 1.554s。
- `native-mcp-auth-cache-three-package-race-v8.log`：mcpruntime/mcptools/mcpauth 三个完整包 -race passed，分别 6.222s / 4.256s / 37.236s。live 等原有显式边界不外推；后续真实消费者 v9 聚焦回归也通过（见上）。
- `native-mcp-auth-cache-options-vet-v10.log`：failed/build-only，共享 in-flight LSP formatter 引用未保存的 protocol helper；peer 随后已保存定义。此命令不计静态检查通过。08 恢复后 fresh `native-mcp-cache-publisher-options-vet-v12.log` 五个包 vet passed；后续源码修改仍需重跑所影响检查。

尚须继续审计：Agent 继承父 authPseudo 的 Native 消费者、其余实际 cache invalidation callsite。下方 Source-only Agent 证据不能外推为 Go 子 Agent 已通过。

## 07 诊断公开退出/历史与 Options cache identity 修复

原普通退出/历史测试只跑一次曾 passed 34.786s，但缺外网隔离。只加私有拒绝代理后真实 RED，捕获 `raw.githubusercontent.com:443` 和 `api.github.com:443` 两个 CONNECT（`public-exit-history-private-guard-v3.log`）。这不能作为确定性通过。

现在 fixture 从实际 `--version` 读取版本，种入同版本暖 changelog/lastReleaseNotesSeen，保存用户 `autoUpdates:false`，保留代理零外连断言。公开 exit/quit/CtrlD/after-read 和 ordinary/fullscreen resume 六分支全部原工具、canonical、scrollback、session hook、termios 断言未删、deadline 未加：`public-exit-history-options-current-v4.log`，application -race passed 23.839s。

08 完整门禁仍出现六分支 35 秒 kill；快照两 driver 和共享 helper 与当前逐字节一致，未漏上述暖缓存修复。按原 `root-option-private-env.json` 的 HOME/TMP 长路径及 PATH 重跑单例得到真实同样 35 秒失败（`public-exit-root08-environment-v1.log`）。`public-exit-root08-python-v1.sample.txt` 捕获 Go/PTY 尚未启动时，Homebrew Python 3.14 的 `HTTPServer.server_bind` 对 `127.0.0.1` 做反向 DNS，主线程停在 `socket_gethostbyaddr → mdns_hostbyaddr → kevent`。旧聚焦环境 PATH 选择 `/usr/bin/python3`，不能覆盖这个依赖。

现仅私有 peer/拒绝代理采用 numeric loopback bind，不查询 DNS；仍使用真实 socket/HTTP，不改产品。新同区回归把 getfqdn/gethostbyaddr 设为失败边界，真实 POST 成功且代理 CONNECT 返回 403。`public-exit-history-root08-no-dns-v2.log` 在原门禁完整私有环境中 -race passed 24.099s，六公开分支及全部原断言通过，unownedNetworkAttempts=0。没有扩大任何期限；08 原失败不改成快照已通过。

同次还通过原完整 Plugin install→top/channel config→reload→Skill ASK→disable/reload→enable/reload→相同 North 再保存→uninstall 链。Source options writer 即使值不变也可真正写盘并失效 initial settings cache，因此新增独立 id/server-bound Options producer certificate；只允许 `pluginConfigs[id].options` 或 `pluginConfigs[id].mcpServers[server]`，不允许 installation、permission、其他 plugin/server 或未知字段变化。保留实际 manifest/schema/Store 写入，不重建 AppState/权限、不自动清插件 memo。19 个域边界正负例与 10 个 uninstall 限定删除反例 -race passed 3.276s；peer 的前置 cache identity RED 保留。

## MCP 命令发布范围与原版挂载对象

`publishNativeMCPCatalog` 原先重新加载全部插件/本地定义，超出原 `useManageMCPConnections` 只更新 `prevState.mcp` 的范围。现只发布实际 `mcpTools.PromptCommands`；Registry 按原 metadata 替换 MCP 定义，保留本地/插件闭包和 plugin metadata，远端安全目录不接收 MCP prompt。交互式合并沿实际 `useMergedCommands`，同名本地命令优先，不按名字前缀误删本地定义；headless 保留原 combined/first-match 路径。

`native-mcp-catalog-scope-source-race-v6.log`：原 React hook + 正反同名/旧闭包/清空 + 真正 prompts/list/get/list_changed/取消，4 个准确 top-level -race passed：sdkhost 2.474s、clibootstrap 4.167s；Source blocked=[]。v1 fixture 缺 USERPROFILE、v3/v4 调用原 render 暴露的未绑定 waitUntilExit 导致 driver 超时均保留，未当作产品差异。

这项独立修复没有单独解决 UpdateNow 的提前生效 RED。另一个具体 Source 区别由 TUI peer 处理：普通 slash 使用挂载的 `context.options.commands`，模型 Skill 工具读取 global fresh memo；不能将两者全部改为旧快照。peer 原公开 Update v12 已通过原全部断言，证据由 peer 记录。本次没有宽泛 reload。

## 无远端持久化的实际 Promise 边界

完整原 `Project.appendEntry` 对新主 transcript 即使无远端也 `await persistToRemote`。新 fixture 执行原版真实 record/flush/append 和文件写入，用只读 `Bun.peek.status` 观察：remote 返回 fulfilled，而 await 前的 append 仍 pending。Go 原 completion 在同 callback 完成，`native-append-no-remote-actual-source-red-v1.log` -race 真 RED 保留。

现仅该分支补一个已完成 Promise 的 Owner reaction，无文件/网络/计时工作；duplicate、sidechain、非 transcript 分支不新增 await，不改 CCR/ingress 错误路径、跟踪计数或 writer FIFO。`native-append-no-remote-actual-source-race-v2.log` 14 个 top-level passed 3.241s；后续实际 CCR/ingress/clear/已接纳 append/独立 Record 回归 `native-append-no-remote-ccr-ingress-regression-v3.log` passed：bootstrap 4.971s、remoteio 6.287s、sessioncontroller 2.697s。

随后原严格 cold 5 对测试仍 **failed**（`sdk-monotonic-cold-picker-no-remote-await-v4.log`，64.62s / 117 expect）：Source 全部 `now original next original`，Go 全部 `later original`。各自实际文件冷恢复后的下一 HTTP messages 仍相同。21 个当前 indexed raw/summary 与 SHA 独立保存在 `cli-flags/sdk-cold-no-remote-await-v4-preserved/manifest.json`；Root 原 v3 preserved 目录未改。该 await 修复不能作为标题迁移完成的证据。

`import-cutpoint-actual-source-v1.json` 是额外只读运行时诊断：cold/warm-module/warm-cleanup 各 5 次，实际 Source import/cleanup 与真实私有 stat/immediate 的阶段不同，全部 blocked=[]。它不是普通 SDK 的允许结果集证明，未向生产加入假 I/O、delay、排序或标题策略。

后续完整调用链只读实验保存在 `cli-flags/sdk-cleanup-causality-v1/`。原 `session-controller.fixture.ts` 的 SHA 与上述 v4 完全一致，未启用 `inspectRecords`，使用同一 SIMPLE/nativeMain/递增时间输入，5 cold 与 5 次额外完整 CU cleanup 预调用。cold 全部 `now original next original`，额外预调用后全部 `later original`；后者 index 0/1/4 的有序 type/operation/uuid/parentUuid/content/lastPrompt 行与保留 Go v4 实际文件逐行相等，index 2/3 则出现另一个更早的 last-prompt。完整 raw 未排序，全部 1 真 HTTP/0 blocked，10 个实际 HTTP messages 全相等。`ordered-physical-comparison.json` 明列这是诊断投影及旧 Go reference SHA，不是当前新 Go binary 或正式 canonical PASS。

`record-stat-causality.json` 保留原生 Promise 观测：cold 首个 Record stat 在 phase 12 启动、13 完成，早于下一 ASK phase 20；预调用 index 0 的同一 stat 12 启动、22 才完成，晚于下一 Record phase 21 的选择。完整 Source 的冷 await 是 `query.ts:1059/1521` 与 `query/stopHooks.ts:206` 导入 CU cleanup，随后 `cleanup.ts:76` 导入 daemon；sessionStorage 在 fixture:93 已静态导入。Go 对这些已链接模块保留 fulfilled Promise 反应。由显式预调用带来的 I/O 调度机会是因果推断；它也执行了一次原 inactive 状态更新，不能宣称证明所有正常冷启动已等价。当前正常 cold strict 可见标题仍 RED，不能借预热诊断改绿。

`cli-flags/frozen-build-entry-audit-v1/receipt.json` 只读保存冻结 MAIN 的原始文件、Git blob 与 SHA。存在两个正式入口 profile：根 package 的 bin 指向 `bin/claude-haha`，它直接 Bun 执行 `src/entrypoints/cli.tsx`，没有 CLI bundle，因此冷源码模块载入并非仅假设的开发入口。桌面发布则由原 `desktop/scripts/build-sidecars.ts:201` 把合并 sidecar `Bun.build`/`compile` 成单 exe，未指定 splitting，external 只有可选 npm 包而无本地 CU 模块；sidecar cli 分支导入同一 cli.tsx。后者不需要配置外部 cleanup/daemon 源文件，但尚未运行原 compiled 产品证明具体内部 Promise 调度与 SDK 输出。两者分开记录，不能用 compiled/warm 推断替换独立 CLI 的冻结 cold oracle。

## 同一诊断 sink 的 MCP 消费者

新增 NativeSink 的 MCP debug/error 写入方法，复用既有缓冲、路径 sanitize、模块启动时间和 debug 文件；没有第二个 logger。实际 Source MCP 日志独立于普通 errorLog 的 privacy/ant/memory gate，保留 debug 级别过滤、JSONL 字段顺序和当前 session/cwd。Native MCPAuth worker 的真实 OnDebug 与 AuthTool 成功/晚失败路径绑定同 sink，普通回合取消不取消该后台认证，全局关闭则 cancel/join。

- `native-mcp-log-dedicated-source-v2.log`：完整原 Source errorLogSink 5 profiles 严格比较实际文件、长 Unicode server 路径及 debug，-race passed 1.774s。v1 的 debug fixture 在真实异步 symlink 完成前采样失败已保留，未当作产品 RED。
- `native-mcp-auth-tool-log-source-harness-v5.log`：原认证工具与实际 MainPrint/Harness，真实 callback 成功和 `access_denied` 晚失败，下一 HTTP 已收到 URL，日志经真实缓冲写盘，-race passed 6.431s。分别匹配原版 `OAuth complete, reconnected with 1 tool(s)` 和实际晚失败文案，均 private guard 0。
- `native-mcp-diagnostic-three-package-race-v6.log`：errorlog / mcptools / mcpauth 全包 -race passed 7.561s / 3.616s / 32.618s；不外推现有显式 live 边界。
- `native-mcp-log-append-publisher-vet-v7.log`：七包 vet passed。其余 client/runtime 的日志与 telemetry callsite 未由此证明完整。

## Agent 认证工具的原版合同审计

`native-mcp-agent-auth-inheritance-source-v1.log` 执行完整原 `runAgent` 的真实工具过滤/child context 创建及真实 OAuth/reconnect；仅在实际 `onCacheSafeParams` 观察点截停模型，-race passed 4.189s，sync/async 两 profile，0 模型 / 0 外网越界。

两种 Agent 都继承父目录的同一个 authenticate Tool 对象及 needs-auth client。同步 Agent 共用 parent setAppState，授权后 parent 目录成为 inspect；异步 Agent 的 setter 不更新 parent，parent 仍 authenticate。两者已捕获的 child options.tools 都保留本轮 authenticate。Go `InstallAgentConnections` 尚未安装对应 authenticationTools，真实初始工具目录有缺口；不能通过总是更新 parent 或重建 child 本轮工具冒充原版。Native 子 Agent 完整消费者仍 OPEN。

## 普通非 bare 首条记录的真实等待

Source `QueryEngine.submitMessage` 仅在 `isBareMode() && !isSessionMessage` 才 fire-and-forget 首条用户 Record；Go 原来只对 Agent/session-message 等待，普通 Native 输入仍忽略该 Promise。新测试在真实第一回合 HTTP 和记录后，把私有 canonical 目标替换为 FIFO，第二回合实际 open 挂起，随后真实 descriptor stat 拒绝非 regular 目标。没有新增生产 I/O seam、假 FS 或延迟。

`native-initial-transcript-nonbare-source-red-v7.log` 保留真 RED：完整原 QueryEngine、processUserInput 和 query 在第二回合记录拒绝后未请求模型，下一 human 恢复后共 2 POST；Go 旧分支第二回合却返回 nil 且已发模型，最终 3 POST。Source 隔离修正过程 v2–v5 拒绝未声明父目录文档/工具路径，不算通过；v1/v6 只是编译中间态，不是业务失败。

现仅 `sdkhost/host.go` initialRecord 分支按实际 SIMPLE truthiness 等待普通非 bare Record。既有 scope 属于 session，模型取消不取消文件作业。`native-initial-transcript-nonbare-cancel-resume-race-v9.log`：4 profiles -race passed 8.621s。Source normal/cancel 均实际等待 IO 拒绝、下一 human 请求成功并经原恢复 loader 读取物理文件，0 blocked；Native normal/cancel 均同样拒绝，随后真实输入恢复，再构造完整新 MainPrint 从自己的物理文件 resume，下一 HTTP 保留前文，各 3 POST。此 fresh MainPrint 证明不外推为新 OS 进程验收。

该修复不影响 SIMPLE 冷 SDK profile，不用于把既有 cold picker/title RED 改绿。

`native-initial-transcript-root-env-regression-v10.log` 按原门禁 `root-option-private-env.json` 执行三包相关原回归 -race 全 passed：sdkhost 7.910s、clibootstrap 13.447s、application 48.042s。包含 bare/header、有限 guard、取消/两 ASK、只读失败、原 replay、实际 stdin priority、新 normal/cancel/恢复，以及真实公开六个退出/历史分支和零 DNS/代理拒绝。公开 fixture 仍断言真实 Read/HTTP、session hook、canonical 不变、scrollback、termios 与零外网；分支实际耗时 1.59–2.60s，不扩大期限。

## 普通 `/diff` 与初始 MCP prompt 目录（进行中）

新增独立 `terminaldiff` 数据生产者、MainPrint 真实 Git/structured tool-result 快照、受 Session 管理的异步面板生命周期与 TUI DiffDialog 状态机。挂载先展示 Loading；实际 Git 子进程完成后只更新 Current，保留该次挂载的逐轮 history。Esc/全局退出取消并 join 已持有 Git，完成回调通过 mount ID 拒绝旧更新。仍共用原 Owner、Controller、Host；没有另建 Harness。

- `terminal-diff-source-v1.log`：完整原 parseGitNumstat/parseGitDiff/parseShortstat 与 useTurnDiffs，限制/累计/恢复等 -race passed 2.470s。
- `terminal-diff-keys-v3.log`：完整原 DiffDialog React 状态/真实原 key callback 的 source/file/detail/dismiss 对照 passed 1.787s。v1 是 NewState 参数编译错误；v2 是 fixture 未执行 React effect 后再渲染，不算生产 RED。
- `terminal-diff-fallback-source-v1.log` 保留窄宽度中文/组合字符换行的真实 RED；修复字素显示宽度后 v2 passed：terminaldiff 2.441s、tui 2.714s，24 组完整原 StructuredDiff/Fallback 与原 diffWordsWithSpace 语义。尚需保留后续实际完整 UI 回归结果。
- `terminal-diff-public-v2.log`：application 19.886s、clibootstrap 4.882s、sessionplans 1.493s，全部 -race passed。真实编译的 Go CLI 经 PTY 打开实际 Git 详情→Esc 回列表→Esc 关闭；再实际 held Git→Esc 取消/join→下一 human HTTP；又 held Git→双 Ctrl-D 全局退出，真实 termios 恢复、exit 0、0 外网。另同一 Session 真实 Read→Write→Current/逐轮 diff→下一 model POST，Owner/Controller/Host 身份不变。v1 公开脚本未等待第一 Esc 的可见列表切换就连续发第二 Esc，raw 保留；没有新增延迟或扩大 deadline。
- `terminal-diff-original-git-source-v4.log` 保留真实完整原 fetchGitDiff/fetchGitDiffHunks 对照 RED：execa 默认剥掉最后换行，Go 初版未剥，导致多出空 hunk 行。修复仅该真实命令返回边界，parseGitDiff 的显式末尾空行规则保持。v5 完整原模块在真实私有 Git repository / MERGE_HEAD / 非 repository 三 profiles -race passed 4.087s，严格 subprocess/fs guard 0 blocked。v1–v3 为未显式绑定继承 cwd 或临时目录不满足私有 guard 的 fixture 拒绝，不计通过。
- `terminal-diff-integrated-v3.log` app 19.442s / terminaldiff 3.749s / tui 2.422s passed；整个命令退出失败，因为独立 clibootstrap vet 读到同组 Root 正在接线的 nativePlanCommand 声明中间态，不算完整门禁 passed。

09 冻结快照的 `TestPublicCLIMCPPromptListGetMediaAndCanonicalModel` 在当前真实入口重现 unknown command（v3 raw 保留）。根因是 headless MCP discovery 结束时 Host 还未构造，初始 registry 没收到完成的 MCP prompts；窄 publisher 不应通过重新加载所有非 MCP 命令掩盖。现仅在该实际启动完成点调用 typed MCP-only publication，原公开 prompts/list→prompts/get→实际音频文件→canonical model POST 断言与完整原 metadata/get/初始 registry 断言在 v2 全绿；不刷新插件闭包、不放宽 foreign guard。

`SlugForSession(scope,targetSID)` 是实际 `/branch` 预切换 plan-cache overload，真实冲突文件/随机重试/保留 parent SID/foreign scope 测试 passed，不能单独作为 slash 项验收。

## 普通分支、恢复及同一运行图的下一请求

现有 Session/Owner/Controller/Host/Writer 图上新增有限原版 REPL 切换入口。实际选择能力只能来自本进程的文件枚举/完整 log；恢复等待 SessionEnd/SessionStart hooks，保留原 file-history/read-cache、plan/worktree 与 target-cost-before-old-save 顺序，再修改同一个 bootstrap SID 和 Writer 目标，安装原历史。没有重建 Harness，也没有放宽 headless/foreign/运行中 ASK 的限制。

- `terminal-branch-resume-pending-public-v13.log`：application 21.075s / clibootstrap 5.689s / tui 2.608s，均 `-race passed`。真实 Go CLI PTY 中先 human，再 `/branch`、`/fork`，两份旧 parent 文件逐字节保持；接着 `/resume UUID`、`/continue TITLE`、普通 picker 的 search/preview/rename/select。六次实际 loopback HTTP 均检查恢复 SID 和指定历史，退出返回 0，termios 恢复，外网尝试为空。
- v6 的下一 human 提前进入旧 SID 是真实 RED，保留原文件。修复分两层：原 `processSlashCommand` 外层 Promise 只由 `onDone` 完成，恢复 Invoke 现在等该真实完成；TUI 在异步 local-JSX 被接收时保留具体 pending command，直到所属返回/实际卸载才放行下一项。显示或文件已创建不被当作完成。
- v12 picker 重命名文件已写成功，但搜索保留旧 title、用户无法看到新项，是真实 RED。原 `LogSelector.handleRenameSubmit` 的 `onLogsChanged` 调用 `ResumeCommand.loadLogs`，同步 `setLoading(true)` 卸载选择器，再从真实磁盘加载新 title。Go 现发布对应 loading/unmount；普通同 ID 刷新仍保留查询，只有实际卸载重建才清除它。
- `terminal-resume-rename-source-diff-v1.log`：clibootstrap 14.453s / application 13.334s / tui 1.418s，均 `-race passed`。完整原 ResumeCommand 与真实 sessionStorage/Git/file profiles 覆盖 UUID、trim/case title、missing title/UUID、picker 排除当前 SID、cancel；新增原 `saveCustomTitle` → `onLogsChanged` → loading Box → 实际新 title 的回归。原 `processSlashCommand` canonical 完成工厂包含 branch/resume/diff：system 两条、default caveat/user/stdout、skip 零条，同一个 Native Harness 的下一请求读取该历史。真实公开 Diff 生命周期原断言也通过。
- 先前 v11 是共享 tui 导入的编译中间态；v1–v5 Source 构造期间的 guard/React callback fixture 错误另有原始日志，不能作为业务失败或通过。公开测试没有增加 deadline、sleep、flush、排序或假 success notice。原版 system/local_command renderer 返回空，分支验收依赖真实 SID、文件及下一请求。

`terminal-resume-preview-capture-red-v5.log` 保留另一个真实产品 RED：预览完成后向同一私有物理文件追加新 user；旧 Go 的预览确认再次读文件，实际第三次模型 HTTP 包含用户没看到的 `EXTERNAL_AFTER_PREVIEW_UNSEEN`。完整原 SessionPreview 的真实 React effect/loadFullLog 与 confirm:yes 回调，连同原 ResumeCommand 父 onSelect，在相同物理追加之后仍传递预览时已捕获的 fullLog。

现应用保存预览生产者实际返回的 sealed `TerminalResumeSelection`，通过独立 preview-confirm 消费；显示 JSON 只供呈现，不能充当恢复能力。普通列表选择、仍加载或失败的 lite preview 继续走原 loadFullLog；再次预览清除旧展示投影，新挂载显示 loading。v6 `-race passed`：application 25.992s（原六 HTTP 流程 13.34s、新 captured-preview 三 HTTP 10.34s）、clibootstrap 11.959s（完整原八 profiles 和物理同图）、tui 1.650s。v2/v4 公开脚本等待“已经可见的历史必须重绘”或误把重命名输入当作 fresh row，属于 driver 失败，未称为生产 RED；修正为实际 modal unmount 的 prompt 和真实 renamed row，未增加等待期限。

这只是上述已执行范围的证据。高级 picker tree/group 布局、remote-agent task 恢复、contentReplacement true gate、ant asciicast producer，以及 raw UTF16 slash 参数跨 UI 的全部边界尚未由这组证明闭合；不把启动 resume 或服务器 branch 的旧通过外推给这些消费者。

## 原生文件结果与真实 Promise 反应

`pending_job.dispatch` 和 `append_job.dispatchAppendOwned` 先前在实际 syscall 完成的 Owner 回调内直接执行 await 后续逻辑。新增完整原 `Project.prepareTranscriptFile` 的真实私有 mkdir/open/stat/close 观测：mkdir 的 Promise resolve 时 open 尚未调用，下一实际 await reaction 才调用 open；没有假文件结果、延迟或额外 I/O。v3 原行为回归在预期的 inline/no-reaction 断言失败，原始 Source receipt `blocked=[]/physical=true` 已保留。

窄修复将相同不可变 native 结果交给既有 Owner.Microtask，原 worker 的 native reference 覆盖该 reaction，随后才释放。`transcript-native-io-promise-reaction-v4.log` 相关物理 prepare/record/append/flush/取消/finally/foreign scope `-race passed` 3.733s；`transcript-native-io-sdk-parent-regression-v5.log` sessioncontroller 6.581s / clibootstrap 82.875s passed，包含真实 stdin priority/EOF、parent headers/physical writer 和关闭边界。SDKHost selector 没有命中测试，明确不计该包消费者证据。四包 vet 和 diff check passed。

该实际 await 修复没有闭合 cold title。`sdk-monotonic-cold-picker-native-io-await-v6.log` 原严格五对仍 failed 45.728s / 117 expects：每次 Source title 为 `now original next original`，Go 为 `later original`；每一方从自己的文件冷恢复后的下一 HTTP messages 相等。新 raw 与 SHA 在 `cli-flags/sdk-native-io-await-v6-preserved/manifest.json`，numeric 0..4 明确是本轮 fresh 范围；另保留的无编号旧文件不计 fresh。十个本轮 accepted 文件都有两条正确 UUID/parent/content authored user，绝非丢掉两条 user。没有修改 strict 期望、golden、Source 排序或模块预热。

## enqueue finally 与 Record 返回 Promise 的实际消费者

新增完整原 Project/recordTranscript 观测，实际私有文件与原 memoized UUID Set 保持。原 pending-zero Promise 在 trackWrite 的 finally 被 resolve；它的反应先于 appendEntry 的 await，所以当时 user UUID 未加入、追加仍 pending。旧 Go 把 token finally、Set.add 和追加返回放在同一回调，`transcript-enqueue-finally-source-red-v1.log` 真 RED 1.984s（user、queue-operation、metadata 三条均不同）。现分开实际 body/finally/caller 反应，不新增文件、网络、timer 或 fairness 工作。v2 相关实际文件/追加/记录/flush/取消回归 -race passed 4.729s。

原 async insertMessageChain `return trackWrite(...)` 还有真正的返回 adoption。完整原 recordTranscript 的零计数观测：track 已 fulfilled，而 insert/record pending；下一观察 insert fulfilled、record pending；再下一观察 record fulfilled。旧 Go 在 finally 回调直接返回 Record，v2 `transcript-record-finally-source-red-v2.log` 真 RED 1.493s。现仅在实际插入分支建 body/track/insert-return Promise，按原 body 调用之后登记 adoption，最后在 recordTranscript 的 await 计算返回 UUID；没有修改 parent/SID/metadata/lastPrompt 业务规则。行政 Owner close 仅拒绝外部 obligation，不在 watcher 跑 finally 或修改缓存。

- `transcript-record-finally-source-race-v3.log`：原 Source/真实文件、记录/追加/取消/拒绝/finally，-race passed 6.378s。
- `transcript-record-finally-native-parent-sdk-remote-v4.log`：Owner close/foreign/missing/duplicate 负例、实际 stdin priority、held 非 bare canonical IO、已接纳 parent append、CCR/ingress 真实 Source/HTTP，-race passed sessioncontroller 3.456s / clibootstrap 11.748s / remoteio 6.763s。
- `transcript-return-public-branch-resume-v5.log`：原公开 branch/fork 与 captured-preview 三 HTTP，application -race passed 39.797s（branch 子例包含本次 CLI 构建，公开 driver 期限未增加）。
- `transcript-return-public-resume-source-guards-v6.log`：原六 HTTP UUID/title/continue/picker/rename/preview 同 Harness 流程和 Source/guard，application 11.509s / sessioncontroller 5.211s passed。

两次修复后严格 cold 分别仍 failed：`sdk-monotonic-cold-picker-enqueue-finally-v7.log` 35.50s、`sdk-monotonic-cold-picker-record-return-v8.log` 36.07s，各 117 expects。每次五条 Source picker title 都是 now+next，Go 都是 later；自己的实际文件冷恢复后的下一 HTTP messages 全相同。两轮各 21 个本轮 raw/summary 逐字节保存到 `sdk-enqueue-finally-v7-preserved` / `sdk-record-return-v8-preserved`，manifest 列 SHA。不能把这两项真正 Promise 修复宣称为 cold title 完成。

## 仍未通过的整体要求

SDK cold strict/picker 的可见标题差异和原 RED 继续保持 OPEN，当前转回本组审查实际 Writer/Controller 调用边界；没有改绿。09 全 parity diff 中两条 authored user 在 Go 文件内均存在，位置移到了 queue-operation 前；该日志不是“两条 user 丢失”的证明。最终 lastPrompt/可见 title 仍不同，不能因后续 HTTP history 相同就宣称完成。完整最终 `verify`、正式 changed coverage ≥90%、同一最终候选的桌面/Web/CLI 验收、真实平台/账号边界和 120 对 Benchmark 均不能从上述聚焦证据推导。正式 Benchmark 未执行。

## 2026-10-14 /resume 远程任务恢复：有界实际消费者证据

已接冻结 Source 的两个真正调用点：REPL 非 fork adopt/worktree 恢复之后 fire-and-forget，以及 TUI 初始历史非空的实际 mount effect。沿原生侧文件、cached account/organization、官方 Sessions URL（私有 CONNECT/TLS 传输）、task、1000ms poll、同一 Controller AppState tasks/输出/通知。后台消费者用真实 Future.Then reaction 返回 Owner，不占据 idle callback 栈，不新建 Harness。初始空会话、headless、foreign、nil 和 closed authority 不取得该入口。

`remote-agent-source-native-regression-v21.log` focused -race 全 passed：clibootstrap 9.054s、ccrsessions 2.271s、application 12.110s。完整原版 restore/Task/API/输出/queue 与实际私有 TLS 测了 11 个业务 profile：成功/错误、长期任务和 TodoWrite、ultraplan result 不完成、review progress/tag/split/assistant/error、archived，以及原始 JSON 字符串 flag 的 JS truthiness。实际 API 分页、metadata 失败保留 delta、关闭在途 HTTP passed。完整原版与纯状态对照单列，不能代替原生公开消费者。

原生 MainPrint/Session 五个 profile passed：初始历史和普通 /resume callback 均恢复实际侧文件，404/archived 删除、401 保留，输出持久化，下一普通模型 HTTP 保留恢复历史；held poll 与普通 ASK 实际同时执行且 Owner/Controller/Host 不变；TaskStop 真实 archive POST，kill 不取消已在途 poll、晚到输出不能复活 killed 状态；global close 取消/join HTTP，不调用 cloud archive、不删除可继续恢复的侧文件。

`remote-agent-public-pty-v19.log` 两条真实 RunCommand PTY（初始 resume、普通 /resume）passed 8.895s。每条两个 held remote HTTP、第一 human model POST、/tasks 可见/选择/x、一个 archive POST、第二 human model POST、双 Ctrl+D 退出，总计四个普通模型 POST。退出时未结束的第二远程任务侧文件原字节仍在。Go 与物理 slave termios 都恢复。v17/v18 原始失败仅 XNU PENDIN 0x20000000，完整 before/after 保留；复用现有真实 FIONREAD probe 得到 pendingInput=0，无 read/discard、无 flags mask。

`remote-agent-native-vet-v22.log` 六包 vet exit0，git diff --check exit0。`artifacts/go-migration-20261013/cli-flags/remote-agent-restoration-bounded-receipt-v22.json` 保存原版 SHA、19 个新文件与 13 份原始日志 SHA/字节数。v20 guard test 修改启动后 immutable Options.Interactive，触发 plugin worker race；去掉不当测试修改后 fresh v21 race 全绿，没有改 worker 或权限守卫。v9/11/12 slash fixture 缺实际 RunCommand 的 NativeInteractiveQueue 或绕过 TUI local-JSX 入口，原 guard 正确拒绝；实际绑定后 v14/21 passed，不是生产权限放宽。v15 archived wire fixture 缺 session_context，原 getBranchFromSession 抛错并吞掉 metadata，修有效 SessionResource 后原断言 v16/21 passed。

尚未覆盖真实 Claude OAuth/云 CCR（用户明确排除）、30min review deadline/持续 API 错误、五次实际 1s stable-idle、畸形 API/侧文件与原始 UTF16 通知边界、更丰富远程任务 detail/browser/todo UI。这是有界消费者证据，不是完整远程 live 功能或最终门禁。冷 SDK strict 仍 OPEN；未排序、预热、延迟、flush、偏好标题或改变原版标准。

## materializeSessionFile async 返回：独立真实缺口

完整原版实际私有 prepare/mkdir/open/stat/close、materialize 与 insertMessageChain 观测显示：第一个 prepare Promise 完成后的原反应图，四次只读探针得到调用次数 `[1,1,2,2]`；旧 Go `[1,2,2,2]`，跳过了调用者 `await materializeSessionFile()` 的 async 返回反应。`transcript-materialize-return-source-red-v1.log` 为真实 RED 2.007s。窄修在真正 materialize 的 buffered-loop 完成之后返回一个 fulfilled Promise，再由现有 Owner reaction 调用第二次 prepare；没有增加文件调用、timer、fairness 或改变 lastPrompt/父链。完整原版空队列、真实 buffered queue 及 Record 返回 adoption 的 v2 `-race passed` 3.943s。

严格 cold 五对仍 failed：`sdk-monotonic-cold-picker-materialize-return-v9.log` 39.95s / 117 expects，Source title 五次 now+next、Go 五次 later；各自文件冷恢复后的下一 HTTP messages 全相等。21 个本轮 raw/summary 逐字节保存到 `product-audit/monotonic-v9-materialize-return-preserved/receipt.json`，不能把这一 Promise 缺口修复宣称为 cold title 完成。

新公开恢复回归暂未重新宣布通过。v3/v4 的 UUID 后下一 human 超时经终端原始输出确认：driver 以旧历史文本为恢复完成信号，但 Loading 重绘也包含该文本；输入实际仍在 modal 中。已改为真正恢复后会话 prompt 或 VT 当前屏幕中新历史出现、旧会话独特内容消失、Loading/Picker 卸载。v5 错误假定恢复的 branch prompt 一定带标题；v6 又碰 VTScreen.text 的 rstrip 将 `> `裁成`>`；这些 driver 失败保留，所有 HTTP 数量/body/SID/父文件/termios 断言未放宽，当前等待 compiler-cache 安全窗口后复跑。v3 第二 preview 测试另为 HelpOpen 共享声明中间态 build-only，不能把第一条运行时超时也归为编译问题。

后续 fresh `transcript-materialize-return-native-public-v9.log` application `-race passed` 23.359s。原六 HTTP 的 UUID/title/continue/picker rename 与下一 human，同一实际 CLI/Controller/Host；另一 captured-preview 三 HTTP 不包含确认前新追加、未展示的 user。driver 只改为真正 VT 当前屏幕的旧会话独特内容消失、新内容出现、Loading/Picker 卸载，再发送下一输入；原 25s deadline 和全部网络/SID/父文件/termios 断言保持。v7 对增量字节重画 session-name prompt 的错误要求、v8 Teams 声明 file-list 编译窗口均保留。

## recordQueueOperation 的 async body/finally/返回链

完整原版 `recordQueueOperation → Project.insertQueueOperation → trackWrite(async () => await appendEntry)`，先缓冲 queue-operation，随后由真实 record/materialize 持久化。五个只读 microtask 探针的原 pending-write 计数 `[1,0,0,0,0]`、返回状态 `[pending,pending,pending,fulfilled,fulfilled]`。旧 Controller.logQueueOwned 在 append 的第一个 reaction 直接减少计数，得到 `[0,0,0,0,0]`；`transcript-queue-operation-return-source-red-v1.log` 真 RED 1.886s，原私有文件 persisted、guard blocked=[]。

窄 helper 保留真实 append，使用同一个 Owner 的 body/track/insert-return/outer-await Promise，再由原 logQueueOwned 直接忽略其返回。没有新增 I/O、timer、预热或队列排序。v3 完整原版/native 的计数和返回 Promise 逐步严格相等，nil/foreign/missing/wrong-kind/closed 负例，`-race passed` 5.309s。v4 真实持久化/SDK pipe/EOF/parent/CCR/Ingress/flush/finally 回归三包 passed：sessioncontroller 7.113s、clibootstrap 18.254s、remoteio 5.966s。实际 held private stat 后强制 Owner close 只拒绝返回观察，未在 administrative watcher 执行业务 finally。四包 vet exit0。

严格 cold v10 仍 failed 37.785s / 117 expects：五对 Source title now+next、Go later；各自物理文件冷恢复的下一 HTTP messages 全相等。21 个本轮 raw/summary 逐字节保存到 `product-audit/monotonic-v10-queue-operation-return-preserved/receipt.json`；`transcript-queue-operation-return-bounded-receipt-v10.json` 保存本轮生产/Source/test/log SHA。该真实 async 缺口修复独立于尚 OPEN 的冷启动可见标题，不改变任何严格期望或宣布整体通过。

## 冷 SDK 的 observation Promise 身份独立审计

实际原 `recordTranscript` 在真实私有文件上证明：同步 read-only adapter 返回同一 callee Promise，原 Promise fulfilled 时 adapter 已 fulfilled；`async (...) => return originalPromise` 返回不同 Promise，当时仍 pending，下一反应才 fulfilled。`native-transcript-observer-identity.test.ts` pinned Bun 1.3.14 passed：1 test、4 expects、0 blocked、两条记录实际 persisted。这个差异不能自动证明既有 cold 失败来自观测器。

真正直接 main 入口的 v3 独立对照使用相同五组 frozen inputs，每组完全原入口、去掉目标观测器、同步返回原 Promise、旧 async adapter 的静态 copy 和 Go，各自真实 accepted 文件 → getSessionFilesLite/enrichLogs → 选择自己的物理文件 → 下一 HTTP。copied test main 仅用 TypeScript AST 将相对 import 指向相同原模块，未修改 product Source、预热、增加 yield、改 input 或伪造文件。50 次实际进程、50 个私有 socket model POST：全部 20 个 Source accepted 及 resumed 文件在各组内与原入口逐字节相同，标题均 now+next；Go 五次 later。25 条下一请求 messages 与原入口严格相等，仍不能验收可见 title/lastPrompt。`controller-observer-identity-v3-direct-entry-preserved/final-receipt.json` 保留 60 份 raw/copied entry/summary/import-map 的 SHA。

v1 另用了 Bun.plugin wrapper 后 dynamic import 旧 test main。该额外入口下 direct/sync/未改 async Source 都得到 later，与 Go 相同；连未改 async 都受影响，证明它是额外入口/模块加载的 confounded 时序诊断，不能替换正常 CLI Source 或把 P0 改绿。40 份进程/HTTP raw 与 manifest 保留。v2 static-copy 的第一次准备仅遇 root TypeScript 包不存在，控制原入口已执行但 generated entry 缺失；改用已安装 desktop TypeScript parser 后 fresh v3 完整执行。`controller-observer-identity-bounded-receipt-v3.json` 明确区分以上证据。旧 strict/golden/所有失败归档保持；正常 cold P0 仍 OPEN。

## Record 真正调用的 getBranch 外层 Promise

原 `utils/git.ts:263 getBranch` 是 async 包装器，返回 `gitFilesystem.getCachedBranch()` 的 Promise；现 Git Service 和旧 Source 合同针对 getCachedBranch 层，不包括这层 async return。完整原两个模块、真实私有 Git primitives、同步返回原 callee 的只读 observer：inner fulfilled 时 outer pending，下一反应 outer fulfilled，HEAD 值不变、samePromise=false、blocked=[]。旧 OwnedRecordBranch.Begin 直接返回 inner，首 probe 就 fulfilled；`transcript-record-branch-return-source-red-v1.log` 真 RED 2.044s。

窄修只给实际 Record binding 返回包装 Promise，在同一 Owner 登记原 return-adoption job 和 forwarding reaction。实际 Git Service、缓存、watch/cleanup、CWD getter、物理调用不变；legacy 同步 profile 不走这条。已有 context regression 原强转 `*gitbranch.BranchFuture` 错把 callee 当实际 getBranch 返回，按原版证据改为 distinct `*ownedRecordBranchReturn`，原 await/value、唯一 process service/cleanup=1、live CWD、nil/foreign token 等全部断言保持。v2 该旧类型断言失败保留；fresh v3 三包 -race passed：sessioncontroller 7.721s、gitbranch 5.290s、clibootstrap 92.119s（含真实 SDK pipe/EOF、held 非 bare first Record 和 parent）。v4 公开九 HTTP branch/resume/捕获预览回归 -race passed 18.045s；四包 vet exit0。

严格 cold v11 仍五对 failed：Source title now+next、Go later，各自文件冷恢复下一 HTTP messages 相等。21 个本轮 raw/summary 的 SHA 保存在 `product-audit/monotonic-v11-record-branch-return-preserved/receipt.json`。`transcript-record-branch-return-bounded-receipt-v5.json` 列生产、原版 fixture、test 和日志 SHA，不能把该 alias Promise 修复宣称为 P0 或最终门禁通过。


## GitFileWatcher await computeBranch 的返回反应

完整原版 get('branch', computeBranch) 的真实无仓库 HEAD 计算 Promise fulfilled 时，返回仍 pending、cache 尚未发布；下一反应 fulfilled/published。v2 真 RED 1.216s：旧 Go 在同一个 computed-return 回调已 fulfilled/published。原版 watcher 初始化仅该 leaf profile 的前提，未作为普通 cold 预热。窄修只增加同 Owner 的实际 compute 返回 Promise reaction；真实 IO/watch/value/API 不变。v3 -race passed gitbranch 7.922s、sessioncontroller 4.390s；原九 HTTP 公开 UUID/title/continue/picker rename/preview 链 passed 18.648s；四包 vet exit0。

strict cold v12 仍 failed 五对 / 117 expects / 48.426s：Source now+next，Go later，下一 HTTP messages 全相等。21 raw 已冻结 monotonic-v12-compute-branch-return-preserved/receipt.json，独立 bounded-receipt-v5 保存本轮源码和日志 SHA。本项 async 缺口修复不代表可见 cold 标题验收通过。


## checkpoint10 两项 running-Agent 跟随消息 fixture

当前原selector先 -race 真失败4.249s，实际第二HTTP无coordinator消息且task pending未排空。两个旧测试在 MainPrint 启动后改外部 Native.Environment；子Agent从已绑定scalar process environment继承原 DISABLE_ATTACHMENTS=1。只改为已有before-constructor callback设置0，生产Agent/附件guard不变。原两selector -race passed4.881s；完整原版 getAttachments 四gate + 原真实HTTP两测试 passed5.437s/0blocked；同正式unit原参数 p4/json/count1/15m/atomic/fullcoverpkg，仅两selector passed2.107s（并非整包/90门禁）。

两实际disabled/SIMPLE阴性下一Bash-result HTTP不含pending，task保留同消息、均恰好2HTTP，-race passed4.149s。初版阴性多设置SIMPLE=0导致已有bare-env guard在HTTP前拒绝，v5/v6日志保留；改用原默认absent或实际1，未放宽guard。running-agent-message-bounded-receipt-v7.json保存源码/原失败/新pass/独立profile SHA。


## Git ensureStarted async return identity 与反应

完整原版 cold/concurrent ensureStarted 两个caller各自返回不同于单一start/init且互不相同的Promise。实际start fulfilled时两个返回pending，下一反应fulfilled。旧Go返回单一s.init，phase0全fulfilled；真RED v1 .807s/0blocked。窄拆原start body，保留单一缓存init与全部真实IO/watch/值，分别同Owner返回adoption，热initialized原fulfilled返回不变。v2 -race gitbranch2.875s/sessioncontroller25.795s passed；四包vet passed。

原9HTTP公开resume新v4捕获driver只等physicalfork文件时下一输入prefix被实际ConversationResumed的Editor.Reset清掉，原body断言失败保留。依据原branch call显式awaitcontext.resume后才onDone，driver等待唯一实际Source成功行再输入，v5 -race passed36.003s，全body/SID/不可变父文件/preview/termios原断言保持。新增fullOriginal heldresume file-before-onDone证明尚因固定Bun二进制消失未跑，不冒充Source新动态PASS。

strict cold fresh v14 仍五对 failed46.030s/117expects、title Source now+next vsGo later，下一HTTP相等；21本轮raw和4明确单列旧非索引raw保留manifest。v13是securestorage共享声明中间态build-only失败，分开保留。git-start-return-bounded-receipt-v5.json保存所有SHA；coldP0仍OPEN，无公平性/预热/排序/timer。


## loadTranscriptFile → loadSessionFile → getSessionMessages 的真正返回链

原完整三函数/private小JSONL真实读取：transcript fulfilled后四probe，cache为[pending,pending,fulfilled,fulfilled]；原session alias先pending后fulfilled，且memo两次caller为同一最终Promise、原uuid键不变、0blocked。旧uuid_job.next在FinishRead后同callback建Set/settle，四probe均fulfilled，真RED v3 1.173s。恢复同Owner async loadSessionFile的返回adoption与getSessionMessages的await，真实IO/reducer/SID缓存身份/路径/Set键与closed/foreign/cancel语义保持。无新IO、timer、公平性、预热或FIFO改变。v1/v2固定Bun被删除导致exec ENOENT、nativev4语法中间编译错误明确单列保留，不能称产品RED。

v5 -race passed20.109s，含原UUID物理/coalescing/cacheclear/cancel/descriptorclose/ownerclosed及Record/Pending/Append/Materialize/queueoperation/prepare。原9HTTP公开同CLI恢复/下一请求v6 -race passed16.672s，所有body/SID/父字节/preview/termios断言保持；四包vet exit0。恢复后原Branch held-resume两profiles动态Source证明也passed2.828s：fork文件已写入但onDone仍0，resume释放后原callback1。

strict cold fresh v15仍五对failed39.775s/117expects、Source now+next vsGo later，下一HTTP messages严格相等。21raw已冻结monotonic-v15-uuid-load-return-preserved/receipt.json；本轮源码/原失败/新pass/hash在transcript-uuid-load-return-bounded-receipt-v7.json。冷P0仍OPEN，最终90%/同版本三入口/120Benchmark未从上述bounded证据外推。

### 2026-10-14 public subprocess coverage collector

Read-only Go1.25.7 runtime audit proved two actual losses: uninstrumented fresh CLI builds produce no counters; reused `m.Run` children ignore an environment-only `GOCOVERDIR` unless `test.gocoverdir` is aligned, and parent test teardown filters distinct fresh CLI metadata. The toy audit's six physical processes, original profiles and independent `covdata textfmt` remain archived in `cli-flags/coverage-collector-audit-20261014/`.

The quality runner now preserves its original `coverage.out`, writes a source/target/coverpkg manifest in the standard unit `-outputdir`, retains actual child raw data and binaries, and produces a separate matching-block union. A shared fixture-only Go utility plus thirteen thin test adapters handles existing builds/commands; ordinary/race/benchmark invocations stay uninstrumented. Relative parent `./...` is resolved to its module import scope for existing child builds in a different cwd. Source SHA, binary SHA, actual Go target/version/cover/tags and exact compiler block/statements are checked; mismatch fails. Existing testdata policy and the two explicit `native_process_fixture` OS files remain raw evidence and do not replace the default denominator. No threshold, Source declaration, test assertion or deadline was lowered.

The bounded immutable backend copy's original public `/exit`, `/quit`, Ctrl-D and post-Read PTY cases passed. With app+cmd parent inventory the strict union used 16 actual counter files, retained one tagged Go1.25.7 darwin/arm64 binary and189300 matching blocks. Original374858 executable lines stayed374858;1599 covered lines became23092 (+21493). This is a small selected consumer experiment, NOT full90 evidence. The first experiment omitted cmd from its parent selector and correctly failed on `early_settings.go`; its raw rejection is retained. Source pre/copy/post/final SHA equality and both parent/child profiles are preserved under `cli-flags/coverage-public-actual-v1/`.

Current policy including actual compiler-counter regression:608pass/0fail/7897assertions. Latest actual fresh/self/nested runtime counter and failed source/binary/target/block/mode checks:21assertions pass. Thirteen adapter packages compile-only passed. Exact commands/log paths/file SHA are in `coverage-collector-handoff-receipt-20261014.json`. Formal full Go90 gate, all-surface acceptance and120pairs remain not passed/not run. Cold SDK five-pair title contract remains RED OPEN; no fairness/timer/flush/sort/title changes were made.
