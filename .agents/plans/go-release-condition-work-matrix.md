# Go 迁移待办：原发行条件与默认能力工作矩阵

本次只补充静态工作拆解，不执行产品复现、模型或功能实现。主范围仍是 D01–D35 与原附件 95 条要求。所有条件项保留，没有据此关闭迁移工作。

## 核对基线

原构建配置取自 Git `6b9d2aec1d9321a4dfff90d16b51f10c1ab984c8`：桌面 `Bun.build` 与原 CLI launcher 均只启用 `TRANSCRIPT_CLASSIFIER`。当前 Go 构建已替换这些入口，因此原条件使用 Git 中的原文件核对，不能从改后的 Go 文件反推原能力。它证明仓库原构建配置，不证明用户已安装的每个历史发行包。其他发行 profile 和平台仍须单独核定。

原条件必须逐层保留：注册条件 → `Tool.isEnabled()` → 宿主/权限/模式过滤 → 实际 registry/MCP → Harness 消费者。base registry 中的名字不等于当前会话可调用工具。`assembleToolPool` 保留 builtin/MCP 两个排序分区，builtin 同名优先。

## 49 个原工具注册表达式的工作归属

本表分类的是原注册/已查明 gate，不冒称已核对每个 Tool 的全部业务。具体工具 schema、权限、Hooks、输出、副作用和恢复仍在 D32 待办。feature 关闭、内部与测试项保留原声明；如果其他原发行 profile 可达，再按该 profile 迁移。

