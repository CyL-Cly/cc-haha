# 当前配置目录：TaskStore / TeamStore 有界消费者

本批只闭合原任务文件、团队配置和邮箱 getter 的当前目录采样。普通 Main/RunTurn 的 `CLAUDE_CONFIG_DIR` host capability guard 未开放；不能把 lower typed-environment mutation 当成完整普通 ConfigChange 入口验收，也不能把 Go 的有限 guard 说成 Source 产品限制。

Frozen Source：`6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，Bun `1.3.14`。全部私有 HOME、显式假凭据/原语，无真实模型、Claude OAuth 或外部网络。

## 原完整 Source 路径和实际租约

`native-config-dir-task-team.fixture.ts` 调用原 `tasks.ts`、`swarm/teamHelpers.ts` 和 `teammateMailbox.ts` 的完整业务函数；lock observer 始终调用原 proper-lockfile。四个串行 profile 的 raw 在 `artifacts/go-migration-20261013/config-task-team-source-original-v1`，均 exit 0、stderr 0、blocked 0、secureReads 0。

| Profile | 原物理观测 |
| --- | --- |
| `between` | 相同服务在 env 切换后读写新任务、团队配置和邮箱；旧目录不变，未知 metadata/config 字段保留。 |
| `held-lifecycle` | 旧 `.lifecycle-locks/<list>.lock.lock` 确实持有；回调内的任务创建重新采样新目录，写入新 task 2/revision；释放仍针对旧租约。 |
| `held-mailbox` | `writeToMailbox` await 前捕获旧 inbox；切换后仍只更新旧 inbox，释放旧租约。 |
| `held-config` | 旧 `config.json.lock` 确实持有；锁内 `readTeamFileAsync` / replace 重新采样新目录，保留新配置的未知字段；释放旧租约。 |

Source 最近位置：`tasks.ts` 的 `getTasksDir`、`withTaskListLifecycleLock`、`withActiveTaskListLifecycleLock` 和各 body getter；`teamHelpers.ts` 的 `withTeamFileLock`、`mutateTeamFileAsync` / `replaceTeamFileAsync`；`teammateMailbox.ts` 的 `writeToMailbox` / `updateInbox`。四份 Source blob 与 frozen commit 完全一致，SHA 写入收据。

## 真实 RED 和最邻近修复

旧生产仅复制新测试，`TestActualNativeRootTaskAndTeamCurrentDirectoryAgainstWholeSource` 的实际 MainPrint/StartSession/root TaskStore/leader Config/mailbox 回归失败（0.61s，raw `test-1791971823024.log`）：env 已指向 next，但 Go 更新 old-task description/metadata、保留 old-config future 字段并归档 old-message；Source 对应字段来自 next。

`taskstore.Options.NativeConfigDirectory` 绑定实际 MainPrint 的 live typed-environment getter。任务/生命周期原 path getter 每次采样；Team Config/Mailbox 借用同一个具体 TaskStore 的 producer。有限 store 仍保留各自 `ConfigDir` fallback。MainPrint/root/child 的 singleton 不重建；已 admitted lease 参数和 mailbox `update(path)` 不改。

`WithTeamLifecycleTransaction` 原 scoped clone 继承同 getter，原 same-list/active、generation/identity 以及 retired handle 约束保留。普通 startup guard 的拒绝断言保留。

测试先完全恢复 Source `BeforeFiles`（删除 Source 已生成的 expected history/new task），再运行 Go。严格 physical comparison 覆盖 initial/next 的完整 tasks/teams 两树及无残留租约。MainPrint 构造会额外写 Global `.claude.json`，这是 lower Source fixture 没有构造 Main 的 scope 差异；第一次过宽树检查原失败保留为 fixture scope mismatch，不计作 task/team 产品 RED。

## 当前执行

- 两个新 top / 三条 held 子项：`-p=1 -race -count=1 -json`，bootstrap 3.608s，5 pass events，0 fail / skip，raw `test-1791972373540.log`。
- 原 TaskStore / TeamStore 同区完整套件：29 top，59 pass events，0 fail / skip；taskstore 14.144s、teamstore 8.609s，raw `test-1791972449342.log`。
- 明确枚举的 9 个 native Team/Task 长期消费者：9 top / 9 pass events、0 fail / skip，bootstrap 46.465s，raw `test-1791972674340.log`。包含真实主/子 Harness、物理 mailbox、pane CLI、独立队友 Stop 和 Root publication 消费者；精确 selector 写入收据。

收据：`artifacts/go-migration-20261013/config-task-team-current-directory-evidence-20261014/receipt.json`。

## 保留 OPEN

普通 Main/RunTurn/ConfigChange 完整动态目录 admission、raw launch authority、其它 fresh/memoized plugin/skill/MCP consumers 尚未由本批闭合。`teamplan` 自己的目录枚举及新进程 pane launch 配置目录采样也不由本批 Source profiles证明。

Desktop `src/server/services/taskService.ts` 的独立 path getter 使用 `env || home/.claude` 且不做 NFC，和任务工具的 `?? + NFC` 不同。因此本批曾将两个 desktop getter 接到 task callback 的代码已撤回原固定 fallback；desktop 当前目录动态 consumer 留 OPEN，需要独立 actual desktop callback 和 WholeSource empty/decomposed-path 证明，不能拿本批 absolute-path profiles 外推。撤回未改变已通过测试的 CLI Task/Team 代码；原 desktop 有限代码恢复到此前已通过状态。此处没有迁移全部完成声明。
