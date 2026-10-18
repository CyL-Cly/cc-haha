# TaskList：实际挂载、当前环境与 Owner 收据

本批普通 TaskListV2 的任务区域、实际文件订阅/重置、工具自动展开、当前设置环境和公开下一人类输入已通过有界验证。新发现的 Ctrl+T 间歇死锁已用真实进程栈及确定性 held-output 回归修复。完整 Spinner/队友树等未覆盖范围仍列为 OPEN；没有最终全量门禁、90% 覆盖率或三端/120 性能验收结论。

所有模型请求只到私有 loopback，假凭据、私有 HOME/config/TMP 和固定 Bun 1.3.14。原 7HTTP 场景保持 Python/RunCommand 30 秒、父测试 35 秒；新动态环境场景保持相同 30/35 秒边界。没有真实 Claude OAuth、公共模型调用或开发者全局状态写入。

## 原消费者和最邻近实现

`native-terminal-task-list.fixture.ts` 从原 REPL 提取真实 `showSpinner` 和 standalone TaskList 条件，挂载原 SpinnerWithVerb、TaskListV2、useTasksV2WithCollapseEffect、AppStateProvider/onChangeAppState、GlobalKeybindingHandlers、ThemeProvider，并执行原 TaskCreate/TaskUpdate 和真实任务文件。实际 local-JSX mount object 来自原 processSlashCommand；`/model` 与 PromptInput 的 model 快捷键分别保留其来源。

47 个 profile、223 个 frame 比较六主题、100/24 列、10/17/36 行、空任务、运行/停止/完成、真实 owner/activity、团队数据、展开/折叠和三个实际菜单来源。Spinner profile 比较其 TaskList 子区域，显式裁掉前两行动画头；不能据此声称完整 Spinner 或完整 REPL 逐帧相等。

`native_terminal_task_list.go` 使用同一个 MainPrint/Owner/Controller/TaskStore，持有实际文件订阅、50ms debounce、不完整列表 5 秒 fallback 和全完成 5 秒重新读取/reset。读操作在 Owner 上启动真实有限文件 future，外层等待并 join。关闭、无订阅和变更 listID 都退休相应 watcher/timer；不会以单纯隐藏 UI 替代实际文件 reset。pane MAIN 保留原非 leader 禁用名单行为。

TaskCreate 只在成功创建与原 hook 完成后展开；TaskUpdate 在读取/存在检查之前展开，所以失败 update 也保持 Source 行为。普通 foreground Agent 使用实际 sealed authParent 和已有 bridge，把更新提交给父 Owner；ordinary async 初始目录和 promoted callback 保持原后台工具过滤/无父展开行为。foreign、expired、closed Scope 继续拒绝。

## 动态设置环境及旧计时器

真实 settings.env 保存/ApplyEnvironment 后，Controller 的旧 startup map 导致下一 TaskCreate 继续写旧列表。旧实现原生回归与公开 CLI 均真 RED。修复只在现有 native TaskList 初始化处绑定实际 Startup/settings/state/environment 身份；teamID 仍先于 env 采样。有限 map fallback 不变，nil/foreign/closed/rebind 拒绝保持，没有新增设置 wake 或持久化 shape。

新公开 5HTTP 场景由实际私有 StatusLine shell 观测环境提交，再让实际 TaskCreate 写新列表 ID1；旧 task bytes/ID1 保留，新 watcher 更新 UI。之后 `/help` mount/onDone、精确两条 canonical、下一人类 ASK、正常退出及 Python/Go 双 termios 都通过。

另一原 mounted Source 场景在完成任务后保存新 listID，再等真实 5300ms：旧列表文件/cache 保留，旧 hide timer 不得 reset 新列表。old-reader Go overlay 真 RED（删除旧文件），当前实现通过。没有把 30 秒近期完成排序的时钟算术 profiles 当作真实组件 30 秒 timer 证明。

## Ctrl+T 实际死锁：保留原窗口

