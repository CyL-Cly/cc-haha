# 原 tmux 子进程真实计数证据

本批仅修改 `native_process_fixture` 测试构造器与 testdata coverage instrumentation，不修改 release 行为、raw launch 环境或普通 ConfigDir admission。真实 macOS 原 SIGHUP 终止保留，未调用业务 Close、未增加等待或修改原 45 秒窗口。

旧真实两入口在路径/HEAD 修复后已经通过；第一次 instrumented v1 同样通过，但子进程只有实际 atomic metadata、没有 counter。SIGHUP 的默认终止不会自动落盘计数，故 v1 不计覆盖率通过。后续使用实际非 test Go executable 验证 strict build/receipt/source/binary/runtime metadata attestation；仅 opt-in 原 SIGHUP 处 `runtime/coverage.WriteCountersDir`，随后 Reset 并重新发送同一 SIGHUP。机制 2 tops/6 negatives、8 pass events，真正计数与原信号 waitstatus 均通过；不注册或 symlink/outside/非 atomic/源码不符拒绝。

真实 pane v2/v3 两原消费者及 counter 都已跑通，但严格合并分别拒绝新 tag-only coverage wrapper 和 guard 文件不在默认 parent 分母。保留拒绝，没有新增忽略名单。最终把这两 helper 移到既有原 tagged adapter，collector 原有两项 default-tag 差异继续沿原规则处理。

v4 两原入口 `TestActualPaneProducerStartsGoCLIBinaryFromPrivateTmuxInitialInboxWritesAndParentCloseKills` 与 `TestTerminalTeamFooterRealDaemonToolSpawnShutdownApprovalRosterAndUnsubscribe` 在实际 loopback HEAD、原 POST/Write、真实 tmux/pane PID 退出/daemon roster/unsubscribe 断言下通过 15.41 秒与 9.61 秒，bootstrap package 26.879 秒、0 skip/fail。新的 fresh manifest 包含本次所有源码 SHA，未复用旧计数或改标源码。

strict merge 获得 1 actual binary、3 counter files、2 child profiles、29,106 executed blocks 和 8,932 newly covered blocks。当前同 source 默认 parent 的 executable statement 分母 376,110 前后完全相同，covered statements 12,869→26,414；compiler blocks 189,899，pre/post Source SHA 相同。分母随新增真实 utility 源码相对旧 v1 增加，不能当跨版本分母相同。

完整原始失败、机制、v2/v3 拒绝、v4 compiler parent/真实计数/profile/raw JSONL 及 SHA 在 `artifacts/go-migration-20261013/native-pane-real-counter-evidence-20261014/receipt.json`。这只证明两原间接 tmux 入口和信号计数机制；其它公开 TUI 重执行 carrier、最终 tagged same-area rerun、完整 Go/90%、Desktop/Web/120 未在此批关闭。

随后 Root 三原 Application consumer 实际业务通过，但 strict merge 正确拒绝 PID88090/88152 的零字节 `tmp.covcounters`。不删除或忽略文件。两旧微型并发机制（正常23与 SIGHUP 同信号的真实监听）各32次没有复现；不将它们标作 RED，也不以它们断言实际应用调度因果。Go1.25.7 Source runtime 的 dieFromSignal 不调用 coverage exit hook，故 normal os.Exit 和独立手写快照可能交叠是具体需关闭的写入边界。

当前仅真实 attestation 成功的 tagged adapter 绑定 serial Exit：正常真实 os.Exit 全程与 SIGHUP snapshot→reset→reraise 同锁；SIGHUP 分支在内核真正投递前不解锁，重复 Install 同一目录只注册一次。未 opt-in/非法目录不绑定，release 不改。fresh 固定机制 v3 四 tops/16 events、0 skip/fail 通过，含6负例、物理 confinement、pure SIGHUP 原 waitstatus/normal23/31实际并发与 real counters；所有源码SHA前后一致。

Root 随后 fresh 重跑三原 Application consumer 与严格 counter merge，v2 全部通过：三个 top 为 11.79/10.08/14.24 秒，Application package 37.798 秒、完整构建收集 wall 145.648 秒。真实 2 binaries、7 counter files、5 child profiles，78,827 executed blocks 与 3,592 newly covered blocks；当前同 source 默认分母 376,127 不变，covered statements 24,095→29,818，pre/post sourceSHA 相等。未忽略/删除 tmp 文件，未改默认 tags/分母。原失败保留；此项关闭真实三入口的计数边界，不外推完整 Go/90% 或其它公开入口。
