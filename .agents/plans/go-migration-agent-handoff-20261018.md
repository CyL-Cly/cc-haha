# Go 迁移：当前进度与 Agent 交接清单

更新：2026-10-18（Asia/Shanghai）。**迁移未完成；本次是保存全部现有代码的 checkpoint，不是发布或质量门禁通过声明。**

## 1. 当前目标与 Git 交付边界

- 将冻结 MAIN 的桌面、Web/H5、CLI、Server、Harness、工具及 IM 行为迁入同一 Go 运行时，保留独立 CLI，禁止通过 TS 业务 fallback 获得表面兼容。
- 每项通过真实产品入口验收；需要模型时仅使用 DeepSeek 官方 `deepseek-flash`。先通过确定性检查，再做独立 live 验收。
- 最终同一候选必须完成三端验收、原完整质量门禁、完整 Go coverage 流程及正式 **120 对 / 240 次** Benchmark，全部通过后才能宣布完成。
- 当前交付分支：`feat/go-migration-20261013`；远端：`https://github.com/NanmiCoder/cc-haha.git`。**不合入 main，不强推，不发布 Release。**
- 用户在本次交接请求中明确授权提交全部现有应入库代码并推送到该迁移分支。此前文档中的“未获授权 commit/push”是历史状态，不再用于阻止本次 checkpoint；它们也不构成 main 合并授权。
- 提交前 HEAD 为 `f7599e7efdc28f6fc5e07fc2d903cdd67135b90f`；本交接所属的 checkpoint 提交可用 `git log -1 --format=%H -- .agents/plans/go-migration-agent-handoff-20261018.md` 定位。
- 本工作树原为 detached HEAD；同名本地迁移分支被另一工作树占用。本次可提交 detached HEAD 后明确推送 `HEAD:refs/heads/feat/go-migration-20261013`，无需强占另一工作树的分支。

冻结参考保持不变，不随远端 main 更新：

| 身份 | 固定值 |
| --- | --- |
| BASE | `6b9d2aec1d9321a4dfff90d16b51f10c1ab984c8` |
| MAIN / Source oracle | `6d21316ec4635f29ecbaa43e1a5574bfd31d7155` |
| 本次推送前观察到的远端 main，仅作记录 | `84b1466fc71cd6c2a0a6dd5e3dc20d7d63dfcc95` |
| Bun | `1.3.14`，以 `package.json#packageManager` 为准 |
| 最近已验证 Go 工具链 | `1.25.7` |
| 最近普通产品版本 | `0.7.1`；根 package 的 `999.0.0-local` 不能冒充产品版本 |

## 2. 如何理解“完成”

| 状态 | 本清单含义 |
| --- | --- |
| 已实现 | 代码及调用接线已存在；不等于编译、测试或产品验收通过 |
| passed（限定范围） | 有对应实际记录，仅适用于其 Source、资产和测试范围 |
| failed | 已观察失败，必须保留；后续成功不能回填旧记录 |
| not run | 尚未运行，静态审查、准备脚本和路由报告不算执行 |
| blocked | 当前缺少执行条件，例如容量、可操作桌面或测试账户 |
| deferred / excluded | 用户明确延期或排除，不能写成 passed |

**没有可诚实给出的整体完成百分比。大量实现和局部验证已落地，但最终集成验收仍开放。** 旧大表中某个文件/方法完成，不代表其整个 D/R/Q 项目已经关闭。

## 3. 已完成并有实际证据的工作

以下均按原限定范围保留，不自动升级为本次 checkpoint 的全量通过。

