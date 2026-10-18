# 原生 StatusLine 消费者：2026-10-14 有界证据

这一轮实现了普通 external CLI 的实际 StatusLine 命令挂载、回调 JSON、设置热更新、取消和终端呈现，并修复了实际小模型队列遗漏额度回调的差异。冻结原版为 `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，原版 Bun 1.3.14，Go 1.25.7，实际宿主为 macOS。这里记录局部消费者验收，不是完整迁移完成声明，也不是最终候选的桌面/Web/CLI 或 Benchmark 验收。

完整命令、每条日志 SHA256、源码字节 SHA256 和原始成功/失败输出保存在 ignored `artifacts/go-migration-20261013/statusline-consumer-evidence-20261014/receipt-v1.json`。测试使用临时 HOME/ConfigDir、假的未来有效订阅凭据、API key、loopback HTTP、实际 shell/Read/Write/PTY。没有真实 Claude OAuth 登录、刷新或 live-provider quota。原版 fixture 的原生边界拒绝外部请求和非私有路径；Go 的交互构造使用私有预热 producer 与 CONNECT 守卫，相关原始测试均输出 zero external attempts。

| 实际消费者 | 本轮观察结果 |
| --- | --- |
| 原版真实 React StatusLine → executeStatusLineCommand → shell stdin/stdout | 设置/信任/禁用、300ms 的 model/permission/Vim 触发、命令热更新、padding/style 不额外执行、session model override 对照 passed |
| Go 同一 MainPrint/Controller/Host → owned shell | 初始与变化回调、实际 cost/context/模型/Vim 数据、foreign cache、关闭后拒绝 late invocation、held shell 与同时 held model 的全局关闭 passed |
| 原版真实 PromptInputFooter | Bash/help/paste/exit/suggestions 的卸载/重新挂载；保留 AppState text、实际 held PID 回收；fullscreen 23/24 行边界、短 scrollback 可见及 suggestions 隐藏对照 passed |
| 真实 Go TUI startup 失败 | 后续 binding 失败发生于 Program.Run 之前；完成回调不阻塞、held shell 取消/join、PID ESRCH passed |
| 同一原版 API → process-global raw utilization → mounted callback | 订阅成功、429、API key 负条件、-0、超过 uint64 的 radix、二进制舍入边界 5 profiles passed |
| 原版 generateSessionName → queryHaiku → 真实 HTTP → StatusLine | 真 RED：Go 名称成功返回，但丢失 62.5% 额度。补 RunQueued 回调转发后，订阅成功/429/API key 负条件 3 profiles passed |
| 实际普通 Agent → child Read → held child 下一次 HTTP | 4 次父子 POST 共享假的父 Bearer，子查询完成的 37.5% 额度进入父实际命令 stdin；后续无额度 header 响应清空 process 状态 passed |
| 真实公开 RunCommand PTY | `/he` 补全与物理 bracketed paste 开始取消 held PID（原 2 秒窗口），隐藏时热更新零命令；实际 Vim 清草稿、paste-end、`/help` 重新挂载；held `/exit` 原 3 秒窗口 join；4 次模型 HTTP、实际 Write/Read、Go 和 slave termios 恢复 passed |
| 原版 Ansi/StyledText/Yoga 呈现 | 307 profiles 的 6 个主题、OSC8、SGR/extended colors、控制序列丢弃、dim/bold、换行、宽字符、宽度 0/1、负/小数/超额 padding、原版 truncate cell/style projection passed |

`RunQueued` 与远端标题、自动标题、rename、Insights、Web、hook、auto-dream、BTW 使用同一原版 model producer。新增额度 forwarding 复用实际 native callback 和同 Owner/bootstrap；没有改变 purpose whitelist、auth/stat/header 采样、模型权限、取消或 finite capability guard。17 个相关真实辅助消费者定向 race：24 pass events、0 skipped/failed。完整 modelyield/queryloop race：406 pass events、93 top-level passed；4 个 skip 是只允许子进程显式运行的 fixture entry，实际 Source 对照均执行。

最终 StatusLine 18 项选择中，application、clibootstrap、sdkpermissions、clisettings passed；TUI 构建遇到新 cached-teams 文件列表捕获窗口，原 build-only 记录保留。声明可见后，3 个 TUI 原版 ANSI/实际 mount 消费者 fresh race passed（3.215s），8 个相关包 fresh vet passed。真实公开 PTY 独立最终 pass 的 package 时间 19.092s，随后 aggregate 中亦 passed。`git diff --check` passed。没有用这些 focused 结果替代完整质量门禁或 90% 覆盖率。

真 RED 日志分别保留小模型额度遗漏、早期 modal mount、旧 completion/paste mount predicates 和先前数值转换函数。后两项是仅回放具名旧函数/消费者的 Go overlay，不是假称完整历史快照。缺私有 rg 导致 Agent 未发现、Source 新 probe 的计数/索引错误、把 Vim INSERT 的 Esc 当补全菜单关闭，以及并发新文件未纳入构建的输出都单列为无效 fixture/build-only；不计作产品 RED。

后续实际 `?` HelpOpen 入口已补：真实公开 PTY 打开 shortcut help，原 2 秒窗口回收 held shell，帮助可见期间设置热改零新命令，raw Esc 关闭且不触发 rewind，fresh remount 执行新脚本。原模型/Write/Read/退出 3 秒/double-termios 强断言保留。`test-1791958392553.log` application race 22.472s passed；后续 `/help` General 已换为原 Source『Claude understands your codebase』的入口观察，最新同一完整公开回归 `test-1791960283748.log` race 24.023s passed，0 skip/fail。小型 receipt `help-open-public-v2.json` 保存原日志 SHA；metadata/menu 与完整按键行为的 Source 证据由 TUI peer 分开所有，不能把此单一 `?`/Esc 路径外推所有 Help 快捷键。

后续限定 Help 按键链见 `go-prompt-help-input-key-consumers-evidence-20261014.md`：18 个实际原版 keyboard profile 和完整 Vim／UTF16 消费者通过；真实公开 PTY 增加 Undo→补全→Stash 保留 Help、期间零 StatusLine 新命令，随后正确 `/help` 和原四次 HTTP／Write／Read／退出／双端 termios，race 23.891 秒通过。原观察断言错误、实际 Vim 旧草稿复活 RED、准备依赖及编译中间态均分开保留。

仍开放：全部 callback 字段（例如恢复后的 worktree/remote/session name/added_dirs、同 UUID 改 usage 的 200k cache）尚无完整原版 caller 对照；全部 Help Global／REPL caller、完整 ExternalEditor／非空 clipboard／text-paste、Stash cursor/mode／usage-tracking 和 exitMessage UX 未完全验收。实际宿主终端 colour/hyperlink capability 与样式耦合属于 Root 后续验证，307 静态原版 projection 不外推全部 OS/TTY。Windows 实机、完整最终质量门禁、同版本桌面/Web/CLI、120 对 Benchmark 未在这一轮执行。SDK cold title 原有差异仍开放，本轮未通过延迟、flush 或改变选择顺序来处理它。
