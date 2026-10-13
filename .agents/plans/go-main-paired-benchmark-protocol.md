# MAIN → Go 真实配对 Benchmark 冻结协议

状态：协议/runner 已实现，尚未执行最终候选的正式 120 对任务。启动连通性 pilot 不计入成绩。不能据此宣称 Benchmark 不变。

参考 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，实际原版源码来自完整 `git archive`；runner 在开始时逐文件计算 Git blob SHA1，与冻结 MAIN 的 tree 对照，同时计算生产 Source 清单 SHA256。原版 CLI、Harness、工具、SDK、真实模型均实际执行。只绑定隔离机器没有的 OS MDM、keychain prefetch，并使用原版的私有 plaintext store，防止接触开发者真实凭据。Go 使用构建出的原生 CLI，不启用 TypeScript 业务 fallback。

模型为官方 `deepseek-flash`，Anthropic Messages，endpoint `https://api.deepseek.com/anthropic`。固定 Bun 1.3.14 revision `0d9b296af33f2b851fcbf4df3e9ec89751734ba4`、SDK 0.128.0、axios 1.15.2、thinking disabled、context 1,000,000、最大输出32,000、最大20轮、API retries2。不覆盖 temperature，沿用相同 provider 默认值。完整默认工具集合，禁止 `--bare`、`--tools`、自定义 system prompt。Computer Use 在此代码任务组关闭，另做实际签名桌面验收。仅对隔离项目使用已授权的 bypassPermissions；设置关闭 hooks、auto-memory，普通 SubAgent上限2；两边配置相同。

固定六个任务族，每族20个变体，共120对：算术修复、带引号CSV、Unicode文件/表格、多模块修复、实际历史fork、实际Skill命令。历史来自连通性pilot中原版真实DeepSeek会话，并冻结父transcript字节/hash；两端都fork同一份父历史，父内容不得改变。公开测试/输入文件不可被修改；独立Python judge在真实产物上判定，不相信Agent口头报告。

每对交换 Source→Go / Go→Source 顺序。通过透明loopback传输将两端的原始请求转发到真实官方endpoint，保存请求body、真实流响应、公开CLI事件、原始错误、物理产物、judge结果和时间。记录Go binary、Source、wrapper、父历史hash，开始/每对完成后验证冻结输入；改变代码/binary/协议必须新建批次。未完成的一端不选择性重跑，原始失败必须保留。

预先固定判断：

- 全部120个唯一任务对达到预期产物，两边都成功；新增未解释runtime/capability错误为0。普通模型参数/工具校验错误单独记录，不能静默丢弃。
- Go独有任务失败观察值0；其单侧95%零失败二项上界 `1−0.05^(1/120)` 必须≤5%。有限样本只能支持此预设非劣范围，不能证明随机任务分数绝对不变。
- 120对的 log ratio，单侧95% t(119)上界：SDK估算费用 Go/Source≤1.05，整体任务耗时≤1.10。固定t分位值1.657759284926144。记录实际tokens及缓存使用；SDK对未知模型的计价估算不是DeepSeek真实账单。
- 不进行看到结果后的阈值变更、选择性剔除或提前停止。部分批次返回 insufficient；正式统计仅在完整120对结束后判定。失败后修代码必须保留旧批次，新候选重新配对。

runner：`scripts/perf/go-main-paired-benchmark.ts`，core judgments及回归在同目录。默认只冻结 manifest，必须显式 `--allow-live --key-file <私有key文件>` 才允许消耗quota。每次传入 `--source-root`、`--go-binary`、`--allocation`、`--parent-transcript`，所有测试profile和key保持临时目录的0700/0600，不写进Git。正式启动前先通过最终相关确定性检查，最终desktop host/helper/assets也记录同Go binary身份。