原公开 7HTTP 场景在第二次 Ctrl+T 展开时偶发停在 `Crunching`。`task-list-public-toggle-stack-diagnostic-v7.log` 的同条件 count5 为 4 pass / 1 fail；失败 30.18 秒，SIGQUIT 完整栈证明：

- Tea Update defer → SetTerminalExpandedView → Owner Input `Receipt.Wait`。
- 同一 Owner → Host.runAccepted 的最终 direct output → MainPrint.enqueue → Session.output → `Program.Send`。

Tea 等 Owner，Owner 等 Tea。普通 RunOne 最终 output 没有 RunStream iterator 的可 yield await。原断言没有放宽。

nearest 修复在 Tea 当次 Update 按输入次序 enqueue 同一个真实 Owner Input 写入；返回受终端生命周期管理的后台 Cmd 等实际 receipt。该 receipt 只有 completion，没有 Scope 或写入权限。异常回到 Tea，pending wait 关闭时 join。原 non-Tea 同步 Set API 仍等待同一 receipt；Global 仍保存原 `showExpandedTodos`/`showSpinnerTree`，未知字段保留，Controller/AppState/权限/模型/输出链不重建。

确定性回归用已有 Options.OnSDK 只 held 最终实际 raw result，再从真实 Tea 输入触发写入。只恢复旧同步等待的一份单文件 Go overlay，原 2 秒 sentinel 真 RED，之后真实取消也卡住并保留 30 秒 watchdog 栈。当前实现使 final output、按序 global true/false、同 Harness 下一人类输入通过；另一个真实 held-output 场景证明已开始的 pending receipt 在全局取消时完成 join。TUI 同区验证等待 Cmd 倒序执行仍保留输入提交次序、写入错误显示及退休取消结果。

## 当前实际执行

| 收据 | 结果 |
| --- | --- |
| `task-list-whole-source-native-public-and-vet-v5` | TUI Source47/223 与 Buddy/completion 39.452s；8 native tops/Agent profiles 36.877s；新 5HTTP 4.95s；旧 7HTTP 真 FAIL30.17s；六包 vet0。保留原失败，不称整组通过。 |
| `task-list-expanded-current-output-close-fifo-green-v8` | 两实际 application tops race3.806s；FIFO/error/cancel 两 TUI tops race1.590s；0 fail/skip。 |
| `task-list-current-commit-public-repeat-scope-vet-v9` | 原 7HTTP count5 全过9.74/9.70/9.78/9.69/9.83s（pkg50.806）；新 5HTTP4.34s（pkg6.392）；原完整 Source global callback/同步 held HTTP/scope/identity4tops bootstrap7.167s；六包 vet exit0。 |

完整命令、真实原始日志、失败分类及 SHA 在 `artifacts/go-migration-20261013/tui-task-list-source-evidence-20261014/bounded-summary-v10.json` 和对应 `.result.json`。v1/v2 build-only、v3/v4 fixture whitespace/default-field 观测错误、满盘缺失原输出的 v6 不计作产品 RED。有效旧同步等待 RED 为 `task-list-expanded-held-output-old-wait-red-v7`。

原 checkpoint12 的 Buddy、Usage、AddDirectory 三类公开失败也已分别按完整 Source Buddy processSlash/full Message、原精确 prefix command completion 修复/修正旧错误期待，并保留原 22 秒/3HTTP 与其余业务窗口。对应真实公开三组 green raw 在 `tui-task-list-followup-evidence-20261014`；没有删除 canonical 或降低模型次数断言。

## Workflow / in-process 的直接视图回调

`native-task-list-workflow-teammate.fixture.ts` 使用原 CLI JSON agent parser、原 Workflow definition lookup、真实 runWorkflowAgent/runAgent/query/runToolUse/TaskUpdate 与私有 loopback SSE。原 inProcessRunner 的实际 runAgent 参数对象由 AST 提取；原 runAgent 产生的 tool context 经原 TaskUpdate/TaskCreate business call 后，挂载原 useTasksV2WithCollapseEffect 和 REPL standalone JSX 观察父任务区域。没有替换这些业务函数。Source in-process context 捕获后直接执行原工具 call，因此此项不是完整原 in-process provider 循环或完整主 REPL 的证明。

