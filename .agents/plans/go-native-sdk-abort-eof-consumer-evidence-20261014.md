# SDK 权限取消与 EOF / 迟到响应

2026-10-14，局部真实消费者记录。完整迁移仍未完成，不合入 main。

## 复现

checkpoint 12 全量测试中，父级 SDK 取消用例缺少 `can_use_tool` 的取消帧，仍保留原始失败。相同用例单独 `-race -count=20` 得到 40 个 pass 事件；没有生产修复，不能替换全量失败。

新增原版完整 `StructuredIO` / `Stream` 对照，先取消请求再关闭实际输入流。原版仍发送一条 `control_cancel_request`，返回 `AbortError`；普通 EOF 返回 stream-closed。原版三个业务文件与冻结 MAIN 字节一致。

现有原生 MainPrint、真实 Owner、stdio 输入/输出和权限 API 的配对回归添加同样操作。修复前 `-race -count=5` 五次中一次实际失败：Go 返回 `Tool permission stream closed before response received`，Source 返回 `AbortError`。原始 RED 是 `native-sdk-abort-before-eof-consumer-red-v2.jsonl`，不是编译或配置问题。

## 修复

待处理权限记录保留实际 prompt 和它的取消 context。在 EOF 或迟到响应处理时，已取消的请求先走既有 `Cancel`，发送取消帧并以 `AbortError` 拒绝原 Future。普通 EOF 保持原行为；迟到 allow 及权限更新不得覆盖已经发生的取消。

只改瞬态 SDK 状态，不改变持久化结构、Owner 调度、Source 正文、测试窗口或原请求/决定断言。新迟到响应用例还验证 `acceptEdits` 更新不会覆盖取消后的默认权限状态。

## 实际验证

- 新取消先于 EOF 用例 `-race -count=10`：passed，20 个 test pass 事件，0 fail/skip；36.487 秒墙钟。
- 完整 stdio 权限消费者、父级 cancel/EOF/OwnerClose、Hook 赢家、passthrough、正常完成后的迟到 Hook 副作用、原版 schema/control 与原版 Hook race：7 项顶层用例各三次，passed，123 个 test pass 事件，0 fail/skip；100.712 秒墙钟。
- `go vet ./internal/sdkhost ./internal/clibootstrap`：passed，exit 0，17.403 秒。
- 所有测试使用临时 HOME、假凭据、原输入/输出和隔离的 Source，不使用真实模型、外网或 Keychain。

源码 SHA、命令、原始日志和包时间见 ignored `artifacts/go-migration-20261013/native-sdk-abort-eof-bounded-receipt-20261014.json`。缺失证据必须重跑。

原全量的取消帧缺失仍须在新完整门禁确认，不能仅由上述局部结果宣布关闭。最终 Go race/90%、verify、同版本桌面/Web/CLI 和正式 120 对 Benchmark 尚未完成。
