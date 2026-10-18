# 冻结 Source 的剩余终端入口审计

2026-10-14。对照 `6d21316ec4635f29ecbaa43e1a5574bfd31d7155` 的 `src/commands.ts` 外部表、各命令声明和实际 loader/call，以及当前 Go `compiledPrintCommandDeclarations`、`nativeBuiltinPromptCommands`、`Session.setCommands`、`InvokeTerminalCommand`、`ordinaryPanelName`。这是有界接线审计，不是迁移完成或最终候选验收。历史 CLI/TUI 检查表的“尚未迁移”描述不能直接用作当前结论。

## 2026-10-14 盘点时仍缺实际入口

| Source 入口 | 原类型与条件 | 实际业务 / 当前缺口 | 分工 |
| --- | --- | --- | --- |
| `diff` | local-jsx，无 gate | DiffDialog 的未提交 git / 每轮来源、文件列表与详情；现有 IDE permission diff 不替代此入口 | CLI peer |
| `branch` / `fork` | local-jsx；冻结 FORK_SUBAGENT=false 时有 fork 别名 | 创建物理分支转录、保留父会话，经 REPL resume 切入分支；启动 `--fork-session` / HTTP branch 不替代 slash | CLI peer |
| `resume` / `continue` | local-jsx，无 gate | picker / UUID / title 搜索、同仓库及所有项目、跨项目命令复制、实际会话切换；启动 resume picker 不替代 slash | 待接，建议与 branch 同组 |
| `btw` | local-jsx，immediate=true，无 gate | 独立 side-question、实际缓存安全上下文、取消与卸载；不改变主会话历史 | TUI peer |
| `mobile` / `ios` / `android` | local-jsx，无 gate；别名也初始显示 iOS | 实际可扫描 QR、平台 tabs 与退出 | TUI peer |
| `buddy` | local-jsx，无 gate | 原 seed/rarity/stats/sprite、hatch/pet/mute/unmute/release、global 与 AppState 消费 | TUI peer |
| `stickers` | local；非 headless，无 gate | 实际 BROWSER / OS opener，成功或失败文本及 canonical local command history | TUI peer |
| `plan` | local-jsx，无 gate | 进入 plan 权限模式、description 触发同 Harness、已有计划显示、open 编辑器 | Root |
| `statusline` | prompt；disableNonInteractive=true，无 gate | 原 statusline-setup Agent prompt 与 allowedTools / 后续实际配置 | Root |
| `terminal-setup` | local-jsx，无 isEnabled；原生 CSI-u 仅 hidden | Apple Terminal / VSCode / Cursor / Windsurf / Alacritty / Zed 的真实安装、备份、全局 flags | Root |
| `rewind` / `checkpoint` | local，无 gate | 原 skip 与现有 selector 已接通；6 原调用组合、普通 Session 和两个 alias 公开 PTY 有界 passed，详见 progress | Root |
| `sandbox` | local-jsx / immediate；无 isEnabled | 支持平台 / enabledPlatforms / 依赖 / managed 锁条件，交互 settings、exclude 写入与实际 sandbox 下一执行 | 未分工 |
| `team` | prompt；实际 teams opt-in 与 killswitch（默认 true） | 原团队 prompt/allowedTools；已有团队工具与 runtime 并不注册此命令 | 未分工 |
| `feedback` / `bug` | local-jsx；provider、privacy、env、policy 条件；不要求 claude-ai availability | 实际报告表单、提交、取消及错误；不能按“无 OAuth 账户”直接当不可达 | 未分工 |
| `passes` | local-jsx；只有 isHidden(eligibility/cache)，无 isEnabled | 可直接调用；first-visit global、实际 Passes API/UI；Export 营销缓存不是此命令 | 未分工 |
| `keybindings` | local；真实 customization gate 默认 false | 原模板、WX、编辑器、热更新、异常结果及公开 PTY 有界 passed；修改 ConfigDir 的前置 host guard、Windows 等仍 OPEN | Root |
| remote `session` / `remote` | local-jsx；真实 remote-mode 条件 | 当前 wrapper 有 session URL 输出；alias /remote 与原 QR/dialog 仍需独立完整消费审计 | CLI remote 组 |

## 账户或运行时条件项，不能当作 build-false stub

`chrome`（claude-ai 且 interactive）、`desktop` / `app`（claude-ai 且支持平台）、`install-slack-app`（claude-ai）、`install-github-app`（claude-ai **或 console**，另 env gate）、`remote-env`（subscriber 且 allow_remote_sessions）、`upgrade`（subscriber、非 enterprise、env gate）、`extra-usage`（实际 overage eligibility）、`rate-limit-options`（subscriber；hidden 不等于不可调用）、`privacy-settings`（consumer subscriber），以及 `login` / `logout`（非 3P 表注册、各 env gate）均仍需要各自 loader/call 端到端消费者。已有 native CLI auth、Chrome worker、Usage/Export 账户数据不能代替对应 slash 面板。用户明确禁止真实 Claude OAuth；fake-account、loopback、abort 与 Source 契约仍可验证，真实账户结果保持未验收。

