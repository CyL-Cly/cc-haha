# Go 全量迁移：要干的活总表

更新：2026-10-08。当前持续进行全量实现和真实验收。原清单全部保留，完整迁移仍未完成；下方“起点”统计和工作包状态是10月6日的历史盘点，不代表当前API实现数。最新签名桌面、官方DeepSeek、原源码对照、物理进程和失败修复分别记录在[当前验证记录](go-migration-current-validation.md)。

交付目标：React 桌面保留原版全部发行能力；打开桌面默认运行 Go；CLI、TUI、Web、Electron 共用 Go Session Controller/Harness；旧会话可 fork、续聊和跨端恢复。生产链去掉旧 TS CLI/Server/Harness 的依赖，原源码仅作为隔离对照。正常使用无需指定迁移专用工具子集。

## 先确定工作单位

从用户目标推导，工作单位是一次原来能完成的操作。它包括输入、业务执行、权限、数据和外部副作用、反馈、失败处理及重启恢复。需要检查的整条链是：

**原 UI/CLI 入口 → HTTP/WS/IPC → Go 服务 → 共享 Harness/平台服务 → 真实副作用 → 事件与持久化 → 恢复。**

例如 Connector 的交付要覆盖发现、安装、认证、启用、实际工具加载、当前会话刷新、调用、取消和卸载。只补列表或成功响应不能关闭该项。已有组件继续复用，剩余工作按缺少实现、未接通、分支缺口、待验收分别记录。

## 当前源码确认的起点

- 桌面有 240 处 HTTP 调用点；23 组 API 在 Go 顶层整组返回未实现。168 处调用确定缺少入口，含重复调用，不能称为 168 个独立接口或完成比例。
- Task 管理 REST 已接入真实 Task Store；完整默认工具和桌面联调仍未验收。
- Connector 已有 lifecycle、持久化、native/Skill adapter、managed artifact/bridge/cache 等组件；另有 Skill 扫描、plugin loader/schema、安装物化/安装记录/ZIP 和 MCP stdio/HTTP/SSE 客户端代码。后续执行已接 Server 的 plugin/settings state 和 Output Style 消费者；管理 API 仍未注册，CLI/实际 Agent 的完整扩展、凭据和活跃会话消费者未接通。plugin 的来源覆盖、完整更新/卸载/依赖安装与其他生产接线，MCP 的完整认证/其他原传输仍需补齐。组件或单一消费者的存在不关闭整个工作包。
- CLI 默认完整工具目录仍有守卫；普通非根命令、动态扩展、多个工具分支、Hooks 和 TUI 业务动作有明确缺口。81 个原根参数中，当前 60 个仍被明确拒绝。
- `skillcommands` 仍是未接完整生产消费者的草稿；本次修复编译接口，参数处理的 58 个实际 Source 场景通过。完整定义/发现/条件/插件与调用接线继续归入 D02/D27/D32 待做工作。
- React/Electron 原有 89 个 IPC handler 继续保留；其存在不证明 Go 安装候选的整包行为已经通过。
- 已有有限聊天、工具、旧会话和 TUI 证据均按其原候选保留。完整 Electron E2E、默认工具全集、全平台交付及配对 Benchmark 尚未验收。

## 当前实现证据（不会关闭整个工作包）

- Memory 原生服务已绑定实际 `NewDesktopServer`；实际 HTTP 文件保存、revision 冲突、目录/路径边界通过。Memory、Skill definitions/discovery、model selection、native process、application 五包实际 Source-enabled race 已通过。
- 本地/插件 Skill、legacy 命令和条件激活已使用真实文件、settings/policy/plugin producer；修复加载与设置失效的锁顺序和过期缓存发布，Source 对照及 race 通过。完整 Skill 执行、Hooks/fork/模型上下文仍在接线，不标完成。
- Keychain/`.credentials.json` 的原生存储、缓存和 fallback 已实现；实际 Source native-I/O 对照与 race 通过。Plugin Options 已接原 CLI settings writer/shared secure owner，保存/移除/未知字段保留实际文件回归通过。Connectors 的 HTTP、remote adapter、MCP/OAuth 和活跃会话链继续实现。
- Open Targets 已绑定 Go 桌面请求路由，原有 Memory/Skills application 回归通过；完整 Open Targets 集成/原生 UI 验收正在继续。
- 10月6日的这一阶段尚无候选React/Electron与live证据。后续已完成多次真实签名桌面和官方DeepSeek限定任务；配对Benchmark仍未完成。每次证据对应其二进制快照，见当前验证记录。

