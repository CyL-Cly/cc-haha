# 当前策略与契约门禁

实际执行原 `bun run check:policy`，使用固定 Bun 1.3.14、新临时 HOME 和去除真实凭据的私有环境。首次 619 pass / 1 fail：`native-config-dir-mcp-auth-cache.fixture.ts` 导入了未引用的 `dirname`。仅删除该 binding，扫描器、allowlist、阈值及测试声明不变。原命令复跑 58.046 秒，38 files、620 pass / 0 fail，fixture 前后 SHA 相同；首次失败日志保留。

随后并行执行原 `bun run check:provider-contract` 与 `bun run check:chat-contract`。provider 的 36 suites、797 Bun tests 全通过，23.881 秒；chat 的两组 server suite 共 245 Bun tests 和三个 desktop files 的 452 Vitest tests 全通过，25.771 秒。各自 5,521 个相关当前 src/scripts/desktop/manifest 输入在执行前后身份相同，没有真实模型请求或真实 OAuth。

完整命令、日志、输入清单、结果与 SHA 在 ignored `root-current-policy-contract-bounded-receipt-20261015.json`，SHA256 `7f20113d972bb7d5f6e2024dfbcbcb42a14a1ad6358df3262eaa89780050ffbf`。这些是当前独立确定性门禁通过，不是不可变最终 Go/React/native 候选的完整 verify，也不代替 Go 90%、真实三端或正式 120 对。

资源清理 v22 在 Root 和三组均停止 Go/编译、实际 full ps 首 executable 零 Go/compiler/test 后，仅删除任务私有共享 compiler cache；可用空间 1.776→4.225GB，新增策略 raw 与既有受保护依赖、源、计数、manifest、失败证据及阈值 SHA 一致。未提交、推送或合入 main，Claude 真实 OAuth 继续按用户要求排除。
