# Native HTTP 连接池的有限消费者证据

2026-10-14，当前工作分支。冻结参考仍为 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`；没有合入 main，没有进行真实 Claude OAuth。下面是确定性、私有 loopback 证据，**不是完整迁移、最终质量门禁或 Benchmark 通过声明**。

## 实际缺口与实现

原版 `src/utils/apiPreconnect.ts:68` 的 HEAD 与普通 SDK fetch 使用 Bun 的物理连接池。immutable checkpoint 12 的公开 CLI 对照中，Source 5/5 的 HEAD 与 POST 使用相同 TCP 连接，Go 0/5；模型正文和 authored user 内容一致并不能代替连接资源消费者。

新增 `provider.NativeHTTP1Pool` 保留空闲物理连接及正在使用的 reservation，`providernative.Pool` 在每次使用时验证原 Owner/当前 Scope。只有完整无 body 的 HEAD 或实际读到完整 body 的 EOF 才可归还连接；未消费 body、取消、失败、`Connection: close` 和未完成 writer 均关闭资源。正在使用的连接不会给另一个请求复用。TLS writer 使用 pool lifetime，普通请求取消仍关闭它自己租用的连接。关闭原 MainPrint 后取消、关闭并等待 pool 的实际物理资源。

原 MainPrint 的 HEAD 和 sdkhost 普通请求绑定同一 pool。Safe 环境快照、Full 环境后续模型请求、once、provider/proxy/mTLS/unix skip、HEAD 无模型凭据以及 10 秒 timeout 均保留。供 MainPrint 和非 ASK MainCommand 使用的公共 HEAD worker 抽到 `native_api_preconnect_worker.go`，各真实 caller 分别拥有 once/target→TLS→pool 的生产顺序；没有构造替代 ASK。MainCommand 公开入口的独立实现和验收不计入本文件的通过范围。

不能把 TLS 的相同证书当作相同 Bun pool：真实 Source 在 `NODE_EXTRA_CA_CERTS` profile 中，默认 HEAD 与 SDK 显式 `tls.ca` 的连接不同，后续两个 SDK 请求复用。Go 的物理 key 保留这个模式差异，原 v24 错误复用已留档并修复。

原 `proxy.disableKeepAlive()` 是 sticky 状态，由新建 SDK 客户端捕获；它不改已有客户端的参数，也不改 init HEAD 的默认 fetch。已接两个真实消费者：`withRetry.ts:465` 的创建故障，以及 `claude.ts:3081` 在没有 `message_start` 且流内 socket 故障时的状态变更。后者在失败 attempt 的 commit buffer flush 之前执行，与原版位置一致。到达 `message_start` 后断开的连接不使后续 pool 失效。

## 实际验证

证据保存在 ignored `artifacts/go-migration-20261013/cli-flags/`。`native-http-pool-bounded-receipt-v38.json` 列出 24 个相关文件 SHA、14 个原始日志 SHA，以及 5 个参考模块与冻结 MAIN 的逐字节一致性。

| 记录 | 观察结果 |
| --- | --- |
| v21 | 原 fresh driver 对三个 Source TCP 复用 profile 真 RED |
| v24 | 真实 extraCA 模式的 HEAD/SDK pool key 差异真 RED；后修复 |
| v26 | Source 五种 pool profile、原十三条 reservation 合同、10 秒/once/失败/关闭及公开 RunCommand→真实 Read→第二模型 POST；race 共 24 tops/47 pass events，0 skip |
| v27 | 完整 Source getAnthropicClient/withRetry 的私有 TLS 故障→重建→下一客户端，与原生实际端口/重试对应，race PASS 8.564 秒 |
| v28 | 仅移除 sticky callback 的私有单文件 overlay 真 RED；没有更改生产 SHA |
| v32 | 私有物理 Source：headers fulfilled 后、message_start 前断开，两个后续 TCP 端口不同；真实 message_start yield 后断开，两个后续 TCP 端口相同，caps 0 |
| v36 | 相同故障及三个普通请求，旧 Go 在 before-start 分支复用后两连接，Source 使用不同连接，真 RED；after-start 两端已经相同 |
| v37 | 窄接对应消费者后，原 v36 断言及原十五条 full queryModel 流重试对照全部 race PASS；2 tops/19 pass events/0 skip，包 14.396 秒、wall 18.454 秒 |
| Root 75411 | 共享 dispatch 抽取后的 Safe 四 profile、33 个 target gate、10 秒/held close/公开 HEAD→Read TCP：7 tops/18 events/0 skip，bootstrap 15.394 秒、application 10.556 秒 |
| v39 | 当前非 cover 实际 CLI，5690 个真实构建 input SHA 前后无变更；5 个 Source 无观察控制、5 个 Source server TCP 观察、5 个 Go 公开输入均 exit 0，Source 与 Go 的 HEAD→POST 同 TCP 都是 5/5；标题仍 0/5 相等 |

v32/v36 的有限故障 fixture 在原 fetch Promise fulfilled 或原 stream yield 后发只读回执，物理 peer 收到回执才断开；没有 sleep、额外 HTTP 请求、替代 Source 业务函数或提前消费 body。该同步用于证明故障所在阶段，不作为冷启动调度等价证据。所有模型均是假凭据的私有 loopback。

诊断错误也保留：v19 为结构 literal 中间编译态；v20 为未声明 absent path；v25 暴露旧 nil AdoptResponse 清理 panic，修复后原十三条合同全部保留；v29 的 retry budget 为零，原测试请求数假设错误；v30 的原始驱动 argv 错误及未送达 headers 的建连故障；v31 仅证明 bytes 写出、尚未证明 message_start 被消费；v33 少绑定已有 SDKhost BodyIOAwait，v34 为私有诊断 overlay 的重复 import。它们不冒充 sticky 消费者的产品 RED 或正向验收。

## 尚未闭合

上述验证范围是当前 macOS native HTTP1 普通 CLI/Harness 及有明确原 Owner 的子 Host；没有证明所有 Bun pool 的容量、空闲寿命、跨独立 Owner 的全进程策略或其他平台的实际运行。

冷标题 P0 仍 OPEN。v39 当前公开五对在已修复物理连接复用后，Source 五个无观察控制及五个仅 server TCP 观察全部为 `now original next original`，Go 五个全部为 `later original`。author UUID/parent/text 和实际 HTTP messages 在十五次运行中精确相同，但不能消除可见标题差异。构建 29.737 秒，未插桩二进制 SHA `3f224cfe3d88e791ac35da192fc2b0c768aa522fec0d4b2cc4360944af9890aa`；原始 build inventory、JSONL、HTTP body 和逐对结果保存在 `cold-public-source-cli-v1/connection-pooled-current-v39/pooled-cold-title-paired-receipt-v39.json`。旧 Source/golden、immutable12 五对 RED 和所有原始文件保持不变。

最终不变 90% 的全门禁、同一最终候选的桌面/Web/CLI 与 120 对 Benchmark 尚未通过；以上二进制和测试结果不替代这些要求。
