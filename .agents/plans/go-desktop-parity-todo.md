# 原版桌面完整等价：缺口与待办清单

> 2026-10-08当前实施状态：本文件保留10月6日静态盘点，下面的缺失统计与“本轮只梳理”均为当时证据。此后已持续实施、Source/race验证和原生/官方DeepSeek验收；当前结果请读[验证记录](go-migration-current-validation.md)。完整迁移、全部UI及配对Benchmark仍未关闭。

更新：2026-10-06。**本轮按用户最新指令只梳理要做的工作：读取源码、更新静态索引和待办；没有复现、功能实现、模型调用或 UI 操作。** 完整迁移仍未完成。此前的实现证据保留在[执行进展](go-desktop-parity-progress.md)，本轮清单不把那些有限证据扩成整包验收。

## 目标与判断方法

交付目标：保留原版 React 桌面及其全部能力，桌面默认运行 Go CLI/Server/Harness；正常使用不需要迁移专用参数。原版发行能力不因迁移被隐藏、删除、返回空数据或改成未支持。

一次桌面操作的完整路径是：**页面/按钮 → store/API/native 调用 → Go 路由/IPC → 服务与持久化 → Agent 或平台副作用 → 事件/结果 → 重启后的恢复。** 各层均有实现与实际证据后，才关闭该操作。HTTP 返回成功和页面能显示，不能单独关闭能力迁移。

状态标记：**缺失**表示当前入口明确没有实现；**部分**表示有入口或有限业务，但仍有分支/依赖缺口；**保留、待联调**表示原 React/Electron 实现仍在，不能误记为需要重写，也不能据此宣布 Go 整包验收通过；**待验收**表示本轮静态检查不证明运行等价。

从第一性原理看，迁移单位是一次原来可以完成的用户操作。每项工作必须同时明确它的输入、执行者、状态与副作用、反馈和恢复方式。例如 Connector 的“安装并使用”包括安装文件、认证、插件启用、工具注册、当前会话刷新及实际工具调用；列表和安装按钮有响应只覆盖其中两步。所有宿主调用同一个 Go Session Controller/Harness，React/Electron 原有呈现和平台壳继续保留。

## 盘点覆盖与直接结论

本轮通过 TypeScript AST 扫描 544 个桌面生产文件，读取原版核心路由及 40 个 API handler 文件，核对 Go Server/application 显式守卫、桌面内容路由、17 个设置页、HTTP 调用、WS 类型和 Electron IPC 注册。

| 项目 | 静态结果 |
| --- | --- |
| 原版核心 API 资源 | 35 组；另有原版 Server 单独处理的 `public-access` |
| Go 顶层直接返回 501 的资源 | **23 组**；Task 五处调用和 Skills 列表/详情两处调用已接实际服务，Skills mentions 与运行时仍缺失 |
| 前端 HTTP 调用点 | 240 处，其中 237 处能解析路径；3 处是图标/图片参数传入的通用 Blob 获取器，单独登记 |
| 顶层缺失阻断的调用点 | 131 处 |
| 已接通资源内的明确缺失子接口 | 31 处调用点 |
| `public-access` 缺少 Go Server 注册 | 6 处前端调用点，另有 Electron 管理器的控制请求 |
| 确定缺少入口的 HTTP 调用点合计 | **168 处**；这是调用点数，含重复调用，不能称为 168 个独立接口或迁移完成比例 |
| Go Server/application 显式守卫 | 48 处源码命中，含条件守卫、缺失绑定及守卫定义，逐条保留而不混算独立能力 |
| WS | 11 类 ClientMessage、30 类 ServerMessage、9 类子 Agent 流事件 |
| Electron/native | 89 个 IPC handler 已有注册，14 个公开事件与 1 个内部通道；103 处桥接调用、7 处旧通知命令调用均保留对应关系 |

详细逐调用点清单见[HTTP/WS/native 索引](go-desktop-api-checklist.md)，结构化记录和源码哈希见[完整静态盘点](go-desktop-full-api-inventory.json)。这两份索引保留方法、路径表达式、前端文件/行号、直接消费者、原版 handler 选择器、Go 缺口及 native 注册；查询参数和动态表达式未从原记录删除。

此前浏览器测试走 `browserHost`，`Settings` 会选择简化 H5 设置页；原生 Electron 的完整设置页未由那些测试覆盖。已有聊天烟测只关闭其有限测试范围。

## 一、原盘点的 25 组业务资源与当前状态

除 D07 和 D02 的列表/详情外，下面 23 组仍在 [Go 顶层路由](../../backend/internal/server/server.go) 直接进入 `unsupported`。D07 已实现列表、详情和重置的磁盘业务，原版读取对照及普通二进制 HTTP 烟测通过；完整桌面验收仍未关闭。同名底层组件存在不表示其他桌面入口已经接通。

