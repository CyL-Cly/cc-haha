# 当前配置凭据与实际 pane CLI 的有界证据

2026-10-14。继续当前 checkout；没有切分支、提交、推送、合入 main 或发布。本文记录中间候选的确定性回归，不表示迁移完成。真实 Claude OAuth 遵循用户排除；没有调用真实 Keychain、真实登录、真实模型或公共网络。

## 实际差异与实现

`native-config-keychain-runtime.fixture.ts` 在原 disposable HOME 守卫与外部能力拒绝边界下，执行完整原 `macOsKeychainStorage`、`createFallbackStorage`、`plainTextStorage` 和 cache helper。只替换外部 security 读写原语，保留原业务、原 JSON、真实私有明文文件、缓存时钟和操作次序。

原版配置目录 absolute/NFC/relative/empty/absent × seeded-success/absent-success/seeded-fallback 的 15 组实际对照，确认目录变化不会清除共享 30 秒缓存；过期、手动 clear、update、delete 才使用各自当前 identity。Go 原先仍使用启动时 service，真实 RED 后改为同一 store 的当前 service producer。没有第二凭据 store，也没有清除原 cache/generation。

显式空目录是独立真实 RED：原版读取相对 `.credentials.json`，但写入时 `mkdir("")` 失败；Go 原先会在当前目录成功写入。现在 plaintext producer 分别保留 mkdir operand 与物理文件路径，空 mkdir 不转成 cwd。原版失败、旧文件与旧 primary 保留相等；非空写入的未知字段和0600权限仍走已有实现。

新增当前 USER plain/Unicode/empty/absent × 新账户存在/不存在的 8 组完整消费者。Go 原先持续读写启动账户，真实 RED 后改为实际 primitive 时读取当前 USER。固定 Bun 1.3.14 的实际 `os.userInfo().username` 探针还确认其回退值捕获启动 USER：命名值、显式空值、缺失值分别保留原命名值、空值、`unknown`；LOGNAME/USERNAME 不代替 USER。用三个独立实际 Source 进程验证三种启动状态，共69组；不以 Go `os/user` 的宿主用户名替换这个原版回退值。

MainPrint 的当前 directory/service/USER producers 绑定到既有 scalar environment；原六组 settings path 对照现在同时观察真实 fake-security argv 中的 service/account。最初未接 USER callback 的实际 Main 对照真实 RED，接线后通过。普通运行中 CLAUDE_CONFIG_DIR 的 frozen host guard仍保持拒绝；这六组是实际 Main/scoped getter 与 primitive 的有界消费者证明，不能写成动态目录的普通 RunTurn 已验收。

实际团队子 CLI 的启动拒绝另有真实 RED：`tui.Run` 会调用 Buddy snapshot，旧 predicate 排除了带真实 pane identity 的 interactive MAIN。原 `PromptInput` 本身会读取 companion；已只允许实际 pane MAIN 同样使用既有 snapshot/action，保持 Owner、scope、closed、detached/agent-child 等边界。三个完整原 companion producer profile与同 pane 下一 HTTP 保持真实 MAIN 角色通过；旧团队公开 PTY 和已有 Buddy 公开 PTY 均鲜跑通过。新团队三实体 tmux 子 CLI、实际 Write、面板操作、shutdown/kill/prune与同 Harness下一 Write 的完整入口另见 [团队面板证据](go-native-teams-dialog-evidence-20261014.md)。

## 原始结果

日志与 exit receipt 全部保存在 ignored `artifacts/go-migration-20261013/`；最终有界 receipt 会记录每个原始文件 SHA，不修改旧失败。

| 日志 | 实际结果 |
| --- | --- |
| `native-config-keychain-red-v1` | unused import，中间编译失败；不是业务 RED |
| `native-config-keychain-red-v2` | 15组旧 service 的真实 RED |
| `native-config-keychain-empty-plaintext-red-v3` | 14组通过，empty/fallback 保存真实 RED |
| `native-config-keychain-source-green-v4` | 完整 securestorage race passed，2.074s；当时15组 |
| `root-pane-buddy-caller-source-red-v2` | 并行 Help 字段未齐，中间编译失败 |
| `root-pane-buddy-caller-source-red-v3` | 三个实际 pane Buddy snapshot 真实拒绝 RED |
| `root-pane-buddy-config-source-green-v4` | bootstrap race passed，7.044s；pane原身份/下一HTTP与settings六路径 |
| `native-config-keychain-user-source-red-v5` | 原15组通过，新8组当前账户真实 RED |
| `native-config-keychain-user-main-binding-red-v6` | securestorage69组 passed1.904s；Main未接账户producer真实 RED |
| `native-config-keychain-account-pane-integrated-green-v7` | **整体 failed**；新消费者通过，但旧 legacy 夹具启动后改写 Native.Now 与插件 worker 读取产生 race |
| `native-config-account-pane-clock-fixture-green-v9` | 启动前配置原固定时钟/输出，原断言不变；bootstrap race passed22.077s，含legacy报告/取消、Main路径、settings下一HTTP/Bash/CA、pane实际产物与Buddy三profile |
| `root-keychain-buddy-public-regression-v8` | application race passed23.220s；旧实际pane链14.60s、Buddy公开链5.39s、普通Session1.05s；1个没有marker的私有PTY helper入口skip，真实消费者均执行 |
| `native-securestorage-full-user-current-final-v10` | 完整 securestorage race passed2.392s，8个top-level tests、0skip；含69组identity/cache、20组旧根节点与async dedup/update-generation |
| `root-config-account-buddy-vet-v11` | Help新runtime type尚未保存的中间编译失败；不称vet通过 |
| `root-config-account-buddy-vet-v12` | 文件齐备后七个相关包 fresh vet passed；不代替完整check:go或verify |

v7 的旧测试 race由 `Native.Now` 在实际 plugin worker 已启动后被夹具写入引起。现在沿既有 Main fixture 设置入口，在构造/启动前给定原固定时钟和输出，不添加等待、不取消插件 worker、不改生产同步或 Source 期望。原失败和 fresh v9 均保留。

## 剩余边界

- 初始 absent/empty/relative CLAUDE_CONFIG_DIR 的原始环境、实际 cwd及各消费者的 live/captured/memoized 行为仍 OPEN；不能提前开放整体 host guard。
- legacy API-key read/save/remove 仍有启动 service/USER 消费者；其当前目录与 shell USER 展开尚未迁移。本文缓存与账户证明针对 credentials store，不外推 legacy 槽位。
- USER 数值/coercion、原始 UTF-16 异常值、custom/staging OAuth suffix/非法 endpoint 等环境身份边界不由这些 external-string profiles 证明。
- Global/project/transcript/plugins/MCP/providers/Agent/Memory/hooks 的目录消费者仍须逐项闭合。配置 JSON 形状未改变；未知字段仍按原 writer 保存。
- `check:impact` v2 命令 exit0，报告1372 changed files、13 required lanes、Blocked yes（CLI PR label/maintainer条件）；不是 policy passed。v1 只因本地 PATH未含固定 Bun 导致内层命令找不到 Bun，已保留为工具环境失败。
- cache cleanup-v9 在四组自然0 Go、full ps executable盘点0后执行，仅清理本任务可再生共享编译缓存；free11.33→32.97GB，依赖/锁、09覆盖profile及失败证据 SHA全部不变。
- 全量最终 Go/verify/90%、同版本桌面/Web/CLI、Windows实机、外部真实IM/有声语音与正式120对 Benchmark仍未完成。真实Claude OAuth是用户排除，不作为要求用户再授权的阻塞项。