- [x] Go 默认入口、共享 application/Harness、普通 CLI 与 HTTP/WS 桌面链路已建立；迁移实现、Source 对照、回归测试和质量工具保存在仓库。
- [x] 多轮普通 `0.7.1` CLI、sidecar、Electron 目录包构建，以及公开版本、BuildInfo 和签名探针曾实际通过。
- [x] `/clear`：恢复原 admission 检查；SessionEnd 成功后重新读取最新任务分区，避免后台 Agent 已完成的输出链接被迁往新 SID。正常 unit/race 与普通 H0 的官方 Flash Agent/Bash/TaskOutput 路径均有限定范围通过记录。
- [x] workspace 大历史恢复预算修复：已有完成活动超过 16 MiB 后，小 Write 的文件列表/预览可恢复；128 KiB Write 保守拒绝与 diff `413` 边界保持。正常 unit/race 和普通 Web Files/文本预览实际通过。
- [x] Agent 启动失败/取消补偿、Config Escape 的旧 permissions 快照恢复、resume 后按当前 Agent 重新过滤工具集合，均已接入并有正常物理源聚焦 unit/race 证据。
- [x] 浏览器完整截图/恢复/缩放/宿主物理像素预算，以及菜单拒图不隐藏正常页面的修复已接入；相关静态、同区域测试和限定 native fixture 像素检查通过。最终普通菜单仍需复验。
- [x] 普通 CLI 已有 Config/权限 Escape、MCP、插件、编辑器 fallback、TUI 输入与退出等真实入口的限定记录。
- [x] 普通 Web 侧边栏 Pin/Unpin/Hide/Restore，H5 发送/输出偏好保存与冷恢复，项目显示名保存/冷恢复/恢复文件夹名，任务标题 Enter 保存/保持焦点 Escape 取消/冷恢复等实际操作通过。
- [x] 最新 repository prewarm 修复与公开回归测试已经写入正常工作区，并精确同步至隔离产品源码副本。**这一项只完成代码、格式化、审查与输入同步，Go 测试和新产品 GREEN 尚未运行。**
- [x] 当前代码和计划进入本次 checkpoint；生成目录、原始报告、依赖缓存、真实凭据不入 Git。

## 4. 原需求分组：实现状态与剩余验收

完整逐条需求仍以 [冻结计划](go-main-feature-sync-plan-20261012.md) 为准；历史细节见 [实施记录](go-main-feature-sync-progress.md)。以下每行仍有最终验收债务。

| ID | 已落地范围 | 仍需完成，不能直接打勾关闭 |
| --- | --- | --- |
| D01 / D02 | Porcelain/主题/布局、菜单及 capability UI 与 Go 接线，局部 React/Electron 测试 | 最终普通包主题、语言、焦点/键盘/缩放、菜单各实际业务入口 |
| D03 | 市场、分类、详情/文件/安装 owner；warm 技能刷新与命令通知，曾有真实 Skill 执行 | 最终候选安装→同 SID 执行→卸载，失败恢复与目录状态 |
| D04 | webview/guest/IPC/生命周期；截图与菜单拒图修复 | 最终包浏览器导航/find/zoom/多标签、截图/PDF/下载、picker；CU 功能按用户排除 |
| D05 | 文件/stat/NFC-NFD、CSV/TSV、checkpoint/watch、workspace 预算修复 | 最终桌面实际文件/CSV/TSV 预览与更新；Web 小文件证据不代替桌面表格全例 |
| D06 | prompt 编辑重跑、rewind、附件/checkpoint，历史真实模型路径 | 最终同版本重跑、失败时草稿/文件恢复，父会话保持 |
| D07 | trajectory/prompt snapshot、主/子 scope、near/search/大记录预算 | 最终桌面主/子 Agent 浏览、live tail/搜索/定位/Stop |
| D08 | H5 壳和 API，部分设置/侧栏真实 Web 已验 | 手机/平板新任务、真实聊天/审批/停止、断线/旋转/键盘与产物完整流程 |
| D09 | 语音下载/ready/录音/识别/取消与快捷键接线；历史空录音/合成语音记录 | 现场真实有声输入、composer 位置、取消/权限恢复、录音回放可闻确认；Windows 实机延期 |
| D10 | 长历史/工具活动/冷恢复、恢复预算与缩放相关实现 | 最终 700+ live rows、10k+ history、切标签/侧栏响应与无重漏；大 JSON fixture 不等于全 UI 压力验收 |
| D11 | Teams UI 与共享生产运行时 | 多成员真实任务、审批、依赖、失败/Stop/恢复/归档和不重复 echo |
| D12 | 并发上限、Fast/effort、runtime selection、索引升级与 OAuth 接线 | 最终设置真实消费、跨端恢复；OpenAI 专有 live 单列，Claude OAuth 排除 |
| D13 | 数据迁移事务、maintenance/drain、managed 路径、失败恢复 | 最终签名桌面迁移与旧会话/附件继续任务，故障/外部进程/平台边界 |
| D14 / D15 | 更新/打包/保留桌面入口、侧栏偏好和存储升级，部分操作已验 | 最终安装/更新/延期/退出及全部支持入口；Windows 实机延期 |
| R01 | stream commit/retry、预算、watchdog、non-stream fallback、usage | 最终候选完整故障契约与真实任务，无重复工具副作用 |
| R02 / R03 | manual/auto/partial compact、hooks/恢复、同 iterator 前台转后台 | 最终桌面/CLI 长任务、压缩后真实工具、promotion/Stop/恢复 |
| R04 / R06 | 原始历史边界截断、fork/resume、locator 与物理文件 identity | 最终旧会话/未完成 tool_use fork、hook、大小写文件系统入口 |
| R05 | disconnect generation、取消/join、终态持久化与进程回收 | TeamWorker/全局 Close 最终集成、孤儿与晚写；Windows 整树实机延期 |
| R07 / R08 / R15 / R16 | mailbox claim/history、supervisor/retry、依赖/handoff、checkpoint/Goal 等待 | 最终普通 CLI/桌面真实 Teams/长时恢复，失败不丢信、不重做工具、不 busy-poll |
| R09 | 普通 SubAgent 上限、共享域、原子 reserve/release | 独立域/同域、嵌套/前后台与真实并行最终验收 |
| R10 / R11 | Fast 请求链、兼容 wire identity、account metadata、MCP transports | 最终完整候选；OpenAI/Grok 等专有 live 需各自测试身份，DeepSeek 不替代；Claude OAuth 排除 |
| R12 / R13 | managed relocation、全部 IM migration/drain/idle/附件路径 | 最终迁移消费者、获授权真实 IM 收发/附件/恢复，不能用配置页代替消息验收 |
| R14 | 默认 vision、明确拒图后一次降级、TTL 缓存和诊断 | 最终 CLI/proxy/诊断 UI；真实支持图和真实拒图 provider 分别验收 |
| R17 | 索引 commit worker 的 claim/cancel/rollback、SQLite/spool 有界性 | 最终迁移/Close/drain 及全候选性能和故障证据 |
| Q01 / Q02 | path/import 路由与 Go Agent E2E，历史 11 场景已通过 | 本次提交后重新识别最终 diff，完整门禁的当前新鲜度 |
| Q03 / Q04 | Source oracle/corpus、依赖冻结、全 Go/child coverage/AST 归属工具 | 修复完整 Go/race/coverage 债务，原 changed-Go 90% 必须通过 |
| Q05 / Q06 | 双语文档/site/发布资料、provider preset/旧配置兼容 | 最终文档与真实消费核对；本次 checkpoint 不发布产品 |
| B5 | 历史中间包/CLI/Web/live、6 对 pilot 与 runner/protocol | 最终同版本所有开放入口、不可变父历史、正式 120 对和统计结论 |