| ID | 资源/当前状态 | 原桌面要求补齐的操作 |
| --- | --- | --- |
| D01 | `connectors`（API 缺失，底层部分） | 列表、详情、prepare/authenticate/check/cancel/deactivate/remove，安装与认证进度、配置验证、错误重试和运行时刷新；现有 Go package 尚未接入 Server |
| D02 | `skills`（列表/详情已接，完整能力未完成） | 列表/详情已有实际 HTTP/Source 对照；mentions、动态命令、启用条件、执行与会话上下文仍待接 |
| D03 | `market` | 搜索/筛选/分页、详情、文件浏览、安装、卸载、安装状态及各来源状态 |
| D04 | `plugins`（API 缺失，loader/schema/安装/ZIP 部分已有） | 复用安装物化、安装记录与 ZIP；补来源/依赖/策略/缓存、更新/卸载/清理和异常恢复；列表/详情、enable、disable、update、uninstall、reload，以及已运行会话的 registry/MCP/Agent 刷新 |
| D05 | `mcp`（API 缺失，stdio/HTTP/SSE 客户端已有） | 列表、项目路径、状态、创建/编辑/删除、toggle、reconnect；完整认证、其他原传输、连接管理与工具注册 |
| D06 | `agents` | 定义列表、创建/编辑/删除、内置 Agent override/clear override、来源优先级、reload 和实际运行时加载 |
| D07 | `tasks`（部分，REST 已接通） | 所有任务列表、指定列表/任务详情、reset 和真实 Task Store 已接通；继续完成正常桌面/默认工具的联调验收 |
| D08 | `scheduled-tasks` | 创建/编辑/删除/启停、手动运行、近期及按任务运行历史、运行详情、真实调度/通知与恢复 |
| D09 | `teams` | 团队/成员/工作台、按 session 查询、成员 transcript/消息、plan 保存及 approve/return/cancel/retry、删除与生命周期事件 |
| D10 | `workflows` | 定义列表/详情、DSL 校验、保存/删除、运行列表/详情/session runs，执行与工作流 Agent 重建 |
| D11 | `conversations` | 原版 conversation 管理、`sessions/:id/chat/*` 状态、SDK/runtime 协作、输入/停止/恢复和实时事件链 |
| D12 | `session-collaboration` | 会话候选查询，以及原版 list/read/create/send/wait 协作宿主；引用会话上下文进入真实请求 |
| D13 | `search` | 全文和会话搜索、过滤/分页/索引及真实历史数据查询 |
| D14 | `memory` | 项目列表、文件列表、读取/保存记忆文件、作用域和运行时记忆缓存更新 |
| D15 | `haha-oauth` | Claude 官方登录 start/status/logout，浏览器回调、凭据保存/刷新/撤销与模型解析 |
| D16 | `haha-openai-oauth` | ChatGPT/OpenAI 官方登录 start/status/logout，回调、刷新、模型和运行请求 |
| D17 | `haha-grok-oauth` | Grok 官方登录 start/status/logout、认证轮询、刷新、模型和运行请求 |
| D18 | `adapters` | 配置读写；微信、飞书、钉钉、WhatsApp、企微、QQ、Slack 登录/注册/轮询/取消/解绑；Telegram 等原支持平台配置与真实运行 |
| D19 | `computer-use` | 状态、setup、安装应用/图标、系统设置与权限卡、request-access；原兼容端点、全局 consent、OS 权限及真实工具链 |
| D20 | `voice` | catalog、偏好、provider prepare/cancel/status/assets，模型资源生命周期与 multipart transcribe |
| D21 | `traces` | 会话列表、revision、采集设置读写、删除；还需 session trace、call 详情、原始 JSONL 导出及真实采集生产器 |
| D22 | `diagnostics` | 状态、local index/重建、事件读写、issue report、导出 bundle、打开日志目录、clear 和真实错误记录 |
| D23 | `doctor` | report、repair，文件/JSON/JSONL/schema 状态与 protected skips，关联既有允许修复的 UI 状态流程 |
| D24 | `activity-stats` | 活跃度、日期/模型/成本统计、历史聚合及页面需要的全部响应字段 |
| D25 | `open-targets` | 打开目标列表、按文件筛选、应用图标和 open-with，平台发现及真实打开行为 |

### D01–D06 · 扩展、Skills、MCP、插件与 Agent 管理

- [ ] 实现 D01 的完整 ConnectorService/持久化/adapter/plugin bridge；逐个完成六类 action，保留 202、冲突、取消、进度、认证 URL 和失败阶段语义。
- [ ] 接通 D02 已有扫描/详情组件的 Server/plugin/runtime 消费者；完成 mentions 生产器、条件 Skills 与动态 slash 注册；候选必须跟当前项目和 provider 能力一致。
- [ ] 实现 D03 的全部市场来源、分页/安全筛选、详情与安装/卸载业务；安装结果需进入实际 registry。
- [ ] 完成 D04 部分已有的 manifest/schema/component/cache loader、materializer、installed registry 和 ZIP 的生产接线及剩余分支；补原版错误/来源/策略、依赖闭包、迁移/更新/卸载/清理、builtin/inline/project 来源、MCP/Agent/Skill/Hooks 合并和活跃会话 reload。具体执行顺序见总表的 D04 接线细分。
- [ ] 将 D05 已有 stdio/HTTP/SSE 客户端绑定配置作用域、凭据/OAuth、连接缓存与 Harness；补原发行的 WS/WS-IDE、SDK/in-process、claudeai-proxy/IDE 行为、重连、资源清理、状态、工具调用与会话重载。
- [ ] 实现 D06 的内置/用户/项目/本地/插件/策略来源，覆盖与 override，模型/effort/tools/prompt 解析和热更新。
- [ ] 将上述管理状态接入唯一 Go Harness；实际使用新安装的 Skill/插件/MCP/Agent 完成任务。不能仅让管理页面有列表。