| ID | 原声明 | 原条件分类与要求 | Go 剩余工作 | 依据 |
| --- | --- | --- | --- | --- |
| RT01 | AgentTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 已有部分工具入口：Agent；原缺失分支和完整默认消费者仍待做 | [src/tools.ts:206](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:206) |
| RT02 | TaskOutputTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools.ts:207](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:207) |
| RT03 | ...(isBashToolEnabled() ? [BashTool] : []) | 平台条件：非 Windows 有 Bash；Windows 仅有 Git Bash 时有 Bash | 已有部分工具入口：Bash；原缺失分支和完整默认消费者仍待做 | [src/utils/shell/shellToolUtils.ts:14](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/utils/shell/shellToolUtils.ts:14) |
| RT04 | ...(hasEmbeddedSearchTools() ? [] : [GlobTool, GrepTool]) | 运行条件：EMBEDDED_SEARCH_TOOLS 与 entrypoint 决定是否用嵌入搜索替代 Glob/Grep | 已有部分工具入口：Glob/Grep；原缺失分支和完整默认消费者仍待做 | [src/utils/embeddedTools.ts:16](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/utils/embeddedTools.ts:16) |
| RT05 | ExitPlanModeV2Tool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools.ts:213](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:213) |
| RT06 | FileReadTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 已有部分工具入口：Read；原缺失分支和完整默认消费者仍待做 | [src/tools.ts:214](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:214) |
| RT07 | FileEditTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 已有部分工具入口：Edit；原缺失分支和完整默认消费者仍待做 | [src/tools.ts:215](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:215) |
| RT08 | FileWriteTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 已有部分工具入口：Write；原缺失分支和完整默认消费者仍待做 | [src/tools.ts:216](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:216) |
| RT09 | NotebookEditTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 已有部分工具入口：NotebookEdit；原缺失分支和完整默认消费者仍待做 | [src/tools.ts:217](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:217) |
| RT10 | WebFetchTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools.ts:218](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:218) |
| RT11 | TodoWriteTool | 注册项：TodoWrite 的实际 isEnabled/宿主过滤还须保留；不能与 Task 工具互相替代 | 已有部分工具入口：TodoWrite；原缺失分支和完整默认消费者仍待做 | [src/tools.ts:344](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:344) |
| RT12 | WebSearchTool | 运行条件：模型/设置选择的 WebSearch provider 不为 disabled；原外部 provider/fallback 继续保留 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools/WebSearchTool/backend.ts:105](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools/WebSearchTool/backend.ts:105) |
| RT13 | TaskStopTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools.ts:221](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:221) |
| RT14 | AskUserQuestionTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools.ts:222](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:222) |
| RT15 | SkillTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools.ts:223](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:223) |
| RT16 | EnterPlanModeTool | 注册项：原 base registry 包含该项；仍须移植 Tool.isEnabled、deny rules 与宿主消费者 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools.ts:224](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:224) |
| RT17 | ...(process.env.USER_TYPE === 'ant' ? [ConfigTool] : []) | 内部用户条件：USER_TYPE === ant；默认 external 不注册，保留原条件 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:225](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:225) |
| RT18 | ...(process.env.USER_TYPE === 'ant' ? [TungstenTool] : []) | 内部用户条件：USER_TYPE === ant；默认 external 不注册，保留原条件 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:226](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:226) |
| RT19 | ...(SuggestBackgroundPRTool ? [SuggestBackgroundPRTool] : []) | 内部用户条件：USER_TYPE === ant；默认 external 不注册，保留原条件 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:227](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:227) |
| RT20 | ...(WebBrowserTool ? [WebBrowserTool] : []) | 本基线 feature 关闭：WEB_BROWSER_TOOL；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:123](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:123) |
| RT21 | ...(isTodoV2Enabled()       ? [TaskCreateTool, TaskGetTool, TaskUpdateTool, TaskListTool]       : []) | 宿主条件：交互宿主启用；非交互仅 CLAUDE_CODE_ENABLE_TASKS truthy 时启用 | 已有部分工具入口：TaskCreate/Get/Update/List；原缺失分支和完整默认消费者仍待做 | [src/utils/tasks.ts:201](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/utils/tasks.ts:201) |
| RT22 | ...(OverflowTestTool ? [OverflowTestTool] : []) | 本基线 feature 关闭：OVERFLOW_TEST_TOOL；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:113](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:113) |
| RT23 | ...(CtxInspectTool ? [CtxInspectTool] : []) | 本基线 feature 关闭：CONTEXT_COLLAPSE；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:116](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:116) |
| RT24 | ...(TerminalCaptureTool ? [TerminalCaptureTool] : []) | 本基线 feature 关闭：TERMINAL_PANEL；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:119](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:119) |
| RT25 | ...(isEnvTruthy(process.env.ENABLE_LSP_TOOL) ? [LSPTool] : []) | 运行条件：ENABLE_LSP_TOOL truthy 且至少一个实际 LSP server 健康 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/services/lsp/manager.ts:100](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/services/lsp/manager.ts:100) |
| RT26 | ...(isWorktreeModeEnabled() ? [EnterWorktreeTool, ExitWorktreeTool] : []) | 发行必需：Worktree gate 恒为 true；不是内部用户能力 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/utils/worktreeModeEnabled.ts:9](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/utils/worktreeModeEnabled.ts:9) |
| RT27 | getSendMessageTool() | 运行条件：外部用户 opt-in + killswitch；General 显式关闭优先 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/utils/agentSwarmsEnabled.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/utils/agentSwarmsEnabled.ts:31) |
| RT28 | ...sessionCollaborationTools | 桌面条件：合法 loopback endpoint、独立 token 与 session ID 同时存在 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools/SessionCollaborationTool/bridge.ts:1](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools/SessionCollaborationTool/bridge.ts:1) |
| RT29 | ...(ListPeersTool ? [ListPeersTool] : []) | 本基线 feature 关闭：UDS_INBOX；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:132](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:132) |
| RT30 | ...(isAgentSwarmsEnabled()       ? [getTeamCreateTool(), getTeamPlanTool(), getTeamDeleteTool()]       : []) | 运行条件：外部用户 Teams opt-in + killswitch；与 SendMessage 共用同一 gate | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/utils/agentSwarmsEnabled.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/utils/agentSwarmsEnabled.ts:31) |
| RT31 | ...(VerifyPlanExecutionTool ? [VerifyPlanExecutionTool] : []) | 运行条件：CLAUDE_CODE_VERIFY_PLAN 字面值 true；不是编译 feature gate | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools.ts:106](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:106) |
| RT32 | ...(process.env.USER_TYPE === 'ant' && REPLTool ? [REPLTool] : []) | 内部用户条件：USER_TYPE === ant；默认 external 不注册，保留原条件 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:244](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:244) |
| RT33 | getWorkflowTool() | 运行条件：默认可用；env、managed disableWorkflows、用户 enableWorkflows/disableWorkflows 控制 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/utils/workflows/enabled.ts:47](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/utils/workflows/enabled.ts:47) |
| RT34 | ...(SleepTool ? [SleepTool] : []) | 本基线 feature 关闭：PROACTIVE OR KAIROS；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:28](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:28) |
| RT35 | ...cronTools | 本基线 feature 关闭：AGENT_TRIGGERS；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:31) |
| RT36 | ...(RemoteTriggerTool ? [RemoteTriggerTool] : []) | 本基线 feature 关闭：AGENT_TRIGGERS_REMOTE；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:39](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:39) |
| RT37 | ...(MonitorTool ? [MonitorTool] : []) | 本基线 feature 关闭：MONITOR_TOOL；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:42](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:42) |
| RT38 | BriefTool | 本基线 feature 关闭：KAIROS OR KAIROS_BRIEF；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools/BriefTool/BriefTool.ts:126](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools/BriefTool/BriefTool.ts:126) |
| RT39 | ImageGenTool | 配置条件：实际 image provider runtime config 非空，保留 OAuth/API key 分支 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/services/imageGeneration/config.ts:30](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/services/imageGeneration/config.ts:30) |
| RT40 | ImageEditTool | 配置条件：与 ImageGen 共用实际 image provider runtime config | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/services/imageGeneration/config.ts:30](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/services/imageGeneration/config.ts:30) |
| RT41 | ...(SendUserFileTool ? [SendUserFileTool] : []) | 本基线 feature 关闭：KAIROS；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:28](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:28) |
| RT42 | ...(PushNotificationTool ? [PushNotificationTool] : []) | 本基线 feature 关闭：KAIROS OR KAIROS_PUSH_NOTIFICATION；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:49](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:49) |
| RT43 | ...(SubscribePRTool ? [SubscribePRTool] : []) | 本基线 feature 关闭：KAIROS_GITHUB_WEBHOOKS；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:53](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:53) |
| RT44 | ...(powerShellTool ? [powerShellTool] : []) | 平台条件：仅 Windows；显式 false 关闭，外部用户无 Git Bash 时默认回退 PowerShell | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/utils/shell/shellToolUtils.ts:14](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/utils/shell/shellToolUtils.ts:14) |
| RT45 | ...(SnipTool ? [SnipTool] : []) | 本基线 feature 关闭：HISTORY_SNIP；不能以运行时配置打开编译关闭分支 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:129](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:129) |
| RT46 | ...(process.env.NODE_ENV === 'test' ? [TestingPermissionTool] : []) | 测试专用：NODE_ENV === test；不得在生产默认注册 | 保持同基线不可用条件；其他原发行 profile/内部需求待核定，不从原索引删除 | [src/tools.ts:258](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:258) |
| RT47 | ListMcpResourcesTool | 专用消费者：base registry 有定义，但 getTools 特殊排除；资源服务另行接入，不能直接塞入默认工具 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools.ts:320](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:320) |
| RT48 | ReadMcpResourceTool | 专用消费者：与 ListMcpResources 相同，核对原资源消费者 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/tools.ts:320](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/tools.ts:320) |
| RT49 | ...(isToolSearchEnabledOptimistic() ? [ToolSearchTool] : []) | 运行条件：ENABLE_TOOL_SEARCH mode、provider/base URL 乐观判断与实际模型/阈值检查 | 当前普通 Go builtin registry 未含该项；对应业务/消费者仍待做 | [src/utils/toolSearch.ts:270](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/utils/toolSearch.ts:270) |

