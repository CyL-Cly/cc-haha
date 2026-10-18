# Remote Owner 回调与关闭的有界证据

本批仅审核真实 `--remote` gate 的普通 application wrapper。冻结 Source 为 `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，RemoteSessionManager、SessionsWebSocket 和 useRemoteSession 三个当前文件字节均与冻结版相等。没有修改生产文件、CLI/title/MainPrint 或普通环境 guard；没有将 checkpoint12 单文件零覆盖率解释为未执行。

`terminal_remote_session.go` 的 Message/State 回调同步提交到同一 Owner 并等待 receipt。正常 CloseWithReason 先提交 Cancel、标记 closed，再在 Owner 外调用 manager.Close；Cancel 在 Owner 上只排控制请求，不 join。实际 Tea Init 的 tracked command 在订阅与 receive loop存在后才启动 Manager；构造时 manager仍等待 start。正常 program退出/解除订阅早于外层关闭。该正常调用链未发现 Owner→manager.Close→同一 Owner callback 的循环等待，未据假设添加生产修复。Source cleanup/manager.disconnect 使用同步关闭入口，Go另有明确 owned取消/join；这里不声称任意阻塞listener或从Owner重入Close安全。

原样串行 `-p=1 -race -count=1` 三个 selector通过：完整原 Source Manager的真实WS+HTTP、权限、interrupt与permanent-close，公开 Parse→RunCommand 两个真实PTY profile的七次CCR输入、批准真实shell文件、deny/server-cancel/interrupt、忙碌下一输入、零本地ASK与精确termios，以及原Manager持住HTTP的Close。application 9.957s、ccrsessions 1.575s，3 top/5 pass events、零skip/fail。Source20s、PTY38s/45s、Manager5s窗口全未改。

新增 `terminal_remote_lifecycle_test.go` 通过真实prepareCLIRemoteStartup、NewSession与wrapper factory，复用实际缓存账户读取和私有TLS代理。三个阶段为启动前、实际WS upgrade持住、实际CCR POST消费body后持住并同时收到WS task/权限回调；后者还收到实际Cancel interrupt。Close在原3s观察内完成，HTTP/WS peer观察关闭，原Submit已join，late输入被拒绝，限定的冻结ant+sonnet profile没有本地ASK。有效首轮4 pass events/4.443s，重复count3为9个阶段执行/12 pass events、8.379s，零skip/fail。两包vet exit0、diffcheck0。

首次新fixture漏掉RunCommand在prepare之前设置Stderr=io.Discard，constructor正确在目标行为前拒绝nil Error（每项0.04s）。原日志保存为无效fixture，不当作产品RED。只修新test中的该正常caller初始化。

本批结束时 connecting/force reconnect 尚 OPEN；后续原 Source/实际传输真 RED 与最近修复已单独记录在 [握手强制重连证据](go-native-remote-force-reconnect-evidence-20261015.md)，没有重写本批历史收据。Source assistant的viewerOnly入口、未选中的完整render/media/tool-progress与所有timeout facets、任意外部阻塞callback、Windows实机和完整覆盖率/质量门禁均不外推。全部精确args、Source/生产SHA、原始日志与分类在 `artifacts/go-migration-20261013/remote-owner-lifecycle-evidence-20261015/receipt.json`。
