# 自动更新已安装子进程覆盖证据

本批是确定性有界消费者复验，不是最终桌面发布验收、全量门禁或 90% 通过。

checkpoint12 原 `TestTerminalAutomaticUpdaterActualGoReleaseMinimumVersionFailureAndCancellation` 因安装后执行的二进制未登记实际构建身份失败，原日志与冻结输入保留。`terminalUpdatePrivateGoRuntime` 现在仅在原 `go build` 成功后调用既有 `gochildcoverage.RegisterBuilt`，再读构建文件并由原安装器下载、校验、安装和执行。没有修改更新生产代码、版本或 minimumVersion 规则、失败文件保留、取消、重复请求与 Close 断言，也没有延长原 45 秒构建窗口。

从 checkpoint12 的 12,883 个冻结输入独立复制，仅覆盖此测试文件。v1 原消费者实际通过，但严格 collector 拒绝该有界 parent 缺少 CLI package 的编译器 blocks；此失败保留。v2 加入原 default-tag `cmd/claude-haha` parent package（该 selector 无匹配测试，不计测试通过数），完整 `./...` 和两 controlled forks 的 coverpkg 保持不变。

v2 命令实际 exit0、总 99.112 秒，application 原测试 13.14 秒、package 14.726 秒。实际更新/安装/`--version` 产生一个已登记二进制、一个真实 counter file；严格合并 225 个执行 blocks、15 个新增命中 blocks。原 parent 189,831 compiler blocks、375,957 可执行行分母完全保持，命中行 11,522→11,539；不能把这一有界比例用于正式覆盖率判断。仅保留原规则中两个已有 test-only tag adapters 的默认编译 inventory 归属，未增加白名单。源快照检查前后 SHA 相同，原 checkpoint12 未变。

完整 ignored 收据：`artifacts/go-migration-20261013/native-updater-installed-counter-bounded-receipt-20261014.json`，含原失败、v1 严格拒绝、v2 命令/日志/manifest/receipt SHA。全量最终 unit/race/90%/verify、同版本桌面/Web/CLI、正式 120 对仍未通过。真实 Claude OAuth 按用户要求排除；未合 main。