## 全部 35 个工作包

表中的状态指完整业务链。底层有组件而桌面 API 缺失的，仍标明入口缺失；所有工作包均保留剩余工作。

本次交付以这张总表和逐项索引为准。后续工作分成四类：缺少业务实现、已有组件需要接线、已接通能力需要补分支、完整候选需要验收。每项先确认属于哪一类，再安排实现；实现、接线和验收继续执行。D32 的 CLI/TUI/Harness 在下方单独展开，不能用“聊天可用”关闭它。

| ID | 能力与当前状态 | 后续必须干的活 |
| --- | --- | --- |
| D01 | Connectors：API 缺失、组件部分完成 | 接通列表/详情和 prepare、authenticate、check、cancel、deactivate、remove；绑定 3 个 native、9 个 Skill、43 个 remote recipe 的实际加载/认证/工具使用；补完进度、冲突、取消、回退、reload 与清理。 |
| D02 | Skills：列表/详情已接，完整运行时缺失 | 列表/详情已绑定实际 plugin/settings/Server；继续补运行时消费者；补条件启用、mentions、动态 slash、模型上下文及 Skill 执行/Hooks/fork；缓存与活跃会话刷新。 |
| D03 | Market：API 缺失 | 全部原市场来源、搜索/筛选/分页、详情/文件、安装/卸载及状态；安装后的内容实际进入 registry。 |
| D04 | Plugins：API 缺失，loader/schema/安装/ZIP 组件部分已有 | 复用已有安装物化、安装记录和 ZIP 组件，补来源覆盖、依赖安装、更新/卸载/清理及异常恢复；接真实 settings/策略、列表/详情、启停/更新/卸载/reload；绑定 Skill、MCP、Agent、Hooks 和活跃会话刷新。 |
| D05 | MCP：API 缺失，三种客户端传输组件已有 | 接作用域配置 CRUD、toggle/reconnect/status、凭据与 OAuth；补其他原发行传输/IDE 行为；将 tools/resources/prompts、取消/清理绑定连接管理和 Harness 动态注册。 |
| D06 | Agent 管理：API 缺失 | 内置/用户/项目/本地/插件/策略来源、优先级、CRUD、override/clear override、reload；model/effort/tools/prompt 实际作用于子 Agent。 |
| D07 | Tasks：REST 已接通，整链待验收 | 默认 Agent 工具与任务列表/详情/reset 联调；列表归属、并发、修订、高水位、通知和恢复，与桌面显示一致。 |
| D08 | Scheduled Tasks：API 缺失 | CRUD、启停/手动执行、真实调度、并发/超时/失败、历史/详情、通知及重启恢复。 |
| D09 | Teams：API 缺失 | 团队/成员/工作台、inbox/transcript、plan 保存/approve/return/cancel/retry、删除和生命周期事件；实际团队执行。 |
| D10 | Workflows：API 缺失 | DSL/parser、校验/保存/删除、执行器、run/session-run 记录、状态与工作流 Agent 重建。 |
| D11 | Conversations：API 缺失 | 原 conversation/chat 状态与 REST queued/status/reset 语义；输入、停止、恢复及 WS/SDK 共用实际运行会话。 |
| D12 | 会话协作：API 缺失 | UI 候选及 list/read/create/send/wait 宿主、独立凭据、引用上下文、transcript 标记、消息投递与等待。 |
| D13 | Search：API 缺失 | 历史/会话全文搜索、过滤/分页、索引更新、大文件、取消和正确范围。 |
| D14 | Memory：原生 REST 已接，整链待验收 | 已绑定项目/文件列表、读写、revision 冲突和实际文件；继续验收记忆更新后的模型请求、React 操作与跨端恢复。 |
| D15 | Claude 官方认证：API 缺失 | start/status/logout、浏览器回调、凭据保存/刷新/撤销、模型解析及实际运行请求。 |
| D16 | ChatGPT/OpenAI 官方认证：API 缺失 | start/status/logout、回调与刷新、模型和真实运行协议。 |
| D17 | Grok 官方认证：API 缺失 | start/status/logout、轮询/刷新、模型和运行请求。 |
| D18 | IM Adapters：API 缺失 | 全部原支持平台配置、注册/二维码/配对/轮询/取消/解绑；共享 Go 聊天运行时、消息与进程生命周期。 |
| D19 | Computer Use：API 缺失 | setup、应用/图标、权限卡、全局 consent、OS 权限、授权 WS、实际工具/native helper、取消与释放。 |
| D20 | Voice：API 缺失 | catalog/偏好、provider prepare/cancel/status/assets、资源生命周期、录音/上传/transcribe、语言与错误。 |
| D21 | Traces：API 缺失 | 真实捕获生产器、设置/列表/revision/删除、会话/call 详情、脱敏和原始 JSONL 导出。 |
| D22 | Diagnostics：API 缺失 | 实际日志/事件采集、索引/重建、报告、bundle 导出、日志目录、clear 和 Go/Electron 错误接入。 |
| D23 | Doctor：API 缺失 | report/repair、文件/schema/JSONL 检查、作用域和 protected skips；保持原可修复 UI 状态与用户数据保护边界。 |
| D24 | Activity Stats：API 缺失 | 日期/模型/成本/活跃度历史聚合与全部字段，绑定实际运行及历史数据。 |
| D25 | Open Targets：原生服务/HTTP 已接，桌面待验收 | 已绑定平台发现、文件筛选、图标、关联/open-with 与 native launch；继续整包 React/Electron 操作与真实平台验收。 |
| D26 | 会话/项目/工作树：部分 | project-history、batch-delete、repository-branch、repository/worktree 创建、消息分支 fork、history-recovery/chat-status；基础接口完整字段与恢复。 |
| D27 | 侧聊/Inspector/追问：缺失或部分 | side-chat、side-question/取消、完整 slash、inspection/context、子 Agent inbox/追问、分页/嵌套和 workflow reconstruction。 |
| D28 | Workspace/Review/文件预览：部分 | Review status/revision/diff/stage/unstage/hunk/revert；checkpoint/rewind 剩余分支；preview-fs/local-file、附件与多种文件预览、Range/认证/路径/watch。 |
| D29 | Provider/模型/设置：部分 | 官方模型覆盖、保存/未保存配置测试、模型目录、cc-switch 导入、OAuth freshness、代理与协议转换；CLI launcher、status 详情和全部设置消费者。 |
| D30 | H5/公网/远程：部分或缺失 | PublicAccessServer、设备配对/批准/撤销/过期、cookie/Origin/限流/listener；Electron 控制接线、远程细粒度权限及现有 H5 全流程。 |
| D31 | WS/SDK 与运行控制：部分 | 全部 11 类客户端控制、30 类服务端及 9 类子 Agent 流事件；附件/引用、规则、问答、后台停止、snapshot/重放、原 /sdk 通道与取消。 |
| D32 | 完整 Harness/工具/CLI/TUI：部分 | 默认工具全集和同名工具缺失分支；Prompt/上下文/schema、动态扩展、活跃 Hooks、权限、预算/压缩、后台；CLI/TUI 全部原业务入口。见下方细分。 |
| D33 | React/Electron/native：保留、待联调 | 默认 Go 启动/退出与重启；窗口/托盘/菜单、多窗口、剪贴板/拖放、终端、预览/浏览器、通知、Pets、更新及原页面可达性。 |
| D34 | 持久化/升级/恢复：部分 | 全部原配置/凭据/会话/索引/任务/插件等格式与未知字段；并发写入、取消/崩溃/重启、跨端/跨版本、前向迁移与回退，不重复副作用。 |
| D35 | 全量交付与 Benchmark：未验收 | 逐操作原版契约对照、当前候选全部门禁、完整原生安装包、真实旧/新任务、平台/升级/回退、冻结条件的配对 Benchmark 与使用文档。 |

