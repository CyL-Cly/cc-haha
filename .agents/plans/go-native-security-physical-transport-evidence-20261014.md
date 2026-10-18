# 凭据原生运输的实际私有进程证据

2026-10-14。中间工作区证据；无分支操作、提交、推送或 main 合并。Claude 真实 OAuth 按用户要求排除。全部数据为 fake credential，实际 child 仅访问临时 HOME 的私有状态和 trace，不使用真实 Keychain、模型或公共业务网络。

## 真差异与修复

`native-security-physical-storage.fixture.ts` 保留完整原 `macOsKeychainStorage`、共享 cache、`createFallbackStorage`、`plainTextStorage`、sync/async exec wrapper，只接入精确受限的 execa I/O 原语。固定 Bun1.3.14 实际执行原 shell 字符串或直接 `security` argv；PATH 只包含当前私有 executable，cwd/env/stdin/账户值与路径逐项限制，原外部能力拒绝守卫仍零 blocked/secureReads。Go 直接使用默认运输而非 RunSecurity 测试替身。

首先在真正 spawn 之前检查 Go 的实际 Command.Path，安全复现旧默认运输绕过当前 PATH 而固定 `/usr/bin/security`；因此没有触碰宿主 Keychain。抽取同一个默认命令构造后改为当前 NativeSecurityContext 的 PATH/cwd/env 解析，不使用 Go 进程的 ambient LookPath。

随后完整物理链复现两项业务 RED：账户字符串 `$OWNED_ACCOUNT` 在原同步 shell 中展开，但原 async/direct write 保留 literal；非零退出仍带 stdout 时，原同步读取保留结果，而原 async wrapper 清空失败输出。现在 sync read/delete 保留原 quoted shell；async/read 与 stdin/大 argv write保留直接运输。同步 rejecting-false receipt 与任意抛出的替身异常区别不变，cache TTL/stale/generation 与未知 JSON字段仍由原 store 消费。

九组包含 plain/spaces/glob/变量引用/empty/absent USER、带 stdout 的 exit44、relative PATH 和带空字段的 PATH。实际 trace还证实固定 Bun 的直接 spawn 跳过空 PATH 字段，但 `/bin/sh` 保留该字段的 cwd 查询；Go 两种运输分别保持各自原规则。每组比较整个 read→async read→update→read→delete→read→delete 的业务值、私有 security argv/cwd/选择的 executable 与最终假槽位，不只检查命令字符串。

## 实际运行

日志和 command/exit receipts 保存在 ignored `artifacts/go-migration-20261013/`，由 `native-security-physical-transport-receipt-20261014.json` 保存20个原始证据文件与9个生产/测试文件 SHA。

| 日志 | 结果 |
| --- | --- |
| `credentials-physical-default-path-source-red-v1` | unused test import 的 build-only failed |
| `credentials-physical-default-path-source-red-v2` | 原Source实际执行后，七组默认 Path 真 RED；安全 guard阻止旧宿主 executable启动 |
| `credentials-physical-shell-stdout-source-red-v3` | 固定临时 Bun 路径消失，fork/exec ENOENT；工具失败，不算业务 RED |
| `credentials-physical-shell-stdout-source-red-v4-restored-runtime` | PATH 修复后五组通过，原变量展开与非零 stdout 两组业务真 RED |
| `credentials-physical-default-shell-source-green-v5` | 完整 securestorage race passed；七组物理运输加原 store 回归 |
| `credentials-physical-relative-path-source-v6` | 原八组通过，新空 PATH 字段在 direct spawn 选错 executable 真 RED |
| `credentials-physical-all-consumers-green-v8` | 完整 securestorage race passed32.904s；11 top-level/142 test events，0 skip，含九组物理运输、30组legacy、69组identity/cache及旧根/异步回归 |
| `credentials-physical-main-binding-consumers-v7` | bootstrap race passed27.374s；14 top-level/36 test events、0 skip，含Main/scoped六路径、legacy取消/报告、parent/Agent helper缓存、实际下一HTTP/Bash/CA |
| `credentials-physical-shared-consumers-vet-v9` | securestorage/bootstrap/openaiauth/settings fresh vet passed，exit0 |
| `credentials-physical-guarded-source-final-v10` | 加强私有 executable/账户守卫后的原九组 race passed8.702s，0 skip |

v6 未修改原业务输出或放宽期望。原业务字节、进程参数和执行顺序均由完整原代码产生；凭据没有进入 argv明文。没有改变持久化 JSON shape 或开放普通动态 ConfigDir 的 frozen host guard。

## 固定工具恢复

旧临时 Bun/tmux/PowerShell/MCPB 路径在本轮消失，原因未建立；保留 ENOENT、下载中断及原失败。新工具持久保存在本任务 ignored artifacts，避免依赖旧临时指针。

- Bun1.3.14 官方 release zip 的 SHA与官方 asset digest一致；官方 npm package 的 sha512 integrity也验证，二者解包后的 binary SHA一致为 `e0c90ec15d33363e6b70713d56bc3b2c7585c17f40a0fe0f8fd9305901d4e233`。共享 `bun-bin-path.txt` 的目录合同已恢复，env中的 SOURCE_BUN仍是可执行文件绝对路径。
- tmux3.5a从原本任务 artifact恢复，binary SHA与原 `1f15db72b4667991607a7dfa1845dc7d5dc976605203ab60f7ad7dd0b332adb5` 相同，otool确认只依赖系统库。
- PowerShell7.6.6 两次完整下载中断，随后实际 HTTP Range拼接到70604356 bytes；官方 archive SHA与原 `6df833d094ebac1c1a74340d7b3437f4aaf5e03ce640484a1c4359f3ce8b3db1` 一致。578个regular成员逐项比对，真实私有进程版本预检7.6.6通过。
- MCPB1.2.0/Zod3.25.67重新验证原官方 archive SHA与sha512 integrity；版本未改变。实际私有Poppler和Seatbelt的八项消费者另见 [依赖证据](go-native-prerequisites-poppler-seatbelt-evidence-20261014.md)。

checkpoint10副本目录仍存在，但package.json、runner等具体输入已消失；检查完成后的12748 SHA audit为零差异，原manifest/报告/log和完整无损profile归档保留。不能再把当前不完整目录作为可重跑的原快照。下一稳定检查副本也放入 ignored artifacts。

## 范围限制

这关闭当前九组运输与Main接线范围，不外推全部legacy login/logout UI、prefetch、异常UTF-16/coercion、所有OAuth suffix、pre-worktree与多种当前cwd authority。真实Claude OAuth排除不影响 fake credential合同。全量unit/90%/verify、同版本桌面/Web/CLI、真实外部条件和120对Benchmark仍未通过；Source冷启动标题P0仍失败。
