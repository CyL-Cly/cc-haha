# Checkpoint10 空闲终端菜单用例超时诊断

2026-10-14，本机 Go 1.25.7。此项仅修正确定性测试入口及失败观察，不代表完整迁移或最终门禁通过。

冻结 checkpoint10 的 application 包在 900 秒窗口失败，`TestActualIdleSessionFileSignalUpdatesRunningTerminalMenu` 自身运行 14 分 46 秒。完整栈中测试停在 `io.PipeWriter.Write("/computer")`，终端运行 goroutine 已退出，没有 Owner、订阅或 Tea 处理 goroutine；因此不是接近总套件预算的正常运行，也不是这条消费者的产品死锁。

测试现在以异步管道写入，同时观察写入完成、`tui.Run` 返回和原来的 3 秒输入边界。首次运行真实报告 0.39 秒启动失败：`command is forbidden while the existing continuation is active`。旧夹具沿用默认 `Interactive=false` 的 nativeSessionOptions，再直接挂载终端。实际终端组件 getter 要求交互式 native profile，不能降低该产品 guard。

修复仅在这个终端测试的 `NewSession` 前设置 `Interactive=true`；同文件桌面 HTTP／WebSocket 测试保留原构造。真实文件配置更新使空闲终端补全菜单出现 computer-use，共享 Session snapshot 一致，loopback handler 禁止任何模型请求，取消后终端按原 3 秒窗口回收。

已执行：

```text
bun /tmp/cc-haha-session-lifecycle-tests.ts test -p=2 ./internal/application -run '^TestActualIdleSessionFileSignalUpdatesRunningTerminalMenu$' -race -count=1 -timeout=60s -json
bun /tmp/cc-haha-session-lifecycle-tests.ts test -p=2 ./internal/application -run '^(TestActualIdleSessionFileSignalUpdatesRunningTerminalMenu|TestActualDesktopHTTPConfigWritePushesIdleWebSocketSlashMenu)$' -race -count=3 -timeout=60s -json
```

第一次为旧入口失败证据；第二次 passed，6 个测试事件、0 skip、0 fail，application 5.495 秒。原冻结快照未修改。原超时、启动错误及通过日志已保留在 `artifacts/go-migration-20261013/idle-terminal-file-signal-checkpoint10/receipt.json`，每份新日志有 SHA256。

未运行新的全 application 套件或最终完整质量门禁；由主任务在新的不可变候选副本验证。此项没有 Source 业务修改、真实账户或模型调用。