D32 必须拆开执行，不能只写“CLI/TUI 支持”：

1. **CLI 启动与全部参数：**原初始化/配置/策略/凭据/网络/迁移；逐参数默认、校验、组合、业务消费者和退出语义。
2. **全部发行子命令与 worker：**auth、MCP、plugin/marketplace、doctor、install/update/setup-token、auto-mode，以及原可达的平台、worktree/tmux 等 worker。
3. **Slash 与动态命令：**local/local-jsx/prompt、别名/参数/可用性/历史/副作用；用户/项目/managed/legacy/bundled/条件 Skills、插件、MCP 和 workflow 来源。
4. **Headless/SDK：**text/json/verbose/partial/NDJSON、全部控制分支、权限/问答/queue/inbox、远程传输、回压、EOF/信号/flush/退出码。
5. **TUI 输入：**中文/emoji/组合字符、多行/编辑/撤销/stash/Vim、Bash 模式、文本/图片/文件/PDF 粘贴与引用。
6. **TUI 状态与操作：**历史/搜索/真实补全、按键配置/chord/watch、问答/权限/计划表单、Task/后台/Team 面板、model/effort/thinking/fast/workflow 的实际切换。
7. **TUI 呈现与终端：**消息/工具/Markdown/diff/错误/通知/状态栏、复制/滚动、外部编辑器、resize/信号/raw/alternate-screen 恢复、PTY/ConPTY 和子进程释放。
8. **共享 Harness：**系统 Prompt、指令/记忆/style、附件/历史/引用、工具目录/schema/排序、cache/beta/header、Hooks、权限、并行/队列/重试/fallback、压缩/预算/成本、后台/协作及停止/崩溃恢复。