## 5. 最新未闭环问题：下一位 Agent 的首要工作

### P0：repository prewarm 修复尚未实际验证

- 生产文件：`backend/internal/server/websocket.go`，`enqueue` 中仅对 `prewarm_session` 读取当前 launch metadata；`repository` 为 JS truthy 时，在创建/触碰 runtime 前返回。
- 新公开测试：`backend/internal/server/session_repository_prewarm_public_test.go`，`TestPendingRepositoryPrewarmPreservesPlaceholderUntilRealUserTurn`。
- 业务文件 SHA256：production `3d1b4da22e788298c2972dfab66d426ae4c894d6e265b7f244717687f1884d8c`；test `445539f96a56e21d815cb802dd36750b8b4d2303f25df0b8d85a349589afb51c`。
- 已做：gofmt、diff-check、静态独审、精确四文件 R→P 同步及独立完整审计。
- **not run**：旧 Go 的同例业务 RED、修复版 unit/race/default vet、最终普通产品构建与空 Run GREEN。
- 可移植的 OLD 重现办法：在受控临时源码副本中，仅移除当前 `enqueue` 的这十行 repository prewarm guard，保留同一新测试及其余代码；该生产文件应回到 `74be9f776a60352f8449a4780f0a1f4194851a166a1533b6613716171c95086e`。重新冻结该实际 OLD Source，再测试。保留编译/setup 失败与真实业务失败的区别；恢复修复字节后再跑 GREEN 与邻接。不要用 checkpoint 的父提交整个 websocket 文件替代这个十行逆变更，因为父提交还缺此前其他修复。
- 邻接至少核对：`TestReconnectUsesSameRuntimeAndAuthoritativePermissionSnapshot`、`TestHTTPCreatesSourceTranscriptAndPassesOriginalWSControlBytes`、`TestAdmissionPreservesSocketUserOrderWhilePermissionBypasses`、`TestSessionRepositoryHTTPPlansWithoutMaterializingAndValidatesOriginalFields`。
- 普通 Web：真实首页 EmptySession 选私有 Git 的 feature/当前工作树/plan、空 body、无附件，点 Run 一次；实际 UI 应建 SID 并发送 prewarm，Git 保持 main，之后真实用户输入才可 checkout。
- 首轮 UI 收据 `9b63b3b8…` **仍为采集 failed**：点击前 main、关闭后 feature 是窄实际发现；未采到确切 WS 帧，不能声称切换发生在关闭前。新验收须在同一 Ego Node round 内有界累计 CDP events 与 DOM SID，不能重发 WS、注入 store 或用 observer HTTP 变更业务。
- 旧 RED-only driver 始终 exit1、等待 feature 切换，**不能直接拿它作新版本 GREEN judge**。

