# Native SDK 动态输入与本地 cached Claude 身份：2026-10-14

当前分支不合入 main。冻结 Source MAIN 为 `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`。本记录是中间消费者证据，**不代表最终质量门禁或完整迁移通过**。全部 HOME、credentials、transcript、MCP 和模型传输均使用私有 disposable 目录与 loopback；没有真实 Claude OAuth、刷新或 live provider quota。

## 实际 SDK 调用者

`sessioncontroller/testdata/fixture/{main.go,stream.go}` 的动态 profile 现在从 `NativeMainPrint.Run` 普通 stdin 进入同一 Owner、Controller、SerialASK 和 Harness。旧有限 leaf profiles 继续使用原合同。Source 动态 profile 使用实际 `main.tsx` 的 builtin/plugin/command 与 agent definition 预热，再进入原 `runHeadless`；两端均使用 `sdk-cli` entrypoint，不把 Source prepared history、请求或转录输入 Go。`WrapSDKASK` 只做和原版相同的实际 `yield*` observer，不产生替代 ASK。

原版 `QueryEngine.ts:490-531` 在实际 ASK 启动后执行 `processUserInput`、生成附件与 user message，再调用 `recordTranscript`。SIMPLE/bare 分支不等待用户转录的物理 append。动态 Go native producer 已移到同一实际 ASK 时点；有限/non-native producer 保持原 admission 合同。真实 stdin 已缓冲完整行只查看 `bufio.Buffered()` 已知内容，不用阻塞 `Peek` 占住 Owner；半行/EOF 继续异步 Read 和 Owner continuation。

真实 native 文件 stat 完成通过 `SubmitIndependent(Read)` 接入，再以原 Promise reaction 的 `Microtask(Flush)` 消费。没有把物理 I/O readiness 注入正在排空的微任务链，也没有强制所有 append FIFO。

## 可观察结果与保留的 RED

`native-sdk-source-cold-warm-v1.log`：实际 Source fresh cold 5 次、额外实际 cleanup 预热的 warm 5 次，1 test、70 assertions、37.82 秒 passed。**warm 是额外调用 `cleanupComputerUseAfterTurn` 的诊断实验，不是正常启动正向验收。** 原版同 warm profile 自身出现不同原始 append 顺序及 last-prompt 条目数；所有 raw 均保留，不排序。

原版 `query.ts:1059/1521` 在取消时实际 await 动态 import 和 cleanup。它与 `QueryEngine.ts:531` fire-and-forget 用户转录、独立队列日志的物理完成没有全序承诺。原版同输入 cold 的 last-prompt 为 `now original next original`；warm 为 `later original`。`SessionWriter` 的 metadata refresh 在 `sessionStorage.ts:952-961` 重追加 memo 中的 lastPrompt；`readLiteMetadata:5055` 实际优先读取最后一个 last-prompt。因此此差异确实在恢复列表中可见，不能忽略为纯日志变化。

`native-sdk-dynamic-v22-source-main.log` 的原正常 cold strict raw 对照仍 **failed**：当前 HTTP body/header、history、UUID lookup、enqueue/replay、ASK/record/HTTP 因果边界相同，但文件 append 顺序和 last-prompt 不同。该测试与旧 raw 失败保留，未变更排序断言，未对 Go 增加模拟 Bun 冷 import 的业务 await。

新 `own persisted resume and next human HTTP consumer` 各自保留本实现的初始文件，再从新的真实进程执行 resume 和下一条 human 输入，使用实际 resume picker、真实 loopback POST、live assistant envelope、结果与重新持久化转录。Go 没有读取 Source 生成的文件。

- 原 immutable parent 是 2025，而旧时钟是 2023：原版实际恢复选择旧 parent chain；Source cold/warm/Go 下一次 HTTP、恢复历史、live history 与响应严格相同。
- constant2026 同毫秒 profile：原版 `findLatestMessage:2290-2294` 用严格 `t > maxTime`，两个新 user leaf 同毫秒时保留较早 leaf。首版要求全部新 user 恢复的错误 fixture 预期已按真实 Source 修正；v3 失败 raw 留存。
- 新独立 advancing ASK clock profile：两端的外部时钟仅在实际 ASK 开始时推进 1000ms，不增加 await，不改旧时钟。later、now、next 全部进入恢复后的下一次实际 HTTP，body、activation history、live history、`FOLLOWUP_NATIVE_OK` 响应严格 Source cold/warm/Go 相同。恢复列表仍保留 cold=`now original next original`，warm/Go=`later original` 的可观察差异。

