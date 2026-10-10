# Go 钉钉扫码注册：实际业务接线与证据

本记录只证明列出的范围，不关闭 D01–D35/SB01–SB16 或整个 IM 平台迁移。

## 已实现

- `NewDesktopServer` 构造同一配置服务上的 Go 注册生产器；桌面 React 原路由 `POST /api/adapters/dingtalk/registration/begin` 与 `poll` 接入实际 Go HTTP。
- Begin 实际访问 `/app/registration/init`，传配置中的 source，读取 nonce 后访问 `/begin`；返回 deviceCode、确认链接、期限、轮询间隔和 220×220 PNG 二维码。
- Poll 实际访问 `/poll`；成功后使用已有 `adapterconfig.Service.Update` 保存 clientId/clientSecret，返回原脱敏配置，保留旧配置/未知字段和其他平台绑定。
- 原 handler 的 `poll` 未在局部 try 中 await，其 promise rejection 由外层 Server 返回 `500 {"error":"Internal server error"}`；Go 保留此边界。输入校验仍为局部 400，Begin 错误仍按原 errorResponse 映射。
- 路由检查原字符串类型和 UTF-16 256 单元限制；实际向上游的 JSON 保留原 UTF-16，避免 lone surrogate 被 Go rune 转换改写。状态的大写保持原 ECMAScript 的多字符转换，例如 `ß→SS`、`ﬃ→FFI`。
- QR 使用锁定的 Go encoder `github.com/skip2/go-qrcode v0.0.0-20200617195104-da1b6568686e`；新依赖经过标准 checksum 验证，313 私有种子包含 43 项依赖。Goja 仅复用现有纯 Go 嵌入运行时处理原 JSON 的 Number、Boolean、大写语义；无 Node/TypeScript 生产业务子进程。

## 实际执行结果

| 编号 | 实际命令/范围 | 结果 |
| --- | --- | --- |
| 314 | NewDesktopServer 注册路由实际 HTTP 消费者 | intended-red：原 Go 返回 501 |
| 315 | 三包聚焦 Source/race | passed：imregistration 3.543s、application 2.044s、server 3.200s |
| 316 | 三整包 Source/race | passed：imregistration 3.370s、server 54.453s、application 245.912s |
| 317 | imregistration/server/application/adapterconfig vet | passed，exit 0 |
| 318、320 | 新路由 Source fixture | failed：隔离 fixture 环境漏填 BUN_OPTIONS；补环境，不改安全 guard |
| 319 | 扩展原 handler/物理上游/落盘对照 | intended-red：状态大写两例与原版不同；路由部分仍 fixture 环境失败 |
| 321 | 原 handler 与实际 NewDesktopServer 的原始请求体对照 | intended-red：lone surrogate 的 physical JSON 被替换 |
| 322 | 35 组服务 Source + 12 组真实桌面路由 +原注册/解绑聚焦 | passed：application 4.999s、imregistration 4.031s；server selector 无匹配测试，仅编译，不登记为测试通过 |
| 323 | 新独立 RunServer/private TCP 消费者 | build failed：其他活动叶尚未落完 acceptQueuedCommand，非注册行为失败 |
| 324、326 | 新独立 RunServer/private TCP | failed：测试先误把version放在 `/health`，再误用 `/api/health`；改为原 `/health` status/timestamp 与实际 `/api/status` 身份，不改产品响应 |
| 325 | 当前 imregistration/server 两整包 Source/race | passed：imregistration3.149s、server53.565s |
| 327 | 新独立 RunServer/private TCP | build failed：另一个活动schema叶未落完，非入口行为失败 |
| 328、329 | 已提交注册实际client断连与原Source持久化 | intended-red：Go断连取消保存，原Source仍保存；329增加实际Server request context已取消barrier，入口测试通过 |
| 333 | 原Source断连 +实际TCP/保存+独立RunServer/静态/退出+原注册/解绑 | passed：application4.018s；业务使用Server生命周期，输入仍使用request，不取消已提交副作用 |

运行日志位于 `/tmp/go-im-registration-*.log`、`/tmp/go-server-entrypoint-registration-*.log`。测试全部使用私有 HOME/config、假平台凭据、真实本地 HTTP；Source 是仓库实际原 handler，未将 fetch 替换为内存 stub。没有消耗模型额度，这些注册/配置接口不依赖语言模型。

## 仍未证明/未完成

- P296 签名包尚不含此次注册代码；须构建后从实际 Settings/IM/钉钉点击扫码绑定，检验官方 init/begin/poll 与界面。
- 已验证 dataURL 与 PNG 的实际尺寸，尚无独立二维码解码/手机扫码或真实钉钉账号授权完成证据；不声称 PNG 字节与原 QR library 一致。
- 钉钉已提交poll的实际client断连/原Source保存语义已对照并修复；不声称所有HTTP header、网络失败或其他API断连生命周期闭环，SB13 仍开放。
- 钉钉 Stream、收发消息、权限卡片与完整 Agent 消费者未由此次注册接线完成；其他平台也不随此项关闭。
- 完整 verify304 在 Go changed-line 72.26% 和真实缺失 profile 门禁失败，未完成后续 lanes；禁止下调 90% 或伪造通过。完整候选与配对 Benchmark 仍开放。