### P1：repository partial metadata / 失败启动冷重试仍是未验证候选

- `resetRepositoryPlaceholder` 可能只保留最后一条 partial `session-meta`，丢掉之前聚合的 repository/permission/runtime 选择；尚无本次正常树实际 Go RED/GREEN。
- 正确触发必须是 **`user_message`**；冻结 MAIN 会跳过 repository prewarm，不能要求 prewarm 执行 checkout。
- 可移植测试规格：真实 POST 创建 feature/plan 会话，PATCH title；私有 JSONL 追加只含 runtimeProviderId=null/runtimeModelId/effortLevel 的 partial metadata；删除私有 feature 分支；真实 WS user turn 必须在 Prepare 以 `REPOSITORY_BRANCH_NOT_FOUND` 失败且不打开 Agent transport。独立新 Server 冷读 summary/git-info 仍应保留 plan、title、runtime 和 feature 选择；重建 feature 分支后，同 SID user turn 应成功走真实 Prepare/Finalize/checkout，关闭一次，HEAD/index/tracked bytes 保持。
- 不可使用的旧生产方案：将捕获的 `info.Metadata` 七个字段无条件覆盖到后来重新读取的 metadata。该方案可能覆盖更晚的有效更新；必须复用既有结构化解析/有效投影边界并证明新旧字段和并发更新语义。
- 本机 ignored 目录中的 v2 测试/生产草案不是已接入实现，外机没有这些文件也可按上述规格补回归。

## 6. 最终门禁与真实验收：全部仍需关闭

- [ ] 恢复容量：聚焦 Go 原准入至少 8 GiB；完整门禁至少 15 GiB，或可用至少 25 GiB 的挂载临时卷。2026-10-18 本次交接重新观察约 6.6 GiB；这是瞬时值，必须重测。不要删除用户数据/原始证据/模块 seed 或降低门槛。
- [ ] 对本次 commit 的实际输入重新冻结 Source、HEAD/index、依赖、工具链和资产；旧固定 HEAD `f759…` 的脚本不能直接 arm。
- [ ] 本次 checkpoint 新增交接文档且提交了四个旧删除，Source 清单的路径数、旧四个 None 与索引身份都会变化；不要要求提交后仍等于旧 `13305/4None` 或 `0f3b…`，更不能伪造相等。
- [ ] 确定性检查使用私有 HOME/config/cache/loopback，不使用真实用户 `~/.claude`、keychain、provider、transcript 或项目设置；`prepare:go` 走既有 API，不手改 seed manifest/key。
- [ ] `bun run check:impact` 仅选择检查并报告策略；最近实际 exit0 仍显示 CLI-core label `Blocked: yes`，不是门禁通过。不要伪造 label/approval。
- [ ] 在正确隔离环境运行原 `bun run verify`，保留选中的完整 lane。历史完整运行曾到 14 passed / 1 Go failed，不能改称当前候选全绿。
- [ ] 保留 Go 原 build/vet/unit/race、atomic 全 coverpkg、child counter 合并、AST executable/declaration 归属及 changed-Go **90%**；不删包/分母/child，不缩测试列表、过滤失败、放宽时间换绿。
- [ ] 历史 child-merged 诊断约 `293993/399434 = 73.6024%`，仅旧 Source 诊断，离 90% 仍有债务；不是本次 commit 的覆盖率成绩。
- [ ] 质量和 coverage 对比固定基线不得漂移到新 checkpoint 自身。阅读 `collectGoChangedLines`，明确记录 `COVERAGE_BASE_REF` / `PR_BASE_REF` 与 merge-base；保留冻结 MAIN/BASE 和整个迁移范围，不能因为工作树干净而漏掉已提交的 Go 改动。
- [ ] 最终普通三阶段构建、fresh 版本/BuildInfo/签名/完整 H5 清单探针；CLI H0 与签后桌面 H1 可有不同 SHA，分别绑定真实收据，不宣称相同或猜测签名派生。
- [ ] 新版 Desktop/Web/CLI 同版本真实入口验收及上表未闭项。最终功能结果必须来自普通产品，测试 binary、fixture、build-only、旧版 live 不互相冒充。
- [ ] 普通 H1 付费 live 的 A/B/C 已有内容、Stop 和冷恢复的限定证据，但旧整轮因 context ledger 的 200K/1M 断言仍为 failed。不要为补日志重放 A/B/C；剩余 D-only 新 turn 的 1M 验证仍须按既有更严格的 full15/Go90/桌面解锁准入执行，不能静默绕过或把旧 C 内容成功升级为整体通过。
- [ ] 本机 Mac 最后实际观察为锁屏；重新确认可操作界面。用户已授权麦克风并愿意配合，但须准备界面后重新提示，录固定句 `The quick brown fox jumps over the lazy dog.`，播放实际录音并取得可闻确认。合成语音/环境声音/noSpeech 不算现场有声验收。
- [ ] 获授权真实 IM、OpenAI 等专有账户/设备的可用条件与真实收发/请求；缺条件明确 blocked，不拿 DeepSeek 或 mock 代替。
- [ ] 所有门禁与入口完成后冻结不可变 Source 父历史/最终候选，再运行正式 120 对并按冻结统计判定。

