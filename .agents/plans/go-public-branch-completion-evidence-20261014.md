# 公开 branch/fork 完成回执

原 checkpoint12 第三 human 前缀丢失，当前原公开 PTY 也真实失败（第三内容只剩 `_HUMAN`）；原失败保存。实际 whole Source branch.call 的 held context.resume 证明，物理 fork 文件已经存在时 onDone 仍为零，只有 resume 完成后才发布 `Branched conversation … You are now in the branch`。当前两个完整原版模块与 frozen MAIN 字节相同，两 profile 真实执行 exit0/blocked0、父文件不变。

仅修原 Python 验收驱动：文件 materialized 后等待上述实际可见 onDone 回执，再发送下一 human。未增加 sleep、模型请求、重试、Go 生产修改；原 25 秒驱动、35 秒 child、120 秒构建总窗口不变，原 branch/fork 别名、三次真实模型 HTTP、完整三条 human、parent bytes 不变、新 session ID 与双 termios/exit/零外网断言全部保留。

同一公开原测试 `-p=1 -race -count=3` 三次通过（8.25/5.24/4.35 秒，package 20.008 秒，总 108.715 秒）。ignored 完整收据为 `artifacts/go-migration-20261013/branch-source-done-public-bounded-receipt-20261014.json`，含 Source/旧失败/新日志/SHA。这是中间候选有界验证；正式全量/90%/verify、cold title P0、同版本三端及 120 对仍未完成，未合 main。