各命令、参数、Slash、终端动作、SDK 判别项和条件能力的逐项工作保留在 [CLI/TUI 工作索引](go-cli-tui-work-checklist.md)。动态名称是开放输入，必须迁移发现/加载/更新机制；原构建和平台条件需有依据，不能用一个有限 profile 代替全部原发行能力。

原发行条件的补充工作见[默认能力与 worker 工作矩阵](go-release-condition-work-matrix.md)：逐项保留 49 个工具注册表达式、19 个 bundled Skill 模块、16 个 CLI bootstrap 分支，另列 desktop sidecar 的 `--voice-worker`。Worktree、外部 Teams/Workflows、Windows PowerShell 回退、交互/非交互 Task、图像 provider、LSP 与 MCP resource 消费者已明确归属；其他原发行 profile 和各实际运行分支仍须核定与迁移。这个矩阵只细化待做范围，不关闭 D02/D07/D09/D10/D19/D20/D31–D35。

## 已有扩展组件之后仍要干的活

这段用于避免把“已有 package”写成“完整功能已完成”，也避免把已有代码重新从零实现。下列均保留在 D01–D06/D32/D34 的待办内。

| 现有组件 | 接下来具体工作 |
| --- | --- |
| Connector lifecycle/adapter/bridge | 接真实配置、凭据、策略和插件安装管理；Server action 状态/进度/取消接线；安装后进入实际 registry，刷新所有活跃会话；完成真实认证、检查、卸载和恢复。 |
| Skill 扫描/详情与共享 frontmatter | 列表/详情已绑定实际 plugin/settings/Server；继续绑定 mentions、运行时命令生产器；实现条件启用、动态 slash、模型上下文、Skill 执行与 Hooks；文件变化和 reload 作用到当前会话。 |
| plugin manifest/schema/component loader、materializer、installed registry、ZIP | 复用已有 manifest/缓存/项目/版本路径/复制/安装记录/ZIP 组件；补来源、平台、异常和迁移剩余分支，依赖安装闭包、更新/卸载/清理；绑定实际内置注册、settings cache、策略、Server 和 Session 消费者。 |
| MCP stdio/HTTP/SSE 客户端 | 补 OAuth 发现/登录/刷新/存储/权限升级，以及原发行的 WS/WS-IDE、SDK/in-process、claudeai-proxy 和 IDE header；接配置作用域、连接缓存/重连/启停/热更新、工具命名/权限/结果和资源清理；补平台验证和非法结果错误对照。 |
| Agent 定义解析/加载 | 完整来源优先级、编辑和 override/reload 管理、默认及动态定义进入 shared registry；运行时真正消费 model/effort/tools/prompt，覆盖后台和嵌套子 Agent。 |