## 19 个 bundled Skill 模块

显示名称、用户可调用性、模型可调用性和真正执行分开迁移。外部基线的 update-config/debug/simplify/batch 必须包含实际 Prompt/命令生产器；不能只复制名称。

| 模块/名称 | 原条件 | 后续工作 |
| --- | --- | --- |
| [src/skills/bundled/remember.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/remember.ts) · remember | 内部用户条件：注册函数内 USER_TYPE !== ant 时直接 return | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/loremIpsum.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/loremIpsum.ts) · lorem-ipsum | 内部用户条件：注册函数内 USER_TYPE !== ant 时直接 return | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/verify.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/verify.ts) · verify | 内部用户条件：注册函数内 USER_TYPE !== ant 时直接 return | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/loop.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/loop.ts) · loop | 本基线 feature 关闭：AGENT_TRIGGERS | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/claudeApi.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/claudeApi.ts) · claude-api | 本基线 feature 关闭：BUILDING_CLAUDE_APPS | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/debug.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/debug.ts) · debug | 发行注册：initBundledSkills 注册；仍须移植实际 Prompt/执行消费者 | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/stuck.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/stuck.ts) · stuck | 内部用户条件：注册函数内 USER_TYPE !== ant 时直接 return | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/scheduleRemoteAgents.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/scheduleRemoteAgents.ts) · schedule | 本基线 feature 关闭：AGENT_TRIGGERS_REMOTE | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/computerUse.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/computerUse.ts) · computer-use | 平台/配置条件：macOS/Windows、kill switch、stored enabled/default 与 3 秒缓存失效 | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/dream.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/dream.ts) · Source placeholder | 本基线 feature 关闭：KAIROS OR KAIROS_DREAM | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/batch.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/batch.ts) · batch | 发行注册：initBundledSkills 注册；仍须移植实际 Prompt/执行消费者 | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/updateConfig.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/updateConfig.ts) · update-config | 发行注册：initBundledSkills 注册；仍须移植实际 Prompt/执行消费者 | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/hunter.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/hunter.ts) · Source placeholder | 本基线 feature 关闭：REVIEW_ARTIFACT | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/keybindings.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/keybindings.ts) · keybindings-help | 运行条件：注册后 isKeybindingCustomizationEnabled；userInvocable false 不代表模型侧不用迁移 | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/imagegen.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/imagegen.ts) · imagegen | 配置条件：所选会话的 image provider env；Server mentions 必须用目标 provider env | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/claudeInChrome.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/claudeInChrome.ts) · claude-in-chrome | 宿主/配置条件：交互宿主、已安装扩展及 ant/发布 gate；初始化判定缓存 | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/skillify.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/skillify.ts) · skillify | 内部用户条件：注册函数内 USER_TYPE !== ant 时直接 return | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/runSkillGenerator.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/runSkillGenerator.ts) · Source placeholder | 本基线 feature 关闭：RUN_SKILL_GENERATOR | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |
| [src/skills/bundled/simplify.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/skills/bundled/simplify.ts) · simplify | 发行注册：initBundledSkills 注册；仍须移植实际 Prompt/执行消费者 | 原注册、Prompt、文件、参数、可用性与实际消费者；本基线关闭项保留在条件索引 |

