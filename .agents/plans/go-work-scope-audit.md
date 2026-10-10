# 工作清单：原附件范围完整性审计

> 2026-10-08当前实施状态：本文件保留10月6日静态盘点，下面的缺失统计与“本轮只梳理”均为当时证据。此后已持续实施、Source/race验证和原生/官方DeepSeek验收；当前结果请读[验证记录](go-migration-current-validation.md)。完整迁移、全部UI及配对Benchmark仍未关闭。

更新：2026-10-06。审计的是用户明确要求的“梳理全部要干的活”。实现、运行复现、真实模型、完整桌面和 Benchmark 仍是后续未完成工作；本次审计不关闭迁移能力。

## 范围对照结果

- 原附件 35 个工作包、95 条待办全部有当前清单归属；没有删除原工作包。
- 88 条待办全文保留，7 条按当前源码/已有具体实现更新状态，剩余实施和验收要求保留。
- 当前主清单 115 条待办；增加 Connector、CLI/TUI 和共享 Harness 的细分任务。
- 原附件的 36 个代码格式接口路径表达式全部保留；路径表达式不是唯一接口数。
- HTTP 240 处调用、40 个原 handler、WS 11/30/9 类、native 89 个 IPC/103 处 bridge、49 个工具注册表达式均保留逐项索引。
- CLI/TUI 增补原命令/根参数、116 个 Slash 定义、19 个 bundled 模块、16 个 bootstrap 分支、98 个终端动作与43个协议判别项；原发行条件及动态生产器继续列作后续工作。
- 当前源码仍有 23 组整组缺失 API、168 处调用缺入口；未改成“迁移完成”。

## 七条状态更新的含义

| 原附件项 | 当前归属 | 状态更新与保留的工作 |
| --- | --- | --- |
| R002 | [主清单:71](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:71) | 复用已有 Skill 扫描/详情组件；Server/plugin/runtime、mentions、条件与 Slash 的剩余范围保留 |
| R004 | [主清单:73](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:73) | plugin loader/schema/安装/ZIP 部分已有；来源/依赖/策略/管理生命周期及生产接线继续保留 |
| R005 | [主清单:74](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:74) | MCP 三种客户端传输组件已有；完整配置/认证/其他原传输/连接管理/Harness 仍未关闭 |
| R008 | [主清单:91](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:91) | Task REST 已有实现；原 Task Store/工具互通和 UI/默认工具验收继续保留，未关闭完整桌面能力 |
| R058 | [主清单:179](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:179) | 具体配置写入修复有旧窗口证据；配置对服务/当前及后续会话/重启的全生命周期要求保留 |
| R084 | [主清单:272](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:272) | 两个具体落盘原因已有修复；队列/adopt/flush/关闭/恢复和同一候选完整字段/升级/UI验收全部保留 |
| R089 | [主清单:280](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:280) | 已有失败与当前候选分别记录；Server/Source/race/覆盖率映射和阈值/check:impact所选门禁/verify要求保留 |

## 原附件 95 条待办逐项对照