上表保留最初待办范围；后续已经持续执行实现、原生UI与官方模型验证。已有限定证据仍不自动关闭完整业务链，当前结果以验证记录和各工作包后续证据为准。

### D04 的接线和剩余分支，按实际依赖拆开

这些是 D04 现有待办的执行细分，不另算工作包，也不表示已经接通。

| 顺序 | 要做的工作 | 交付结果 |
| --- | --- | --- |
| 1 | 接共享配置和策略 | Server 已接共享 settings state、plugin settings base 与来源优先级；CLI/实际 Agent startup 的完整插件消费者仍须迁移，保持原策略和当前回合更新行为。 |
| 2 | 接安装存储与归档 | Server 的 NativeProcessSettings 已创建 InstalledRegistry、Materializer、ZIP 和 Loader，复用实际 store；继续覆盖 CLI/SDK/Agent 初始化及完整来源、平台、异常和资源清理。 |
| 3 | 补完整来源与版本事务 | 保持原 local/Git/Git-subdir/npm 等实际发行来源、引用/版本/seed/cache/ZIP 分支；补取消、子进程清理、损坏缓存、相对路径和跨平台错误。 |
| 4 | 补管理生命周期 | marketplace 注册/刷新、依赖安装闭包、enable/disable、update/uninstall、旧安装记录合并、数据目录与垃圾清理；保留未知字段与崩溃恢复。 |
| 5 | 接运行时消费者 | Server 的 plugin settings/Output Style 消费者已接；Skill/slash、MCP tools/resources/prompts、Agent 定义、Hooks 和实际 Agent 配置仍须进入同一 Harness，启停产生实际运行变化。 |
| 6 | 接 reload 与失效 | 管理操作、文件变化、配置修改后清理正确的 loader/installed/settings 缓存；刷新全部活跃会话，保留正在执行任务的原语义。 |
| 7 | 接桌面及 CLI 管理入口 | 原 Plugins/Market/Connectors API、普通 plugin/marketplace 子命令使用同一服务；列表状态来自真实安装和加载结果。 |
| 8 | 完成该能力的后续验收 | 同一候选走原公开入口完成安装→认证/启用→正常对话使用→更新/禁用/卸载→重启恢复，覆盖失败、取消与并发。 |

因此，当前应安排的是“补剩余分支并接通已有组件”，不能把 D04 写成零实现，也不能写成插件已经可用。

### D02/D27/D32 的 Skill、Slash 与 mentions 工作细分

以下是现有工作包内的执行条目。扫描出文件、显示候选和模型实际执行需要共用同一份定义与状态。

