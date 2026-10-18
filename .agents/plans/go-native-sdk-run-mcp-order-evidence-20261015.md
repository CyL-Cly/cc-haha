# SDK run 的 MCP 更新与命令出队顺序

这是当前工作树的确定性诊断，尚未关闭冷启动转录/标题差异或完成迁移。没有真实模型、Claude OAuth、真实用户配置或远程账户调用。

原 `src/cli/print.ts` 的 `run()` 在通知 running 后，无条件 `await updateSdkMcp()`，之后才进入 dequeue 循环。空配置也返回 async Promise；同一次 drain 的后续 ASK 不重复调用这个前置更新。当前 Go `RunStream` 则先 `BeginDequeued`，再在 `runAccepted` 调用 MCP 准备，且每个 ASK 都调用。显式 set_servers/reconnect 的 ACK 后 refresh 是另一个原版调用者，不能一并移动。直接 RunOne、RunDequeued、TUI 与 Agent 路径也必须分别保留。

新增 Source 回归 `native-sdk-run-mcp-order.test.ts` 调用完整实际 `runHeadless`，通过 initialize 注册 SDK server，再输入普通 user；实际 MCP SDK Server 与 SdkControlServerTransport 完成 JSON-RPC 握手，随后实际 loopback Anthropic POST 返回结果。观察器保留原 Stream enqueue、queue dequeue 与生命周期生产函数的返回值，没有新增 await、timer 或修改原版业务体。

- 首次运行失败是 private HOME 父目录 `.git` 的缺失夹具声明；保留 `source-v1.log`，不是产品 RED。只增加逐层具体 absent 路径，不开放真实目录。
- 修后实际 Source：1 passed /10 expects /0 failed，0.600秒。initialize 握手开始时 dequeue/started 为0/0，普通输入仍在真实 queue；随后 dequeue/started 各1，实际模型 POST1，capabilities.blocked为空。
- 加入原 parity corpus 后，manifest/fixture guards/新 Source 回归合跑：35 passed /357 expects /0 failed，1.834秒；源码与 routing 输入 SHA 前后不变。

Root 另新增 actual MainPrint.Run→initialize→user→真实独立 Bun MCP peer→loopback POST 的 Go 对照测试。独立副本使用冻结13、两份已明确的 UserCLAUDE test-only overlay 和三份新 Root 测试 overlay，共13,017个源码输入；原13,014 pre/copy/post相同，55模块/15,090文件逐SHA通过，123个 Node 链接均在副本内。runner dry-run通过，原30秒业务 context、`-p=1 -race -count=1`、默认 vet 与90秒 Go test期限保持。当时尚未执行；下文记录同一独占窗口的真实旧版 RED 与修后 GREEN。

ignored 收据位于 `artifacts/go-migration-20261013/root-sdk-run-mcp-order-20261015/`，副本指针是 `owned-preparation-pointer.json`。Source fixture SHA256 `ab6166c1d3afa2bca19e19719b6574673283b4f9f4ff2b14df9c971ccc5d329c`；Source production print SHA256 `6df1d7401d3d3e0b508c2e7139edd00f10920429646a4d950818656ffe21c855`，未改变。

后续真实 Go fail-first 已完成。v1 漏 `BUN_OPTIONS`、v2 的 Source PATH 探测系统 rg 被原 guard 拒绝，均为新测试的 setup 失败，原副本及日志保留。只修新测试的环境：`BUN_OPTIONS=--no-env-file`、PATH 为同一已通过 Source 测试的私有 home/bin；未修改生产、Source、guard、原30秒业务/90秒Go期限。

v3 取得真实业务 RED：完整 Source 与 Go 各完成一个 POST，实际独立 Bun MCP peer 握手成立；原版 initialize 时 dequeue/started=0/0，Go 为1/1。测试在上述比较处失败（top2.03秒/pkg3.109秒，总55.993秒），13,017源码输入稳定，原log SHA256 `8101ef22d67ec3c59644c7b9ef4c7b08f6a5d691a36572c1036d918a406e969a`。

