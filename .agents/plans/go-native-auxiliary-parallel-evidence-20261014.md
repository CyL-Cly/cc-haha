# 实际 native auxiliary 并行：2026-10-14

当前分支、冻结 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`。聚焦修复 checkpoint06 暴露的实际消费者回归，不能作为整个迁移/质量门禁完成结论。没有真实模型或 Claude OAuth，模型与页面 HTTP 全是私有 loopback、临时 HOME 与假凭据。

`TestActualInsightsBatchStartsParallelHTTPAndKeepsInputOrderOnSameOwner` 与 `TestTwoWebFetchAuxiliaryProducersUseOneOwnerAndActuallyOverlap` 在06失败。当前 fresh 同代码复现：前者15.51s deadline，后者3.89s serialized；原日志 `native-aux-parallel-red-v1.log` 留存。它们不是编译或机器冷缓存超时：模型 peer 必须等所有真实 socket 到达才响应，只有最内层同步调用栈能到 socket。

实际 `getAnthropicClient` 必需的本地 credential stat await 增加后，旧 native Web/Insights producer 只克隆同步 Scope。多个模型 Promise.all 的 callback 在 Go Owner pump 上形成嵌套同步栈；内层等待其他 HTTP，而外层仍不能从自己的 stat await 续跑。不能删除实际 Source stat、人工启动 HTTP、复制 prepared 请求、加 fake await 或放宽并行测试。

现在仅 typed `NativeInsightsModels` 与 `NativeAuxiliaryModels` Web producer 使用 Owner 已有的 sealed continuation/token scheduler。每个真正 async producer 在实际 stat/HTTP/iterator await 停车，Owner 独占 token 返回调度器；模型、请求准备、Factory、accounting 与结果 mutation 仍只在同 Owner 执行。复用 continuation 的栈 goroutine 不并发执行业务，只有被 Owner 私有 resume command 授予 token 才执行下一个 segment。

两个 purpose 字段独立，continuation 不携带 SideQuestion authority、不借 child identity、不派生 permission hook、也不开始另一 ASK。其它有限/auxiliary wrapper 保持旧合同。既有实际 fast runtime request binding 现在明确识别正在执行的这两种已绑定 producer；停放、结束、generic callback、foreign/stale scope 仍无 authority。没有改 requestretry 的 request/auth/header/body/500/401 逻辑。

聚焦证据：

- `native-aux-parallel-sealed-v4.log`：真实3路Insights HTTP/返回输入顺序/usage，以及2路WebFetch实际模型 HTTP overlap passed，race clibootstrap5.414s、cooperative2.669s。
- `native-aux-parallel-cancel-guards-v6.log`：上述原消费者、真实3路held HTTP取消→3个body实际关闭→同MainPrint下一普通ASK完成、parked/resumed/completed fast proof与Purpose隔离、generic callback/frame/foreign/stale、既有SideQuestion/permission/fast guards passed，race clibootstrap6.254s、cooperative2.428s。新增cancel fixture 首版漏真实 MaxTurns binding，v5被生产capability拒绝；仅补原required argument，原guard不动。
- `native-aux-owner-close-guards-v7.log`：`Owner.Close` 在真实 native worker 等待期间取消并join actual finally，已结束producer authority失效，与原SideQuestion close负例一起 passed，race1.607s。

新增/修改文件：`cooperative/native_auxiliary_continuation.go`、其同区域测试、两个 typed model Run 入口、`side_question_continuation.go` 的两个私有purpose字段、`native_fast_mode.go` 的明确current-purpose检查；`clibootstrap/native_auxiliary_parallel_cancel_test.go` 的实际模型网络取消消费者。旧正向测试全部保留。

`native-mcp-auxiliary-complete-packages-v15.log` 的 mcptools/mcpruntime/cooperative 全包 race passed：4.843s、7.249s、2.477s。最终全量由 root 重新冻结当前源码执行，不将旧06诊断或这里的聚焦通过当最终90%/verify/Benchmark。

最终本区域五包 vet（cooperative/mcptools/mcpruntime/clibootstrap/application）v16 exit0 passed，当前共享 `git diff --check` passed；不是完整门禁声明。
