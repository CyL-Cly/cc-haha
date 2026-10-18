# MCP 回调端口生命周期复验

checkpoint12 的 state-mismatch 用例曾报 `real callback listener survived completed flow`，原失败与原关闭断言保留。本次原 state-mismatch 连续 50 次 race 全通过（100 个 test pass events，46.653 秒）；未复现旧失败，尚不能确定旧失败原因或宣布它已经修复。

新增实际 MainPrint MCP 认证测试：错误 state 的回调完成后，真实 OS listener 能绑定相同端口；另一 loopback HTTP 服务明确拥有该端口；迟到手动回调和 HTTP 回调均不写原认证凭据、不交换 token、不重连。测试使用临时 HOME、假凭据和 loopback，没有真实 Claude OAuth，也没有修改认证生产逻辑。原断言失败时只增加一秒以内的只读 lsof 端口占用诊断，原断言不放宽、不重试。

新增端口复用、原四种取消及认证/清除后下一 Harness 共三次 race 全通过（21 个 test pass events，37.053 秒）。诊断版再次运行新增及四种取消用例通过（6 个 test pass events，54.328 秒）。同区 clibootstrap vet exit0（57.568 秒）。完整原始命令、日志哈希和记录时测试文件哈希位于 ignored 收据 `artifacts/go-migration-20261013/mcp-callback-lifecycle-bounded-receipt-20261015.json`。

这是当前中间版本的有界验收。旧全量失败仍待新完整候选确认；完整 check:go、90%/verify、同版本桌面/Web/CLI 和正式 120 对未通过。Claude 真实 OAuth 按用户要求排除，未合入 main。