`advisor` 的 GB `tengu_sage_compass` 默认 `{}`；`ultrareview` 的 `tengu_review_bughunter_config` 默认 null；`think-back` / `thinkback-play` 的 `tengu_thinkback` 默认 false。必须按真实缓存/信任 feature seam 验证阳性条件，不能伪造 env 放行。`files` / `tag` 为 USER_TYPE=ant 条件；tag 已有独立消费者，files 仍需核定。

冻结构建未启用的 PROACTIVE/KAIROS/BRIDGE_MODE/VOICE_MODE/CCR_REMOTE_SETUP/FORK_SUBAGENT/UDS_INBOX/TORCH 等声明和原 INTERNAL_ONLY_COMMANDS 保留原 gate；没有把这些 stub 扩成普通外部表。QUICK_SEARCH、TERMINAL_PANEL、MESSAGE_ACTIONS、VOICE_MODE 等按键同理。`DiffDialog` 的默认动作尚依赖缺失 `/diff`；`selection:copy` 的原 fullscreen 选择状态/原 mouse 生命周期与 Go renderer 仍需单独 consumer 审计。

## 已接线但证据有界

当前可从 Session/TUI 真入口到达 help、permissions、tasks、skills、agents、mcp、memory、hooks、config/settings、status、theme、output-style、plugin aliases、reload-plugins、add-dir、context、effort、stats、copy/export/rename/color、clear aliases、vim、条件 usage/workflows/fast，以及独立 model/doctor/ide。local/prompt 管线还有 compact、cost、heapdump、release-notes、goal、review、agent/init/pr-comments/security-review/insights、动态 skills/workflows/plugin/MCP。五个默认动作、Vim、真实 keybinding watcher、媒体、权限 debug、IDE permission diff、PR footer、Teams/pane、退出和原 PTY 有分项 Source/physical/race 证据。

以上只表示已有生产链和分项证据；不承诺完整菜单/所有参数与失败分支。例：插件更新、旧 manifest、卸载后配置、raw UTF-16 channel 字段都曾在菜单“已存在”后发现真实缺口。最近 raw-key v5 的 Source+public+旧配置回归已通过（application30.661s、TUI4.694s），全 TUI 693 events 是较早快照，均不能关闭本审计的新入口。

最终同版本桌面/Web/CLI、全部质量门禁、90% 覆盖与 120 对 Benchmark 仍由 Root 在同一冻结候选上汇总；diagnostic09 不包含随后 raw-key UI 修复，也不是最终候选。

## 2026-10-14 页脚追加边界

- 当前任务/团队/伙伴导航已有完整原 PromptInput handler 的 12 组真实挂载键盘对照；真实 running/background/tree 显隐、顺序、自定义组合键、草稿、消失后不复活均 passed。团队快照来自实际 teamContext，只有队长时仍能导航，状态文本却为空；没有从 BackgroundTasks 推断团队人数。
- 原 BackgroundTaskStatus/TeamStatus 372 组（六主题、100/24 列、任务类型、选中/闲置/彩色 IP 成员）原组件与真实 caller 的文本/每格样式对照 passed。窄列真实 RED 修复了 Box 边界舍入、Text AtMost 二次测量与覆盖；原失败日志保留。缓存团队 getter 12 组、实际 TeamCreate/Delete/订阅退订及真实 Global hint 写入 passed。
- 公开 RunCommand PTY 已实际从 Bash 页脚 Down→Down 打开任务面板，输出/停止/草稿/下一轮三次模型 HTTP 与 Go 和 Python slave termios 恢复 passed（leader 存活时检查）；本轮完整 TUI race passed 88.967s。这些记录不是最终不可变候选门禁。
- TeamsDialog 的 typed mount/retire、真实 service/actions 已接线，但完整原画面与公开 pane/mailbox/退出流程仍在验收，不算整项关闭。`HelpOpen` 的原快捷键页脚消费者尚待实现；Bridge 仍保持冻结 BRIDGE_MODE 禁用/issued external grant 拒绝边界，不能把 CCR 状态当 Bridge。
- HelpV2 仍是明确缺口：原 Commands.tsx 先按 name 保留首项并 localeCompare 排序，原 HelpV2 有 general/commands/custom-commands tabs；当前 Go 的 flat Help 直接循环 commandSnapshot，会重复行且没有这些原 tab 消费者。不能据已有 `/help` 打开或旧六面板 PTY 宣称完整 Help 已通过。
