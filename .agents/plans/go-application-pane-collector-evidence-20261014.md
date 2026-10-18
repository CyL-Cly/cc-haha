# 三个原应用层 pane 入口复验

checkpoint12 的 iTerm→tmux、parent roster/PR footer、Teams dialog 三个原入口仍失败，当前原代码 fresh race 也全部失败，总 190.263 秒。夹具声明私有根为 HOME 子目录，但实际 work 是其同一临时父目录下的 sibling；两个 Python carrier 又把 TMPDIR 指向外层 task temp。现仅修复夹具声明为该实际临时父目录，给子进程创建其内的私有 TMPDIR；iTerm 夹具单独处理原 unauthenticated Bun init HEAD。生产 guard 不放宽，原模型请求、Write 产物、roster/shutdown/PR、未知字段、下一 human、双 termios、退出/所有 PID 回收断言与原 55/45/60/50 秒期限保持。

fresh race 三原 tops passed 29.33/11.82/13.33 秒，application package 56.695 秒、总 157.889 秒。随后仅以既有 strict IndirectEnvironment 接真实 CLI→tmux；两个 Python 重执行 carrier 由父测试对已构建 CLI 创建独立、已登记的 raw collector，再显式传给原 helper，atomic 模式外不传。CLI 自己的 test tag 在真实 metadata、binary/Source SHA、manifest attestation 成功后才允许采集。

首次仪表 v1 三实际消费者均通过，但严格 collector 拒绝两个残留零字节 tmp.covcounters；原 raw 全保留。这是正常 os.Exit 与 SIGHUP 快照交叠，生命周期组在原 test-only helper 串行两种写入并保持原正常 exit code 或 SIGHUP waitstatus，另有真实机制/negative 证明；release 无改动。微型旧并发测试未复现的结果不计产品 RED。

fresh v2 manifest 与独立冻结源码下，三原 tops passed 11.79/10.08/14.24 秒，application package 37.798 秒、总 145.648 秒；strict merge passed，2 个真实 binaries、7 个 counter files、5 个 child profiles。189,906 compiler blocks、376,127 可执行行分母在该同版本合并前后保持，covered lines 24,095→29,818。原两已有 tag adapters 仍沿原 default inventory 规则处理；未放宽 artifact 校验、添加 source 豁免、删除临时 raw 或降低 90%。Source SHA 前后相同，checkpoint12 的 12,883 输入未变。

完整 ignored receipt：`artifacts/go-migration-20261013/application-pane-private-inputs-bounded-receipt-20261014.json`，含所有原失败、fresh race、仪表失败与通过、manifest/inputs/日志 SHA。本批不是完整最终 unit/race/90%/verify；最终同版本桌面/Web/CLI、外部平台与正式 120 对未完成。真实 Claude OAuth 按用户要求排除，未合 main。