| 条目 | 必须干的活 | 应交付的结果 |
| --- | --- | --- |
| 本地来源与策略 | managed/user/project、`.claude`/`.agents`、额外目录、bare、来源开关及 plugin-only 策略 | 当前项目得到与原版一致的可用定义；被策略禁用的来源不参与运行。 |
| 去重与覆盖 | realpath/inode 去重、名称冲突、`.claude` 跨作用域优先、首次插入位置与排序 | UI 候选、命令与模型上下文使用一致的胜出定义。 |
| Legacy commands | 递归 Markdown 扫描、SKILL.md 转换、命名空间、worktree 回退和嵌套仓库边界 | 原命令目录和新 Skill 目录都保持原加载行为。 |
| 条件与动态发现 | paths/brace 展开、ignore 匹配、文件触及后的激活、下层目录发现、缓存/通知 | 正确时点激活，文件变化与 reload 更新当前会话。 |
| 插件命令与 Skills | 默认/自定义目录、单文件、metadata override、inline content、命名空间/aliases 与独立去重 | 原插件各声明方式均能发现并实际调用。 |
| Prompt 与调用配置 | 参数替换、Skill/Plugin/Session 变量、插件 DATA/选项、模型/effort、Hooks/fork | 生成原版等价的实际调用上下文；敏感选项按原规则处理。 |
| Shell 与结果 | Bash/PowerShell 执行、权限检查、取消/错误和工具结果持久化 | Skill 中的命令产生真实结果，恢复不重复副作用。 |
| 共享消费者 | Server 列表/详情/mentions、Slash、Skill 工具、CLI/TUI 与 Harness 的生产接线 | 同一份定义在各端可见且可执行，启停/reload/恢复保持一致。 |

来源：`src/skills/loadSkillsDir.ts`、`src/utils/plugins/loadPluginCommands.ts`、`src/utils/argumentSubstitution.ts`、`src/server/api/skills.ts` 与 `src/server/services/capabilityMentionService.ts`。原版函数对照、跨端正常使用及完整安装候选的验收均继续列为待做；本轮只整理任务。

## 原页面的具体工作归属

这些页面的 React 实现保留。以下是后端补齐及联调工作归属，不是要求重写页面。源码入口为 `desktop/src/pages/Settings.tsx` 和 `desktop/src/components/layout/ContentRouter.tsx`。

| 原生设置页 | 工作包 | 页面背后要补齐的工作 |
| --- | --- | --- |
| providers | D15–D17、D29、D34 | Provider/官方认证、模型目录与覆盖、连接测试、协议代理、切换及恢复。 |
| general | D29、D31、D33、D34 | 原常规/外观/个人及项目偏好、权限/运行选项；保存作用到实际消费者并在重启后恢复。 |
| voice | D20、D33 | Provider/资源准备与取消、录音转写和原生麦克风链。 |
| h5Access | D30、D33、D34 | H5 配置/二维码、公网配对、设备与远程权限、listener 生命周期与恢复。 |
| adapters | D18、D33、D34 | 全部平台账号/配对/解绑和实际 IM 运行，sidecar 重启及状态恢复。 |
| terminal | D29、D32、D33 | 原终端偏好与 Bash path、PTY/ConPTY、完整 bundled Go CLI/TUI。 |
| mcp | D05、D31、D32 | 配置作用域、认证/连接、启停/重连、工具加载与活跃会话刷新。 |
| agents | D06、D31、D32 | 定义/来源/override、加载与刷新；实际子 Agent 使用相应配置。 |
| skills | D02、D04、D32 | 扫描/详情/文件/来源、slash 与模型上下文、实际 Skill 执行。 |
| memory | D14、D32、D34 | 作用域读写、缓存与下一请求的记忆内容、原格式恢复。 |
| plugins | D03–D05、D32、D34 | 安装/缓存/启停/更新/卸载、依赖与策略、MCP/Skill/Agent/Hooks 加载和 reload。 |
| pets | D29、D33、D34 | 原素材/创建/显示/导航、独立凭据、偏好和旧 UI 状态兼容。 |
| computerUse | D19、D31、D33 | setup、全局 consent/OS 权限、授权事件、实际工具/native helper 与取消。 |
| activity | D24、D34 | 真实历史、日期/模型/成本聚合与全部页面字段。 |
| trace | D21、D27、D34 | 实际采集、列表/详情/call/raw、设置/revision/删除与恢复。 |
| diagnostics | D22、D23、D33 | Go/Electron 日志/事件、索引/报告/导出、诊断及原允许修复边界。 |
| about | D29、D33、D35 | 原版本/状态展示、更新检查/下载/安装/重启与正确 Go 安装候选。 |

