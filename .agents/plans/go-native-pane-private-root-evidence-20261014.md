# Native pane 私有根路径证据

本批只修 `native_process_fixture` 测试构造器，不改变 release 生产路径、原始 launch 环境或普通 ConfigDir guard。checkpoint 12 中真实 tmux 子进程的 `/var/...` 私有根与 native CWD 的 `/private/var/...` 是同一物理目录，原字面 `filepath.Rel` 拒绝了该入口。

新增比较先解析实际根及现有路径的符号链接；尚未创建的路径只拼回最近存在祖先后的缺失尾部。HOME、TMPDIR、effective CWD 和 ConfigHomeDirectory 都必须物理位于同一私有根。悬空链接、链接至外部及其缺失尾部、字面外部路径继续拒绝。构造器不重写任何 raw 环境字符串。

原版 fail-first：`go test -p=1 -tags native_process_fixture ./cmd/claude-haha -run '^TestNativeFixturePhysicalRootAliasesMissingTailAndSymlinkEscapes$' -race -count=1 -json` 在实际 alias/inside missing-tail 正例失败（1.153 秒）。五项负例也运行，但原 alias 不一致可能掩盖其拒绝原因；不将其独立当充分负面证明。修复后同命令 `-count=3` 通过 21 个 test events、3 次 top-level 执行，0 skip/fail（2.260 秒），保留全部逃逸负例。

Ignored 收据：`artifacts/go-migration-20261013/native-pane-private-root-evidence-20261014/receipt.json`，含原 RED、修后 JSONL 及当前三个 test-only 文件 SHA。真实原 pane/daemon 全消费者后续结果另附；间接 tmux child 丢失 GOCOVERDIR 是独立 collector 传递问题，此路径修复不宣称已解决覆盖率。

真实原入口后续复播暴露旧 peer 将新增启动 HEAD 计入模型 POST，导致第一条真正模型输入直接走完成分支、没有 Write（25.38/45.04 秒失败，raw 保留）。两测试只在原 body/counter 前加入独立 HEAD204；验证其一次请求、无 Authorization/X-Api-Key、UA Bun/1.3.14，原 POST/物理 Write/生命周期断言与窗口保持。`TestActualPaneProducerStartsGoCLIBinaryFromPrivateTmuxInitialInboxWritesAndParentCloseKills` 和 `TestTerminalTeamFooterRealDaemonToolSpawnShutdownApprovalRosterAndUnsubscribe` 同私有 env 原窗口 `-p=1 -race -count=1 -json` 通过 2 tops/2 events、0 skip/fail（13.491 秒）。一个额外新增 `posts==2` 断言不符合首项在 artifact 出现即关闭父进程的原时序，已移除并将该次作为无效新增 fixture 断言单列，不计产品 RED。

后续实际间接 tmux 计数 v4 已通过 fresh manifest 的 strict merge；历史路径文件与当时 SHA 保留，新增/移动 helper 的当前字节及拒绝历史见 `go-native-pane-real-counter-evidence-20261014.md`。不将这一两入口结果外推完整覆盖率。