v4 advancing profile passed：1 test、75 assertions、52.19 秒（含构建）。v5 原时钟/同毫秒 profile passed：1 test、141 assertions、42.46 秒（含构建）。原时钟 v5 的 Go accepted observation 与实际 Source warm accepted observation **完整相等，包括原始文件字节**，与 Source cold 只在 transcripts 字段不同；这证明存在真实 Source 允许的轨迹，不等于正常 cold strict 对照通过。

## 本地 cached Claude producer

普通 `getAnthropicClient` 的实际 Source 在 API-key 调用者上也先执行本地 credentials stat/cache invalidation。`native_claude_fresh.go` 用真实私有文件 stat 与相同 memo/secure-cache producer 实现；改变 mtime 才清全部 Claude token cache，stat 错误仅清 token memo，保留原 secure-storage cache 的时效。当前模型 AbortSignal 不传入 Source 无 signal 的共享身份 getter。

实际 client header 的 parent SID 在 stat await 前采样；body metadata 继续按每次请求晚采样，旧 500 client 复用与 401 client 重建合同未改。过期且有 refreshToken 的 subscriber 尚无真实 refresh producer，明确 capability failure；不能偷偷当未来有效 token 使用。用户明确要求不执行 Claude 真实 OAuth；本轮未登录、刷新或验收。

`native-claude-local-stat-source-physical-v3.log`：实际 Source `getAnthropicClient + messages.create` 六次 POST 与实际 Go `NativeMainPrint.RunTurn` 六次 POST passed（race，5.667 秒）：cached A、同 mtime 改 B 保留 A、改 mtime 使用 B、未来 expiry+refreshToken 使用 C、过期无 refreshToken 使用 D、删 credentials 后使用实际 managed API-key fallback。两端 header 严格一致；没有注入环境 AUTH_TOKEN 代替 cached 身份。

`native-claude-local-stat-and-guards-v4.log`：失败 identity/current Scope/expired refreshable/foreign guard regression passed，requestretry 1.633 秒、clibootstrap 2.565 秒。此命令只选 guard 测试，六 POST 正向结果单列，不把范围混写。

## 实际执行的聚焦检查

- `bun artifacts/go-migration-20261013/run-focused-parity.ts scripts/quality-gate/go-parity/session-controller.test.ts --test-name-pattern 'actual native main advancing ASK clock'`：passed。
- 相同 runner，pattern `actual native main priority batch: own persisted resume`：passed，旧错误预期失败 raw 保留。
- `run-focused.ts test -race ./internal/sessioncontroller ./internal/clibootstrap ./internal/application -run 'Test(Record|NativeRecord|RealSchedule|NativeSDKBufferedStdin|TerminalPastedImage|TerminalAttachment|TerminalImageCancellation|ActualSourceTerminalPaste|ActualNativeClientCredentialStat)' -count=1 -timeout=180s`：passed；sessioncontroller 2.162 秒、clibootstrap 7.511 秒、application 5.502 秒。包括真实 pipe 一 chunk 多行、半行、最终无换行、EOF，以及原媒体/实际 HTTP 身份。该 pattern 未选 `TestIndependentRecord*`，不得声称覆盖那些测试。
- 有限 Source profiles 的独立 fresh 聚焦回归 passed：40 tests、632 assertions、82.58 秒；4 个动态/诊断 profiles 单独选择，不把原 cold strict RED 排除后称全量通过。

所有大门禁由 root 在 SHA 验证的独立 immutable snapshot 上跑；本记录不把 focused tests、build-only、Source warm 实验或中间覆盖率称作最终 90%/Benchmark/三入口验收。

最终本区域五包 vet（cooperative/mcptools/mcpruntime/clibootstrap/application）v16 exit0 passed，当前共享 `git diff --check` passed；不是完整门禁声明。

## 原始提示文本选择与单调时钟消费者（root 后续）

