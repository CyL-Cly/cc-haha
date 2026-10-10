# 普通 Go CLI 凭据状态补充 · 2026-10-08

本节只关闭 auth status 的特定凭据来源消费者，不关闭整个认证、CLI 或迁移。

## 实现

- MainCommand status 共用现有 desktopoauth 同步 FD reader；OAuth/API key 缓存各自独立，成功空内容和非法 descriptor 不尝试fallback，物理读失败使用CCR fallback；remote成功读按原0700/0600 best-effort持久化。
- 保留 parseInt 十进制前缀、JS Number rounding/Infinity 路径、UTF8解码/JS trim，Source错误输出不泄露secret。
- managed desktop/remote忽略外部Bearer及helper的源类别；Homespace status按原忽略环境API key，bare独立规则保留。内部 USER_TYPE=ant schema startup仍有具体guard，新增测试只核验外部完成bootstrap后的auth consumer，并未覆盖完整内部启动。
- 新路径 primitive NativeBoundary.CCRCredentialDirectory 是明确外部测试适配；普通生产仍用原 `/home/claude/.claude/remote`。native_process_fixture只将此OS路径和managed/Keychain重定向到私有root，不替模型/业务行为。
- 实际 Source fixture现在调用原 authFileDescriptor模块，只将同步FS读写映射到隔离文件；清除每个profile的原bootstrap缓存，原handler/stdout/text/exit与实际文件副作用不替代。40新增profile含JSON/text、FD/CI/bare/managed、空/非法/失败fallback、远端物理persist、Homespace及大数路径。另有40既有profiles。

## 实际检查

| 检查 | 结果 |
| --- | --- |
| 252/253 fixture准备 | 252 failed：新的测试未满足Source cwd guard；253启动后取消并修正绝对fixture/cwd。不是业务红证据 |
| 254实际Source业务红 | failed：有效FD/remote在原Source成功，Go返回未迁移；内部schemaguard另列保留 |
| 255初次修复 | failed：仅Homespace状态hasApiKeyEnv未忽略环境key；desktopoauth FD原Source4.810s passed |
| 256补齐优先级/fallback | passed：clibootstrap12.534、desktopoauth5.363s，76状态profile及原FD领域 |
| 257实际普通二进制 | passed：clibootstrap12.898、desktopoauth5.018、CLI17.535s；4实际继承pipe非remote/remote ×OAuth/API，pipe读取、0600物理文件、状态/退出/agents及无ASK记录 |
| 258 impact | passed/selection only，所有所选checks仍须执行 |
| 259完整相关领域 | passed：desktopoauth19.818、cliauth2.196、openaiauth3.919s；在随后大数reader修改前的快照，不算最终全部领域复验 |
| 260四包vet | passed/exit0，大数reader增量前；最终vet仍需复验 |
| 265增加大数及scope | failed：领域和真实CLI5.164/18.408s passed；测试直接无锁读取Native.Trace与原global watcher观测并发，race报警。业务输出没有不一致；保留报警并修测试观测锁，未改变生产watcher |
| 266最终focused Source/race | passed：clibootstrap13.022、desktopoauth6.945、CLI19.486s；80状态profiles、FD/CCR缓存实际read-call不重读、物理pipe和foreign/cancel范围全部核验 |
| 262既有Server selected lane | passed：542 files/6493tests/0failed；不等于整个Go后端通过 |
| 261/263 policy | failed：旧TeamPlanfixture unused basename、3Telegram硬编码不存在node_modules路径；移除实际dead import、原同版grammy1.42.0经生产目录resolve，未改选择/断言 |
| 264/267复验 | passed：dead-imports28/0failed；policy462/0failed、5923assertions |

## 变更文件

`backend/internal/clibootstrap/main_command{,_auth,_credential}.go`、`main_native.go`、对应原Source/credential测试；`backend/internal/desktopoauth/fd.go`复用reader；`backend/cmd/claude-haha/main_native_process_fixture.go`与`main_auth_process_test.go`；`scripts/quality-gate/go-parity/native-command-auth-status.fixture.ts`。只删除`native-team-plan-approval.fixture.ts`的unused import。README/当前验证同步实际范围。

真实model检查不适用这些非模型状态命令。P250原生官方DeepSeek冷恢复Read/Write/Read是独立SHA收据，未包括本节代码。后续正式完整Go/verify、内部bootstrap、完整OAuth login/异常日志/所有native平台、整个桌面及配对Benchmark仍未完成。