D01 还需逐项完成以下交付。原静态 catalog 是 55 个定义（3 个 native、43 个 remote、9 个 Skill recipe），不是一个聊天 API：

- [ ] 接入 Go Server 的列表、详情与全部 action，完成请求校验、202/400/404/409、动作互斥、取消、认证进度及重启时中断操作的恢复。
- [ ] 完成 3 个 native CLI adapter 的下载/完整性/版本校验、隔离安装、配置与认证、账号验证、升级回退及运行时移除；保留原共享账号和平台限制。
- [ ] 完成 9 个 Skill recipe 的固定 commit/文件哈希、全文件安装与验证、插件包装和可执行 Skill 加载。
- [ ] 实现 43 个 remote recipe 的真实 MCP 连接、OAuth/API key 配置和安全存储、认证/检查/取消/重连、工具注册及卸载清理。
- [ ] 实现实际 plugin bridge：marketplace/manifest/cache/settings/installed registry、enable/disable/uninstall、原所有活跃会话 reload；就绪状态由实际加载与连接结果产生。
- [ ] 补齐现有 Connector lifecycle/persistence/native/Skill adapter 的原版契约、故障、并发和旧状态兼容验证。现有文件没有接入消费者，也没有本轮运行验收，D01 保持未关闭。

**主要入口：**`desktop/src/api/{connectors,skills,composerReferences,market,plugins,mcp,agents}.ts`、对应 stores/pages，以及 `src/server/api/` 同名 handler、`src/server/services/connectorService.ts` 和 `src/services/connectors/`。

### D07–D12 · 任务、调度、团队、工作流和协作

- [ ] 完成 D07 的原生 UI/默认工具联调；REST 已接入实际 Task Store，legacy 字段/排序/错误、reset 高水位/修订和并发锁已有组件证据，Agent 工具实际提交 → 桌面 API 读取已有跨层回归。
- [ ] 实现 D08 的调度、并发/超时/失败、按计划运行、通知、历史和重启恢复；原运行记录与任务会话可导航。
- [ ] 实现 D09 的团队配置、成员 inbox、成员/任务工作台、plan 的保存/approve/return/cancel/retry 状态机和五类团队生命周期通知。
- [ ] 实现 D10 的 DSL/parser、校验/保存/加载、执行器、运行记录和 by-agent 工作流重建。
- [ ] 实现 D11 的原 conversation service 与 chat 子接口，保留 legacy REST 的 202 queued/status/队列重置语义；实际流式生成与中断仍走原 WS/SDK 运行链。SDK input/control、会话与页面状态共用实际运行会话，不把原 REST queue stub 扩成另一个 Agent 循环。
- [ ] 实现 D12 的 UI 候选接口和原协作宿主 list/read/create/send/wait 全部动作，保持其独立 session credential、无 Origin 的内部调用入口；关联 `sessionReferences` 的上下文与 transcript 标记。
- [ ] 绑定任务、团队、后台和子 Agent 的实时事件、断线 snapshot、终止/失败状态及消息发送。已有一次前台子 Agent 成功不关闭这些项目。

**主要入口：**`desktop/src/api/{cliTasks,tasks,teams,teamPlans,workflows,sessionCollaboration,subagents}.ts`、Activity/AgentTeams/SubagentRun UI、`src/server/api/{agents,scheduled-tasks,teams,workflows,conversations,sessionCollaboration}.ts`。

### D13–D18 · 搜索、记忆、官方认证与 IM

- [ ] 实现 D13 的搜索与索引业务，包括历史大文件、分页、过滤、取消及索引更新。
- [ ] 实现 D14 的项目/文件作用域与实际读写；将记忆更新反映到后续模型请求，保留原持久化格式。
- [ ] 分别实现 D15/D16/D17 的开始登录、状态、退出、回调、凭据刷新和失效恢复；接通 provider 模型与真实协议。
- [ ] 补全官方模型覆盖和 auth-status 的 OAuth freshness 分支。只验证 DeepSeek API key 不关闭官方订阅能力。
- [ ] 实现 D18 全部原支持 IM 平台的配置、账号状态、配对/注册/二维码/轮询/取消/解绑和进程生命周期。
- [ ] 迁移 IM 共享业务运行时与 Go Agent 接线；平台 SDK helper 可以保留原语言，不能把旧 TS Agent/Server 业务作为生产 fallback。

**主要入口：**`desktop/src/api/{search,memory,hahaOAuth,hahaOpenAIOAuth,hahaGrokOAuth,adapters}.ts`、对应 stores/settings、`src/server/services/` 和 `adapters/` 原运行链。

### D19–D25 · 平台操作、语音、Trace、诊断与打开文件

