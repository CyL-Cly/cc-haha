# Legacy Keychain 与第十次 Go 检查的中间证据

2026-10-14。继续当前 checkout；没有切分支、提交、推送或合入 main。真实 Claude OAuth 按用户要求排除。本文只有临时 HOME、私有进程、fake Keychain 和 loopback HTTP 的确定性证据，不表示完整迁移或产品验收完成。

## Legacy 实际消费者

新增 `native-legacy-keychain-current.fixture.ts` 执行完整原 `auth.getApiKeyFromConfigOrMacOSKeychain`、`auth.saveApiKey`、`authPortable.maybeRemoveApiKeyFromMacOSKeychainThrows`，保留原 `execSyncWithDefaults` 与业务代码。外部 execa 原语仅允许精确私有 security 可执行文件、私有 cwd/environment、原 legacy shell 字符串和 stdin writer；实际 `/bin/sh` 与私有 Python 子进程执行、写入私有状态并记录 argv/cwd。原外部能力拒绝守卫仍为零 blocked/secureReads，没有真实 Keychain、网络或模型。

原 absolute/NFC/relative/empty/absent 目录 × plain/Unicode/empty/absent/spaces/glob USER 共30组读取旧槽位、切换当前 identity、保存、读取、删除、再次读取，实际复现 Go 使用启动 service/account 和无 shell 展开的问题。当前 legacy getter/remove 使用原 `$USER` shell 字符串，在原生边界按当前环境与 cwd 展开；save 使用当前 service 与原 Bun 启动用户名回退，秘密仍经 stdin hex 运输。物理失败 stdout、reject:false 与任意抛出 primitive 的区别保留；取消与隐私报告另由实际 Main 消费者回归验证。

Main 的原六组目录消费者现在还观察 legacy service 与实际 security cwd/USER producer。普通动态 CLAUDE_CONFIG_DIR 的 frozen host guard 没有开放；这证明 Main/scoped getter 接线，不证明完整动态目录 RunTurn。新增默认运输测试直接执行私有 shell/security，确认使用当前 PATH/env/cwd，删除后 rejecting-false 读取为空；非法相对 cwd 在产生子进程之前拒绝。

| 原始日志 | 结果 |
| --- | --- |
| `legacy-current-physical-source-red-v1` | 30组真实业务 RED，23.438s |
| `legacy-current-physical-source-green-v2` | **failed**，28.121s；物理 argv/cwd/order 与最终状态30组均匹配，但测试错误假设原全局 fallback 固定有值 |
| `legacy-current-physical-fallback-fixture-green-v3` | 完整 securestorage race passed，29.277s；fallback 改为导出实际原 `getGlobalConfig().primaryApiKey`，不改业务或期望生成方式 |
| `legacy-native-context-main-consumers-v4` | **failed**；Main/privacy/cancel11 top-level passed，默认运输观察器比较 `/var` 与实际 `/private/var` 时失败 |
| `legacy-native-context-main-consumers-v5` | race passed：securestorage1.699s、bootstrap36.798s，共15 top-level/37 test events、0 skip；含原 helper cache/Agent borrowing 与设置保存后的下一 HTTP/Bash/CA 消费者 |
| `legacy-native-current-vet-v6` | 工具参数目录拼写错误，**failed**；不称 vet 通过 |
| `legacy-native-current-vet-v7` | securestorage/bootstrap/settings 三相关包 fresh vet passed，exit0 |

v4 修正只让物理 cwd 观察器使用 `EvalSymlinks`，并通过原 callback 返回变量设置非法 cwd；生产行为未为测试变化。所有旧失败日志和 exit receipts 保留。

## 不可变 checkpoint10

第十次完整 Go 检查使用独立 APFS 副本，12748个输入 SHA 在复制前后和检查后全部相等。副本是中间诊断候选，之后的 Source 修复不写入该副本。固定 Bun1.3.14、Go1.25.7、55个校验后的隔离依赖、原 runner 与原90%阈值未改变。

format/build/vet passed；完整 unit **failed**。原事件统计为315个 package start、307 package pass、5 package skip、3 package fail；17744 test run、17669 test pass、71 test skip、3 test fail。application 的一个已开始测试没有终结事件，15分钟超时；不把 package pass 计作 test pass。

失败是两个运行中 Agent 消息消费者、公开 typed-model PTY 和 application 运行中的文件信号测试超时。对应修复和新测试在当前 checkout，不能倒写旧检查为通过。race、最终 coverage policy 与最终 verify 没有被此次失败 runner 执行。

失败单测留下的部分 profile：2888 files，265379/384959 executable lines（68.9369517%）； changed-lines 264956/384377（68.9312836%），原最低要求90%。诊断解析没有重新取得声明 AST evidence，64个缺 mapping 项不能据此判断声明豁免；该结果不能用于最终门禁通过声明。

完整失败 profile 经 gzip 保存，解压 bytes/SHA 与原始一致：3714215167 bytes，SHA256 `f450c9088a2248ad68b127a3f5eecba900a893e76e1ab75dfa636df0c102090d`；归档369875250 bytes，SHA256 `089c30950fb95a78e7db098f98292f6fe787db7fc20b8f21627e530099cf3f37`。验证后仅删除可无损恢复的 raw profile，保留原日志、副本、归档和 receipt。

## 仍未闭合

credentials 的同步 shell 与直接 async/writer 原语仍须证明原 PATH lookup、当前 cwd 和异常 stdout 消费，不能以本次 legacy shell 对照代替。完整 legacy `/login`/logout 面板、全局 fallback/memoization、prefetch、raw UTF-16/coercion 与全部环境 suffix 不在30组 external-string 范围内。最终不变90%门禁、同版本桌面/Web/CLI、Windows实机、真实IM/语音及正式120对 Benchmark仍未通过。
