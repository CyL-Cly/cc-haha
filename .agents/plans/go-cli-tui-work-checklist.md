# Go 全量迁移：CLI、TUI 与共享 Harness 工作索引

更新：2026-10-08。属于主清单 D31/D32/D34/D35。持续实现并进行原源码对照、实际进程和官方DeepSeek验收。下方10月6日盘点保留为历史起点，不能代表当前守卫数量或能力状态；默认19工具、普通Skill/Agent、后台、协作/Teams、async Hook、原生签名桌面和CLI已有分项实际证据，见[当前验证记录](go-migration-current-validation.md)。完整CLI/TUI、全部发行分支与配对Benchmark仍未完成。

本索引以当前生产 Go 入口判断接线情况，原版目录来自保存的 Source 静态 catalog 与有限发行 profile。登记所有找到的条件、内部与测试项，先核定原实际发行可达性；不要求把原来未发行的 stub 变成产品功能，也不删除原来支持的条件能力。动态 Skill、plugin、MCP、workflow 名称是开放输入，迁移对象是发现、优先级、加载及更新生产器。

## 1. 10 月 6 日初始缺口（历史）

本表保存最初盘点，不能按其中的旧守卫推断当前实现。10 月 8 日普通默认目录、Skills/Agent/后台/协作与原生官方模型已有分项证据；普通 `auth status`、OpenAI status、两种 logout 和 `agents` 已通过实际 Go 子进程与原 Source 对照，见[非模型命令证据](go-native-cli-command-evidence-20261008.md)。MCP 正在接普通入口；OAuth login 与其他子命令仍未完成。下面的工作队列保持开放，逐项按最新生产消费者和实测结果核定。

| 当前入口 | 直接源码判断 | 要做的工作 |
| --- | --- | --- |
| 普通 CLI/TUI | application.ParseCommand 要求非空 --tools | 完整默认工具生产器与业务实现接通后恢复正常默认启动 |
| 普通非根命令 | 除 agents 专用路径外，main 进入 action is not migrated | auth/MCP/plugin/doctor/install/update 等实际 handler 与初始化 |
| 根参数 | 当前只接纳有限参数，其他参数在业务前拒绝 | 逐参数业务消费者、默认、冲突、参数组合与退出语义 |
| Slash | 共享 print 输入处理器只接纳 cost；TUI 对无参数 model 单独处理 | 其余 local/local-jsx/prompt、参数、动态命令、可用性与副作用 |
| 动态目录 | print registry 拒绝活跃 .claude skills/workflows/agents 和 .agents skills | 实际扫描、覆盖、条件启用、缓存、文件变化与热更新 |
| TUI 补全 | 普通 RunCommand 未传 Commands、Keybindings、ExternalEditor 的宿主配置 | 真实候选、用户按键配置/监听、编辑器配置接入当前 Session |
| TUI 非文本输入 | Session 拒绝附件和非 text 粘贴 | 文件/图片/PDF/引用的内容生产与上下文传递 |
| TUI 输入模式 | Session 拒绝非 prompt 模式 | ! Bash 直接执行及原回显/历史/权限/取消 |
| TUI Host actions | 当前显式支持 cost/model/modelPicker，其余返回 UnsupportedAction | 快捷键、表单、任务/Team 面板和所有原可达操作 |
| 持久权限规则 | Session.RespondPermission 对 Rule 有未实现守卫 | 原模式、目录与回合/会话/持久规则更新、存储和重放 |
| 已注册 Read/Bash/Agent | 图片/PDF、Bash 后台、Agent 后台分别仍有守卫 | 同名工具的原输入分支、进程/任务所有权、结果和通知 |
| Hooks | 当前 resolver 主要支持空配置，实际 matcher/执行受限 | 配置/会话/程序化 Hooks、事件、输出、超时/取消与清理 |

这些是静态证据。已有编辑、多行文本、历史、文本粘贴、模型选择器、基础审批和终端清理代码保留为部分实现，不能推断完整原交互已通过。

## 2. 可执行工作队列

| ID | 工作 | 必须交付的业务结果 | 依赖 |
| --- | --- | --- | --- |
| C-W01 | 完整生产启动 | 原 preAction/init、配置/策略、keychain、证书/网络、迁移、watcher、错误与退出；普通 CLI 默认可启动 | D29/D34 |
| C-W02 | 命令行参数 | 下方每个原参数实际作用到初始化/会话/工具/输出，保持别名、默认、校验与组合 | C-W01、对应消费者 |
| C-W03 | 全部发行 subcommand | auth/MCP/plugin/marketplace/doctor/install/update/setup-token/auto-mode 等产生真实结果；agents 剩余作用域补齐 | D04–D06/D15–D17/D23 |
| C-W04 | Bootstrap/worker | 原发行的 Computer Use、Chrome、worktree/tmux、更新及其他原可达 worker 正常启动/停止 | D19、平台边界 |
| C-W05 | Slash 业务 | 原 local/local-jsx/prompt handler、accessor/loader/onDone、历史与副作用；完整候选来自当前项目 | C-W01/C-W06 |
| C-W06 | 动态及 bundled 命令 | 用户/项目/managed/legacy/条件 Skill、插件、MCP、workflow 的来源优先级和缓存更新 | D01–D06/D10 |
| C-W07 | Headless 输出 | text/json/verbose/NDJSON/partial 的字段、顺序、流式停止、错误和退出码 | D31、共享 Harness |
| C-W08 | SDK stdio 控制 | 下方全部实际控制分支、回调/响应、队列、权限、问答、动态 reload、task/session inbox | D05/D11/D12/D31 |
| C-W09 | SDK 远程传输 | 原 SSE/WebSocket/CCR 及 sdk URL、认证、重连、半关闭、EOF、回压与最终 flush | C-W08/D30 |
| C-W10 | Continue/resume/fork | 会话选择、分支点、工作树、文件历史、恢复配置与跨端续聊，不手工改日志 | D26–D28/D34 |
| C-W11 | Recovery | 原数据检查/恢复、交互与 headless、失败/退出边界以及独立发行入口 | D23/D34 |
| U-W01 | 原输入交互 | 中文/emoji/组合字符、多行、编辑、撤销、stash、Vim、模式切换与提交/取消 | C-W01 |
| U-W02 | 粘贴/附件 | 原文本 pill、图片/文件/PDF、预算、移除、引用、重放，扩展一次进入模型上下文 | D28/D31 |
| U-W03 | 历史与补全 | 重启历史、搜索、编辑草稿恢复；slash/路径/Skills/MCP/@Agent 的真实候选与排序 | C-W05/C-W06/C-W10 |
| U-W04 | 按键与 Host action | 原按键上下文、chord、用户配置与 watcher，全部发行动作有实际消费者 | 下方 K 表 |
| U-W05 | 业务表单 | AskUserQuestion、权限与规则、多字段、计划、选择器、设置/插件/Agent 表单 | D01–D06/D31 |
| U-W06 | 状态面板 | 任务/后台/Team、进度、通知、消息操作、差异/上下文、终端和原可达面板 | D07–D12/D28 |
| U-W07 | 运行配置切换 | model/effort/thinking/fast/workflow、模式/目录作用于实际请求及恢复 | D29/D34 |
| U-W08 | 呈现与复制 | 原消息/工具结果/markdown/diff、错误、spinners、主题/状态栏、选择/复制与滚动 | U-W05/U-W06 |
| U-W09 | 外部编辑器 | EDITOR/VISUAL/原配置选择、PTY 暂停/恢复、文件变化、进程失败、退出清理 | C-W01/U-W01 |
| U-W10 | 终端生命周期 | resize、EOF、Ctrl-C/D/Z、SIGTERM、raw/alternate screen 恢复、子进程释放、PTY/ConPTY | 全部 U 工作 |
| H-W01 | 模型请求生产 | Prompt/指令/记忆/style、上下文、工具 schema/描述/过滤/排序、cache/beta/header 保持原行为 | D02/D05/D06/D14/D29 |
| H-W02 | 默认工具全集 | 补齐 Source 全部实际发行工具与已注册工具的缺失分支、权限/结果/Hooks | D32、49 个注册表达式索引 |
| H-W03 | 活跃 Hooks | 原允许来源、matchers、事件、命令/prompt/agent 等原支持类型，阻断/输出/取消/清理 | D04/C-W01 |
| H-W04 | Agent/后台/协作 | 子 Agent 配置、后台 Bash/Agent、TaskOutput/Stop、Team/Workflow/session 协作及通知 | D06–D12 |
| H-W05 | 限额及压缩 | context/output/task/USD/turn 预算，压缩前后、模型重试/fallback、用量与成本恢复 | D29/D34 |
| H-W06 | 执行与恢复 | 并行、排队、权限等待、问答、停止/断线/失败/finally、真实写入与副作用恢复 | D31/D34 |
| V-W01 | 原版契约对照 | 输入、模型请求、工具轨迹、输出、错误、持久化与副作用；保留原比较条件 | D35 |
| V-W02 | 正常跨端任务 | CLI/TUI/Web/Electron 默认入口完成旧 /tmp session fork/续聊与新任务 | 所有相关能力 |
| V-W03 | 安装与平台交付 | standalone/bundled 同一 Go 二进制，PATH/cwd/env、签名、更新、回退及平台实跑 | D33–D35 |
| V-W04 | Harness Benchmark | 原模型/任务/权限/预算/超时/评分器不变，冻结双方条件执行配对评测 | V-W01/V-W02 |