跨机执行入口：先读根及相应子目录 `AGENTS.md`，用 frozen lockfile 安装；`bun run prepare:go`、`bun run check:impact`、`bun run verify` 与普通产品构建的实际参数以当前源码为准。这些命令不是可直接继承个人环境的一键脚本；先准备仓库要求的隔离运行环境和 Source oracle。

## 7. 用户已确认的范围调整

| 项目 | 当前范围 |
| --- | --- |
| Windows | 实机验收 deferred；不是 Windows passed，也不删除已有实现或矩阵检查 |
| Computer Use | 功能及 CU 专属检查 excluded；其余 native lane/签名/包验证保留，不整条跳 native |
| Claude OAuth | excluded，不继续 Claude 官方 OAuth 登录 |
| Xcode/Apple 登录 | CU-only XCTest 不再阻塞已排除范围；不要恢复已停止的 Apple 登录/CU 安装支线。其他实际需要单列 |
| 模型 | 官方 `https://api.deepseek.com/anthropic`，实际 model literal `deepseek-flash`；凭据仅有效私有文件注入，不入 Git/报告 |
| Git | 本次 checkpoint commit/push 已授权；只推迁移分支，无 main merge、force-push 或 Release 授权 |

## 8. 正式 Benchmark 冻结条件

详见 [完整协议](go-main-paired-benchmark-protocol.md)，不得用本摘要替换其 manifest：

- 正式累计 **0/120 对**；历史 6 对 setup pilot 不计入正式成绩。
- 固定 MAIN 原版与 Go 最终普通候选；6 族各 20 变体、共 240 次，按协议交替顺序，独立产物 judge。
- 官方 `deepseek-flash` / Anthropic Messages；Bun 1.3.14、SDK 0.128.0、axios 1.15.2；thinking disabled；context 1,000,000；max output 32,000；20 rounds；API retries 2；普通 subagents 2；默认完整 19 工具，CU 关闭，无 WebSearch，不能 `--bare`/简化 tools/换 system prompt。
- 两边 120 个任务均成功；Go 独有未解释失败为 0；其单侧 95% 零失败上界 ≤5%。
- 120 对 log-ratio 的单侧 95% t(119) 上界：费用 Go/Source ≤1.05，整体耗时 ≤1.10；固定 t 值 `1.657759284926144`。
- 不筛选、丢失败、选择性重跑、提前停止或事后调阈值。修复后新候选重新完整配对，旧批次原样保存。

## 9. 证据索引与跨机限制