Web host 走 `H5Settings`，只呈现 providers/general；Electron host 走上述完整 17 页。保留原 host 差异，Web 页面验证不能关闭 Electron 的其他设置页。

| 原内容页/状态 | 工作包 | 后续工作 |
| --- | --- | --- |
| session、空会话入口 | D11、D26–D32、D34 | 创建/历史/上下文/审批/附件、工具与运行控制、workspace、跨端恢复；切 Tab 保留原当前会话行为。 |
| settings | 上表 | 17 页逐项工作及原 host 分流。 |
| scheduled | D08、D31 | 调度 CRUD/执行/历史/通知、会话导航和恢复。 |
| connectors、market | D01–D05、D32 | 原 ExtensionMarket 页各来源、搜索/安装/认证/启停、实际 runtime 消费。 |
| trace、traces | D21、D27 | 列表/会话/call/raw、窗口及返回列表导航。 |
| subagent | D06、D27、D31、D32 | run/history/追问、活动分页、嵌套/后台、流事件和生命周期。 |
| team-member、team | D09、D31、D32 | 成员/工作台、消息/plan 状态机、事件与实际执行。 |
| terminal | D32、D33 | Go CLI/TUI、spawn/write/resize/kill/exit、隐藏 Tab 保留终端和正确清理。 |
| legacy workbench | D28、D33、D34 | 按原状态迁移回来源会话并关闭旧 Tab；保留兼容重定向，不创建第二套 workspace controller。 |

## 后续实施顺序

| 阶段 | 先干什么 | 阶段交付条件 |
| --- | --- | --- |
| 0 | 冻结原发行边界、基线、能力账本与比较条件，落实 D34 状态契约 | 每项原操作有来源、实现归属、依赖和验收条目；未知发行条件保留明确待办。 |
| 1 | 补共享运行基础：plugin/Skill/MCP/Agent registry、默认工具、Hooks、Provider/认证、上下文和持久化 | 同一 Go Harness 有真实消费者，配置变化作用到当前/后续会话；正常默认启动不靠工具子集绕行。 |
| 2 | 沿实际业务链接通 D01–D06、D13–D17、D21–D29 的管理 API 与消费者 | 每个操作连同副作用、反馈、失败/取消和恢复一起交付；不只添加返回成功的 handler。 |
| 3 | 接通 D07–D12、D18–D20、D31 的后台/调度/协作/IM/平台/实时能力，同时补完整 CLI/TUI 宿主 | 相同会话、工具和任务状态跨宿主一致；所有原控制与事件有实际生产者。 |
| 4 | 完成 D30/D33/D34 的远程、native、完整安装包和升级交接 | 桌面默认直接使用 Go；Web、公开 CLI/TUI、Electron 按原入口工作，跨端与重启恢复一致。 |
| 5 | 执行 D35 全量验收与配对 Benchmark | 绑定同一候选，记录所有要求的通过/失败/未运行；必需项目全部关闭后才宣布迁移完成。 |

D34 的数据与恢复、D35 的原版对照贯穿各阶段。具体 API 与其运行时可以连续交付；已完成组件复用，按实际依赖推进。主负责人维护一份状态账本，不用 Agent 数量或测试数量代替业务进度。

## 每项怎样才算完成

每项记录：原入口与发行条件、Go 实现/消费者、候选版本、输入与初始状态、权限、响应/事件、实际文件/进程/外部结果、失败/取消、持久化与重启恢复、证据及剩余边界。