- [ ] 实现 D19 的 API、平台 setup、应用发现/图标、权限卡和 Go 工具调用。保持 Settings 全局 consent + OS 权限边界；兼容 `authorized-apps` 名称不新增逐应用提示/allowlist。
- [ ] 实现 Computer Use 的 WS 授权请求/响应、snapshot、取消和实际 native helper 资源清理。
- [ ] 实现 D20 的资源准备/取消/删除、偏好和语音转写，接通麦克风、录音、上传、语言和错误状态。
- [ ] 实现 D21 真实 Trace 捕获与存储，再接列表、设置、revision、详情/call 和删除；保留脱敏与字段语义。
- [ ] 实现 D22 日志/事件/索引/报告/导出业务，并接入 Go 运行和 Electron host 的实际错误。
- [ ] 实现 D23 的 `GET /api/doctor`、`GET .../report` 与 `POST .../repair`，保持 targetIds/项目作用域、原版诊断报告与保护边界；自动修复仍限于既有 allowlist 的可重建 UI 状态，保护用户数据。
- [ ] 实现 D24 的真实历史/运行统计与聚合；当前有限模型计费组件测试不代替该 API。
- [ ] 实现 D25 的目标发现、图标、文件关联和打开行为，保持平台路径和授权来源语义。

**主要入口：**`desktop/src/api/{computerUse,voice,traces,diagnostics,doctor,activityStats,openTargets}.ts`、对应设置页/Trace 页面、`src/server/api/` 同名 handler 和原平台 services。

## 二、已接通资源仍缺少的子能力

### D26 · 会话、项目、工作树与历史（部分）

Go 已有会话创建、列表/基础历史、改名、删除、近期项目和部分仓库查询。下面仍要完成：

- [ ] `GET /api/sessions/project-history`：项目历史分页/cursor。
- [ ] `POST /api/sessions/batch-delete`：成功/失败逐项结果、活动会话清理。
- [ ] `POST /api/sessions/repository-branch`：新分支创建及返回仓库上下文。
- [ ] `POST /api/sessions` 的 repository/worktree 分支：原请求中的非空 repository 当前被拒绝。
- [ ] `POST /api/sessions/:id/branch`：从原消息分支点 fork，保留原历史和新会话关系。
- [ ] `GET /api/sessions/:id/history-recovery`：恢复目标/Todo/activity/usage 等状态。
- [ ] `GET /api/sessions/:id/chat/status`：原 chat 状态与 activity 状态。
- [ ] 完整核对基础列表/messages/summary/git-info 的字段、分页/预算、memory session、索引、工作区丢失、未知字段和重启行为；现有入口按原行为验收，不能以 routed 状态直接关闭。

**缺口证据：**`backend/internal/server/sessions.go` 中 project-history/repository-branch/batch-delete 显式守卫、repository 创建守卫及子资源 default；原版入口 `desktop/src/api/sessions.ts`。

### D27 · 侧聊、检查器、slash、子 Agent 追问（缺失/部分）

- [ ] `POST /api/sessions/:id/side-chats` 与 `DELETE /api/sessions/:id/side-chats/:sideId`：创建/销毁及正确父子关系。
- [ ] `POST /api/sessions/:id/side-question` 与 `DELETE .../side-question/:questionId`：旁路提问、history/schema/消息体预算、取消与主回合隔离，恢复原 `/btw` 相关能力。
- [ ] `GET /api/sessions/:id/slash-commands`：完整当前会话命令生产器，包含动态 Skills/插件。
- [ ] `GET /api/sessions/:id/inspection`：状态、usage、context、contextOnly 与错误字段；恢复上下文菜单和 Inspector。
- [ ] `GET /api/sessions/:id/trace`、`.../trace/calls/:callId` 与 `.../trace/raw`：详情与原始 JSONL 导出对应 D21。
- [ ] `POST /api/sessions/:id/subagents/by-tool/:toolUseId/messages`：inbox 路由、queued/resumed 状态与实际投递。
- [ ] by-agent 子 Agent 查询的 workflow reconstruction 分支；完整活动分页、嵌套 Agent、Team 成员和终止状态。

**缺口证据：**`backend/internal/server/sessions.go`、`subagent_runs.go`；UI 入口 `sideChats.ts`、`sessions.ts`、`subagents.ts`。

### D28 · 工作区、Review、预览与文件 URL（部分）

Go 已有 workspace status/tree/search/file/raw/diff/watch 和有限 checkpoint/rewind。仍需：

- [ ] 完成 `/api/sessions/:id/review` 的 status、revision、diff、stage、unstage、stage-hunk、unstage-hunk、revert；保持 Git 与 checkpoint 来源选择及事务/错误语义。
- [ ] 补全 checkpoint/rewind 的 shell classifier、缺少备份等分支，按原版说明哪些文件能恢复、哪些副作用未被捕获。
- [ ] 移植 `/preview-fs/:sessionId/*` 的真实预览服务及 `/local-file/*` 的本地文件服务；当前 Go 没有原版专用处理路径。
- [ ] 保持原 HTML/图片/PDF/Docx/Xlsx/音视频资源请求、Range/content-type/cache/认证与目录边界；正文、附件和浏览器预览均可打开。
- [ ] 核对图片/图标的三个参数化 Blob 调用及包内静态资源；不能用 fallback 图标或 H5 shell HTML 代替资源。
- [ ] 关闭 filesystem/search、文件历史与配置绑定的条件守卫，核对原 `.gitignore`、路径别名、跨平台文件名、SSE 重连和缓存更新。