**下面是历史记录身份，不是本次提交重新执行的成绩。** 原始 JSON/log/PNG、准备脚本、依赖 seed、私有测试 HOME 主要在 ignored `artifacts/` 与 `/private/tmp/`；不会随 Git push 到另一台机器。Git 中保存的是实现、测试和本清单/历史 Markdown。外机没有原始证据时应记录“历史摘要，未独立复核”并为最终候选重跑，不能仅凭 SHA 前缀宣称通过。

| 事项 | 记录/身份 | 结论及范围 |
| --- | --- | --- |
| clear admission + 最新任务快照 | `6cd37c65…`；普通官方 Flash `51a513f5…`，独审 `666c1722…` | 方法 unit/race 与该普通 CLI 功能范围 passed |
| workspace 预算 | OLD `8ec478ba…`；GREEN `d479d0dc…` | 正常 unit/race 各 11 tops/13 终态 passed；Web `fc5f18ff…` 独审 `d8f97061…` |
| 普通 H5 偏好/草稿/cold | `ff79deee…`；独审 `94637a19…` | zero-model，未提交聊天 |
| 项目显示名/cold/恢复 | `e73f0edc…`；独审 `d5f9aaa4…` | 显示名，不是物理目录 rename |
| 任务标题/Enter/Escape/cold | `6cb30661…`；独审 `fd46421e…` | 空闲任务、保留控制任务；不证明运行中 rename/delete |
| 最新 prewarm 代码接入 | `b0e0b276…`；独审 `02b84845…` | edit/format-only，不是 Go GREEN |
| 首轮 empty-Run UI | `9b63b3b8…`；窄独审 `18f7dd1d…` | capture failed；main→关闭后 feature 的副作用保留，不认证精确 WS |
| 最新正常/P 源码同步 | `645225bc4b20eed9f6bdf39781d0243036700b16688615153cd7453ba63dfc3d`；独审 `deff44a2abfe692086a773e5997dd104b07cd92d57b6c041b43b042592e9a3d0` | 四文件同步 passed，20 新库存/34 read-only commands，未构建产品 |
| 上轮源码身份，交接文档/commit 前 | `0f3b107c34cf0831cc69338b3ddb2a048ed6b462827a1366a8f5a77bc97880e9`，13305/4 None | 只作历史定位，本次提交后失效须重采 |
| 最后普通资产 | build `655578d45ac0990b0ddbbf2eaf7669865d5c49c1d14b7e5c977f43392caf69c6`；probe `800a53172a58d678edb253db30da7d4df020541757af9a1a682b3e980d892d07` | Source `7a2ea9b2…`/13304，版本0.7.1；不含最新 prewarm 修复 |

本机最近交接记录位于 `artifacts/go-migration-20261018/root-repository-prewarm-implementation-and-sync-current-handoff-v1/`：`handoff.json` SHA `2d374fc1f95f8cdd72033193ff772f422e9173e8d99c0186723d3a263d52ed47`，`terminal-complement.json` SHA `85a5982af9784b23e60aab2500b99328d177df5c3f05c6adc21cfafcf41f4ee0`。两者记录的“未 commit/push”和旧磁盘值均是其创建时历史，已由本次用户请求及新 checkpoint 替代。

旧普通 CLI/desktop 及 Source archive 的本机路径不是新机器配置。旧 driver 硬编码旧 HEAD/index/Source/路径/资产，必须按实际 checkout 重新审查与绑定；不能只替换期望 hash 或放宽 freshness 检查让它运行。

## 10. 建议下一轮顺序与接手入口

1. 从迁移分支取得本 checkpoint，先读本清单、`AGENTS.md`、[冻结需求](go-main-feature-sync-plan-20261012.md)和[Benchmark 协议](go-main-paired-benchmark-protocol.md)；检查真实分支、工作区、容量和账户/桌面条件。
2. 私有环境和冻结 Source oracle 就绪后，优先关闭 P0 的真实 Go OLD/GREEN 及邻接；再处理 P1，避免继续堆积未验证生产改动。
3. 新 Source 冻结→普通重建→fresh 资产探针→新版真实入口，按 D/R/Q 逐项更新上述开放项。
4. 完整原质量门禁及 changed-Go90→同候选剩余 live→正式完整 120 对；失败保留，修复后重新绑定实际候选。
5. 报告完成/失败/未执行/排除/阻塞及完整证据身份。所有目标通过以前持续标记“迁移未完成”。

可直接交给下一位 Agent 的提示词见 [续迁提示词](go-migration-next-agent-prompt.md)。