确定性对照、真实模型任务、原生整包及 Benchmark 分别留证。后续真实验证包括旧 `/tmp` session 的正常 fork/续聊和新任务，经 CLI/TUI/Web/Electron 正常对话完成并检查实际产物；私有导入或工具子集不代替原公开入口。

Harness 验收先冻结双方模型 ID/协议/参数、Prompt 与上下文、工具 schema/顺序、权限、任务/fixture、预算、超时和评分器。DeepSeek 官方真实任务作为约定的在线验证；若替换原 Benchmark 模型，需同时重建原版基线，不将换模型的差异归因于 Go。确定性部分要求逐项等价；在线模型的随机性用预先冻结的配对和统计判定处理，不能用少数聊天回合承诺得分完全相同。不能降低评分或删题来过关。

## 逐项附件与本次核对范围

- [D01–D35 详细待办](go-desktop-parity-todo.md)：115 条未关闭子任务；已保留原附件全部 95 条要求及新增细分。
- [桌面 API/WS/native 索引](go-desktop-api-checklist.md)：240 处 HTTP 调用、40 个原 handler、全部协议类型、IPC/bridge、工具注册表达式和 Go 守卫，保留来源位置与当前缺口。
- [CLI/TUI 工作索引](go-cli-tui-work-checklist.md)：逐命令/参数/slash/worker/终端动作/SDK 控制及业务工作。
- [Server 独立入口与生命周期](go-server-boundary-work-checklist.md)：16 项接线细分，包含 Pet 投影、SDK/协作凭据、OAuth 回调、文件/代理、公网配对、请求断开、索引/调度/监听及启动关停；归入现有 D01–D35。
- [原附件范围对照](go-work-scope-audit.md)：原要求与当前任务的逐项映射。
- [历史执行证据](go-desktop-parity-progress.md)：组件已有实现与其限定检查；不扩成完整功能验收。

最新用户要求的清单复核：当前路由中的 23 组整组未实现与索引一致；240 处 HTTP 调用点中，131 处整组缺失、31 处子路由缺失、6 处公网资源缺少注册，合计 168 处。另有 repository/worktree 与 workflow reconstruction 两处部分行为缺口，均保留在 D26/D27，未混入上述调用点数。650 个桌面、260 个 CLI/TUI 和 43 个发行条件输入文件的哈希复核无漂移；35 个工作包、115 条未关闭主任务及原附件 95 条要求的映射均保留。此次仅做文档和静态核对，没有产品实现、功能复现、UI 操作或模型请求。

本次重新读取当前 Go 顶层路由、生产 NewDesktopServer、CLI 默认目录/非根分派、动态 registry/Slash 守卫和 Electron Go 默认选择；650 个桌面盘点输入与 260 个 CLI/TUI 盘点输入哈希均无漂移，复用其逐入口静态索引。D04 已有接线和剩余消费者分别记录；D02/D27/D32 新增草稿仍按待做处理。静态核对支持上述缺口和来源覆盖；没有新启动产品测试、原生 UI、真实模型或 Benchmark。

后续持续目标的实现记录独立保留在[执行进展](go-desktop-parity-progress.md)。新增 D02/D06 原生解析/扫描组件不关闭其 Server、完整运行时和真实整包要求；本表的静态梳理与后续组件验证分开。

## 后续实际接线记录：Skills 列表与详情

本次持续目标窗口已将列表/详情接入 NewDesktopServer 的实际 plugin/settings/skillregistry；开关、错误与插件禁用有 11 个真实 Source handler/HTTP 请求对照。新 native 设置字段通过 41 个 Source schema case；命令参数通过 58 个 Source case。完整 D02、CLI/Harness 消费者和全部桌面能力仍未完成。正式 Go unit/race 各 6320 pass，build/vet 通过；总体因 74.53% 改动覆盖率和 8 个映射缺口失败。UI/native、真实模型和 Benchmark 本轮未运行。[限定证据与剩余边界](../../artifacts/go-native-skills-api/report.md)。这段实施记录不将前文静态梳理冒称产品验收。