实际 Source `getFirstMeaningfulUserMessageTextContent` 与 `extractFirstPrompt` 的完整业务体用于 14 组输入：内建命令、自定义命令参数、IDE/通知块、Bash 输入、中断提示、多文本块、空白、UTF-16 截断及真实用户文字 `No prompt`。原 Go 独立 Record 与旧 Record 都只取首条文本，修前正确 fixture 的 v2 原始失败保留。现两路径复用既有 insights 选择器，保留 undefined 与字面 `No prompt` 的区别，命令集合只在实际 command-tag 分支惰性读取，原查找错误原样传播。没有修改存储格式或原版 Source。

`record-meaningful-prompt-source-v4.log`：`go test -p=2 -race ./internal/sessioncontroller ./internal/insights` 的聚焦 selector/Record pattern passed，sessioncontroller 2.773 秒、insights 1.962 秒、0 skip。包括 14×2=28 组真实文件刷新/metadata memo 消费者、undefined/惰性查找与错误身份负例，以及既有独立 Record/Source first-prompt 回归。该命令不是两包所有测试。

新的 `actual Go cold SDK file and selected picker retain Source title and next request with a monotonic clock` 保持两端独立原始文件，每次 Date/now 读取推进 1ms，每实际 ASK 推进 1000ms，然后新进程从自己的真实 picker 行选择 fullPath 并执行下一次 HTTP。v1 原始四次运行和消费者 JSON 已按 SHA256 保存到 `product-audit/monotonic-v1-preserved/`。实际恢复消息及下一次 HTTP 的 messages 相等；cold Source picker 标题 `now original next original` 与 Go `later original` 仍不同，测试为 **failed**。此修复只关闭提示文本解析差异，未关闭此独立 Record 完成顺序差异；未加延时、flush、排序或预热来把它改绿。

v2 将 HTTP messages 的严格断言置于标题前，并在任何标题断言前收齐五个独立样本：117 assertions、35.45 秒，五次 HTTP messages 全部相等，五次标题均不同，**failed**。21 份实际文件/请求/消费者记录及 SHA256 存在 `product-audit/monotonic-v2-preserved/`。另一个明确标记 diagnostic 的观察器只在 fixture 内导出原 Project 类并记录实际 insert 参数与 lastPrompt setter，不改 Source 文件、方法体或 Promise 身份；其插桩本身已使 cold Source 出现重叠批次/`later original`，所以不把这项诊断作为未插桩正常 cold 的正向验收。

补齐 inactive Computer Use 的实际清理消费者后，新 v3 正常 cold 对照仍 **failed**：117 assertions、38.26s，五次下一 HTTP messages 相等，五次标题仍不同；21 份 raw／消费者／SHA 保存为 `product-audit/monotonic-v3-inactive-cleanup-preserved/`。CU 清理的独立通过证据不等于关闭此项，未改变标题断言。

`record-insights-full-current-v1.jsonl`：完整 `go test -p=2 -race -json ./internal/sessioncontroller ./internal/insights -count=1 -timeout=240s` passed，sessioncontroller 45.761 秒、insights 3.263 秒、493 pass events、0 fail、6 skip。其中三个 skip 是只供子进程进入的 helper，另三个要求单独 `CC_HAHA_PINNED_BUN` 的原桌面 Source 测试随后以该路径独立补跑，`record-desktop-three-source-current-v1.log` 三项全部 passed、race 3.912 秒。没有把缺少环境的初次 skip 写成通过。

## 子 Agent 的元数据边界

实际完整 Source `recordTranscript(main)` → `recordSidechainTranscript(child, agentID)` → `reAppendSessionMetadata` → `flushSessionStorage` 的物理文件表明：主文件保留主提示，子文件不写 `last-prompt`。原 Go child writer 会把子提示缓存并追加成该条目；`record-sidechain-metadata-source-red-v1.log` 真实 RED 为 Go `["actual child work"]`、Source `[]`。`updateRecordPrompt` 现按已有 child writer 的 AgentID 跳过主 picker memo 更新，不改父历史、子消息链或存储格式。

`record-sidechain-and-selector-source-v2.log`：Source 物理反例修复、28 组提示元数据、既有独立 Record 与 Goal Source 消费者聚焦 race passed（sessioncontroller 3.234s、goalstate 3.052s）。`record-sidechain-public-agents-current-v1.jsonl` 及同名 receipt 保留准确 argv：七个原生入口测试覆盖实际 Agent child Read、Worktree、workflow、hook、后台 sidechain、冷恢复与父权限／子 cwd，11 test events／0 skip／0 fail，bootstrap race 16.042s。该修复不关闭独立 SDK cold 标题 RED。
