# Teams local JSX 与真实 inbox 请求

2026-10-14。冻结 Source `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`。这是具体原 caller 与实际同 Host 消费者证据，不是整个 Teams 或迁移完成声明。

## 原 Source 业务

`useInboxPoller` 没有过滤 `idle_notification`。它将这类消息作为 regular message，空闲时提交，busy/focused 时可靠排入 AppState.inbox，idle effect 再提交。`REPL.handleIncomingPrompt` 只拒绝 active QueryGuard 或已排队的 human prompt/bash；`getFocusedInputDialog` 不把 Teams local tool JSX 当 permission/focused dialog。

新 `native-team-inbox-query.fixture.ts` 挂载原 InboxPoller、原 TeamsDialog，调用 AST 取得的完整原 `getFocusedInputDialog`/`handleIncomingPrompt` body，并在 onQuery 边界接原 `query()`/QueryEngine 到私有 loopback HTTP。它没有替换过滤、格式化或 query body；没有声称完整 `onQueryImpl` 的 UI bookkeeping 或整个 REPL tree 已执行。原 compile-time ULTRAPLAN condition 通过同 Bun `feature` 编译 profile 读取。

五个 Source profile 实际通过：idle 1 POST；Teams focus=null 且 1 POST；true permission/busy 先 0 POST + queued，解除后各 1 POST；human queue 0 POST 且保留 pending。POST 中保留原 idle JSON 和 teammate-message wrapper。Source capability blocked=0，fake credential/loopback，未执行真实 OAuth。

## 真 RED 与最窄修复

新增实际 Session 消费者 `TestActualSessionTeamsPanelIdleNotificationStartsSameHostWithoutUnmount`：原 Submit/Host → TeamCreate → 真 cached Footer frame → OpenTerminalTeams → 物理私有 mailbox idle。原 3 秒窗口中原实现只有初始 2 POST，关掉实际 Teams 面板后同一 queued idle 才产生第 3 POST。该失败有区别于 missing producer/丢消息的完整观察。

只给 `terminal_local_ui_state.go` 增加 actual Teams + pending inbox 的专用 admission，`team_leader_inbox.go` 两个读点使用它。原 human command/task-notification queue 的 local JSX guard 保留，pending permission、已在运行的 ASK、human queue 优先级仍由原边界控制。没有为旧公共测试的固定计数吞掉业务消息。其他未证明的 local JSX caller 不在这次放行内。

## 当前结果与剩余

Go 1.25.7、Bun 1.3.14、临时 HOME/config，原 3 秒和原生命周期窗口不变：新 Source 五 profile + 新实际 Session + 原 busy inbox/idle同Host物理写入 + 原 human queue/UI Source consumer，`-p=2 -race -count=1 -json` 合计 4 top-level / 9 pass events，8.682 秒，0 skip/fail。`git diff --check` passed。

新 fixture 仅格式化后，新 Source/实际 Session 两项复测 4.716 秒，2 top-level / 7 pass events，0 skip/fail；其余生产字节没有再变化。

后续补齐五个 approval profile，原 Source 共十个 profile / 11 pass events，`-race` passed 5.327 秒，0 skip/fail。合法 in-process 成员的 paneID 为空，沿原 no-kill 分支；没有替换 kill primitive，也没有把这一分支外推为 tmux PID 验收。受信 approval 经原私有 team file 删除 worker、原 `createTask`/`unassignTeammateTasks` 解派真实任务并保留未知 metadata，真实 POST 中恰有一次 approval 和一次生成的 system termination。本机该原 React 消费分成两个 POST，业务通知内容已逐项验证；批次数未作为固定合同。

负面行为按实际 Source 保留：伪 sender/未知 member 不启动 query 且不改 roster/task；strict-schema 多一个未知字段时不触发 shutdown cleanup，但该内容仍作为普通 chat 进入一次 POST。同批的两个合法物理 approval envelope 均 pass-through，AppState roster guard 只生成一个 termination；成员移除后再次 approval 被确认已读，0 新 POST。公共单次生产者场景的 exact-once 断言不能被解释为新全局过滤/去重政策。

完整命令、原失败/无效 fixture 分类、修复后日志与 SHA 保存在 ignored `artifacts/go-migration-20261013/teams-inbox-local-jsx-source-evidence-20261014/receipt.json`。最初 feature import、编译条件、verified-absence 和 Native fixture 缺 pane mode 均保留为无效 fixture，未冒充产品 RED。

TUI 组仍须复验完整公开 Teams PTY 的原 pane/model/mailbox/next-human/关机和两个 termios 断言。旧 parentPosts=7 的断言未改；Source 正常业务会触发 inbox turn，需按实际原通知批次证明其计数，不能为了旧假设过滤消息。全量门禁/90%、最终同候选桌面/Web/CLI 和 120 对仍属于总体任务，尚未通过。
