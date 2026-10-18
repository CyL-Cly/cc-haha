# 普通 native host 的 SIMPLE=0

2026-10-15，当前有界修复，公开入口已用 fresh binary 实际复验。

完整原版普通 CLI 在明确 `CLAUDE_CODE_SIMPLE=0` 的五个实际 parent→Agent→Read/TaskOutput→parent loopback 控制中全部成功。fresh Go v5 同值在首模型 POST 前失败，实际 HEAD 1 / POST 0，错误为 `API presentation capability: original bare environment value`。这不是连接池 RED；变量 absent 的新控制另证实池差异，两个缺口分开记录。

新增 actual `NativeDomainFromOriginalHost` 回归在旧代码真正失败。`apipresentation/presentation.go` 只为 native 域接受这个已证明的 `0` 字面值；原独立 finite bare leaf 的值域和原拒绝测试不改。整个原 package 按默认 vet、`-p=1 -race -json -count=3 -timeout=90s` 复跑通过，12 distinct tops / 213 pass events，pkg 5.327 秒、总 7.751 秒、输入 SHA 一致。原 opt-in 生成 fixture helper `TestTerminalCreationFixture` 三次 skip 如实保留，不能写零 skip。

fresh ordinary CLI SHA256 `c739470e48a0f73e2997a00b3d7536533fe2c82a8a0ab5d141d8c27905cd8b72` 在 stable 6,619 输入下 build 0 / 36.765 秒。变量 absent 与原 explicit-zero 两个普通公开入口均 exit 0 / stderr 空 / HEAD 1 + 6 POST，实际 Agent→Read→TaskOutput→parent 产物与连接池复用成立。先前一次 build 0 因复制期间新增 test 使 strict inventory 6,618→6,619 被拒绝，原拒绝保留；本次重新构建得出稳定身份，没有把首次 build-only 当稳定验收。

实际 Source 五次、旧普通 Go 原始失败、新 regression 与 fresh public consumer 的 RED/GREEN、命令/输入/SHA 收据位于 ignored `artifacts/go-migration-20261013/native-plain-env-zero-bounded-receipt-20261015.json`。其他 SIMPLE 字面值未因此开放，不外推官方 OAuth、全环境域或最终门禁。没有真实模型 quota、Claude OAuth 或合入 main。