**缺口证据：**`backend/internal/server/server.go` 的 proxy/preview-fs 守卫与 static 分派；workspace/checkpoint 条件守卫；`desktop/src/api/review.ts` 的八类调用。原版专用路径在 `src/server/index.ts`、`api/previewFs.ts`、`api/localFile.ts`。

### D29 · Provider、模型、设置与桌面偏好（部分）

Go 已有 provider 基础 CRUD/激活/排序、部分模型/effort、用户/项目设置、desktop-ui 偏好和 H5 设置入口。仍需：

- [ ] `GET/PUT /api/providers/:id/models`：官方模型覆盖配置。
- [ ] `POST /api/providers/:id/test`、`POST /api/providers/test`：保存/未保存配置的真实连接测试。
- [ ] `GET /api/providers/cc-switch/scan`、`POST .../import`：发现、选择、导入与未知字段保留。
- [ ] `POST /api/providers/models`：真实模型目录获取和 provider 特性。
- [ ] models/current、auth-status 的独立/官方 OAuth、freshness、managed/user settings 分支。
- [ ] `/proxy/v1/messages`、`/proxy/providers/:id/*` 和原支持协议转换/认证/headers/streaming/重试。Go 当前 proxy 路径直接被拒绝；OpenAI 兼容 provider 不能因基本 CRUD 存在而算可用。
- [ ] `GET /api/settings/cli-launcher` 及原桌面 CLI 安装/卸载/检测/PATH/native 操作完整链。
- [ ] `GET /api/status/diagnostics`、`/usage`、`/user`：当前 Go 只有根 status 健康响应；补齐诊断、用量与项目/用户信息业务。
- [ ] 完成运行配置持久化的全部生命周期验收；设置更改作用于原来的服务、当前/后续会话与重启恢复，保留已修复具体场景的限定证据。
- [ ] 完整核对 17 个设置页各项保存后的实际消费者：常规/外观、模型、语音、远程、IM、终端、MCP、Agent、Skills、Memory、Plugins、Pets、Computer Use、Activity、Trace、Diagnostics、About。
- [ ] 核对 desktop-ui 的 sidebar/profile/avatar/project-name/pet 全部读写、旧数据迁移与通知；当前端存在不等于整包联调通过。

**缺口证据：**`backend/internal/server/providers.go`、`models.go`、`settings.go`、`server.go`；`desktop/src/api/providers.ts` 的七处缺失调用及 `settings.ts` 的 cli-launcher。

### D30 · H5、公网配对与远程权限（缺失/部分）

- [ ] 原版单独的 `PublicAccessServer`：本机 status/enable/origin/disable/pairing/approve/reject/revoke，设备持久化、过期与 listener/socket 生命周期。
- [ ] 远端 `session/pair/claim`、cookie/设备认证、限流、Origin、撤销和配对状态。
- [ ] 接通 Electron PublicAccessManager 的 Go backend 控制请求；目前壳层实现仍在，但 `/api/public-access` 无 Go 注册。
- [ ] 对齐原远端 Provider/General 可访问 API、凭据脱敏和变更规则；Go 当前对远端 settings/providers 整组拒绝，与原细粒度允许路径不同。
- [ ] 完整核对已有 h5-access enable/disable/regenerate/verify/update、CORS、disconnect grace、固定端口、public URL 和二维码流程。

**原版入口：**`src/server/publicAccess.ts`、`src/server/index.ts`、`remoteBrowserPolicy.ts`、`desktop/electron/services/publicAccess.ts`、`desktop/src/api/publicAccess.ts`。公网配对与 H5 token 是两条原产品能力，均须保留。

## 三、WS 与 Agent 执行链的剩余工作

### D31 · 输入、权限、问答与后台控制（部分）

| ClientMessage | 当前 Go 代码状态 | 必须完成的工作 |
| --- | --- | --- |
| `user_message` | 普通文本可用；非空 attachments/sessionReferences 被拒绝 | 原附件、粘贴图片/文件、引用、prompt 模式、上下文生产与重放 |
| `permission_response` | 单次批准/拒绝与 updatedInput 有有限证据；rule/permissionUpdates/runtimeOverride 有守卫 | 允许本回合/会话/持久规则、修改命令、模式/目录更新、取消与重连 |
| `computer_use_permission_response` | 显式未实现 | Computer Use 授权回送、取消和 snapshot |
| `ask_user_question_activity` | 显式未实现 | 问答状态、用户活动/提交/取消、恢复与正确控制响应 |
| `stop_background_task` | 显式未实现 | 后台任务停止、失败反馈、取消资源和通知 |
| `set_runtime_config` | 入口已实现，两个具体落盘原因已有修复；整体待验收 | 元数据等实际写入完成才返回，native CWD 与原版 canonical/NFC 一致；补齐完整配置字段、整包检查与 UI 切换/重启验收 |
| `set_permission_mode` | 有入口 | 所有原模式与运行时规则、状态、持久化一致 |
| `stop_generation` | 基础取消有证据 | 所有工具/子 Agent/后台/审批/流式分支的取消与 finally |
| `prewarm_session` | 工厂初始化后直接返回 | 完整 registry/Skills/MCP/Hooks/Agent 预热与原失败语义 |
| `sync_state` | 部分 snapshot | 全部审批、问答、后台、Team、会话状态及断线重放 |
| `ping` | 有入口 | 保持原心跳、连接关闭和错误策略 |

