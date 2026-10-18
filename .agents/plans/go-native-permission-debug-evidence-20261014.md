# 原生权限调试入口证据

2026-10-14，冻结 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`。这是聚焦证据，不代表完整迁移、最终门禁或三个产品表面已经通过。

普通 CLI 的实际模型工具调用进入共享 Harness/Session；交互式 NativeProcess 的 Bash/PowerShell 请求附加只读快照，来自当前 Host 权限上下文、实际 debug 参数和 sandbox 可用性。调试不重新判断权限。字段为 `json:"-"`，没有新的持久化或桌面 API 形状。

Confirmation 的 `permission:toggleDebug` 不依赖提示开关。调试页替换 Select，确认、拒绝、导航和编辑消费者退出，再次切换恢复。更新保留当前 ID，关闭、取消及旧 ID 退役后不复用视图；现有 Global 消费者保留。

实际原版对照：

- 384 个来源/ask/deny/双规则/sandbox 组合，原解析、不可达规则及模式/来源显示：race **passed，2.403 秒**。
- 完整原 Bash/PowerShell、Ink、KeybindingProvider、默认绑定和 Select，四种 shell/提示组合共 44 帧：调试时 Enter/y/n/Esc/上下箭头/Tab 没有 allow/reject，恢复后仅一次 allow 和 done，**passed**。
- 13 种原完整 DebugInfo 输入 × 100/47/24 列，共 39 个完整去 ANSI 文本/换行比较：规则、目录、最后模式、嵌套子命令、不可达筛选及真实窄屏浮点布局，**passed**。这不是六主题全部 ANSI 属性的逐字对照。
- 实际 TUI State 选项卸载、同 ID 更新、关闭后 ID 复用、取消及非 shell 拒绝进入，**passed**。

上述 DebugInfo、Bash/PowerShell 组件、shadowedRuleDetection、PermissionMode、PermissionUpdate、permissionRuleParser、settings/constants 和默认绑定与冻结 MAIN 的 diff 均为空。

组合日志 `artifacts/go-migration-20261013/permission-debug-render-fork-v5.log`：TUI **4.360 秒 passed**，application 实际 fork **4.464 秒 passed**。Source 只用私有 HOME/ConfigDir、假密钥、外部能力拒绝边界。早期夹具截断/EAGAIN、配置未初始化、继承 PATH、缺少必填字段以及真实布局失败输出全部保留；没有删场景或改变期待。

公开 CLI PTY：Bash/私有官方 PowerShell 7.6.6 × 无/有 `--debug`，四组，每组三次实际 loopback HTTP。工具生成真正权限请求；展开后九种按键不审批、不产生文件/下一 HTTP；切回后实际写文件、下一 human 请求继续，双侧 termios 恢复。application race **passed，21.291 秒**，日志 `artifacts/go-migration-20261013/cli-flags/native-terminal-permission-debug-public-v4.log`。

Snapshot 回归：实际实时上下文变化、副本不改变 Host、真实 CLI 参数解析、仅 debug 文件不显示提示，以及 nil/foreign/retained/closed scope，bootstrap race **passed，5.802 秒**；独立重复日志 `artifacts/go-migration-20261013/cli-flags/native-terminal-permission-debug-snapshot-v5.log`。

第六份快照的实际 desktop fork 测试在 idle 后立即读盘，可能早于原版独立 Record 提交。现在观察实际 `OnCanonicalTranscriptMutation` 写入提交通知，保持 15 秒原窗口，不调用人为 flush/sleep、不改 Harness 顺序；父历史不变，fork 的实际 Write/Read 文件与回传、最终 assistant、forkedFrom 完整落盘。重复 race **passed，2.30/2.27 秒**，原快照失败保留。

PowerShell 的 macOS 私有实物执行不代表 Windows 实机。没有真实 Claude OAuth、真实模型请求、提交、推送或合入 main。完整 Go、最终覆盖率、正式 120 对 Benchmark 和最终候选桌面/Web/CLI 仍待完成。