## CLI bootstrap 与桌面独立 worker

16 个 CLI bootstrap 分支逐项保留。`--voice-worker` 属于 desktop sidecar 入口，另外登记；它不在原 CLI 16 分支中。不能以 CLI catalog 覆盖这条桌面路径。

| ID/入口 | 原条件 | 当前工作 |
| --- | --- | --- |
| B001 · --computer-use-repl-worker | args[0] === "--computer-use-repl-worker" | 迁移独立 Computer Use REPL worker 分派与 kernel 协议；隔离普通 CLI 配置、凭据和模式初始化，补取消/退出/清理；[src/entrypoints/cli.tsx:37](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:37) |
| B002 · --version, -V, -v | args.length === 1 && (args[0] === "--version" \|\| args[0] === "-v" \|\| args[0] === "-V") | 保留单参数 version 快速入口，核对三个别名、输出和退出；已有入口不关闭其他 CLI 初始化；[src/entrypoints/cli.tsx:31](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:31) |
| B003 · --dump-system-prompt | feature("DUMP_SYSTEM_PROMPT") && args[0] === "--dump-system-prompt" | 原基线 DUMP_SYSTEM_PROMPT 关闭；保留 gate，其他发行启用时迁移实际 Prompt 生产器与输出；[src/entrypoints/cli.tsx:57](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:57) |
| B004 · --claude-in-chrome-mcp | process.argv[2] === "--claude-in-chrome-mcp" | 接 Chrome MCP worker 的实际 server/工具/传输、配置和生命周期；配套浏览器扩展与权限链；[src/entrypoints/cli.tsx:79](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:79) |
| B005 · --chrome-native-host | process.argv[2] === "--chrome-native-host" | 接 Chrome native host 消息协议、扩展发现/安装注册、管道/子进程、平台路径与退出清理；[src/entrypoints/cli.tsx:86](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:86) |
| B006 · --computer-use-mcp | process.argv[2] === "--computer-use-mcp" | 接独立 Computer Use MCP 入口、OS/helper/权限、实际工具与资源清理，保持平台条件；[src/entrypoints/cli.tsx:93](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:93) |
| B007 · --daemon-worker | feature("DAEMON") && args[0] === "--daemon-worker" | 原基线 DAEMON 关闭；保留 gate 与 Source placeholder 状态，其他发行实现按证据核定；[src/entrypoints/cli.tsx:102](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:102) |
| B008 · bridge, rc, remote, remote-control, sync | feature("BRIDGE_MODE") && (args[0] === "remote-control" \|\| args[0] === "rc" \|\| args[0] === "remote" \|\| args[0] === "sync" \|\| args[0] === "bridge") | 原基线 BRIDGE_MODE 关闭；保留入口/别名条件，其他发行的真实 bridge 认证和生命周期另行核定；[src/entrypoints/cli.tsx:119](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:119) |
| B009 · daemon | feature("DAEMON") && args[0] === "daemon" | 原基线 DAEMON 关闭；不凭 Source placeholder 创建新业务，原其他发行范围继续核定；[src/entrypoints/cli.tsx:102](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:102) |
| B010 · --background, --bg, attach, kill, logs, ps | feature("BG_SESSIONS") && (args[0] === "ps" \|\| args[0] === "logs" \|\| args[0] === "attach" \|\| args[0] === "kill" \|\| args.includes("--bg") \|\| args.includes("--background")) | 原基线 BG_SESSIONS 关闭；保留声明和条件，不能与原可用的后台 Bash/Agent 工具混为一项；[src/entrypoints/cli.tsx:189](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:189) |
| B011 · list, new, reply | feature("TEMPLATES") && (args[0] === "new" \|\| args[0] === "list" \|\| args[0] === "reply") | 原基线 TEMPLATES 关闭；保留命令条件和 Source placeholder，其他发行内容继续核定；[src/entrypoints/cli.tsx:219](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:219) |
| B012 · environment-runner | feature("BYOC_ENVIRONMENT_RUNNER") && args[0] === "environment-runner" | 原基线 BYOC_ENVIRONMENT_RUNNER 关闭；保留入口及 Source placeholder，其他发行继续核定；[src/entrypoints/cli.tsx:231](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:231) |
| B013 · self-hosted-runner | feature("SELF_HOSTED_RUNNER") && args[0] === "self-hosted-runner" | 原基线 SELF_HOSTED_RUNNER 关闭；保留入口及 Source placeholder，其他发行继续核定；[src/entrypoints/cli.tsx:242](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:242) |
| B014 · --tmux, --tmux=classic, --worktree, --worktree=, -w | (args.includes("--tmux") \|\| args.includes("--tmux=classic")) && (args.includes("-w") \|\| args.includes("--worktree") \|\| args.some((a) => a.startsWith("--worktree="))) | 迁移 tmux/worktree 参数组合、仓库/工作树准备、实际进程启动、工作目录、信号/错误及清理；[src/entrypoints/cli.tsx:254](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:254) |
| B015 · --update, --upgrade | args.length === 1 && (args[0] === "--update" \|\| args[0] === "--upgrade") | 迁移 update/upgrade 快速入口至真实更新器；保留下载/校验/安装/失败/回退与退出语义；[src/entrypoints/cli.tsx:284](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:284) |
| B016 · --bare | args.includes("--bare") | 迁移 bare 的初始化与实际消费者：来源、Prompt、默认 shell/Read/Edit、扩展/记忆和权限过滤；[src/entrypoints/cli.tsx:288](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/entrypoints/cli.tsx:288) |
| DS-W01 · --voice-worker | 桌面 sidecar 在普通 mode 初始化之前独立分派 | Go 当前无专用分派；迁移 launcher、私有配置/认证、ready、loopback 转写、取消/释放和平台运行资源，归 D20/D33/D35 |

