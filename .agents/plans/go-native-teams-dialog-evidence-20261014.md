# TeamsDialog 有界原版/实际消费者证据

2026-10-14。冻结 Source `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，继续当前分支，未切分支、提交、合并或调用真实模型。下述为 focused 确定性证据，不替代最终同版本 CLI/桌面/Web、全门禁、90% 覆盖率和 120 对 Benchmark。真实 Claude OAuth 登录/刷新未执行。

## 已实现的实际链路

实际 Footer 的 `teamContext` 快照打开 Teams 模态；同步同 ID loading 挂载，异步实际团队配置文件读取，1 秒刷新，真实 detail/tasks。列表/详情操作消费当前渲染帧的具体成员：原版权限模式切换和实际邮箱消息、shutdown 邮箱、实际 tmux/it2 view/kill、成员第一匹配删除、任务 reclaim、当前 AppState roster/inbox 更新、并行 idle prune。外部 Source 的 hide/show 函数体为空；保留 cached-backend supportsHideShow 的入口与无物理副作用，不扩大 ant profile。

`teamstore/terminal_dialog.go` 保留未知字段、原始 UTF-16、exact-false idle 状态、第一匹配 pane/hidden 删除、单次/批次模式更新和实际私有文件 lease。`teamspawn/pane_dialog.go` 复用既有 native process/cross-spawn 边界、实际私有 PATH/CWD/environment 和 context-bound 子进程。

App 的 mount、detail reader 和已接受的 async action 分开管理。同步 Retire 阻止尚未进入 Open 的旧 mount；关闭/替换只取消组件读者，已启动的 Source action 继续完成。全局 Close 取消并 join 全部实际 action/native 子进程与模型。晚到 refreshed/closed 消息受相同 mount ID、kind、active state 和 retired lifetime 约束。

## 真正失败及修复

原 3 秒全局 Close 回归失败：已取消模型/Dialog pane 后，registered cleanup 又启动 orphan pane kill；原 `KillPane` 使用 Background context，仍阻塞退出。原始 Go 堆栈在 `test-1791954344424.log`。原版完整 `gracefulShutdown → runCleanupFunctions → cleanupSessionTeams` 加真实 held pane 的实际 `process.exit` 证明整个 cleanup registry 的原 2000ms race；没有 mock exit 或释放 FIFO。Native registered cleanup 使用一个覆盖整个团队回调的 2 秒 context，所有 sibling panes 共享，普通 TeamDelete 保留独立传入 context。超时取消/join 子进程，不进行晚到目录/成员删除。

连续 5 次 actual Session Close race passed，保留原 3 秒窗口。每次检查 held 模型物理连接关闭、accepted Dialog kill 和 orphan cleanup 两条实际子进程的每个 PID ESRCH、无晚到文件/成员/UI 写入。日志 `test-1791955947338.log`，application 18.732s，0 skip/fail。

两轮独立 captured-render 真反例：

1. 原版完整已挂载 Teams detail 中，实际配置把同 agent 的 pane `%1` 改成 `%9`，随后 Enter 仍执行原渲染 closure 的真实 `select-pane -t %1`；0 blocked，新配置保留。Go 下一真实文件读取更新 native ledger、尚未提交 App 画面时，却执行 `%9`。`test-1791956284099.log` 保留 Source pass / Go fail。修复为私有不可变 `NativeTerminalTeamsSnapshot`，操作/tasks 捕获具体已发布成员。`test-1791956415543.log` passed。
2. App 已提交新刷新、Tea 仍持旧画面的已排队按键，又能读取新的成员。`test-1791956652316.log` 保留第二轮真正失败。`LocalPanel`/`LocalPanelResult` 只通过 `json:-` 私有 frame carrier 传递实际不可变视图与渲染 row/index/level。App 校验具体私有 carrier、当前 mount ID、exact native dialog、row/index、level、retired lifetime；JSON-only 与另一个真实 dialog 的帧均拒绝。`test-1791956921986.log` captured-render + 全局退出 aggregate passed：5 pass events、2 top tests、0 skip/fail，application 12.585s。此处为真实 App/native/Source 消费者证据；完整公开 PTY 结果另列，不外推。

## 其他已通过的 bounded 检查

`test-1791956036635.log`：whole original React/Ink TeamsDialog 的 12 个操作阶段、真实 mode/config/mailbox/pane/tasks/roster 消费者；真实 Source global force exit；两个 idle pane 的独立 controlled-ready-order 并行 prune、unmount 后原 action 继续；私有状态投影/第一匹配文件更新；既有实际 pane shutdown 命令/退出策略。`-race -count=1` 共 9 pass events、5 top tests、0 skip/fail；clibootstrap 8.009s、teamstore 2.519s、teamspawn 3.223s。

并行证据让 `%2` 的真实 pane primitive 阻塞 FIFO，`%4` 先完成，实际任务/inbox 先观察 four；Esc 卸载后 `%2` 保持运行，释放后观察 two 并 join。没有生产排序、sleep、假 FS ready、单成员替代或归一化完成顺序。较早未受控两 idle 的采样次序 Source four→two / Go two→four 记录保留；受控 ready-order 证明不等价于整个未受控调度已严格一致。

TUI peer 的原版整组件绘制比对另有 6 themes × 100/47/24 widths × 5 stages、90 frames passed，12.351s race；包括实际 Parser cells/styles 与原 Yoga 高度/截断。该 receipt 由 peer 所有。`git diff --check` 在 frame consumer 完成后 passed。

## 仍开放

TUI peer 的真正公开 Teams v7 passed，application `-race` 34.837s（PTY 32.61s）：三个实际 tmux CLI child 的 Write，真实列表/详情/prompt，批量模式及实际 store/已消费 mailbox archive/current，shutdown 审批/roster，kill/prune，下一次同 Harness Write，全 child join 和双 termios。原公开 pane 曾启动即 continuation guard 拒绝，在 Root 的独立 buddy consumer 修复后，既有 public parent-pane 测试已再次 passed；该 startup 根因与本文件的 Close/frame 修复分别记录。新增 cached Footer claim 的最新公开重跑仍待 peer；不把早期 public v7 外推到此后代码。

后续两个 genuine Source 反例已修复：

- `test-1791957288913.log`：原版真实 1 秒刷新把相同 name 的 agentId 改成 next-held，原 detail effect 再读实际任务并替换旧 owner 的 task；Go 仍显示旧 owner task。修复跟踪捕获的 agentId/name 与 detail presence，身份变化才启动新任务读取、revision fence 拒绝旧结果、组件缺失时退休 detail state。`test-1791957414548.log` 3 top / 8 pass events，0 skip/fail，application 15.999s race。
- `test-1791959314151.log`：真实 React parent 运行原 `cachedTeams` declaration 和实际 `useAppState/useMemo`，original TeamDelete 删除 config/context，parent 新 initialTeams 为空，同一已挂载 TeamsDialog 保留原 initial name/空成员；Source passed/0 blocked，Go async Open 的 fresh context guard 拒绝。仅实际 cached subscription 发行私有 `NativeTerminalTeamsInitial`；actor 在 TUI 挂载时携带该值。App/native 再验证 exact concrete issuer、MainPrint/native root 生命周期、请求初始 JSON 与 issued projection 相符、retired/closed。JSON-only、任意 callback、另一个 actual MainPrint 均不能发行/使用该 claim；nil 继续原 fresh gate。pane/agent 的可选订阅读空投影，不取得 root dialog 权限。首次 top-level Ink rerender 改树导致 state reset，第二次 TypeScript case-sensitivity startup probe 被 guard 拒绝，均保留为无效 fixture，未计 Source proof。

最终 bounded aggregate `test-1791959965935.log`：7 top / 14 pass events，0 skip/fail，application 22.096s、clibootstrap 9.806s race，覆盖上述 claim/frame/task/lifetime/parallel/full operation 消费者和否定边界。前一次 whole operation 在 first mailbox 完成即读取其余三个仍 pending 的 Source sequential mailbox，日志 `test-1791959745786.log` 保留。修正观察每个实际 mailbox fulfilled 后再严格比较；原 12 秒总窗口和值比较未改变。

尚未证明：独立 helper 直接替换 Teams、未经过 TUI committed Retire 的所有调用；普通未受控两 idle 的完整 phase 次序；Windows 实机；最新 cached claim 的完整公开 PTY。上述不写为全支持。全量最终 verify/race/coverage 与 120 对 Benchmark 未由本 subtask 执行。

原始日志在 ignored `artifacts/` 或本机专属 hermetic test home。小型结构化 receipt：`artifacts/go-migration-20261013/teams-dialog-consumer-evidence-20261014/receipt-v1.json`。构建期 helper/字段未齐的日志仅为 build-only；不计产品失败或通过。
