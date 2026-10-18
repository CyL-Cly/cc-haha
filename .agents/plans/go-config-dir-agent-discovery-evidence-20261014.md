# 动态配置目录：Agent 发现与暖缓存

本批只闭合实际 native Agent catalogue 在已暖 settings sources 下的目录采样、同 cwd 缓存、完整 Agent 清理和相对路径 metadata。普通 Main/RunTurn 的 `CLAUDE_CONFIG_DIR` frozen host guard 仍保留；首次冷 settings、完整 memory 与其它配置目录消费者仍未闭合，不把 Go 当前拒绝当作 Source 产品限制。

本文保留当时的暖缓存范围与冷读失败事实。首次冷 source reader 的后续限定修复与新 RED/复验另见 [首次冷读证据](go-config-dir-agent-first-cold-evidence-20261014.md)，不改写此处历史收据。

## 冻结 Source 的真实消费者

`native-config-dir-agents.fixture.ts` 使用 Bun 1.3.14，执行原 `getAgentDefinitionsWithOverrides`、`loadMarkdownFilesForSubdir`、`loadPluginAgents`、builtin override/settings getter 与实际 native filesystem walk。三个 profile 的旧/新 user Agent 和 project Agent 都是私有实际 Markdown 文件；没有代写业务函数、模型调用或真实凭据。最终 stdout/input/env/stderr 保存在 `artifacts/go-migration-20261013/config-agents-directory-source-final-v3`，均 exit 0、stderr 0、`blocked=[]`、`secureReads=[]`。

- `loadMarkdownFilesForSubdir` 进入时先采 userDir，再同步计算项目目录，随后才到首次 `Promise.all`。项目 Stat walk 是同步调用，不能将该处的外部并发修改当成 Source 异步采样反例。
- outer Agent cache 以 cwd 为 key，markdown cache 以 subdir/cwd 为 key；普通同 cwd 暖调用仍返回旧 Agent 和同一 Result 对象。完整 `clearAgentDefinitionsCache` 清这两层及 plugin Agent cache，下一次发现才采当前目录；该调用保留独立 per-source settings cache。
- `held-after-sampling` 先自然完成一次原 Agent 加载，再完整清 Agent caches，留下真实已暖 settings/plugin loader。随后实际读 old user Markdown，hold 已执行的 `readFile` 完成，切 next，释放后返回 old；再次完整清理才返回 next。Source/Go 使用相同文件和读完成边界，不注入缓存、文件内容或结果。
- `relative` 将 raw ConfigDir 切为 `owned-next`。实际 filesystem 按原进程 cwd 解析，Agent `baseDir`/`sourceFilePath` 保持原请求的相对拼写。逐字段、完整 `getSystemPrompt()` 与 Source 比较。
- 三个 profile 均逐项证明 Agent Result 的暖/清理 identity，以及五个 settings source 的 cached object/null identity 在 Agent 清理前后保持。自动记忆通过原明确 disable fastpath 关闭；真实 plugin loader 返回空 enabled 列表。

## 原始 RED 与最近修复

旧生产实际 `NativeProcessSettings.AgentCatalog` 回归先通过 Before/Warm，再在 full clear→List 被 `changed filesystem/schema host CLAUDE_CONFIG_DIR` 拒绝；三 profile 真 RED 保存在 `original-production-red.jsonl`。本批修复：

- `agentcatalog.Options.NativeConfigHome` 仅实际 native factory 绑定 canonical process environment；markdown 在原入口位置采一次。有限 profile 仍使用固定 `ConfigHome`。
- 原 catalogue 构造器提取 Options，生产仍构造同一个 service。actual native filesystem adapter 只在 OS 调用解析相对路径，metadata 和 catalogue identity 保持。
- Agent-specific settings getter 对已暖 source 委派实际 `p.settings.GetEditableSource(source, "")`，保留 schema/merge capability 检查和原 cached pointer。冷 source 仍委派 `p.startup.GetSettingsForSource`，没有开放 startup guard。
- 自动记忆仅原明确 disable fastpath 在完整 startup getter 前返回 false。空 enabled plugin 的实际 loop 不额外消费 memory/settings；完整非空 plugin/options/raw prompt 路径保持其它已实现消费者。

## 明确保留的冷边界

首次冷加载在真实 old user `ReadFile` 完成后切目录，Source 会继续成功；Go 仍拒绝。实际调用链是 `Service.List` → `builtin` → `Options.Settings("userSettings")` → `nativeAgentSettingsForSource`（per-source cache miss）→ `startup.GetSettingsForSource` → `checkHostControls`，在检查 `CLAUDE_CONFIG_DIR` 时拒绝。带该准确方法标签的原日志是 `first-cold-held-read-open.jsonl`。这发生在同一 merged settings 已被 `InitialSettings` 自然加载、但 user per-source cache 尚未被原 Agent builtin override getter加载的状态。没有用手动 cache 注入掩盖它。

对已暖 source，`p.settings.GetEditableSource` 命中 `settingsState`，只验证缓存值，不重新计算 Path。其真正冷路径是 `clisettings.Service.Path("userSettings", "")` → Options `NativeUserSettingsPath` → `n.nativeUserSettingsPath(n.CWD, p.environment)`；当前 authority 是 `NativeProcessSettings.environment` 及 Root 已绑定的实际 native path producer。本批没有绕过或直接调用该冷路径来放宽 startup guard。

另一次将 native 同步项目 Stat 人为 hold 后并发切目录的失败保存在 `invalid-synchronous-stat-cutpoint.jsonl`，明确不计 Source 产品 RED。完整 memory、cold settings、outer-only cache clear/cross-owner process memo 与普通 Main 动态目录入口仍 OPEN。

## 当前验证

- 新 whole Source/native actual factory 三 profile，`-p=1 -race -count=3 -json`：12 pass events、3 top executions（同一 top 三轮），0 skip/0 fail，bootstrap 7.370s。原 Source 20s、native 10s 窗口保持。
- 原 `agentcatalog` 全包 `-p=1 -race -count=1 -json`：24 tops、38 pass events，0 skip/0 fail，4.999s；有限 profile、owner/foreign/cancellation、Managed/source overrides、native walk/links/dedup、JSON/raw prompt 和原 Source 对照均通过。
- 原 bootstrap 15 tops、31 pass events，0 skip/0 fail，24.575s：Main agents 原 parser/readonly/private provenance/guard/close、实际 hardlink、真实 ripgrep、原 memory prompt、Plugin raw options→child model、CLI/SDK JSON Agent→真实 child Read、SDK initial definitions 和 parent skill memo 均通过。模型只是假凭据/loopback。
- `go vet -p=1 ./internal/agentcatalog ./internal/clibootstrap` exit 0；本批 `git diff --check` exit 0。

精确 args、原始日志、文件 SHA、Source 冻结字节校验和 vet 结果在 `artifacts/go-migration-20261013/config-agents-current-directory-evidence-20261014/receipt.json`。本批结果不代替普通 CLI/TUI/Desktop/Web 最终入口、整体质量门禁、90% 或 120 对 benchmark。