- [ ] 逐项接通表中控制及 `ClientMessage` 全部可选字段；保持错误码、字段缺省和授权语义。
- [ ] 补齐 30 类 ServerMessage 与 9 类子 Agent 流消息的实际生产者、字段、顺序和恢复状态，尤其 Computer Use、后台停止失败、Team/plan/workbench、task、title、协作重放。
- [ ] 实现原 `/sdk/:sessionId` 独立 SDK WebSocket 通道语义、session token 认证、消息/control 转换与生命周期；当前 Go 无原专用 `/sdk/` 注册。`/ws/` 前缀统一走桌面 ClientMessage 处理，不能用另造的 `/ws/sdk/...` 被接受替代原 SDK 通道。
- [ ] 完成后台 Agent、Teams、工作流、预算、压缩、队列、Hooks、Skills/MCP、工具重试及异常恢复，接入同一 Go Harness。

**主要缺口：**`backend/internal/application/desktop.go`、`session.go` 的显式守卫；`backend/internal/server/websocket.go`、`src/server/ws/handler.ts` 和 `desktop/src/types/chat.ts` 的完整协议。

### D32 · 完整默认工具、CLI 与 TUI（部分）

- [ ] 接通原完整默认工具目录。当前 Go 原生 registry 仅枚举 Agent、Bash、Glob、Grep、Read、Edit、Write、NotebookEdit、TodoWrite、TaskCreate/Get/Update/List；不能以此替代原版动态/平台目录。
- [ ] 补齐 TaskOutput/TaskStop、AskUserQuestion、计划/工作树、WebFetch/WebSearch、Skill、MCP 与原实际启用的其他工具及 schema/权限/Hooks/结果。
- [ ] 逐项处理原 `src/tools.ts` 中 SendMessage/会话协作、TeamCreate/Plan/Delete、Workflow、Brief、ImageGen/ImageEdit，以及原条件启用的 PowerShell、LSP、WebBrowser、Cron、Sleep、RemoteTrigger、Monitor、TerminalCapture、通知/发送文件/PR 工具等。原 feature/env 条件、测试专用和内部构建项在索引中保留，按原发行可达性分类，不擅自变成全开或删除。
- [ ] 关闭普通 CLI 默认目录守卫，补全原参数/命令/slash/SDK/Recovery/输入模式；桌面 bundled CLI、终端和 Server 正常启动均无需 `--tools` 等迁移绕行。
- [ ] 保持原命令生产器和各宿主配置，完成复杂取消/压缩/恢复/子 Agent 链；不另建桌面专用 Agent 循环。

CLI/TUI 不能合并成“能打开终端”一项。逐命令、逐参数和终端业务待办见[CLI/TUI 完整工作索引](go-cli-tui-work-checklist.md)，当前静态分类见[CLI/TUI 数据清单](go-cli-tui-work-inventory.json)。包括原有限发行 profile 的 36 个 Commander 节点、81 个根参数、61 个已观察 slash 名称、16 个 bootstrap 分支；动态和其他发行 profile 仍按原条件展开，这些数量不是完整运行能力总数。

- [ ] 补齐已注册基础工具的缺失分支：Read 图片/PDF、Bash 后台、Agent 后台/父会话独立生命周期、Task Hooks；已有工具名不能关闭其原分支。
- [ ] 完成全部实际发行 subcommand handler，包括 auth、MCP、plugin/marketplace、doctor、install/update/setup-token、auto-mode；已有 `agents` CLI 的有限路径继续按原作用域验收，D06 的桌面管理 API 另行接通。
- [ ] 补齐原根参数的业务消费者及组合，包括 continue/fork/分支点、worktree/tmux、额外目录、Agent/Team、MCP/plugin、权限、预算、Prompt 文件、输出 schema、调试、SDK 远程连接、持久化开关。
- [ ] 完成 slash 的 local/local-jsx/prompt 执行、别名、可用性、动态发现、参数、成功/失败/取消、历史副作用；完整 slash 候选接入 TUI，不能只复制 help 或命令名。
- [ ] 接通 TUI 的 Bash 模式、文件/图片/PDF 粘贴、引用与附件管理、Vim、终端选择/复制、真实 slash/文件/MCP 补全和历史恢复。
- [ ] 完成 TUI 的问答/计划/权限多字段表单、规则持久化、Task/后台/Team 面板和所有原发行快捷键动作；模型/effort/thinking/fast/workflow 等切换更新实际 Session。
- [ ] 按原显示行为完成消息/工具/进度/错误/差异/上下文/任务的终端呈现，接通主题、状态栏、通知及消息操作，保留原可达 UI。
- [ ] 补齐 stdio/SDK 控制、partial/verbose/json/text、EOF/信号/退出码与 finally、Recovery 和 PTY/ConPTY 生命周期；桌面终端及独立 CLI 使用同一普通 Go 二进制。

