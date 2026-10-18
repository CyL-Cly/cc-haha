# 普通 CLI init 的 safe / full 环境阶段

2026-10-14。这是局部消费者证据，完整迁移仍未完成；不合入 main。

## 原版与真实失败

`src/entrypoints/init.ts` 的正常 `preAction` 调用先启用配置，执行完整 `applySafeConfigEnvironmentVariables()` 和 CA fallback，再触发 detached HEAD。`src/main.tsx` 随后的 action 才应用完整合并环境。上述文件及 `managedEnv.ts`、`caCertsConfig.ts`、`apiPreconnect.ts` 均与冻结 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155` 字节一致。

新 fixture 使用原版完整导出、真实临时用户和项目 settings、真实 loopback HTTP。原版 HEAD `/safe`，之后 full 环境为 `/full`。新的公开 `ParseCommand` → `RunCommand` → 同一 Go Harness 回归在修复前实际得到 HEAD `/full`、模型 POST `/full/v1/messages`。`native-init-environment-phases-red-v2.log` 是行为 RED；v1 是并行新增 TUI 声明的编译错误，不作为行为复现。

## 修复与范围

原生 Main 在插件/action setup 前启用配置，调用既有完整 safe 和 CA 导出。预连接保存这一阶段的环境快照；后续 full 环境仍供模型和下一请求使用。没有改 Source 正文、全局 host guard、10 秒超时、once 或全局关闭的 join。

新回归包含用户 safe/项目 full、项目 proxy 在 init 后生效、用户 proxy 跳过 init 而项目随后清空、禁用项目来源四种组合；所有实际 HEAD/POST 和受保护 settings 文件均检查。测试只用假的凭据、临时 HOME、隔离配置和 loopback，不使用模型配额、真实 Keychain 或用户目录。

## 实际验证

- 修复后的单项公开回归：passed，Go 测试 1.811 秒。
- 四种组合 `-race -count=3`：passed，15 个 test pass 事件，application 15.044 秒；0 fail/skip。
- 既有预连接 target/gate/once、10 秒 held HEAD、失败、全局 Session 关闭及 settings 到下一模型的 5 项顶层回归：passed，9 个 test pass 事件；application 4.539 秒、clibootstrap 15.264 秒；0 fail/skip。
- 相关 settings/Bash/hook/CA、插件缓存和环境、冻结 host guard 等 10 项顶层回归：passed，32 个 test pass 事件；clibootstrap 25.531 秒、clistartupenv 1.576 秒。该选择对 cliglobalconfig/tlsconfig 没有匹配测试，不当作这两包测试证据。
- clibootstrap/application/clistartupenv 的 `go vet`：passed，exit 0，10.285 秒。
- `check:impact` 使用正确的 `PR_BASE_REF` 与 `COVERAGE_BASE_REF` 冻结 MAIN：passed，exit 0，1.413 秒，选出 14 项门禁、8,456 个改动输入；这是路由结果，不是这些门禁通过。

原始记录和 SHA 存在 ignored `artifacts/go-migration-20261013/native-init-environment-phases-bounded-receipt-20261014.json`；缺失时须重跑。

## 仍未通过

预连接与模型共享实际 TCP/TLS 池、首次 cold transcript 的标题差异、所有正常子命令的 init 消费者、普通可变 filesystem/schema host 控制仍为 OPEN。局部门禁不等于最终全量 Go race/90%、verify、同版本桌面/Web/CLI 或正式 120 对 Benchmark。

checkpoint 12 的全量 Go 在 unit 阶段实际失败：111 条 test/package fail 记录，format/build/vet 已到达并通过，race 和正式覆盖率判定未执行。12,883 个仓库输入（含 4 个预期删除项）的运行前后 mode/kind/SHA 相同。3,733,100,417 字节原始 profile 已保存为可无损解压的 gzip，原始/解压 SHA 均为 `89e74dc6e2ed5e5043b9dd7c177d9046ee2a78998f1677506cd442e2c653bbff`；所有失败记录和子进程计数仍保留。该检查点不含本页的新修复，不是最终候选。

另一项 MCP 默认目录失败的 fixture 只改了旧 `ConfigDirExplicit` 标记，却保留实际环境属性；原版 fixture 删除了该属性。测试输入对齐后原版期望不变，三次 Source/race 共 9 个 pass 事件；没有改生产 getter。MCP state-mismatch 监听器失败在原断言下十次聚焦 race 重跑共 50 个 pass 事件，但没有查明全量失败的原因或生产修复，仍保留为 unresolved；不能用聚焦通过替换 checkpoint 12 的失败。

## 普通 init 的可变目录：原版证据，Go 未闭合

同一完整 Source fixture 另跑了用户 `settings.env.CLAUDE_CONFIG_DIR` 赋值的绝对、相对、显式空值和 NFD 路径四个 profile。实际原版在 safe 阶段保留首次 user source cache 的 `user` 标记，但 cc-haha 文件读取已经进入新目录；full 的 merged settings 读取新目录用户设置，标记为 `next-user`。四次实际 HEAD 均为 `/safe`，caps/secureReads 均 0，受保护文件未改变。

这些只是原版证据：Go 普通 startup/assignment guard 仍拒绝这样的目录赋值，尚未修复。不能仅凭 Agent 或 skills 的独立消费者通过就解除整个普通 Main 的 guard；初始化后的插件目录、完整 memory 等消费者及各自缓存寿命仍须实测。原始记录为 ignored `native-init-config-redirect-source-receipt.json`。fixture 扩展后既有四项公开环境阶段回归再次 `-race -count=1` passed，5 个 test pass 事件、0 fail/skip，18.114 秒墙钟。
