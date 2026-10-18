# 实际 plugins handler 导入协议与消费者

2026-10-15。仅当前实现的有界消费者证据，迁移仍未完成。Claude 真实 OAuth 按用户要求排除；没有切分支、提交、推送或合入 main。

原 Commander 的 `plugin marketplace list --json` action 先 await 实际 `cli/handlers/plugins` 动态导入，再执行原 handler。旧 Go 直接调用 handler，缺少该协议。新增共享 `nativemodule.Entry` 供真实已链接模块使用：保留 cached fetch/load/evaluation、两臂 join、namespace resolve 与 outer import adoption，每次返回不同 Promise，缓存同一 namespace。具体调用方仍拥有有限 key、factory、namespace 和 exports 验证，不能靠 JSON 或外来 Owner 构造能力。它不模拟 Source 文件读取、转译、CPU、定时器或网络延迟。

实际 MainCommand 的 factory 现在 await plugins namespace 后调用已有 handler；手工旧 facade 的原有兼容路径保留。共有模块已由实际 CU 三模块调用方复用，移除其重复私有协议。完整 Source plugins module 的 warm import fixture 使用冻结 MAIN 的原模块业务，实际 Promise/namespace 身份、FIFO observer 和两臂挂起/拒绝均有对照。

## 实际运行

- v1 只因旧声明 GOMODCACHE 已空且网络关闭而 setup failed，未运行测试。v2 实际 guard 三次通过，新的 handler 测试三次失败：Root 复用了含 `mkt` 的夹具，却错误预期空列表。只将新测试纠正为完整四字段固定 `mkt` JSON，保存失败 raw；不把它归为生产 RED。
- v3 对现存隔离缓存按准备 manifest 校验 55 modules / 15,090 files，缺失和 SHA 差异均 0；实际 `go mod verify` 通过。
- 实际 MainCommand namespace 两项 tops，各三次 `-p=1 -race -json -count=3 -timeout=90s` 通过，0 fail/skip，pkg 3.197 秒，总 24.151 秒。验证真实市场结果、缓存下轮 namespace、零遗留引用，以及 manual/foreign/expired/retired/closed factory 拒绝。
- 共享模块全部七项 tops（含实际 Source 和两臂四个子用例）通过，11 pass events，0 fail/skip，当前 fixed join 代码，race 总 2.919 秒。
- 未改动的原 `TestActualOrdinaryPluginCommandInitHEADAgainstWholeCommanderSource` 与原四种 known-file 子场景全部通过，pkg 6.962 秒，总 21.461 秒。真实 Source/Go 都需原一次 HEAD；文件结果、unknown 字段、syntax/schema error 和私有设置保护断言不变。
- 因旧 HEAD 曾间歇失败，再以原强断言 `-count=5` 实际连续通过五次，0 fail/skip，pkg 8.656 秒，总 15.220 秒。所有这些命令保留默认 vet 与原期限；相关输入 before/after SHA 一致。

共享 actual Source 独立收据 `prelinked-module-source-evidence-20261015/receipt.json` SHA256 `b39331382f3e07e3f05c9fe5b48478979b58eb83ce8f640f0d4ea2be976b04fc`；CU adapter 收据 `cli-flags/bun-pinned-module-audit-v43/cu-shared-adapter-receipt-v51.json` SHA256 `4d053bcd138253cce8569bc9c3372cc2f07d41fb1189924e94155b4a5ebb038b`。Root 完整 raw、输入清单和各结果汇总在 ignored `artifacts/go-migration-20261013/native-prelinked-plugin-bounded-receipt-20261015.json`；不提交生成输出。

## 仍待完成

fresh ordinary CLI 已实际复验，并仍失败：当前 native_process_fixture binary 构建 40.545 秒、全部 Go/锁/C 输入 before/after SHA 一致，binary SHA256 `fd5cabfdaf6d33880e806361b77a02dea217fc2ded417ba46649a519a5bf91f3`。严格原生 sandbox 下，无普通 fs/fetch Promise 包装、无 exit diagnostic listener，实际 Source 五次 HEAD 各 1，fresh Go 五次各 0。十次业务 `[]`、exit 0、stderr 空、私有 settings 字节不变均成立，原 Source 3,148 文件与 Git blob/冻结 MAIN 再次逐文件一致。原有 race selectors 六次通过不能替代该普通入口；物理 HEAD 缺口继续 OPEN，不把协议接线认作完整修复。

native cold import 的完整 producer 与原 strict cold title 五对仍 OPEN；warm protocol 的通过不关闭它。完整 Spinner 渲染、parent/actual async child/parent 的 process-global HTTP pool、全量 Go 与 90% / verify、同版本三端实际验收和正式 120 对也仍未通过。不会宣布迁移完成。