共享 Harness 的内部工作不能被 API 清单遗漏：

- [ ] 完成系统 Prompt、CLAUDE/AGENTS 指令、记忆/output style、条件 Skill/Agent/MCP 注入、工具描述/schema/过滤/排序、cache/beta/header 的完整生产器。
- [ ] 完成附件/引用/本地命令/历史进入模型上下文的路径，保留原 token/context/output 上限、压缩触发及压缩后的恢复。
- [ ] 实现实际配置 Hooks、matcher、原支持 hook 类型/事件、输入输出、超时/取消/阻断/追加上下文及 cleanup。当前 Go 的空 Hook resolver 不等于 active Hooks 可用。
- [ ] 完成后台进程和 Agent 的所有权、输出/停止、完成/失败通知、父会话关闭后的行为，以及 Team/Workflow/协作的实际调度。
- [ ] 对齐权限模式/规则/目录/工具输入更新、并行工具、重试/fallback、模型及任务预算/成本、流事件/排队和工具失败后的继续。
- [ ] 对齐停止/断线/崩溃/恢复的状态转换与写入顺序；工具副作用完成、消息落盘和下一回合之间的边界保持原版语义。

## 四、React/Electron 原能力的保留与联调

### D33 · Native 壳与 UI 状态（保留、待联调）

89 个 IPC handler 已注册。这部分不能全部列作“Go 未实现”，但必须把 Go 依赖和完整安装候选验证补齐：

- [ ] 启动/重启/退出、托盘、窗口/菜单、多窗口、应用模式切换、服务 URL/local/pet token；Go 子进程与 user config 路径正确。
- [ ] 剪贴板、拖放/选文件/保存对话框、shell/open path、系统打开目标，绑定上述附件和文件 API。
- [ ] Terminal spawn/write/resize/kill/output/exit、Bash path、启动关联、终端恢复与完整 bundled Go CLI。
- [ ] Preview webview 的导航/布局/缩放/可见性/消息/截图/选区，以及 workspace browser 的创建、前后退、刷新/停止、查找、捕获、snapshot、消息、PDF/下载与关闭。
- [ ] Native 通知权限/发送/动作/确认；绑定定时、后台、Agent 完成事件和正确会话导航。
- [ ] Pets 的列表、创建/图集/bytes、选择素材、显示/隐藏、拖动/鼠标区域、窗口聚焦、会话导航及独立访问凭据。
- [ ] 更新检查/下载/安装准备/取消/relaunch 与完整 Go 包替换；无 Bun/旧 TS 业务依赖。
- [ ] 语言/系统语言、外观/缩放、个人信息/avatar、项目显示名、tabs/sidebar/聊天草稿与 workspace 状态的原行为和旧数据迁移。
- [ ] 全部菜单/Tab/Settings/工作区/活动面板仍可达，功能状态与后端一致；不隐藏报错入口来宣布完成。

**完整通道与 handler：**见索引中的 native 表；原实现 `desktop/src/lib/desktopHost/electronHost.ts`、`desktop/electron/ipc/channels.ts`、`desktop/electron/main.ts` 与 services 保留。已有壳层单元测试不等于新 Go 候选整包证明。

专用入口的细分工作见 [Server 独立入口与生命周期](go-server-boundary-work-checklist.md)。尤其 Pet 凭据当前被 Go 直接拒绝：需要原限定会话投影、HTTP/WS 白名单和输出脱敏；不能只验窗口显示。该索引也展开 SDK/协作/OAuth 回调、请求断开和后台服务所有权，全部归入本清单已有工作包。

## 五、状态、数据和验收交付

### D34 · 持久化与兼容性（部分，整体待验收）

- [ ] 完成 runtimeProviderId 等配置的整体生命周期验收，覆盖队列、adopt、flush、关闭与后续恢复。此前已修复首轮前关闭丢失的缓冲路径，并修复 native CWD 别名产生两个项目 ID；仍需同一候选的完整字段、整包、升级和实际 UI 恢复证据。
- [ ] 原配置/服务商/凭据、项目和会话索引、JSONL、checkpoint、记忆、插件/MCP、任务/团队/工作流、配对、语音和 Trace 按原格式迁移，未知字段保留。
- [ ] 崩溃/断线/取消后继续，内存会话、跨 CLI/Web/Electron、跨 TS/Go 版本升级与整版本回退；一次状态写入者，已完成副作用不重复。
- [ ] 改持久化形状时提供 forward migration、旧 fixture 与 persistence-upgrade；保护用户真实状态，不通过手工改历史模拟产品兼容。

### D35 · 完整桌面等价验收（待验收）