相同 JSON agent declaration 的旧 Native v7 真 RED：Workflow 实际两次 HTTP 后父 view/global 为 none/false，Source 为 tasks/true；in-process 实际 TaskUpdate 后子 view 变 tasks，Source 是 none→none。v4–v6 未找到自定义物理 agent 文件属于夹具准备失败，不能计作这两个回调的产品 RED。新共享 watcher 断言曾把 Native 未出现 expandedView 字段误当作展开，已按实际 revision=0 保持当前 view 修正；这条失败也不是 watcher 产品 RED。

nearest 修复仅增加由实际 Workflow factory 产生的 private callback carrier，并通过已有 native future / parent Owner receipt 调用父 direct setter。实际 in-process identity 的 direct setter 为 no-op，子自身也不改变 view；独立 setAppStateForTasks、真实 TaskStore 和 watcher 不变。普通 foreground / background / promoted 回调保持原行为，外来或退休 Scope 和关闭父图谱仍拒绝，无新持久化 shape。

原实际 TeamCreate→队友 TaskCreate→父 singleton subscription 收到 pending 文件，但 parent/child/global 仍 none/none/false、原 REPL paint 为空。Native 同一 factory / Host 执行失败 Update 与成功 Create，真实父订阅收到共享任务后，下一父 human query 保持收起；另一个真实 Tea subscriber-frame 消费回归证明没有隐式自动展开，显式 Ctrl+T 才显示任务。真实 held Workflow HTTP 取消后的原 Source parent view 保持 none；Native 也取消 provider 并在原 3 秒边界内 join，不产生晚 TaskUpdate 展开。

| 新收据 | 结果 |
| --- | --- |
| `task-list-workflow-teammate-current-roles-and-ui-v10` | Bootstrap race7.589s，实际两角色/共享 watcher/下一父 human及原 scope guards；TUI race1.698s，实际 Tea frame两形状/下一human/显式Ctrl+T；0 fail/skip。 |
| `task-list-workflow-teammate-guards-roles-and-vet-v11` | 原 in-process8HTTP 3.97s、ordinary foreground/background/promoted 6.85s通过；新 guard fixture 漏 OnAgentID callback 导致 panic，已修准备；六包 vet0。保留整组失败。 |
| `task-list-workflow-teammate-fresh-source-cancel-and-guards-v13` | Bootstrap race9.657s，3 tops：actual closed-parent/foreign/retired、heldHTTP取消/join、Source取消及两角色/共享订阅/nextHuman；0 fail/skip。 |
| `task-list-role-fix-original-public-seven-five-http-v14` | Application race24.989s：原7HTTP 15.25s、新5HTTP 7.55s；原窗口、keys、canonical、模型次数、文件及双 termios 断言保持；0 fail/skip。 |

合并的命令、SHA、错误分类、原失败与当前生产文件 manifest 在 `workflow-inprocess-bounded-summary-v15.json`。这些结果只闭合此处角色回调，不外推完整 Spinner 或所有 Workflow/队友 UI。

## 仍需独立消费者证据

- 完整 Spinner 动画头、tips/budget/brief 和 TeammateSpinnerTree 的整体 cell/style/时间行为。
- Workflow / in-process 的其它业务及完整 in-process Source provider 循环；本处 direct view callback 已有上述有界 Source/native 证明。
- TaskListV2 原 30 秒最近完成 expiry/unmount 的真实 timer；目前只有实际 mounted clock arithmetic，真实 5 秒 singleton reset 已证明。
- native interactive 初始化以外的 child/noninteractive 动态 task-list env 消费者。

这些缺口不以本批局部绿、普通 Source 菜单名或 Source build gate 推断为已完成。
