# Remote 握手中的强制重连

本批闭合前一份 Owner 生命周期证据中的 connecting/force reconnect 分支。冻结 Source 为 `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，当前 RemoteSessionManager、SessionsWebSocket、useRemoteSession 文件字节仍等于冻结版本；没有修改 Source 业务、application wrapper、CLI/title/MainPrint 或普通环境 guard。

原 `SessionsWebSocket.reconnect()` 重置重试计数，调用 `close()` 关闭包括 connecting 在内的真实 WebSocket，再设置可由 close 清理的原 500ms timer；RemoteSessionManager 直接委派该方法。新增完整 Source fixture 使用原 Manager 和 Bun 1.3.14 原生 WebSocket，与私有 TLS peer 做真实 rendezvous：第一次 HTTP upgrade 保持未完成，调用原 reconnect 后 peer 收到该实际请求取消，第二次原生连接建立并在 disconnect 后关闭。Source 的原 500ms 未替换，capabilities blocked 为零，两个连接的认证/version headers 均从原代码产生。

旧 Go 在握手中 `conn=nil`，Reconnect 只设置 forced，没有关闭仍在 `websocket.Dial(m.ctx,...)` 的传输。相同私有 peer 的 Go 消费者在原 5s 窗口真实失败：`force reconnect retained the old held Dial and did not connect a replacement 1`。旧 manager 完整字节与日志已保存，失败不是 build-only、计时桩或模拟 websocket。

最近修复仅在 `ccrsessions/manager.go` 登记每次实际 Dial 的私有 attempt/context。Reconnect 在同一 mutex 下退役该确切 attempt，随后在锁外取消实际 Dial；握手完成在发布 socket 前核对该指针和关闭状态，退役后的成功连接立即实际关闭。当前成功连接的 attempt lifetime 保持至原连接 read/ping 清理完成。原 500ms/2s 重连、重试额度、OAuth reader、控制请求、Close join 和 established-connection 行为保持。

新增实际边界回归把第一次真实 TLS/101 response 的 RoundTrip 返回暂时持住，未伪造 status/body/socket；Reconnect 后释放该实际晚到响应，旧 socket 和 replacement 都关闭，仅 replacement 发布 connected 一次。另一回归仅观察原 native Wait admission 的 500ms 参数，随后仍委派原 native timer；Close 在原 3s 内取消/join、peer 观察第一握手关闭，没有开始 replacement。三 top 的 `-p=1 -race -count=3` 通过，9 top executions/15 pass events，ccrsessions 8.463s，零 skip/fail。

修复后原四 selector 串行 `-p=1 -race -count=1` 通过：完整 Source Manager 的 WS/HTTP、权限与永久关闭，公开真实 PTY 两个 profile 的七次 CCR 输入/实际批准 shell/deny/cancel/interrupt/忙碌下一输入/零本地 ASK/精确 termios，原持住 HTTP 的 Manager Close，以及实际 wrapper factory 的启动前/持住 upgrade/持住 POST 且并发回调三个 Owner 阶段。4 top/9 pass events，application 11.879s、ccrsessions 1.443s，零 skip/fail；Source 20s、Go Manager 5s、PTY 38s/45s 和新增 Close 3s 均未延长。

两个早期 Source setup 失败分别漏 `USER_TYPE=ant`，以及在 capabilities guard 安装之后误捕获其禁止 WebSocket facade。二者均在目标 native transport 前被 guard 拒绝，保留为无效 fixture；仅修私有 fixture 入口，没有放宽 guard，不能计产品 RED。有效 old-Go RED、首修通过、count3、原消费者复测和静态检查的精确参数、原始日志、SHA 均记录在 `artifacts/go-migration-20261013/remote-force-reconnect-evidence-20261015/receipt.json`。

范围限制：本批不是全部 Remote/Assistant 产品、任意阻塞 callback 或重入 Owner Close 的证明；assistant viewerOnly、未选中的完整 renderer/media/tool-progress、Windows 实机、正式覆盖率 90 与最终完整质量门禁仍不外推。前一份无生产改动的 Owner 正常关闭证据保留独立历史收据。