当前窄修复仅两份 sdkhost 生产文件：RunStream 先开始原 run 状态并等待实际 SDK 更新的 async 返回，再进入 dequeue；每个 drain 只准备一次。直接 RunOne/RunDequeued/terminal/Agent 路径保留原准备，显式 set_servers/reconnect 的 ACK 后 refresh 保留。实际 ASK iterator 已证明 streaming caller，因此无需额外调用或第二 MCP store；没有添加 timer、虚构I/O或等待HEAD。

v4 独立相同13基础加两份明确生产 overlay，原同一完整业务回归 race/defaultvet 实际 GREEN（pkg4.306秒，总55.042秒），13,017输入前后稳定，Source仍为0/0，Go修后同为0/0，single handshake/one POST成立。原17个 MCP/RunStream/RunState 邻接 tops随后实际全部 passed：sdkhost2.977秒、clibootstrap14.734秒、总33.664秒，保持默认vet/race/p1/90秒原期限；同一两包独立vet实际passed，总40.896秒；13,017输入前后不变。空配置原调用者的精确FIFO、完整cold转录/title与真实CLI冷启动仍需另验。不得以本次有界绿色关闭上述 P0 或最终迁移。

原 required `multiple-sdk-original-priority-batch-and-last-uuid` 在同一独立候选单独复验中；仅包含冻结13与上述两份SDK生产修复，未把后续CLI sinks/TUI/User生产 overlay 混入诊断。实际仍 failed：Source self-replay通过，原118行Go↔Source比較失败，28.312秒；Go两条用户写入过早、额外初始last-prompt与最终later标题差异原样保留。

随后同一候选fresh ordinary CLI实际build通过9.263秒，五对原v49公开冷启动输入复验总8.582秒：十端exit0/stderr空/真实POST1，Source五端均now+next，Go五端仍later，两个Go端额外写重复later。13,017候选/3,148冻结MAIN Source/driver controls/binary前后身份全部一致。此次Source含既有JS fs/fetch guard；较早v42独立原生Seatbelt无包装五次结果是分开的控制，不冒充本次重跑。MCP前置修复未关闭原priority/完整转录/冷title P0。收据为 `root-sdk-run-mcp-order-20261015/bounded-red-green-neighbors-and-open-priority-receipt-v1.json`。

只读诊断进一步区分观察器：既有inspectRecords开关通过Bun.plugin附加Project export，冷/暖两端在actualinsert中均选later与now+next并最终两次写later；六份Source磁盘SHA不变。对齐原utimes后重复仍如此，说明这种export/module图观察不能当原冷启动authority。原required/no-inspect和freshpublic五次now+next结果不改，Go仍失败；不改golden、不追加无业务await。

2026-10-15只读复核原 required case 的未开启 inspectRecords raw：Source第一条 record 后，实际 transcript stat 在phase12启动/13完成，两轮 prepare 的 mkdir/open均在phase13完成，第二条record是phase22；最终磁盘顺序为四条enqueue/dequeue、later用户、两条dequeue、next用户、now+next的last-prompt。Go同一旧候选的实际磁盘顺序仍是early later last-prompt、两条用户、六条queue-operation、最后later last-prompt。这里只能确认真实写入交错不同，不能从native trace推断未记录的Go准备完成时点，也不能以新增CPU观察器取代原冷启动。已声明testdata-only Writer.Snapshot诊断overlay，尚未执行；不修改生产调度、Source或golden。

testdata-only快照诊断现已独立执行：原相同required case仍在118行失败，Source self-replay已先通过；terminal exit1/42.895秒，13,017源码和五份依赖/runtime前后相同。实际既有callback观察到：首record admission时Writer.path为空/pendingCount3，第二record admission时仍为空/pendingCount6；不是事先猜测已materialize。最终early/last later标题与用户先于queue-operation差异保留。这些快照不能证明未观察的native I/O准备完成顺序或缺少哪个业务await；观察器CPU时序影响仍单列，不能据此改golden或增加等待。ignored `root-priority-writer-observer-owned-v1/observational-receipt-v1.json` 保存raw/result/log SHA与全部十个快照。Root窗口结束实际Go/compiler/test均零并释放给TUI独立clock回归。