所有工作都以用户操作和原结果关闭。确定性门禁、真实模型任务、原生整包和 Benchmark 分别留证，证据对应实际二进制快照。以下登记工作范围继续保留；不能以部分普通任务成功关闭整项。


## 3. 原命令节点（含条件声明）

普通有限 profile 注册 36 个节点；静态扫描保留 58 条含条件的 argv 声明。节点和声明不是独立业务数量。每个节点的全部专属 options/arguments 仍在原 catalog 中，实施时必须一起移植。

| ID | 原命令/别名 | 原注册位置 | 冻结特性/条件 | 当前工作 |
| --- | --- | --- | --- | --- |
| C001 | `claude [prompt]` | [src/main.tsx:971](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:971) | 启用；[] | 部分；完整默认目录被拒绝 |
| C002 | `claude mcp` | [src/main.tsx:3974](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:3974) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C003 | `claude mcp serve` | [src/main.tsx:3975](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:3975) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C004 | `claude mcp remove <name>` | [src/main.tsx:3996](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:3996) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C005 | `claude mcp list` | [src/main.tsx:4004](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4004) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C006 | `claude mcp get <name>` | [src/main.tsx:4010](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4010) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C007 | `claude mcp add-json <name> <json>` | [src/main.tsx:4016](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4016) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C008 | `claude mcp add-from-claude-desktop` | [src/main.tsx:4025](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4025) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C009 | `claude mcp reset-project-choices` | [src/main.tsx:4033](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4033) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C010 | `claude server` | [src/main.tsx:4042](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4042) | 未启用；[{"predicate": "feature(\"DIRECT_CONNECT\")", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C011 | `claude ssh <host> [dir]` | [src/main.tsx:4126](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4126) | 未启用；[{"predicate": "feature(\"SSH_REMOTE\")", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C012 | `claude open <cc-url>` | [src/main.tsx:4139](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4139) | 未启用；[{"predicate": "feature(\"DIRECT_CONNECT\")", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C013 | `claude auth` | [src/main.tsx:4180](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4180) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C014 | `claude auth login` | [src/main.tsx:4181](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4181) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C015 | `claude auth status` | [src/main.tsx:4205](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4205) | 启用；[] | 普通入口已接40基础+40 FD/CCR/managed Source profile与实际继承pipe；完整模型凭据/内部schema/异常与平台仍开放 |
| C016 | `claude auth logout` | [src/main.tsx:4215](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4215) | 启用；[] | 原两种退出已接；4 Source状态与实际凭据文件/marker/Global/普通CLI通过；全账号/启动profile仍待闭环 |
| C017 | `claude plugin`；`plugins` | [src/main.tsx:4234](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4234) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C018 | `claude plugin validate <path>` | [src/main.tsx:4235](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4235) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C019 | `claude plugin list` | [src/main.tsx:4245](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4245) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C020 | `claude plugin marketplace` | [src/main.tsx:4257](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4257) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C021 | `claude plugin marketplace add <source>` | [src/main.tsx:4258](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4258) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C022 | `claude plugin marketplace list` | [src/main.tsx:4268](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4268) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C023 | `claude plugin marketplace remove <name>`；`rm` | [src/main.tsx:4277](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4277) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C024 | `claude plugin marketplace update [name]` | [src/main.tsx:4285](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4285) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C025 | `claude plugin install <plugin>`；`i` | [src/main.tsx:4295](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4295) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C026 | `claude plugin uninstall <plugin>`；`remove,rm` | [src/main.tsx:4306](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4306) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C027 | `claude plugin enable <plugin>` | [src/main.tsx:4318](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4318) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C028 | `claude plugin disable [plugin]` | [src/main.tsx:4329](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4329) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C029 | `claude plugin update <plugin>` | [src/main.tsx:4341](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4341) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C030 | `claude setup-token` | [src/main.tsx:4353](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4353) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C031 | `claude agents` | [src/main.tsx:4364](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4364) | 启用；[] | 有限 agents 入口存在；完整范围待验收 |
| C032 | `claude auto-mode` | [src/main.tsx:4375](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4375) | 启用；[{"predicate": "feature(\"TRANSCRIPT_CLASSIFIER\")", "value": true, "branch": "then"}, {"predicate": "getAutoModeEnabledStateIfCached() !== \"disabled\"", "value": null, "branch": "then"}] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C033 | `claude auto-mode defaults` | [src/main.tsx:4376](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4376) | 启用；[{"predicate": "feature(\"TRANSCRIPT_CLASSIFIER\")", "value": true, "branch": "then"}, {"predicate": "getAutoModeEnabledStateIfCached() !== \"disabled\"", "value": null, "branch": "then"}] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C034 | `claude auto-mode config` | [src/main.tsx:4383](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4383) | 启用；[{"predicate": "feature(\"TRANSCRIPT_CLASSIFIER\")", "value": true, "branch": "then"}, {"predicate": "getAutoModeEnabledStateIfCached() !== \"disabled\"", "value": null, "branch": "then"}] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C035 | `claude auto-mode critique` | [src/main.tsx:4390](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4390) | 启用；[{"predicate": "feature(\"TRANSCRIPT_CLASSIFIER\")", "value": true, "branch": "then"}, {"predicate": "getAutoModeEnabledStateIfCached() !== \"disabled\"", "value": null, "branch": "then"}] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C036 | `claude remote-control`；`rc` | [src/main.tsx:4409](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4409) | 未启用；[{"predicate": "feature(\"BRIDGE_MODE\")", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C037 | `claude assistant [sessionId]` | [src/main.tsx:4421](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4421) | 未启用；[{"predicate": "feature(\"KAIROS\")", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C038 | `claude doctor` | [src/main.tsx:4432](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4432) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C039 | `claude update`；`upgrade` | [src/main.tsx:4448](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4448) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C040 | `claude up` | [src/main.tsx:4457](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4457) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C041 | `claude rollback [target]` | [src/main.tsx:4468](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4468) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C042 | `claude install [target]` | [src/main.tsx:4481](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4481) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C043 | `claude log` | [src/main.tsx:4498](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4498) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C044 | `claude error` | [src/main.tsx:4506](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4506) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C045 | `claude export` | [src/main.tsx:4514](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4514) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C046 | `claude task` | [src/main.tsx:4526](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4526) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}, {"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C047 | `claude task create <subject>` | [src/main.tsx:4527](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4527) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}, {"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C048 | `claude task list` | [src/main.tsx:4536](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4536) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}, {"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C049 | `claude task get <id>` | [src/main.tsx:4546](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4546) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}, {"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C050 | `claude task update <id>` | [src/main.tsx:4554](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4554) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}, {"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C051 | `claude task dir` | [src/main.tsx:4567](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4567) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}, {"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C052 | `claude completion <shell>` | [src/main.tsx:4578](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/main.tsx:4578) | 未启用；[{"predicate": "inventoryUserType === \"ant\"", "value": false, "branch": "then"}] | 条件/其他 profile；核定原发行可达性并迁移 |
| C053 | `claude mcp add <name> <commandOrUrl> [args...]` | [src/commands/mcp/addCommand.ts:34](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/mcp/addCommand.ts:34) | 启用；[] | 普通非根分派仍未迁移（父分组沿子命令完成） |
| C054 | `claude mcp xaa` | [src/commands/mcp/xaaIdpCommand.ts:25](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/mcp/xaaIdpCommand.ts:25) | 启用；[] | 条件/其他 profile；核定原发行可达性并迁移 |
| C055 | `claude mcp xaa setup` | [src/commands/mcp/xaaIdpCommand.ts:29](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/mcp/xaaIdpCommand.ts:29) | 启用；[] | 条件/其他 profile；核定原发行可达性并迁移 |
| C056 | `claude mcp xaa login` | [src/commands/mcp/xaaIdpCommand.ts:150](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/mcp/xaaIdpCommand.ts:150) | 启用；[] | 条件/其他 profile；核定原发行可达性并迁移 |
| C057 | `claude mcp xaa show` | [src/commands/mcp/xaaIdpCommand.ts:219](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/mcp/xaaIdpCommand.ts:219) | 启用；[] | 条件/其他 profile；核定原发行可达性并迁移 |
| C058 | `claude mcp xaa clear` | [src/commands/mcp/xaaIdpCommand.ts:243](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/mcp/xaaIdpCommand.ts:243) | 启用；[] | 条件/其他 profile；核定原发行可达性并迁移 |

## 4. 原普通根参数逐项清单

81 行原参数；60 行在当前 root 业务解析器明确不接纳。接纳参数仍受默认工具/宿主/业务分支约束，不能算参数验收通过。version 的单参数 fast route 另行保留。

| ID | 原参数（保留别名） | 属性 | 当前工作 |
| --- | --- | --- | --- |
| O001 | `--add-dir <directories...>` | `addDir` | root 显式拒绝；需补实际业务消费者 |
| O002 | `--agent <agent>` | `agent` | root 显式拒绝；需补实际业务消费者 |
| O003 | `--agent-color <color>` | `agentColor` | root 显式拒绝；需补实际业务消费者 |
| O004 | `--agent-id <id>` | `agentId` | root 显式拒绝；需补实际业务消费者 |
| O005 | `--agent-name <name>` | `agentName` | root 显式拒绝；需补实际业务消费者 |
| O006 | `--agent-type <type>` | `agentType` | root 显式拒绝；需补实际业务消费者 |
| O007 | `--agents <json>` | `agents` | root 显式拒绝；需补实际业务消费者 |
| O008 | `--allow-dangerously-skip-permissions` | `allowDangerouslySkipPermissions` | root 显式拒绝；需补实际业务消费者 |
| O009 | `--allowedTools, --allowed-tools <tools...>` | `allowedTools` | root 接纳；业务/组合/恢复仍需验收 |
| O010 | `--append-system-prompt <prompt>` | `appendSystemPrompt` | root 显式拒绝；需补实际业务消费者 |
| O011 | `--append-system-prompt-file <file>` | `appendSystemPromptFile` | root 显式拒绝；需补实际业务消费者 |
| O012 | `--bare` | `bare` | root 显式拒绝；需补实际业务消费者 |
| O013 | `--betas <betas...>` | `betas` | root 显式拒绝；需补实际业务消费者 |
| O014 | `--chrome` | `chrome` | root 显式拒绝；需补实际业务消费者 |
| O015 | `--dangerously-skip-permissions` | `dangerouslySkipPermissions` | root 接纳；业务/组合/恢复仍需验收 |
| O016 | `--debug-file <path>` | `debugFile` | root 显式拒绝；需补实际业务消费者 |
| O017 | `--debug-to-stderr` | `debugToStderr` | root 显式拒绝；需补实际业务消费者 |
| O018 | `--deep-link-last-fetch <ms>` | `deepLinkLastFetch` | root 显式拒绝；需补实际业务消费者 |
| O019 | `--deep-link-origin` | `deepLinkOrigin` | root 显式拒绝；需补实际业务消费者 |
| O020 | `--deep-link-repo <slug>` | `deepLinkRepo` | root 显式拒绝；需补实际业务消费者 |
| O021 | `--disable-slash-commands` | `disableSlashCommands` | root 显式拒绝；需补实际业务消费者 |
| O022 | `--disallowedTools, --disallowed-tools <tools...>` | `disallowedTools` | root 显式拒绝；需补实际业务消费者 |
| O023 | `--effort <level>` | `effort` | root 显式拒绝；需补实际业务消费者 |
| O024 | `--enable-auth-status` | `enableAuthStatus` | root 显式拒绝；需补实际业务消费者 |
| O025 | `--enable-auto-mode` | `enableAutoMode` | root 显式拒绝；需补实际业务消费者 |
| O026 | `--fallback-model <model>` | `fallbackModel` | root 显式拒绝；需补实际业务消费者 |
| O027 | `--file <specs...>` | `file` | root 显式拒绝；需补实际业务消费者 |
| O028 | `--fork-session` | `forkSession` | root 接纳；业务/组合/恢复仍需验收 |
| O029 | `--from-pr [value]` | `fromPr` | root 显式拒绝；需补实际业务消费者 |
| O030 | `--ide` | `ide` | root 显式拒绝；需补实际业务消费者 |
| O031 | `--include-hook-events` | `includeHookEvents` | root 显式拒绝；需补实际业务消费者 |
| O032 | `--include-partial-messages` | `includePartialMessages` | root 接纳；业务/组合/恢复仍需验收 |
| O033 | `--init` | `init` | root 显式拒绝；需补实际业务消费者 |
| O034 | `--init-only` | `initOnly` | root 显式拒绝；需补实际业务消费者 |
| O035 | `--input-format <format>` | `inputFormat` | root 接纳；业务/组合/恢复仍需验收 |
| O036 | `--json-schema <schema>` | `jsonSchema` | root 显式拒绝；需补实际业务消费者 |
| O037 | `--maintenance` | `maintenance` | root 显式拒绝；需补实际业务消费者 |
| O038 | `--max-budget-usd <amount>` | `maxBudgetUsd` | root 显式拒绝；需补实际业务消费者 |
| O039 | `--max-thinking-tokens <tokens>` | `maxThinkingTokens` | root 接纳；业务/组合/恢复仍需验收 |
| O040 | `--max-turns <turns>` | `maxTurns` | root 接纳；业务/组合/恢复仍需验收 |
| O041 | `--mcp-config <configs...>` | `mcpConfig` | root 显式拒绝；需补实际业务消费者 |
| O042 | `--mcp-debug` | `mcpDebug` | root 显式拒绝；需补实际业务消费者 |
| O043 | `--model <model>` | `model` | root 接纳；业务/组合/恢复仍需验收 |
| O044 | `--no-chrome` | `chrome` | root 显式拒绝；需补实际业务消费者 |
| O045 | `--no-computer-use` | `computerUse` | root 显式拒绝；需补实际业务消费者 |
| O046 | `--no-session-persistence` | `sessionPersistence` | root 显式拒绝；需补实际业务消费者 |
| O047 | `--output-format <format>` | `outputFormat` | root 接纳；业务/组合/恢复仍需验收 |
| O048 | `--parent-session-id <id>` | `parentSessionId` | root 显式拒绝；需补实际业务消费者 |
| O049 | `--permission-mode <mode>` | `permissionMode` | root 接纳；业务/组合/恢复仍需验收 |
| O050 | `--permission-prompt-tool <tool>` | `permissionPromptTool` | root 显式拒绝；需补实际业务消费者 |
| O051 | `--plan-mode-required` | `planModeRequired` | root 显式拒绝；需补实际业务消费者 |
| O052 | `--plugin-dir <path>` | `pluginDir` | root 显式拒绝；需补实际业务消费者 |
| O053 | `--prefill <text>` | `prefill` | root 显式拒绝；需补实际业务消费者 |
| O054 | `--remote [description]` | `remote` | root 显式拒绝；需补实际业务消费者 |
| O055 | `--replay-user-messages` | `replayUserMessages` | root 接纳；业务/组合/恢复仍需验收 |
| O056 | `--resume-session-at <message id>` | `resumeSessionAt` | root 显式拒绝；需补实际业务消费者 |
| O057 | `--rewind-files <user-message-id>` | `rewindFiles` | root 显式拒绝；需补实际业务消费者 |
| O058 | `--sdk-url <url>` | `sdkUrl` | root 显式拒绝；需补实际业务消费者 |
| O059 | `--session-id <uuid>` | `sessionId` | root 接纳；业务/组合/恢复仍需验收 |
| O060 | `--setting-sources <sources>` | `settingSources` | root 接纳；业务/组合/恢复仍需验收 |
| O061 | `--settings <file-or-json>` | `settings` | root 接纳；业务/组合/恢复仍需验收 |
| O062 | `--strict-mcp-config` | `strictMcpConfig` | root 显式拒绝；需补实际业务消费者 |
| O063 | `--system-prompt <prompt>` | `systemPrompt` | root 接纳；业务/组合/恢复仍需验收 |
| O064 | `--system-prompt-file <file>` | `systemPromptFile` | root 显式拒绝；需补实际业务消费者 |
| O065 | `--task-budget <tokens>` | `taskBudget` | root 显式拒绝；需补实际业务消费者 |
| O066 | `--team-name <name>` | `teamName` | root 显式拒绝；需补实际业务消费者 |
| O067 | `--teammate-mode <mode>` | `teammateMode` | root 显式拒绝；需补实际业务消费者 |
| O068 | `--teleport [session]` | `teleport` | root 显式拒绝；需补实际业务消费者 |
| O069 | `--thinking <mode>` | `thinking` | root 接纳；业务/组合/恢复仍需验收 |
| O070 | `--tmux` | `tmux` | root 显式拒绝；需补实际业务消费者 |
| O071 | `--tools <tools...>` | `tools` | root 接纳；业务/组合/恢复仍需验收 |
| O072 | `--verbose` | `verbose` | root 接纳；业务/组合/恢复仍需验收 |
| O073 | `--workload <tag>` | `workload` | root 显式拒绝；需补实际业务消费者 |
| O074 | `--worktree-base-ref <ref>` | `worktreeBaseRef` | root 显式拒绝；需补实际业务消费者 |
| O075 | `-c, --continue` | `continue` | root 显式拒绝；需补实际业务消费者 |
| O076 | `-d, --debug [filter]` | `debug` | root 显式拒绝；需补实际业务消费者 |
| O077 | `-n, --name <name>` | `name` | root 显式拒绝；需补实际业务消费者 |
| O078 | `-p, --print` | `print` | root 接纳；业务/组合/恢复仍需验收 |
| O079 | `-r, --resume [value]` | `resume` | root 接纳；业务/组合/恢复仍需验收 |
| O080 | `-v, --version` | `version` | 单参数 fast route 存在；混合调用按原语义 |
| O081 | `-w, --worktree [name]` | `worktree` | root 显式拒绝；需补实际业务消费者 |

专属 subcommand 参数、bare/print/XAA profile 的差异，以及静态声明中未被有限 profile 展开的参数，见 [scripts/quality-gate/go-parity/cli-inventory.fixture.json:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/scripts/quality-gate/go-parity/cli-inventory.fixture.json:1)、[backend/internal/cliargs/spec.json:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/cliargs/spec.json:1) 和 [.agents/plans/go-cli-capability-catalog.json:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-cli-capability-catalog.json:1) 的 argv/options。C-W02/C-W03 要逐项接通这些消费者，不能只覆盖本表根参数。

## 5. 原 Slash 定义及 bundled 命令

保留 116 个 external/internal 定义记录，有限 Source 运行观察到 61 个名称。名称是运行时 profile 的结果，动态目录还可以新增名称。

| ID | 定义/候选名称 | 原文件 | 来源/构建条件 | 要做的工作 |
| --- | --- | --- | --- | --- |
| S001 | `addDir`；`add-dir` | [src/commands/add-dir/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/add-dir/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S002 | `advisor`；`advisor` | [src/commands/advisor.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/advisor.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S003 | `agent`；`agent` | [src/commands/agent.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/agent.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S004 | `agents`；`agents` | [src/commands/agents/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/agents/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S005 | `branch`；`branch` | [src/commands/branch/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/branch/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S006 | `btw`；`btw` | [src/commands/btw/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/btw/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S007 | `chrome`；`chrome` | [src/commands/chrome/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/chrome/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S008 | `clear`；`clear` | [src/commands/clear/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/clear/index.ts:1) | external；`无静态 build gate` | 有元数据；main Slash handler 仍拒绝 |
| S009 | `color`；`color` | [src/commands/color/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/color/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S010 | `compact`；`compact` | [src/commands/compact/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/compact/index.ts:1) | external；`无静态 build gate` | 有元数据；main Slash handler 仍拒绝 |
| S011 | `config`；`config` | [src/commands/config/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/config/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S012 | `copy`；`copy` | [src/commands/copy/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/copy/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S013 | `desktop`；`desktop` | [src/commands/desktop/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/desktop/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S014 | `context`；`context` | [src/commands/context/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/context/index.ts:1) | external；`无静态 build gate` | 有元数据；main Slash handler 仍拒绝 |
| S015 | `contextNonInteractive`；`context` | [src/commands/context/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/context/index.ts:1) | external；`无静态 build gate` | 有元数据；main Slash handler 仍拒绝 |
| S016 | `cost`；`cost` | [src/commands/cost/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/cost/index.ts:1) | external；`无静态 build gate` | cost 有限业务存在；其余账号/作用域待验收 |
| S017 | `diff`；`diff` | [src/commands/diff/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/diff/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S018 | `doctor`；`doctor` | [src/commands/doctor/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/doctor/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S019 | `effort`；`effort` | [src/commands/effort/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/effort/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S020 | `exit`；`exit` | [src/commands/exit/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/exit/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S021 | `fast`；`fast` | [src/commands/fast/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/fast/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S022 | `files`；`files` | [src/commands/files/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/files/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S023 | `heapDump`；`heapdump` | [src/commands/heapdump/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/heapdump/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S024 | `help`；`help` | [src/commands/help/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/help/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S025 | `ide`；`ide` | [src/commands/ide/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/ide/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S026 | `init`；`init` | [src/commands/init.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/init.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S027 | `keybindings`；`keybindings` | [src/commands/keybindings/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/keybindings/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S028 | `installGitHubApp`；`install-github-app` | [src/commands/install-github-app/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/install-github-app/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S029 | `installSlackApp`；`install-slack-app` | [src/commands/install-slack-app/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/install-slack-app/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S030 | `mcp`；`mcp` | [src/commands/mcp/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/mcp/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S031 | `memory`；`memory` | [src/commands/memory/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/memory/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S032 | `mobile`；`mobile` | [src/commands/mobile/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/mobile/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S033 | `model`；`model` | [src/commands/model/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/model/index.ts:1) | external；`无静态 build gate` | TUI 无参数 model picker 存在；参数/完整 handler 待完成 |
| S034 | `outputStyle`；`output-style` | [src/commands/output-style/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/output-style/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S035 | `remoteEnv`；`remote-env` | [src/commands/remote-env/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/remote-env/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S036 | `plugin`；`plugin` | [src/commands/plugin/index.tsx:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/plugin/index.tsx:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S037 | `pr_comments`；`pr-comments` | [src/commands/pr_comments/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/pr_comments/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S038 | `releaseNotes`；`release-notes` | [src/commands/release-notes/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/release-notes/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S039 | `reloadPlugins`；`reload-plugins` | [src/commands/reload-plugins/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/reload-plugins/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S040 | `rename`；`rename` | [src/commands/rename/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/rename/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S041 | `resume`；`resume` | [src/commands/resume/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/resume/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S042 | `session`；`session` | [src/commands/session/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/session/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S043 | `skills`；`skills` | [src/commands/skills/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/skills/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S044 | `stats`；`stats` | [src/commands/stats/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/stats/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S045 | `status`；`status` | [src/commands/status/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/status/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S046 | `statusline`；`statusline` | [src/commands/statusline.tsx:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/statusline.tsx:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S047 | `stickers`；`stickers` | [src/commands/stickers/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/stickers/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S048 | `tag`；`tag` | [src/commands/tag/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/tag/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S049 | `team`；`team` | [src/commands/team.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/team.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S050 | `theme`；`theme` | [src/commands/theme/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/theme/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S051 | `feedback`；`feedback` | [src/commands/feedback/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/feedback/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S052 | `goal`；`goal` | [src/commands/goal/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/goal/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S053 | `review`；`review` | [src/commands/review.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/review.ts:1) | external；`无静态 build gate` | 有元数据；main Slash handler 仍拒绝 |
| S054 | `ultrareview`；`ultrareview` | [src/commands/review.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/review.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S055 | `rewind`；`rewind` | [src/commands/rewind/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/rewind/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S056 | `securityReview`；`security-review` | [src/commands/security-review.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/security-review.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S057 | `terminalSetup`；`terminal-setup` | [src/commands/terminalSetup/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/terminalSetup/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S058 | `upgrade`；`upgrade` | [src/commands/upgrade/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/upgrade/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S059 | `extraUsage`；`extra-usage` | [src/commands/extra-usage/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/extra-usage/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S060 | `extraUsageNonInteractive`；`extra-usage` | [src/commands/extra-usage/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/extra-usage/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S061 | `rateLimitOptions`；`rate-limit-options` | [src/commands/rate-limit-options/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/rate-limit-options/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S062 | `usage`；`usage` | [src/commands/usage/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/usage/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S063 | `usageReport`；`insights` | [src/commands.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S064 | `vim`；`vim` | [src/commands/vim/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/vim/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S065 | `webCmd`；`web-setup` | [src/commands/remote-setup/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/remote-setup/index.ts:1) | external；`feature("CCR_REMOTE_SETUP")` | 冻结特性未启用；核定原发行可达性 |
| S066 | `forkCmd`；`(动态/未解析名称)` | [src/commands/fork/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/fork/index.ts:1) | external；`feature("FORK_SUBAGENT")` | 冻结特性未启用；核定原发行可达性 |
| S067 | `buddy`；`buddy` | [src/commands/buddy/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/buddy/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S068 | `proactive`；`(动态/未解析名称)` | [src/commands/proactive.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/proactive.ts:1) | external；`feature("PROACTIVE") \|\| feature("KAIROS")` | 冻结特性未启用；核定原发行可达性 |
| S069 | `briefCommand`；`brief` | [src/commands/brief.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/brief.ts:1) | external；`feature("KAIROS") \|\| feature("KAIROS_BRIEF")` | 冻结特性未启用；核定原发行可达性 |
| S070 | `assistantCommand`；`(动态/未解析名称)` | [src/commands/assistant/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/assistant/index.ts:1) | external；`feature("KAIROS")` | 冻结特性未启用；核定原发行可达性 |
| S071 | `bridge`；`remote-control` | [src/commands/bridge/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/bridge/index.ts:1) | external；`feature("BRIDGE_MODE")` | 冻结特性未启用；核定原发行可达性 |
| S072 | `remoteControlServerCommand`；`(动态/未解析名称)` | [src/commands/remoteControlServer/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/remoteControlServer/index.ts:1) | external；`feature("DAEMON") && feature("BRIDGE_MODE")` | 冻结特性未启用；核定原发行可达性 |
| S073 | `voiceCommand`；`voice` | [src/commands/voice/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/voice/index.ts:1) | external；`feature("VOICE_MODE")` | 冻结特性未启用；核定原发行可达性 |
| S074 | `thinkback`；`think-back` | [src/commands/thinkback/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/thinkback/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S075 | `thinkbackPlay`；`thinkback-play` | [src/commands/thinkback-play/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/thinkback-play/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S076 | `permissions`；`permissions` | [src/commands/permissions/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/permissions/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S077 | `plan`；`plan` | [src/commands/plan/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/plan/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S078 | `privacySettings`；`privacy-settings` | [src/commands/privacy-settings/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/privacy-settings/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S079 | `hooks`；`hooks` | [src/commands/hooks/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/hooks/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S080 | `exportCommand`；`export` | [src/commands/export/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/export/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S081 | `sandboxToggle`；`sandbox` | [src/commands/sandbox-toggle/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/sandbox-toggle/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S082 | `logout`；`logout` | [src/commands/logout/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/logout/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S083 | `login`；`login` | [src/commands/login/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/login/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S084 | `passes`；`passes` | [src/commands/passes/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/passes/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S085 | `peersCmd`；`(动态/未解析名称)` | [src/commands/peers/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/peers/index.ts:1) | external；`feature("UDS_INBOX")` | 冻结特性未启用；核定原发行可达性 |
| S086 | `tasks`；`tasks` | [src/commands/tasks/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/tasks/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S087 | `workflowsCmd`；`workflows` | [src/commands/workflows/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/workflows/index.ts:1) | external；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S088 | `torch`；`(动态/未解析名称)` | [src/commands/torch.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/torch.ts:1) | external；`feature("TORCH")` | 冻结特性未启用；核定原发行可达性 |
| S089 | `backfillSessions`；`stub` | [src/commands/backfill-sessions/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/backfill-sessions/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S090 | `breakCache`；`stub` | [src/commands/break-cache/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/break-cache/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S091 | `bughunter`；`stub` | [src/commands/bughunter/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/bughunter/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S092 | `commit`；`commit` | [src/commands/commit.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/commit.ts:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S093 | `commitPushPr`；`commit-push-pr` | [src/commands/commit-push-pr.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/commit-push-pr.ts:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S094 | `ctx_viz`；`stub` | [src/commands/ctx_viz/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/ctx_viz/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S095 | `goodClaude`；`stub` | [src/commands/good-claude/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/good-claude/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S096 | `issue`；`stub` | [src/commands/issue/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/issue/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S097 | `initVerifiers`；`init-verifiers` | [src/commands/init-verifiers.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/init-verifiers.ts:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S098 | `forceSnip`；`(动态/未解析名称)` | [src/commands/force-snip.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/force-snip.ts:1) | internal；`feature("HISTORY_SNIP")` | 冻结特性未启用；核定原发行可达性 |
| S099 | `mockLimits`；`stub` | [src/commands/mock-limits/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/mock-limits/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S100 | `bridgeKick`；`bridge-kick` | [src/commands/bridge-kick.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/bridge-kick.ts:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S101 | `version`；`version` | [src/commands/version.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/version.ts:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S102 | `ultraplan`；`ultraplan` | [src/commands/ultraplan.tsx:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/ultraplan.tsx:1) | internal；`feature("ULTRAPLAN")` | 冻结特性未启用；核定原发行可达性 |
| S103 | `subscribePr`；`(动态/未解析名称)` | [src/commands/subscribe-pr.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/subscribe-pr.ts:1) | internal；`feature("KAIROS_GITHUB_WEBHOOKS")` | 冻结特性未启用；核定原发行可达性 |
| S104 | `resetLimits`；`stub` | [src/commands/reset-limits/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/reset-limits/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S105 | `resetLimitsNonInteractive`；`stub` | [src/commands/reset-limits/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/reset-limits/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S106 | `onboarding`；`stub` | [src/commands/onboarding/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/onboarding/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S107 | `share`；`stub` | [src/commands/share/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/share/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S108 | `summary`；`stub` | [src/commands/summary/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/summary/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S109 | `teleport`；`stub` | [src/commands/teleport/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/teleport/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S110 | `antTrace`；`stub` | [src/commands/ant-trace/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/ant-trace/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S111 | `perfIssue`；`stub` | [src/commands/perf-issue/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/perf-issue/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S112 | `env`；`stub` | [src/commands/env/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/env/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S113 | `oauthRefresh`；`stub` | [src/commands/oauth-refresh/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/oauth-refresh/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S114 | `debugToolCall`；`stub` | [src/commands/debug-tool-call/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/debug-tool-call/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S115 | `agentsPlatform`；`(动态/未解析名称)` | [src/commands/agents-platform/index.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/agents-platform/index.ts:1) | internal；`process.env.USER_TYPE === "ant"` | 按原发行条件完成 handler、accessor 和实际副作用 |
| S116 | `autofixPr`；`stub` | [src/commands/autofix-pr/index.js:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/commands/autofix-pr/index.js:1) | internal；`无静态 build gate` | 按原发行条件完成 handler、accessor 和实际副作用 |

### Bundled 定义

19 个模块保留原注册条件、model/user invocation 条件和别名；不把 bundled 文本存在视作运行时已加载。

| 模块 | 定义名称 | 原注册/发行工作 |
| --- | --- | --- |
| [src/skills/bundled/remember.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/remember.ts:1) | `remember` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/loremIpsum.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/loremIpsum.ts:1) | `lorem-ipsum` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/verify.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/verify.ts:1) | `verify` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/loop.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/loop.ts:1) | `loop` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/claudeApi.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/claudeApi.ts:1) | `claude-api` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/debug.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/debug.ts:1) | `debug` | registered-current-profile；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/stuck.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/stuck.ts:1) | `stuck` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/scheduleRemoteAgents.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/scheduleRemoteAgents.ts:1) | `schedule` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/computerUse.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/computerUse.ts:1) | `computer-use` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/dream.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/dream.ts:1) | `` | generated-feature-placeholder；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/batch.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/batch.ts:1) | `batch` | registered-current-profile；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/updateConfig.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/updateConfig.ts:1) | `update-config` | registered-current-profile；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/hunter.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/hunter.ts:1) | `` | generated-feature-placeholder；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/keybindings.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/keybindings.ts:1) | `keybindings-help` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/imagegen.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/imagegen.ts:1) | `imagegen` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/claudeInChrome.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/claudeInChrome.ts:1) | `claude-in-chrome` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/skillify.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/skillify.ts:1) | `skillify` | feature/runtime/ANT conditional；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/runSkillGenerator.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/runSkillGenerator.ts:1) | `` | generated-feature-placeholder；接入生产 registry/上下文及实际 handler |
| [src/skills/bundled/simplify.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/simplify.ts:1) | `simplify` | registered-current-profile；接入生产 registry/上下文及实际 handler |

## 6. Bootstrap / worker 分支

冻结特性未启用的分支保留条件；如果在原其他发行 profile 可达，必须纳入该 profile 的迁移。版本入口已有代码，其他分支不能用分类器或帮助输出替代执行。

| ID | 原 tokens/分支 | 原入口 | 冻结构建 | 当前工作 |
| --- | --- | --- | --- | --- |
| B001 | `--computer-use-repl-worker` | [src/entrypoints/cli.tsx:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:37) | 启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B002 | `--version, -V, -v` | [src/entrypoints/cli.tsx:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:31) | 启用 | 版本单参数入口存在 |
| B003 | `--dump-system-prompt` | [src/entrypoints/cli.tsx:57](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:57) | 未启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B004 | `--claude-in-chrome-mcp` | [src/entrypoints/cli.tsx:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:79) | 启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B005 | `--chrome-native-host` | [src/entrypoints/cli.tsx:86](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:86) | 启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B006 | `--computer-use-mcp` | [src/entrypoints/cli.tsx:93](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:93) | 启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B007 | `--daemon-worker` | [src/entrypoints/cli.tsx:102](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:102) | 未启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B008 | `bridge, rc, remote, remote-control, sync` | [src/entrypoints/cli.tsx:119](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:119) | 未启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B009 | `daemon` | [src/entrypoints/cli.tsx:102](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:102) | 未启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B010 | `--background, --bg, attach, kill, logs, ps` | [src/entrypoints/cli.tsx:189](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:189) | 未启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B011 | `list, new, reply` | [src/entrypoints/cli.tsx:219](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:219) | 未启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B012 | `environment-runner` | [src/entrypoints/cli.tsx:231](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:231) | 未启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B013 | `self-hosted-runner` | [src/entrypoints/cli.tsx:242](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:242) | 未启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B014 | `--tmux, --tmux=classic, --worktree, --worktree=, -w` | [src/entrypoints/cli.tsx:254](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:254) | 启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B015 | `--update, --upgrade` | [src/entrypoints/cli.tsx:284](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:284) | 启用 | 按原发行条件接通 worker/native/runtime 与生命周期 |
| B016 | `--bare` | [src/entrypoints/cli.tsx:288](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:288) | 启用 | bare 普通 root option 被拒绝 |

Recovery 是独立入口，见 C-W11；bootstrap 表不代替它的验收。

## 7. TUI 原动作逐项工作

Source 静态 catalog 保留 98 个字面量动作，其中 87 个在公开 schema；包含运行时追加的滚动/选择动作。条件保留在 JSON 的 bindings/handlerReferences。

Go 路由存在只表示动作能进入现有处理，原状态/表单/语义仍需核对。当前 host 对多数其他动作没有实现；键位声明本身不能关闭业务。

| ID | 动作 | 原公开 schema | 当前工作 |
| --- | --- | --- | --- |
| K001 | `app:exit` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K002 | `app:globalSearch` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K003 | `app:interrupt` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K004 | `app:quickOpen` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K005 | `app:redraw` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K006 | `app:toggleBrief` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K007 | `app:toggleTeammatePreview` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K008 | `app:toggleTerminal` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K009 | `app:toggleTodos` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K010 | `app:toggleTranscript` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K011 | `attachments:exit` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K012 | `attachments:next` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K013 | `attachments:previous` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K014 | `attachments:remove` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K015 | `autocomplete:accept` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K016 | `autocomplete:dismiss` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K017 | `autocomplete:next` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K018 | `autocomplete:previous` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K019 | `chat:cancel` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K020 | `chat:cycleMode` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K021 | `chat:externalEditor` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K022 | `chat:fastMode` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K023 | `chat:imagePaste` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K024 | `chat:killAgents` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K025 | `chat:messageActions` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K026 | `chat:modelPicker` | 是 | 模型 Host 分支已有；完整参数/状态待验收 |
| K027 | `chat:newline` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K028 | `chat:stash` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K029 | `chat:submit` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K030 | `chat:thinkingToggle` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K031 | `chat:undo` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K032 | `chat:workflowKeywordToggle` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K033 | `confirm:cycleMode` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K034 | `confirm:next` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K035 | `confirm:nextField` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K036 | `confirm:no` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K037 | `confirm:previous` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K038 | `confirm:previousField` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K039 | `confirm:toggle` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K040 | `confirm:toggleExplanation` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K041 | `confirm:yes` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K042 | `diff:back` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K043 | `diff:dismiss` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K044 | `diff:nextFile` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K045 | `diff:nextSource` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K046 | `diff:previousFile` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K047 | `diff:previousSource` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K048 | `diff:viewDetails` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K049 | `footer:clearSelection` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K050 | `footer:close` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K051 | `footer:down` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K052 | `footer:next` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K053 | `footer:openSelected` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K054 | `footer:previous` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K055 | `footer:up` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K056 | `help:dismiss` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K057 | `history:next` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K058 | `history:previous` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K059 | `history:search` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K060 | `historySearch:accept` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K061 | `historySearch:cancel` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K062 | `historySearch:execute` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K063 | `historySearch:next` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K064 | `messageSelector:bottom` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K065 | `messageSelector:down` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K066 | `messageSelector:select` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K067 | `messageSelector:top` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K068 | `messageSelector:up` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K069 | `modelPicker:decreaseEffort` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K070 | `modelPicker:increaseEffort` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K071 | `permission:toggleDebug` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K072 | `plugin:install` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K073 | `plugin:toggle` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K074 | `scroll:bottom` | 运行时 handler | 终端路由已有；原状态转换/场景待验收 |
| K075 | `scroll:fullPageDown` | 运行时 handler | 当前无对应生产 Host action；需实现原消费者 |
| K076 | `scroll:fullPageUp` | 运行时 handler | 当前无对应生产 Host action；需实现原消费者 |
| K077 | `scroll:halfPageDown` | 运行时 handler | 当前无对应生产 Host action；需实现原消费者 |
| K078 | `scroll:halfPageUp` | 运行时 handler | 当前无对应生产 Host action；需实现原消费者 |
| K079 | `scroll:lineDown` | 运行时 handler | 终端路由已有；原状态转换/场景待验收 |
| K080 | `scroll:lineUp` | 运行时 handler | 终端路由已有；原状态转换/场景待验收 |
| K081 | `scroll:pageDown` | 运行时 handler | 终端路由已有；原状态转换/场景待验收 |
| K082 | `scroll:pageUp` | 运行时 handler | 终端路由已有；原状态转换/场景待验收 |
| K083 | `scroll:top` | 运行时 handler | 终端路由已有；原状态转换/场景待验收 |
| K084 | `select:accept` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K085 | `select:cancel` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K086 | `select:next` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K087 | `select:previous` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K088 | `selection:copy` | 运行时 handler | 当前无对应生产 Host action；需实现原消费者 |
| K089 | `settings:close` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K090 | `settings:retry` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K091 | `settings:search` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K092 | `tabs:next` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K093 | `tabs:previous` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K094 | `task:background` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K095 | `theme:toggleSyntaxHighlighting` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K096 | `transcript:exit` | 是 | 终端路由已有；原状态转换/场景待验收 |
| K097 | `transcript:toggleShowAll` | 是 | 当前无对应生产 Host action；需实现原消费者 |
| K098 | `voice:pushToTalk` | 是 | 当前无对应生产 Host action；需实现原消费者 |

原上下文：`Global`, `Chat`, `Autocomplete`, `Confirmation`, `Help`, `Transcript`, `HistorySearch`, `Task`, `ThemePicker`, `Settings`, `Tabs`, `Attachments`, `Footer`, `MessageSelector`, `DiffDialog`, `ModelPicker`, `Select`, `Plugin`。原静态 catalog 有 7 个动态 handler table 需要沿表达式逐项补齐；保留在 JSON，不能视作遗漏已不存在。

## 8. SDK/stdio discriminants 逐项工作

43 个 schema/实际 dispatch 判别名；包括请求、server callback 和 response，不能全部称作入站 request。schema/handler 原位置、方向及未匹配分支均保留在 JSON。已存在的有限 Go 控制代码保留，逐项核对完整 native 生产者和消费者后关闭。

| ID | 判别名 | 原入口 | 剩余工作 |
| --- | --- | --- | --- |
| Q001 | `apply_flag_settings` | [src/entrypoints/sdk/controlSchemas.ts:559](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:559) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q002 | `can_use_tool` | [src/entrypoints/sdk/controlSchemas.ts:110](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:110) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q003 | `cancel_async_message` | [src/entrypoints/sdk/controlSchemas.ts:398](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:398) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q004 | `cancel_side_question` | [src/entrypoints/sdk/controlSchemas.ts:553](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:553) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q005 | `channel_enable` | [src/cli/print.ts:3502](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:3502) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q006 | `claude_authenticate` | [src/cli/print.ts:3736](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:3736) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q007 | `claude_oauth_callback` | [src/cli/print.ts:3831](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:3831) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q008 | `claude_oauth_wait_for_completion` | [src/cli/print.ts:3832](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:3832) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q009 | `elicitation` | [src/entrypoints/sdk/controlSchemas.ts:617](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:617) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q010 | `end_session` | [src/cli/print.ts:2959](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:2959) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q011 | `enqueue_session_message` | [src/cli/print.ts:2918](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:2918) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q012 | `error` | [src/entrypoints/sdk/controlSchemas.ts:696](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:696) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q013 | `generate_session_title` | [src/cli/print.ts:4068](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:4068) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q014 | `get_context_usage` | [src/entrypoints/sdk/controlSchemas.ts:200](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:200) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q015 | `get_session_usage` | [src/entrypoints/sdk/controlSchemas.ts:211](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:211) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q016 | `get_settings` | [src/entrypoints/sdk/controlSchemas.ts:570](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:570) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q017 | `hook_callback` | [src/entrypoints/sdk/controlSchemas.ts:431](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:431) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q018 | `initialize` | [src/entrypoints/sdk/controlSchemas.ts:61](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:61) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q019 | `interrupt` | [src/entrypoints/sdk/controlSchemas.ts:101](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:101) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q020 | `mcp_authenticate` | [src/cli/print.ts:3515](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:3515) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q021 | `mcp_clear_auth` | [src/cli/print.ts:3873](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:3873) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q022 | `mcp_message` | [src/entrypoints/sdk/controlSchemas.ts:442](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:442) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q023 | `mcp_oauth_callback_url` | [src/cli/print.ts:3685](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:3685) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q024 | `mcp_reconnect` | [src/entrypoints/sdk/controlSchemas.ts:503](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:503) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q025 | `mcp_set_servers` | [src/entrypoints/sdk/controlSchemas.ts:452](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:452) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q026 | `mcp_status` | [src/entrypoints/sdk/controlSchemas.ts:182](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:182) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q027 | `mcp_toggle` | [src/entrypoints/sdk/controlSchemas.ts:512](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:512) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q028 | `reload_plugins` | [src/entrypoints/sdk/controlSchemas.ts:473](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:473) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q029 | `remote_control` | [src/cli/print.ts:4163](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts:4163) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q030 | `rewind_files` | [src/entrypoints/sdk/controlSchemas.ts:376](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:376) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q031 | `seed_read_state` | [src/entrypoints/sdk/controlSchemas.ts:419](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:419) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q032 | `send_agent_message` | [src/entrypoints/sdk/controlSchemas.ts:533](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:533) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q033 | `session_message_receipt` | [src/entrypoints/sdk/controlSchemas.ts:745](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:745) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q034 | `set_computer_use_approval_token` | [src/entrypoints/sdk/controlSchemas.ts:136](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:136) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q035 | `set_max_thinking_tokens` | [src/entrypoints/sdk/controlSchemas.ts:171](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:171) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q036 | `set_model` | [src/entrypoints/sdk/controlSchemas.ts:162](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:162) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q037 | `set_permission_mode` | [src/entrypoints/sdk/controlSchemas.ts:149](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:149) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q038 | `set_proactive` | [src/cli/print.ts（原动态位置待核定）](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/cli/print.ts) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q039 | `side_question` | [src/entrypoints/sdk/controlSchemas.ts:542](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:542) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q040 | `stop_task` | [src/entrypoints/sdk/controlSchemas.ts:524](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:524) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q041 | `success` | [src/entrypoints/sdk/controlSchemas.ts:688](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:688) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q042 | `team_plan_pause` | [src/entrypoints/sdk/controlSchemas.ts:647](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:647) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |
| Q043 | `team_runtime_snapshot` | [src/entrypoints/sdk/controlSchemas.ts:646](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/sdk/controlSchemas.ts:646) | 核对/补齐原输入或回调、业务、队列/取消、输出与 finally/恢复 |

## 9. 静态证据及关闭要求

| 当前 Go 证据 | 源码 |
| --- | --- |
| rootEntry | [backend/cmd/claude-haha/main.go:122](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/cmd/claude-haha/main.go:122) |
| nonrootGuard | [backend/cmd/claude-haha/main.go:160](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/cmd/claude-haha/main.go:160) |
| defaultCatalogGuard | [backend/internal/application/command.go:85](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/command.go:85) |
| slashGuard | [backend/internal/sdkhost/process_user_input.go:45](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/sdkhost/process_user_input.go:45) |
| tuiHostActions | [backend/internal/application/actions.go:13](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/actions.go:13) |
| tuiBindings | [backend/internal/application/command.go:162](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/application/command.go:162) |
| readImagePDFGuard | [backend/internal/builtintools/read.go:148](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/builtintools/read.go:148) |
| bashBackgroundGuard | [backend/internal/builtintools/process.go:86](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/builtintools/process.go:86) |
| agentBackgroundGuard | [backend/internal/builtintools/agent.go:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/backend/internal/builtintools/agent.go:31) |

本轮结构化记录：[.agents/plans/go-cli-tui-work-inventory.json:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-cli-tui-work-inventory.json:1)，保存 260 个输入文件哈希。Source 原 catalog 的有限构建条件和未解析项保留；静态扫描不证明运行等价。

主待办：[.agents/plans/go-desktop-parity-todo.md:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:1)。HTTP/WS/native、默认工具原 49 个注册表达式和 Server/application 守卫：[.agents/plans/go-desktop-api-checklist.md:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-api-checklist.md:1)。

每个操作后续关闭时记录原输入/结果/错误/副作用、Go 接线、测试候选、真实产物及恢复结果。没有绑定实际消费者的元数据不关闭任务；旧候选、组件通过、构建通过和真实模型单场景分别保留范围。

## 10. 原发行条件的补充待办

[默认工具、bundled Skill 与 worker 工作矩阵](go-release-condition-work-matrix.md)按仓库原构建配置细分条件，保留全部 49/19/16 项。其范围不代替本索引的 58 条 argv 和 116 个 Slash 定义，也不代表其他历史发行 profile 已核定。尤其要迁移外部用户可达的 Teams/Workflows、恒启用的 Worktree，以及 Windows 无 Git Bash 时的 PowerShell 回退。

另列 `desktop/sidecars/claude-sidecar.ts` 的 `--voice-worker`，它属于桌面独立启动路径，不包含在 CLI 16 个 bootstrap 分支中。必须补正确的 launcher、私有配置/认证、ready、loopback 转写、取消与资源释放，归 D20/D33/D35；不能以 Go 默认 CLI 参数解析代替该路径。