- [ ] 每个 D01–D34 工作包绑定原版 handler/服务/数据/协议，逐项比较输入、响应、错误、实际副作用和重启结果。
- [ ] 收敛此前记录的 Server、Source 差分、race、coverage mapping/阈值失败及稳定性缺口；冻结当前候选执行 `check:impact` 所选门禁和 `verify`，历史结果与当前结果分别记录。
- [ ] 原生 Electron 完整安装候选逐个桌面区域/设置页/操作执行，使用正常默认入口；Web UI 只关闭其自身覆盖的范围。
- [ ] 旧 `/tmp` session 代表场景和新的代码任务跨端执行；不得把私有导入算 UI fork 完成或把七个有限回合算全产品完成。
- [ ] 原支持平台实机运行、原生能力、PTY/ConPTY、安装/升级/PATH/签名/整包回退，保留平台未运行项。
- [ ] 运行时专属诊断（如 V8 heapdump）明确兼容策略和实际消费者；Go profile 不能冒充原格式，未解决的格式/行为差异继续列为缺口。
- [ ] 保留原 Benchmark 的题目、fixture、模型参数、权限、预算、超时和评分器；冻结 TS/Go 条件后做确定性差分与真实配对评测。
- [ ] 交付安装候选与完整使用/升级/恢复文档；所有必需工作包关闭后才更新完整迁移状态。

## 优先级与依赖

| 顺序 | 工作包 | 交付边界 |
| --- | --- | --- |
| 1：共享运行基础与数据契约 | D02、D04–D06、D14–D17、D29、D31–D32、D34 | 先补实际 registry/工具/Hooks/Provider/上下文与状态生产器，保留原发行和对照条件 |
| 2：完整 API 与业务消费者 | D01–D06、D13–D17、D21–D29 | 扩展与设置、会话/检查器/Review、认证/诊断等入口与实际消费者连续交付 |
| 3：完整 Agent 与实时能力 | D07–D12、D18–D20、D31–D32 | 后台/调度/Team/工作流/IM、问答/审批、语音/Computer Use、完整默认 CLI/TUI 与 SDK |
| 4：远程、native 与整包交接 | D30、D33、D34 | 远程控制/配对、全部 native 依赖、真实 Electron 和跨端恢复 |
| 5：全量判定 | D35 | 同一候选全部门禁、真实任务、平台交付和原 Benchmark 验收；D34/原版对照贯穿各阶段 |

工作包有依赖时，从持久化/registry/runtime 生产器实现到 API 和 UI 消费者。每个交付关闭的是可使用的原版业务操作；不能先补一个返回 200 的 handler，再把底层业务留作“后续优化”。

具体依赖：D01/D03 依赖 D02/D04/D05 的真实加载、认证和 reload；D06 与 Agent 工具共用定义解析；D07–D12 依赖后台任务、共享 Harness 和事件生产者；D19/D20 依赖平台 helper/资源生命周期；D21/D22/D24 依赖真实 Trace/日志/用量生产器；D26–D28 依赖会话、工作树、checkpoint、文件服务；D29 依赖 Provider/auth/proxy；D30 依赖远端认证和 listener；D33 依赖上述业务与 Go 整包。D34 和 D35 贯穿全部工作包。

后续落地顺序按可完成的业务链安排：先补共享 registry/工具/Hooks/Provider/状态基础，再完整交付扩展和各桌面 API，接通其 Agent/实时消费者，然后完成默认 CLI/TUI、native/远程及安装候选，最后做全量验收。具体管理 API 可与其运行时连续交付。每个批次结束只关闭已贯通的操作，记录仍缺的依赖。

每项关闭记录：原版入口、Go 实现、候选、正常用户操作、数据/副作用断言、实际证据和剩余边界。本清单不把静态存在或本轮扫描当运行通过。

## 静态盘点交付与校验

- 完整待办：本文件的 D01–D35；Connector、CLI/TUI 和 Harness 已展开为具体子任务，接口/协议/native 细项见索引。checkbox 数和调用点数均不作为迁移完成比例。
- [逐入口索引](go-desktop-api-checklist.md)：240 个 HTTP 调用点、全部原版 API handler 路由条件、专用 transport、11/30/9 类 WS 类型、native 通道/桥接、完整工具注册表达式和显式能力守卫。
- [结构化当前记录](go-desktop-full-api-inventory.json)：保留动态表达式、直接消费者、源码位置与 650 个读取文件的 SHA-256；原 25 组缺失/175 处调用的初始记录另存于执行证据目录。
- [CLI/TUI 工作索引](go-cli-tui-work-checklist.md)与[结构化分类](go-cli-tui-work-inventory.json)：保留原有限发行 profile、每个命令/参数、slash、bootstrap 与终端动作的当前生产入口和剩余工作，不使用历史注册状态冒称当前业务已实现。
- [附件范围对照](go-work-scope-audit.md)：原附件 95 条待办逐项对应当前清单，保留全部原接口路径；状态更新不删除其剩余实施和验收要求。
- 原静态窗口的调用点/工作包/通道、本地链接及输入哈希校验通过；实现后的记录按当前源码重新生成。代码/运行证据以[执行进展](go-desktop-parity-progress.md)为准。
- 静态盘点本身不计作功能验收。完整桌面、真实模型和 Benchmark 状态保持未完成。

## 后续接线状态：D02

列表/详情已接真实 NativeProcessSettings/plugin/skillregistry，11 个 Source HTTP 请求对照通过；mentions、完整定义/动态/条件/插件命令、Skill/slash/Hooks/fork 与共享 Harness 仍待接。D02 原 checkbox 保持未关闭。当前整体 Go 门禁仍失败，完整 UI/native/真实模型/Benchmark 未验收。[限定证据](../../artifacts/go-native-skills-api/report.md)。
