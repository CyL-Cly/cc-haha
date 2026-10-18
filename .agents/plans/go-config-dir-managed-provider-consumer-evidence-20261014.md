# 当前 cc-haha/provider 配置目录读取消费者证据（2026-10-14）

本轮只闭合当前配置目录的无缓存文件读取与已绑定 native saved-provider 合并，不代表普通 Main 构造或 RunTurn 已支持动态 `CLAUDE_CONFIG_DIR`。原冻结 host controls、赋值拒绝和变更拒绝均保持。Source 固定 `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，Bun 1.3.14、Go 1.25.7；所有数据位于私有 HOME，只有 fake 凭据，无真实 Claude OAuth、模型额度或外部网络。

## 原消费者与实际失败

新 fixture `native-config-dir-managed-provider.fixture.ts` 运行原完整 `applySafeConfigEnvironmentVariables` 和 `applyConfigEnvironmentVariables`，原 getCcHahaSettingsEnv 仅追加私有导出用于观察，函数体、过滤器、顺序及 provider merger 没有替换。用户 env 令 safe 阶段读取 second，项目 env 令 post-trust 阶段读取 third；desktop 原 spawn-key 保护使两者继续 initial。各阶段观察原完整 reader，并在物理改写 managed settings 后再次读取。原全局配置路径和用户 settings 缓存仍保持旧值，Source 最终逐文件核对 provider indexes 原字节不变。

原 Go 的 legacy reader 在两个阶段仍读取 initial，native saved-provider reader 返回 `fake-private-initial` 而 Source 返回 second/third 的 fake token。分别有原生产真实 RED，未修改断言或延长窗口。另增加物理不变断言，原 native reader 对坏 JSON index 执行恢复重命名，4 个阶段原文件均 ENOENT；Source catch 后保留原文件。普通 MainCommand 亦有当前目录真实 RED。

## 修复与边界

`clistartupenv.Options.NativeCcHahaConfigDirectory` 仅已绑定 native policy profile 可用；finite profile 构造拒绝该 callback。每次无缓存 reader 采样一次当前文件目录，legacy settings 和 provider absence 检查复用该值。MainPrint 和 NativeProcessSettings 绑定各自已有 scalar environment，读取当前目录；Root 在两个现有构造点添加 callback。MainPrint、scoped settings 和普通 MainCommand 的 saved-provider reader 复用既有纯读 `providerconfig.NewCLIRuntime`。它保留未知输入，坏/缺 index 回退，不恢复、搬移或写 schema。缓存不重建、不普遍失效，原 guard 仍阻止普通目录变化 admission。

Go 对照使用实际 Native Main/非模型 MainCommand 构造和已有消费者。构造前仅移除本测试两项目录改变 settings producer，再在当前 scalar authority 上采样 reader；这是明确的 lower consumer 对照，不能当作完整动态目录 Main/RunTurn 通过。坏 index 在 MainPrint lower-reader 观察前恢复精确 Source operand，普通 Main 构造的其它旧 bookkeeping 不在这一纯读证明范围。未执行 proxy-required provider 的目录变化生命周期。

## 实际验证

| 验证 | 结果 |
| --- | --- |
| 原 native provider 目录 / legacy 目录 | 原生产 RED，raw 已保留 |
| 原坏 index 物理不变性 | 原生产 RED，4 阶段原文件被搬走 |
| 原普通 MainCommand 当前 provider 目录 | 原生产 RED，raw 已保留 |
| 集成 10 项消费者 | race 10 tops / 39 pass events，0 fail、0 skip；startup 9.021s、bootstrap 26.266s |
| 最终新 4 项消费者（含 Source index 终态断言） | race 4 tops / 16 pass events，0 fail、0 skip；startup 5.745s、bootstrap 12.544s |
| 原冻结 Source 4 文件 | 当前 Git blob 与 Source commit 全部一致 |
| `git diff --check` | passed |

10 项集成包含新两目录 reader、finite callback/error/relative guard、普通命令 reader、原 Main/scoped settings-path Source proof、真实下一请求/Bash/hook/CA、原普通 command/provider/proxy-close Source、原 host/value guards 和完整 trusted assignment Source。最终精确命令、selectors、全部 raw 日志与 SHA 在 `artifacts/go-migration-20261013/config-dir-managed-provider-evidence-20261014/receipt.json`。

完整 native 目录 admission、初始 absent/empty/relative provenance、其它 fresh 与 memoized consumers、实际同版本产品验收、正式完整门禁、120 对 benchmark 均未由本轮执行或关闭。没有提交、切分支或合入 main。
