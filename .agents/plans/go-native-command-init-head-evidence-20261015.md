# 普通命令 init HEAD 与 marketplace 文件消费者

本批新增真实 CLI 的七个 preAction 调用点；MainCommand/MainAgents leaf 构造和 Run 不自行执行 HEAD。真实 factory 与 Owner/Native/Settings/environment 四个 identity 绑定、once、Safe 环境、TLS、原 10 秒 timeout/HEAD headers/20 redirects、取消及 Close join 保留。普通 CLAUDE_CONFIG_DIR guard 未开放。只使用 disposable HOME、fake OS credential primitive、loopback，没有真实 Claude OAuth 或模型。

原 whole Source main/Commander→init→真实 plugin marketplace list 的 HEAD1，对原 Go 入口为 fetch0/HTTP0，接调用点后为 fetch1/HTTP0，均保留 true RED。真实 Source fetch-return FIFO 持住 JS 时物理 HEAD 已到 peer；响应头保持未发，释放 return FIFO 后原 handler仍 []/exit0，证明请求启动与响应完成分开。在 fetch 返回后立即真实 process.exit 的独立 cutpoint诊断是 fetch1/HTTP0，不代表 normal handler通过，也排除了退出阶段强制发送 HEAD 的假设。

实际 native HeadTransport 初始化 receipt 仅在 strict primitive 已进入真实 transport、reserve 登记真实 dial/write worker后结算；不等待 TCP、TLS、请求写出或响应头。普通 callback/JSON不能制造该 receipt；foreign/expired/zero object 拒绝，primitive rejection/取消/close结算。原 MainPrint transport/once/10秒/真实pool复用及 heldheaders 独立handler/close、same-area negatives ×3 passed；这不保证快速退出前 physical packet 已到达。

最近一层 marketplace-list 另有业务归属差异：旧 Go 把 actual file read、JSON/schema parse、catch/格式化全放在物理 worker；Source FsOperations async return 原 readFilePromise，loadKnown 在 Owner await后 parse/catch，然后 handler await结果。新 helper仅 exact native factory采用该链：外部 worker只读实际字节，FsOperations return使用 OwnerFuture.ResolveFrom真实 adoption，Owner Then解析/错误处理，handler Await后用原共享纯 formatter。其他 action/finite facade保留原路径，read/error/schema/path规则未变。

有效 old-router overlay只移除该新 consumer路由：实际 private FIFO reader/writer rendezvous、真实 FD held、write `{}`/EOF与 []结果均完成，仅 native readFile authority缺失失败0.21s。当前对应 read/native Close与 foreign/substituted/retained scope ×3通过。whole frozen Commander 四个 known-file profile（空/真实entry未知字段/syntax/schema错误）与原29个 marketplace物理 Source action同区回归通过。默认 private fixtures有既存known文件，第一次 Mkfifo(file exists)未到目标行为；第一次 FD observer未先rendezvous也无有效证明，原日志全部保留，先前误分类已纠正。

首轮4tops/37pass events全部 passed；后续6tops count3为28pass events/16top passes、0skip，强公开 HEAD只有1/3 actualpacket passed、2/3 SourceHEAD1/NativeHEAD0，仍 FAILED。未删除/放宽该断言，未加timerdelay、伪import/FS或HEAD completion await。正常 Source7profiles、held原FIFO/响应与当前 args/SHA/原始失败都在 `artifacts/go-migration-20261013/command-init-head-evidence-20261015/receipt.json`。

本批尚未关闭快退出的跨 runtime I/O时间差与 cold module-load边界、其他六个普通命令的 whole Source/public完整链、Windows实际host初始化边界、sinks/migrations等非模型完整init producer；完整 Go/90%、Desktop与120由Primary负责，不能从本批外推完成。

新增只读 macOS native phase probe 使用真实 DYLD interpose，不包装 Bun builtin fetch。Pinned revision `1.3.14+0d9b296af` 的 exact official Git blobs 已验 SHA：fetch 在返回 Promise 前同步初始化 HTTP thread、入 native queue；暖线程通过 Mach port唤醒。五个独立冷进程、各三次真实 loopback HEAD全部 exit0/stderr空：冷主线程 before→真实 pthread_create→after，然后被创建的 network thread才 socket/connect；十个暖样本 before→真实 mach_msg send→after，再 network send。它直接排除了这组样本的同步 socket/connect假设，不能把 queue/wakeup当请求或响应完成，也不由异线程抢在 after-marker前推断 fetch return时刻。探针 await响应仅用于收齐 trace和暖下一样本；interpose日志会改变调度，不能当普通命令包到达率证据。原 native dylib loader139/无events与首次 SDK header缺失均单列无效工具证据。完整线程/seq、官方 commit/tree/blob SHA和边界在 `artifacts/go-migration-20261013/bun-fetch-native-phase-probe/receipt-v5.json`，本批未改输运或业务，原强公开 HEAD仍 OPEN。
