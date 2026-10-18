# PromptInput 帮助按键的限定消费者证据

2026-10-14。冻结 Source MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，本机 Bun 1.3.14／Go 1.25.7。此项不关闭全部 PromptInput、完整迁移或最终质量门禁。

新 fixture 从原 `PromptInput.tsx` AST 提取完整 `onChange`、`trackAndSetInput`、Undo／Newline／Stash／Image／Picker callbacks、Chat keybinding hook、Help dismissal 和整个普通 raw-key `useInput` hook；用原 React、Ink、TextInput、useInputBuffer、KeybindingProvider 运行实际 stdin 按键。没有重写这些业务函数。图片用例仅替换 OS clipboard 读取为不可用结果；完整原 empty-clipboard 异步 handler 仍执行。未提供的 editor／mode／队列分支会抛错，不能作为通过证据。

原始 Source→State 回归确认四项 RED：空 Undo、空 Stash、empty clipboard 和自定义 `chat:newline` 均应保留 HelpOpen；Go 曾因为任意 keybinding match 都关闭帮助而错误地退出。自定义换行的原 callback 直接调用 `trackAndSetInput`，插入换行也不走 `onChange`。

修复按实际输入生产者区分：这些 Chat handlers 保留帮助状态，普通 `onChange` 仍关闭／切换帮助；原 Picker 和 Mode callbacks 显式关闭。自定义 `chat:newline` 使用现有 Editor 的实际换行消费者。另一个实际 `?`→custom newline→Stash 反例显示，原 `.trim()===''` 且无 stash 必须保持换行不变；Go 增加这一个 no-op 分支，并复用 JavaScript whitespace 判断。

最初 17 个原版按键 profile，包括空 Undo／Stash／clipboard、custom newline、普通文字、Escape、Return、Model/Thinking Picker、双问号、Backspace、显式 null binding、Ctrl+U、普通可打印自定义绑定、stash restore 后再输入、换行 whitespace stash。原 Source 输出 stderr 与 blocked 均为零。聚焦回归还运行原 binding override／chord timeout／draft preservation、帮助初次挂载和 stale result fence，共 23 个测试事件、6 个 top-level passed，tui race 7.837 秒。

扩展实际公开 Undo／Stash 链随后发现真实 Vim controlled-input 问题：新输入 `/help` 被旧草稿复活为 `/h/helpe`。增加第 18 个原版按键 profile，用原 VimTextInput 和原 Stash callback 执行 stash restore→再次 stash 清空→新键 `/help`，真实 Source 输入 `/`，Go 输入 `/he/`。修复只在 `VimEditor.sync` 记录实际已经消费的新 Editor projection；下一次外部更新即便回到更早的值，也仍会被消费。原完整 Vim state-machine／operators／repeat／UTF16、两个终端 Vim 消费者和 Help 18 profile 合跑 race，465 个测试事件、4 个 top-level passed，tui 10.634 秒。

公开 PTY 的新增 Undo 观察起初要求帮助始终可见，实际 Undo 恢复了 `/he`，补全菜单覆盖帮助。原 `PromptInputFooter` 明确先显示 suggestions 再检查 HelpOpen，因此这是观察断言错误，保留原失败日志。修正观察后才捕获上述真正旧草稿复活问题。新公开链按原顺序验证：Undo 恢复补全，Stash 清空草稿后帮助重新可见，期间已热改的 StatusLine 不得启动；随后实际 Escape remount、正确 `/help`、四次模型 loopback 请求、真实 Write/Read、退出和双端 termios。Vim 修复后该完整公开链 passed，application race 23.891 秒，0 skip/fail，`test-1791963358865.log`。一次编译遇并行重命名 Help test 的旧 testmain 引用，属于 build-only，不计产品 RED；另一轮原 pinned Bun 临时目录丢失属于准备依赖失败，Root 经官方 digest 校验恢复同一 1.3.14 后才继续。

仍开放：所有 Global／REPL 快捷键的完整 caller、发生内容改变的 Undo 缓冲时序、完整 ExternalEditor 及非空 clipboard、所有 Stash cursor/mode／usage-tracking 组合、原 text-paste callback 与帮助的关系。上述条件不在本轮 passed 的限定范围内。最终完整门禁、同版本桌面／Web／CLI、120 对 Benchmark 由主任务处理。
