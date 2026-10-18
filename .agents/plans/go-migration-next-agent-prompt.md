# 下一位 Agent：继续 Go 迁移的提示词

将下面内容作为新任务提示词。先从 `origin/feat/go-migration-20261013` 获取包含本文件的最新 checkpoint；在该迁移分支的工作副本中开始，保留任何已有未提交修改。

```text
继续 cc-haha 的 Go 迁移，目标不是只整理计划，而是实现、修复并完成剩余验收。

首先读取：
1. AGENTS.md，以及将修改目录的嵌套 AGENTS.md。
2. .agents/plans/go-migration-agent-handoff-20261018.md（最新简明进度/未闭环项/证据边界）。
3. .agents/plans/go-migration-cross-machine-handoff.md（历史交接，读最新段，早期状态可能过期）。
4. .agents/plans/go-main-feature-sync-plan-20261012.md 与 go-main-feature-sync-progress.md（完整 D01–D15/R01–R17/Q01–Q06 要求和历史记录）。
5. .agents/plans/go-main-paired-benchmark-protocol.md（正式120对冻结协议）。
按需再读总迁移、CLI/TUI、server-boundary、desktop/API 清单；不能因本提示词简化就删除原开放项。

Git：只在 feat/go-migration-20261013 继续，可将工作检查点提交/推送到该迁移分支；不得合并/推送到main、force-push或发布Release。用户已授权本次交接checkpoint提交，但这不代表迁移通过。当前分支以真实git状态为准，先git status，保留已有改动，不能强抢其他工作树占用的分支。
冻结MAIN=6d21316ec4635f29ecbaa43e1a5574bfd31d7155，BASE=6b9d2aec1d9321a4dfff90d16b51f10c1ab984c8，不随当前origin/main漂移。禁止将TS业务重新作为Go入口fallback。

当前状态：大量Go/前端/质量工具实现与局部证据已落地，但迁移未完成。正式Benchmark是0/120对，历史6对pilot不计。最新repository prewarm guard和公开回归已提交，gofmt/diff-check/源码同步通过，但该测试的OLD/GREEN、unit/race/vet和新普通产品GREEN都尚未运行。最后0.7.1普通资产来自较早7a2 Source，不含此修复。此前14/15门禁等旧成绩不能冒充本checkpoint通过。

先处理条件与首要问题：
- 重测容量，聚焦Go原准入8GiB、完整门禁15GiB，或可用至少25GiB的临时卷。只清理已确认关闭且可再生的自有缓存；不删用户文件、原证据、模块seed。当前机器/路径/容量不能沿用历史文字。
- 测试一律隔离HOME/config/cache/credentials和项目，确定性门禁不碰真实用户~/.claude/keychain/providers/transcripts，不用真实模型/公网上游/秘密。依赖走frozen lockfile及原prepare:go API，不篡改seed manifest。
- 先完成TestPendingRepositoryPrewarmPreservesPlaceholderUntilRealUserTurn的可复现OLD业务失败与修复版GREEN/邻接。只在受控副本中移除enqueue的十行repository prewarm guard构造OLD，保留相同测试和其余代码；旧生产文件SHA74be9f776a60352f8449a4780f0a1f4194851a166a1533b6613716171c95086e，修复文件SHA3d1b4da22e788298c2972dfab66d426ae4c894d6e265b7f244717687f1884d8c。重新冻结每次实际Source；编译/setup失败不是业务RED。不能整文件退到checkpoint父提交丢掉其他修复。
- 继续核查placeholder reset丢partial metadata的失败启动/冷重试候选，按交接清单P1规格走真实HTTP/WS user_message。不要期待repository prewarm checkout；不要用捕获旧metadata覆盖更新后的有效字段。候选不等于已接入/通过。
- 然后新Source冻结、普通CLI/desktop/H5三阶段构建、fresh版本/BuildInfo/签名/完整资产清单，再从真实EmptySession空Run证明预热不切分支、首次真实user turn才checkout。首轮9b63采集failed必须保留，不补造WS帧。浏览器按ego-browser skill；UI/真实WebSocket发送来自产品，不用store注入、observer业务HTTP/WS重发来冒真实入口。

跨机/提交后新鲜度：原artifacts/和/private/tmp/不会随push带来，历史Markdown中的SHA只是记录，没有原始报告不能自称独立复核。必要证据重跑。旧driver固定f759 HEAD/index/13305四None/Source0f3/本机绝对路径都已过期；提交还移除了四个已删除路径的索引条目，需重采实际Source/依赖/HEAD/index/资产，不改期望hash或放宽guard绕过。Go coverage/impact必须保留整个迁移相对冻结MAIN/BASE的范围，明确COVERAGE_BASE_REF/PR_BASE_REF及merge-base，不能因已commit、工作树干净而漏检或缩分母。

质量：运行check:impact选择的原检查及完整bun run verify。保留原完整lane、Go build/vet/unit/race、atomic全coverpkg、child counter merge、AST归属、changed-Go90%，不删失败/包/child/分母，不放宽期限或只跑替代子集。最近check:impact exit0仅路由报告且CLI-core label仍Blocked，不是门禁PASS，不伪造维护者approval。旧73.6%只是旧Source诊断，不是当前覆盖率。

真实产品：按D/R/Q表补最终同版本Desktop/Web/CLI，含市场Skill、文件/CSV/TSV、编辑重跑、trajectory、长历史/compact、SubAgent/Teams、设置/恢复、迁移、IM、更新等开放项。每项区分implemented、passed、failed、not run、blocked、excluded/deferred。
用户范围：Windows实机延期；Computer Use功能和CU专属检查跳过，其他native/签名/包验证保留，不整条跳native；Claude OAuth排除，不继续该登录。别恢复已停止的CU/Xcode/Apple登录支线。
麦克风已授权且用户愿配合，先把新包界面准备好再提示现场读固定英语The quick brown fox jumps over the lazy dog.，实际播放录音并取得可闻确认；合成语音/noSpeech不是现场有声通过。桌面锁屏时不得绕过，需实际用户解锁。真实IM与OpenAI等专有账户须核实可用测试身份，缺条件明确blocked，不用DeepSeek冒充专有能力。

模型：本迁移所需live已获用户授权，但须确定性相关检查先过并使用隔离的有效凭据，禁止寻找/使用个人已保存provider状态。只调用DeepSeek官方https://api.deepseek.com/anthropic，实际model literal deepseek-flash；不写入Git/日志/报告。不要无理由重跑已完成的付费场景；为最终候选新鲜度确需重跑时记录理由和实际调用数，不把失败调用说成零成本。普通H1的A/B/C限定证据保留、旧整体context失败保留，不为补日志重放A/B/C；剩余D-only新turn验证1M必须遵守既有full15/Go90/解锁准入，不能静默绕过。

最终：所有原质量门禁、Go90、同版本入口及必要live通过后，冻结不可变Source父历史和最终候选，按协议正式跑6族×20=120对/240次。固定默认完整19工具、thinking disabled、context1M/maxout32000/20round/retries2/subagents2/CUoff，其他条件以完整协议为准。两边全部任务成功、Go独有未解释失败0、单侧95%失败上界≤5%、费用比UCB≤1.05、耗时比UCB≤1.10。不筛选/早停/事后改阈值；失败保留，修后新候选重新配对。

持续推进有条件可做的实现与检查。真实外部条件缺失时记录具体阻塞，不把静态准备当实际通过；也不要反复做无关准备来掩盖阻塞。未全部通过前不得宣布迁移完成，不合main。交付时更新已完成/未完成清单、真实运行命令与结果、Source/资产身份及下一步。
```