| 原附件项 | 原分组 | 当前清单位置 | 范围审计 |
| --- | --- | --- | --- |
| R001 | D01–D06 · 扩展、Skills、MCP、插件与 Agent 管理 | [待办:70](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:70) | 全文保留 |
| R002 | D01–D06 · 扩展、Skills、MCP、插件与 Agent 管理 | [待办:71](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:71) | 状态更新；剩余范围保留 |
| R003 | D01–D06 · 扩展、Skills、MCP、插件与 Agent 管理 | [待办:72](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:72) | 全文保留 |
| R004 | D01–D06 · 扩展、Skills、MCP、插件与 Agent 管理 | [待办:73](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:73) | 状态更新；剩余范围保留 |
| R005 | D01–D06 · 扩展、Skills、MCP、插件与 Agent 管理 | [待办:74](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:74) | 状态更新；剩余范围保留 |
| R006 | D01–D06 · 扩展、Skills、MCP、插件与 Agent 管理 | [待办:75](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:75) | 全文保留 |
| R007 | D01–D06 · 扩展、Skills、MCP、插件与 Agent 管理 | [待办:76](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:76) | 全文保留 |
| R008 | D07–D12 · 任务、调度、团队、工作流和协作 | [待办:91](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:91) | 状态更新；剩余范围保留 |
| R009 | D07–D12 · 任务、调度、团队、工作流和协作 | [待办:92](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:92) | 全文保留 |
| R010 | D07–D12 · 任务、调度、团队、工作流和协作 | [待办:93](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:93) | 全文保留 |
| R011 | D07–D12 · 任务、调度、团队、工作流和协作 | [待办:94](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:94) | 全文保留 |
| R012 | D07–D12 · 任务、调度、团队、工作流和协作 | [待办:95](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:95) | 全文保留 |
| R013 | D07–D12 · 任务、调度、团队、工作流和协作 | [待办:96](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:96) | 全文保留 |
| R014 | D07–D12 · 任务、调度、团队、工作流和协作 | [待办:97](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:97) | 全文保留 |
| R015 | D13–D18 · 搜索、记忆、官方认证与 IM | [待办:103](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:103) | 全文保留 |
| R016 | D13–D18 · 搜索、记忆、官方认证与 IM | [待办:104](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:104) | 全文保留 |
| R017 | D13–D18 · 搜索、记忆、官方认证与 IM | [待办:105](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:105) | 全文保留 |
| R018 | D13–D18 · 搜索、记忆、官方认证与 IM | [待办:106](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:106) | 全文保留 |
| R019 | D13–D18 · 搜索、记忆、官方认证与 IM | [待办:107](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:107) | 全文保留 |
| R020 | D13–D18 · 搜索、记忆、官方认证与 IM | [待办:108](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:108) | 全文保留 |
| R021 | D19–D25 · 平台操作、语音、Trace、诊断与打开文件 | [待办:114](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:114) | 全文保留 |
| R022 | D19–D25 · 平台操作、语音、Trace、诊断与打开文件 | [待办:115](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:115) | 全文保留 |
| R023 | D19–D25 · 平台操作、语音、Trace、诊断与打开文件 | [待办:116](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:116) | 全文保留 |
| R024 | D19–D25 · 平台操作、语音、Trace、诊断与打开文件 | [待办:117](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:117) | 全文保留 |
| R025 | D19–D25 · 平台操作、语音、Trace、诊断与打开文件 | [待办:118](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:118) | 全文保留 |
| R026 | D19–D25 · 平台操作、语音、Trace、诊断与打开文件 | [待办:119](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:119) | 全文保留 |
| R027 | D19–D25 · 平台操作、语音、Trace、诊断与打开文件 | [待办:120](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:120) | 全文保留 |
| R028 | D19–D25 · 平台操作、语音、Trace、诊断与打开文件 | [待办:121](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:121) | 全文保留 |
| R029 | D26 · 会话、项目、工作树与历史（部分） | [待办:131](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:131) | 全文保留 |
| R030 | D26 · 会话、项目、工作树与历史（部分） | [待办:132](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:132) | 全文保留 |
| R031 | D26 · 会话、项目、工作树与历史（部分） | [待办:133](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:133) | 全文保留 |
| R032 | D26 · 会话、项目、工作树与历史（部分） | [待办:134](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:134) | 全文保留 |
| R033 | D26 · 会话、项目、工作树与历史（部分） | [待办:135](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:135) | 全文保留 |
| R034 | D26 · 会话、项目、工作树与历史（部分） | [待办:136](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:136) | 全文保留 |
| R035 | D26 · 会话、项目、工作树与历史（部分） | [待办:137](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:137) | 全文保留 |
| R036 | D26 · 会话、项目、工作树与历史（部分） | [待办:138](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:138) | 全文保留 |
| R037 | D27 · 侧聊、检查器、slash、子 Agent 追问（缺失/部分） | [待办:144](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:144) | 全文保留 |
| R038 | D27 · 侧聊、检查器、slash、子 Agent 追问（缺失/部分） | [待办:145](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:145) | 全文保留 |
| R039 | D27 · 侧聊、检查器、slash、子 Agent 追问（缺失/部分） | [待办:146](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:146) | 全文保留 |
| R040 | D27 · 侧聊、检查器、slash、子 Agent 追问（缺失/部分） | [待办:147](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:147) | 全文保留 |
| R041 | D27 · 侧聊、检查器、slash、子 Agent 追问（缺失/部分） | [待办:148](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:148) | 全文保留 |
| R042 | D27 · 侧聊、检查器、slash、子 Agent 追问（缺失/部分） | [待办:149](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:149) | 全文保留 |
| R043 | D27 · 侧聊、检查器、slash、子 Agent 追问（缺失/部分） | [待办:150](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:150) | 全文保留 |
| R044 | D28 · 工作区、Review、预览与文件 URL（部分） | [待办:158](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:158) | 全文保留 |
| R045 | D28 · 工作区、Review、预览与文件 URL（部分） | [待办:159](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:159) | 全文保留 |
| R046 | D28 · 工作区、Review、预览与文件 URL（部分） | [待办:160](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:160) | 全文保留 |
| R047 | D28 · 工作区、Review、预览与文件 URL（部分） | [待办:161](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:161) | 全文保留 |
| R048 | D28 · 工作区、Review、预览与文件 URL（部分） | [待办:162](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:162) | 全文保留 |
| R049 | D28 · 工作区、Review、预览与文件 URL（部分） | [待办:163](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:163) | 全文保留 |
| R050 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:171](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:171) | 全文保留 |
| R051 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:172](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:172) | 全文保留 |
| R052 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:173](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:173) | 全文保留 |
| R053 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:174](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:174) | 全文保留 |
| R054 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:175](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:175) | 全文保留 |
| R055 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:176](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:176) | 全文保留 |
| R056 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:177](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:177) | 全文保留 |
| R057 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:178](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:178) | 全文保留 |
| R058 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:179](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:179) | 状态更新；剩余范围保留 |
| R059 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:180](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:180) | 全文保留 |
| R060 | D29 · Provider、模型、设置与桌面偏好（部分） | [待办:181](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:181) | 全文保留 |
| R061 | D30 · H5、公网配对与远程权限（缺失/部分） | [待办:187](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:187) | 全文保留 |
| R062 | D30 · H5、公网配对与远程权限（缺失/部分） | [待办:188](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:188) | 全文保留 |
| R063 | D30 · H5、公网配对与远程权限（缺失/部分） | [待办:189](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:189) | 全文保留 |
| R064 | D30 · H5、公网配对与远程权限（缺失/部分） | [待办:190](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:190) | 全文保留 |
| R065 | D30 · H5、公网配对与远程权限（缺失/部分） | [待办:191](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:191) | 全文保留 |
| R066 | D31 · 输入、权限、问答与后台控制（部分） | [待办:213](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:213) | 全文保留 |
| R067 | D31 · 输入、权限、问答与后台控制（部分） | [待办:214](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:214) | 全文保留 |
| R068 | D31 · 输入、权限、问答与后台控制（部分） | [待办:215](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:215) | 全文保留 |
| R069 | D31 · 输入、权限、问答与后台控制（部分） | [待办:216](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:216) | 全文保留 |
| R070 | D32 · 完整默认工具与 CLI（部分） | [待办:222](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:222) | 全文保留 |
| R071 | D32 · 完整默认工具与 CLI（部分） | [待办:223](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:223) | 全文保留 |
| R072 | D32 · 完整默认工具与 CLI（部分） | [待办:224](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:224) | 全文保留 |
| R073 | D32 · 完整默认工具与 CLI（部分） | [待办:225](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:225) | 全文保留 |
| R074 | D32 · 完整默认工具与 CLI（部分） | [待办:226](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:226) | 全文保留 |
| R075 | D33 · Native 壳与 UI 状态（保留、待联调） | [待办:254](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:254) | 全文保留 |
| R076 | D33 · Native 壳与 UI 状态（保留、待联调） | [待办:255](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:255) | 全文保留 |
| R077 | D33 · Native 壳与 UI 状态（保留、待联调） | [待办:256](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:256) | 全文保留 |
| R078 | D33 · Native 壳与 UI 状态（保留、待联调） | [待办:257](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:257) | 全文保留 |
| R079 | D33 · Native 壳与 UI 状态（保留、待联调） | [待办:258](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:258) | 全文保留 |
| R080 | D33 · Native 壳与 UI 状态（保留、待联调） | [待办:259](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:259) | 全文保留 |
| R081 | D33 · Native 壳与 UI 状态（保留、待联调） | [待办:260](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:260) | 全文保留 |
| R082 | D33 · Native 壳与 UI 状态（保留、待联调） | [待办:261](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:261) | 全文保留 |
| R083 | D33 · Native 壳与 UI 状态（保留、待联调） | [待办:262](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:262) | 全文保留 |
| R084 | D34 · 持久化与兼容性（部分/已知失败） | [待办:272](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:272) | 状态更新；剩余范围保留 |
| R085 | D34 · 持久化与兼容性（部分/已知失败） | [待办:273](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:273) | 全文保留 |
| R086 | D34 · 持久化与兼容性（部分/已知失败） | [待办:274](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:274) | 全文保留 |
| R087 | D34 · 持久化与兼容性（部分/已知失败） | [待办:275](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:275) | 全文保留 |
| R088 | D35 · 完整桌面等价验收（待验收） | [待办:279](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:279) | 全文保留 |
| R089 | D35 · 完整桌面等价验收（待验收） | [待办:280](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:280) | 状态更新；剩余范围保留 |
| R090 | D35 · 完整桌面等价验收（待验收） | [待办:281](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:281) | 全文保留 |
| R091 | D35 · 完整桌面等价验收（待验收） | [待办:282](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:282) | 全文保留 |
| R092 | D35 · 完整桌面等价验收（待验收） | [待办:283](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:283) | 全文保留 |
| R093 | D35 · 完整桌面等价验收（待验收） | [待办:284](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:284) | 全文保留 |
| R094 | D35 · 完整桌面等价验收（待验收） | [待办:285](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:285) | 全文保留 |
| R095 | D35 · 完整桌面等价验收（待验收） | [待办:286](/Users/nanmi/.codex/worktrees/d7f5/claude-code-haha/.agents/plans/go-desktop-parity-todo.md:286) | 全文保留 |

## 证据与范围

[逐条原文/当前原文和附件哈希](go-work-scope-audit.json)、[当前主清单](go-desktop-parity-todo.md)、[API/WS/native 索引](go-desktop-api-checklist.md)、[CLI/TUI 索引](go-cli-tui-work-checklist.md)。

静态输入哈希核对：649 个桌面/API输入和260个CLI/TUI输入没有漂移。数量、工作项归属、原路径保留及文档链接按当前文件校验。没有运行功能复现、产品测试、模型请求或 UI 操作，也没有本轮产品实现改动。

梳理交付完整不代表 Go 产品等价。D01–D35 的实际能力、原生整包、跨端恢复、平台与原 Benchmark 都必须按主清单后续关闭，完整迁移状态保持 false。