语音 worker 的实际契约是认证 loopback HTTP 转写服务：[src/server/services/voice/sensevoice/worker.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/services/voice/sensevoice/worker.ts)；实际启动者：[src/server/services/voice/sensevoice/recognizer.ts](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/src/server/services/voice/sensevoice/recognizer.ts)。Go 的对应实现和平台候选均未验收。

## 新明确的实施边界

- Worktree gate 恒为 true：必须覆盖外部 CLI、TUI 和桌面仓库/工作树操作。
- Teams 有外部 opt-in 路径；General 显式关闭优先，killswitch 继续保留。
- Workflows 默认可用；env、managed 与用户关闭要作用到真实执行器。
- Windows 无 Git Bash 时，PowerShell 是外部用户默认回退；显式关闭、Git Bash 存在及 opt-in 分支分别迁移。
- Task 工具要区分交互与非交互宿主；不能让所有端默认注册相同列表来冒称行为一致。
- ImageGen/ImageEdit/imagegen Skill 要用当前会话实际 provider env；mentions 不能误读 Server 全局 env。
- LSP 要同时覆盖 env 选择和真实连接状态；ToolSearch 要保留模式/provider/model/阈值生产器。
- MCP resource 定义在原 getTools 中有特殊排除；迁移原资源消费者，不直接放入默认工具目录。

上述全是实施与后续验收待办；本次静态检查不关闭工具或 UI 能力。

## 记录与后续验收

本矩阵保留 43 个当前输入文件哈希和两个原构建 Git blob 哈希。结构化记录见 [JSON](go-release-condition-work-matrix.json)。引用的总工作清单：[D01–D35](go-migration-worklist.md)；完整条件声明：[CLI/TUI 索引](go-cli-tui-work-checklist.md)。

后续按原条件构造实际入口场景，再比较 Prompt/schema/工具可用性、执行结果、错误和恢复。平台编译、静态 gate 分类、源码存在与真实平台执行分别留证。完整迁移及 Benchmark 保持未完成。
