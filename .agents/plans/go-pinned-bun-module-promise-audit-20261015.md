# 固定 Bun 的动态模块 Promise 审计（2026-10-15）

本轮先核对官方运行时源码，再在三个 CU 模块范围实现原生 Promise 消费者，后续结果单列。没有修改旧 Source 对照、golden、排序或时限。冷启动标题 P0 仍 OPEN。初始只读收据为 `artifacts/go-migration-20261013/cli-flags/bun-pinned-module-audit-v43/receipt-v43.json`；当前有限适配收据为同目录的 `cu-shared-adapter-receipt-v51.json`，保留 v47/v49/v50 历史收据、源码 SHA 和旧失败/新结果。

实际二进制是 Bun `1.3.14+0d9b296af`，SHA256 为 `e0c90ec15d33363e6b70713d56bc3b2c7585c17f40a0fe0f8fd9305901d4e233`。对应官方 [Bun commit](https://github.com/oven-sh/bun/commit/0d9b296af33f2b851fcbf4df3e9ec89751734ba4) 的 [WebKit 构建依赖](https://github.com/oven-sh/bun/blob/0d9b296af33f2b851fcbf4df3e9ec89751734ba4/scripts/build/deps/webkit.ts#L6) 固定为 `5488984d20e0dbfe4be2c3ba8fb18eb81a5e0e8b`。这是已验证的构建源码 pin；没有声称从二进制独立提取了 JSC revision。该版本已经使用 C++ 模块加载链，不能套用旧 JavaScript `ModuleLoader.js`。

原版 `stopHooks.ts:206` 先 `await import(cleanup)`，再等待真实 `cleanupComputerUseAfterTurn`。后者的默认消费者还会 fire-and-forget 导入 badge，并等待 daemon import。即使三个模块已加载，原版 import 也不直接返回已完成的模块 Promise。

| 实际操作 | 固定源码位置 | 已缓存路径的效果 |
| --- | --- | --- |
| 获取 registry fetch Promise | `JSModuleLoader.cpp:349–391`、`ModuleRegistryEntry.cpp:103` | 重用 fetch 结果，创建新的顶层 intermediate/result Promise |
| fetch 完成反应 | `JSMicrotask.cpp:982–1040` | 创建每次 import 的 state Promise、host-load Promise 和两臂 AND-join |
| host-load 已缓存模块 | `JSModuleLoader.cpp:737–750`、`:900–951` | 复用 record/load Promise，执行实际 FinishLoadingImportedModule |
| requested graph | `JSModuleLoader.cpp:1001–1048`、`:824–891` | 创建图加载 capability；已 evaluated 模块跳过 NEW 遍历并完成图 |
| link/evaluate | `JSMicrotask.cpp:1261–1336`、`CyclicModuleRecord.cpp:411–439` | 在图完成反应中链接，获取缓存的 evaluation capability，再取命名空间 |
| 两臂完成 | `JSMicrotask.cpp:1115–1185` | load 和 state 各自递减真实 pending count；两者成功才结算，任一失败即拒绝 |
| internal forwarding | `JSPromise.cpp:216–225` | `pipeFrom` 以一个 forwarding reaction 传递 join 结果 |
| 顶层结果与用户命名空间 | `JSMicrotask.cpp:1062–1099`、`:1338–1357` | 顶层错误处理/pass 后，对用户 import Promise 执行 namespace resolve |
| import 表达式的外层 Promise | `JSGlobalObjectFunctions.cpp:799–837` | Bun additions 创建另一用户 Promise；采用 pending/rejected loader Promise，已 fulfilled 则直接完成 |
| 原 await/then 消费 | `JSPromise.cpp:389–428` | 已完成父 Promise 的反应也进入 microtask queue |

这里 namespace、fetch/load 和 evaluation capability 可缓存，但每次 import 的 intermediate/state/host-result/join/user Promise 分别创建。三个 CU namespace 均没有 callable `then` export，因此最终 namespace resolve 在这一配置直接完成；一般 thenable 分支不能由此删去。同步 `require(esm)` 的特殊模块队列不是这些普通 `import()` 调用的路径。具体实现见官方 [JSModuleLoader](https://github.com/oven-sh/WebKit/blob/5488984d20e0dbfe4be2c3ba8fb18eb81a5e0e8b/Source/JavaScriptCore/runtime/JSModuleLoader.cpp)、[JSMicrotask](https://github.com/oven-sh/WebKit/blob/5488984d20e0dbfe4be2c3ba8fb18eb81a5e0e8b/Source/JavaScriptCore/runtime/JSMicrotask.cpp) 和 [JSPromise](https://github.com/oven-sh/WebKit/blob/5488984d20e0dbfe4be2c3ba8fb18eb81a5e0e8b/Source/JavaScriptCore/runtime/JSPromise.cpp)。

已有 v41 实际 warmed import 观测中，`Promise.resolve(p) === p` 对三个目标成立，完成 consumer 在八个竞争 probe 后运行。数字本身不是可迁移操作，不允许实现成八次 no-op/yield。首次仅移植 loader 图的 v45 仍真实失败：单 import Go 七、Source 八；完整 cleanup Go 十四、Source 十五；默认 caller Go 二十三、Source 二十五。这促使继续核对表达式调用入口，而没有添加空反应。

后续核对确认漏掉上述 `globalFuncImportModule` 的 Bun-specific 外层 Promise，它对 pending/rejected loader 结果执行 public resolve。这个 adoption job 在加载中先登记，最后 forwarding 才等待实际 namespace 结果。因此已有 `OwnerFuture.ResolveFrom` 在这一外层是对应的真实操作；loader 内的 `pipeFrom` 仍必须使用独立的一次 forwarding。官方入口见 [JSGlobalObjectFunctions](https://github.com/oven-sh/WebKit/blob/5488984d20e0dbfe4be2c3ba8fb18eb81a5e0e8b/Source/JavaScriptCore/runtime/JSGlobalObjectFunctions.cpp#L799)。v43 仅 loader 的初始审计收据和 v45 失败完整保留，后续实际对照结果另记，不重写旧输出。

初始 Go `sdk_computer_use_cleanup.go` 的 `fulfilledComputerUseCleanupPromise` 只是创建 future 并立即 Resolve，然后调用者 Then/Await。它没有上述 registry、per-import state、graph、两臂 join 或 namespace 消费。已有 `OwnerFuture.ResolveFrom` 又包含 thenable adoption job 和 forwarding，不能直接冒充原 `pipeFrom` 的一个 forwarding reaction。

若继续修复，最窄的可审核方向是三个真实已预链接 CU 模块的固定 typed entry/namespace/evaluation cache，以及每次 import 的原 load/state 两臂与 namespace Promise 操作。每个阶段必须读写实际 module 状态、传播对应错误或完成真实 join，不以匿名反应凑 probe 数量。应先证明 warmed import 的实际身份、反应与失败/关闭边界，再独立重跑原 cold 产品入口。冷 Source fetch/transpile/文件准备仍是另一项真实外部工作；Go 已预链接模块没有理由伪造此 I/O，因此 warm 操作补齐本身不能宣布 cold P0 通过。

随后已按这个有限范围实现：`sdk_computer_use_modules.go` 只持有三个 CU 模块的私有 typed cache，每次调用保留 state/load 两臂、namespace 与外层 public adoption；`ownerasync/PipeFrom` 单独传递内部结果，保持现有 ResolveFrom 语义。badge、daemon 和 cleanup 三个实际默认消费者经命名空间调用，reconcile 的空 async Promise 保留原逻辑。没有新增物理 I/O、计时器或其他模块导入，没有写入用户配置 schema。

有效 fail-first v44 是原 whole cleanup 的 Source 十五、Go 七。补外层 opcode 后 v46 有界对照通过：三个 module 八/八、完整 cleanup 十五/十五、默认 caller 二十五/二十五。原 overlay/finally 与独立取消回归、缓存/不同 Promise/同 namespace、两臂 join、原错误、foreign/expired/closed 和 PipeFrom 守卫共十个 top-level tests 通过（mcptools 1.017s、ownerasync 0.159s）。v48 同一 selector 的 `-p=1 -race -count=1` 通过（mcptools 2.030s、ownerasync 1.397s），共二十个 pass events、零 skip/fail；两包 `go vet -p=1` exit 0。v47 另保留原版全部五行 stdout，stderr 为空、无被拒绝能力；四个实际 Source 函数 Git blob 均与冻结 MAIN 相同。测试只重定位原 caller 的 import 字面量，不用 async observer 替换 import。

v49 用当前完整生产源重新构建非 cover 公开 CLI（`go build -p=1 -tags=native_process_fixture`，5697 个实际构建输入 SHA 前后不变），再跑 Source/Go 各五次相同输入。所有产品与 driver exit 0、stderr 为空、父文件不变；用户 UUID/parent/content 和下一真实模型请求 messages 五对相等，HEAD→POST 同 TCP 复用两端均五次成立。但严格标题仍零对相等：Source 全部落盘 `now original next original`，Go 全部为 `later original`，其中 Go index 1/2 各自两行重复 later 的原始产物完整保留，没有去重。原生沙箱无 JS FS/fetch 普通包装的 v42 原版五次控制也仍是 now+next；插桩开销已被排除为充分原因。冷 P0、90% 门禁、三个产品入口和 120 对 Benchmark 均不能由这些有限绿证据外推为完成。

继续只读核对的官方冷路径表明：`feature_flags.zig:104` 开启 `concurrent_transpiler`，`RuntimeTranspilerStore.zig:112` 默认 enabled，`VirtualMachine.zig:65` 的 `plugin_runner` 初始 null。真实 `Bun.plugin` 的 onLoad/onResolve 注册调用 `Bun__onDidAppendPlugin`，后者在 `virtual_machine_exports.zig:137–146` 建立 runner；`mock.module` 的 `BunPlugin.cpp:394–403、:704` 只写 virtual-module map，不走这个 setter。因此目前未调用 `Bun.plugin` 的原版入口不能被静态归类为同步插件转译路径。官方 [VM exports](https://github.com/oven-sh/bun/blob/0d9b296af33f2b851fcbf4df3e9ec89751734ba4/src/jsc/virtual_machine_exports.zig#L137) 与 [BunPlugin](https://github.com/oven-sh/bun/blob/0d9b296af33f2b851fcbf4df3e9ec89751734ba4/src/jsc/bindings/BunPlugin.cpp#L394) 已逐文件验证 Git blob。

未缓存普通 TS import 满足 `ModuleLoader.zig:981–1031` 条件时，真实 `RuntimeTranspilerStore.transpile` 创建 pending internal Promise、提交 WorkPool，工作线程完成后进入实际 concurrent task，再由 JS 线程 `AsyncModule.fulfill`。后续 `ModuleRegistryFetchSettled`、`ModuleRegistryModuleSettled`、requested graph 与依赖 record link/evaluate 也有真实状态。Go 的三个模块已经预链接，不能用不存在的源文件转译或固定数量反应冒充这些工作；目前没有据此新增 cold 生产补丁。下一项只读原生 loader 日志诊断与未观测五次权威输出分开保存。

v50 已在实际普通 `bin/claude-haha --bare --print --input-format stream-json --resume ...` 入口执行一轮原生诊断。仅新增已核对的 `BUN_JSC_dumpModuleLoadingState=true`，复用 v42 相同 Seatbelt 规则、新临时 HOME 和原 stdin；没有替换普通 FS/fetch/业务模块，也没有预热 CU。driver/product exit 0，1.802s；两次真实 HTTP 是 HEAD 和 POST，正常 success result 与物理 JSONL 均已保存。全部 8,487,665 bytes / 207,219 行 stderr 保留在 `cold-public-source-cli-v1/native-loader-cold-diagnostic-v50/native-loader.stderr`，被日志扰动的 now+next 标题仅属诊断，不作为新权威五对结果。

首次导入字面量 `./utils/computerUse/cleanup.js` 来自 query 的 abort cleanup：原生日志行 207119–207204 依次显示首次 fetch 和真正 evaluate。实际首次 fetched 文件共十一项：cleanup、withResolvers、computerUseLock、escHotkey、winCursorBadge、cuHelperDaemon、pythonBridge、cuHelperInstall、cuHelperPeerAttestation、pipInstall、cuHelperBridge。十一份实际代码均逐字等于冻结 MAIN Git blob。后续 stopHooks 的 `../utils/computerUse/cleanup.js` 及两个内层 import 只有 resolve，没有再次 fetch/evaluate；这直接区分首次 NEW registry/dependency graph 与缓存路径，不能把 cached 图当作完整 cold 图。

这些文件的顶层初始化主要是常量、函数、空局部状态和路径值。`pythonBridge.ts:23–28` 还会在模块 evaluate 时读取实际 ConfigDir 并生成一次 `randomBytes(4)` Windows input tag，即使当前平台是 macOS；调用函数内的 mkdir/install/spawn/锁探测不能冒充顶层操作。同步随机值并不等于可以添加异步文件工作。真正可继续核对的边界是首次 import 的依赖 registry/record 创建、namespace 初始化和 loadRequestedModules 的 pending/visited 完成条件，及其与预链接 native entry 的对应关系；还没有证明哪个具体 native 业务操作应取代运行时转译器工作。冷 P0 继续 OPEN。

v51 将三个 CU 模块改为复用 `nativemodule.Entry[computerUseModuleKey, *computerUseModuleNamespace]`，专属文件从 10743 bytes 缩为 4232 bytes，只保留三键 cache、actual record/service/key/namespace 链接和 cleanup/overlay/badge 出口。共享图由原模块反应实现抽取，构造先检查 active scope；它不做 Source 转译 I/O 或调度延迟。原 CU Source fixture 与对照测试 SHA 均未修改。

旧 CU 私有 join 两种完成顺序、原错误身份、held evaluation→cancel→Owner Close 的断言移到同一被实际复用的共享实现，不保留只供旧测试调用的重复生产 loader。共享叶级五个 tops 的 race 和随后 whole original main/plugins 两个 Source/import tops 的 race 分别通过；后者实际三次 warm observer 八、distinct Promise/same namespace，另实际 held host-load/evaluation success/reject 四组均在另一臂结算前保持 pending。这些来自 Root/Lifecycle 的有界证据仍单列。

CU 层另从真正 public Import 路径验证：已受理后改变 record/namespace 链接时拒绝，恢复链接后两个新 Import 仍返回原 cached ErrRestricted；调用者取消结束其 Wait，但已受理 import 保留自己的 namespace 结果；真正 off-owner Close 在 queued graph 开始前取消 Owner，pending import 返回 ErrClosed、没有迟到 CU 出口。没有新增可任意赋值 evaluation/failure 的生产测试接口。v51 原 selector 加上述消费者共十一 tops、二十一个 pass events，ordinary 与 race 均零 skip/fail（普通 mcptools 0.944s/ownerasync 0.406s；race 2.106s/1.482s），两包 vet exit 0。每轮五个关键输入 SHA 前后相同，原三个 import 八/八、cleanup 十五/十五、default caller 二十五/二十五严格相等。收据为 `cu-shared-adapter-receipt-v51.json`；冷五对、本轮全门禁及正式 Benchmark 没有借此改成通过。
