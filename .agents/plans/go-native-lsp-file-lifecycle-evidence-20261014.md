# LSP 文件通知与进程生命周期的限定证据

2026-10-14。冻结 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，本机 Bun 1.3.14、Go 1.25.7。以下是持续修改中的聚焦证据；不关闭整个 LSP、完整迁移或最终质量门禁。

## 实际消费者

- 普通 `MainPrint` 在原生 MCP 初始化之后创建具体 LSP manager。Write 和 Edit 使用同一 manager、Owner 与实际 Writer native ledger；外来 manager 仍被拒绝。普通子任务通过现有私有 detached factory 登记自己的具体 ledger，关闭时只撤销本任务登记。
- 从实际已安装插件读取 `.lsp.json`，读取配置不启动进程。SIMPLE 模式与交互信任前不创建 manager；交互信任后和普通 headless 初始化配置。工具目录的 headless／interactive 取样差异另外处理，不能由配置存在推导模型工具已可用。
- Write／Edit 在实际写盘成功之后清理已交付诊断，并独立启动 change、save 通知；普通工具不等待语言服务。插件重载保持 manager 指针，回收旧进程、清空旧打开文件集合，后续使用才启动新进程。全局退出先取消 LSP，再等待 Host native 引用并回收实体进程。

## 原版与真实进程观察

`native-lsp-file-consumer.fixture.ts` 调用原版完整 Read→Write／Edit，仅在 manager 接口的异步通知边界注入 held、reject、missing 六组条件。原工具均先写盘，通知看到新物理字节和旧缓存，工具返回时缓存已更新；held 不阻挡工具返回，reject 进入原错误报告路径。该接口夹具不是语言服务进程证据。

另一个 `native-lsp-runtime.fixture.ts` 使用原版已安装插件加载器、实际 manager 和真实 Python stdio peer。原始 Content-Length 协议显示：热服务为 initialize→initialized→didOpen→didChange→didSave；冷服务为 initialize→initialized→didOpen，启动完成前的 save 被原版丢弃。原注释曾让夹具错误期待 `$/setTrace`，实际原版没有发送，保留该无效前提的失败，删除 Go 多余通知。

Go 热服务的真实 Write 10／10 和 Edit 9／10 曾先发 save，属于已复现的顺序缺陷。修复 native writer 的同步入队顺序，两个独立通知任务各自等待实际 I/O；重跑各 10 次全部通过，没有等待延迟或扩大窗口。冷启动采用原版 starting 状态语义，不把 save 改成等待初始化。

## 已运行检查

本机 ignored `artifacts/go-migration-20261013/` 保存原始日志：

| 检查 | 观察结果 |
| --- | --- |
| 原版完整工具 held／reject／missing 六组 | passed；零被拒能力、零秘密读取 |
| 原版安装插件→实际 manager→真实 peer 冷／热 | race passed，clibootstrap 3.422 秒 |
| Go Write／Edit 热通知，各 10 次 | race passed，builtinwrite 3.486 秒、builtintools 3.408 秒 |
| Go Write／Edit 实际 FIFO 挂起初始化，各 3 次 | 初次 race passed，2.588／2.429 秒；随后原 trace 前提修正，完整包必须重新检查 |
| 启动四种 profile＋实际 held initialize→Session shutdown | race passed，clibootstrap 4.905 秒；原进程已回收，无晚到 initialized |
| 两个实际 workflow detached factory 子任务共享／撤销 ledger | race passed，3.349 秒；同一 PID、一次初始化、一次 didOpen，子任务关闭后父进程继续处理真实请求 |
| 实际 ReloadComponents→旧进程 shutdown／回收→新配置懒启动→新 PID | race passed，3.140 秒；同一个 manager，旧 open 状态不复用 |
| 新 MainPrint 接线后的 CLI／SDK JSON Agent 实际 Read、MCP 只读诊断 | race passed，5.307 秒 |

完整 builtin 包第一轮只在新增 held 测试错误等待至少两帧时失败：原版没有 trace，真实初始化只有一帧。已将探针改为观察实际 initialize，原窗口与工具／缓存／磁盘／cold-save 断言保留。第二次完整包在另一任务尚未保存完 LSP 类型声明时编译失败，未执行测试；不能记为产品 RED 或 passed。待不可变快照重跑。

后续当前树 `go test -race -p=3 -count=1 -timeout=5m -json ./internal/builtinwrite ./internal/builtintools ./internal/lspruntime` 全部通过：250 个 test run events，零 fail／skip，三个包分别 2.717／48.738／2.575 秒。该结果包含已修正的实际 FIFO 初始化测试与所有既有 Write／Edit 消费者；原失败仍保留，不将此聚焦包结果称为整个 Go 门禁。

## 完整配置生产器与错误报告

新 `native-lsp-configuration.fixture.ts` 执行原版完整 `getAllLspServers` 和真实已安装插件加载器，与普通 Native MainPrint 的配置结果对照。九组覆盖默认配置、环境变量／回退／缺失变量、已保存选项、原始 UTF-16 选项、无 manifest 的 user placeholder、缺必需选项时单插件隔离、manifest 覆盖 `.lsp.json`、socket／workspace／initializationOptions，以及禁用插件。所有 Source 守卫均没有被拒能力；凭据读取仅为私有替代 primitive，不访问系统 keychain。

实际 Source DEBUG 文件复现了 Native 缺失 WARN 的 RED；补回 command／args／env／workspace 插值的缺失变量收集，按原顺序去重，并复用当前进程的错误日志与 debug sink。随后又复现单插件配置失败时 Native 漏掉原 ERROR 行，补回原错误格式，保留其它健康插件。九组完整 Source 回归最终 race 10.778 秒通过；既有完整 pluginmcp 包的九个顶层测试 race 7.059 秒通过，零 skip／fail。没有更改 MCP 的错误格式或创建第二套 logger。

## 实际会话关闭的取消竞态

原 MainPrint 层的 held initialize shutdown 通过，不足以证明外层 `application.Session.Close`：新实际会话→loopback scripted provider→实体 LSP→FIFO 阻塞的初始化和 hover 两组均复现关闭先等 `turnMu`、尚未取消 LSP 的 RED。该提供方仅为确定性 HTTP 夹具，没有调用真实模型。会话退出前置清理增加 `BeginNativeLSPClose` 后，又在重复回归中抓到 hover 竞态：取消请求先从 pending 表删除，退出检查便可能误认为语言服务空闲，随后 shutdown 卡在原请求之后。

新的真实 stdio client 回归在 request 已返回取消之后执行正常关闭，稳定复现该失败。现在实际已入队请求在 context 取消分支先终止其语言服务，再返回并移除 pending 项；正常空闲 shutdown／exit／Source 默认 SIGTERM 语义保持原路径。该边界没有增加延迟或 deadline。client 取消和正常关闭各五次 race 3.555 秒通过；实际 Session 的初始化阻塞和 hover 阻塞各五次 race 10.437 秒通过，模型仅请求一次、零外部网络尝试、全部实际子进程 PID 已回收。

另一真实 Write 消费者先启动热服务，让 peer 在第一条 didChange 后实际阻塞，再执行超过 2MiB 的第二次 Write。磁盘与共享 Read cache 已更新，实体 stdin 管道却无法继续写入；原 BeginClose 仅检查 RPC pending，稳定复现通知 writer 阻挡 Close 的 RED。现在将实际写入项保留在已有队列中，物理 Write 完成后、交付完成通知之前移除；BeginClose 因而能精确取消尚未完成的 writer。没有使用 goroutine 仍活跃作为 I/O 未完成的替代判断，也没有为正常 idle 服务增加超时。五次大写入／热 change-save／冷 initialize 的消费者 race14.940 秒通过，client 取消／正常关闭各五次 race3.793秒通过。原版全局 `gracefulShutdown` 自有 cleanup／failsafe 预算，不能把原 manager.stop 的等待或这项 native 回收修复描述成新增 Source 单操作超时。

## 尚未关闭

上述共享生命周期测试是实际 workflow factory 范围，不能替代普通前台／后台 Agent 的完整模型工具链。另一实现任务已有九操作及公开交互消费者的新聚焦结果，仍须与当前取消修复和最终候选统一检查；此文档中的配置／日志证据不替代其它分支。原版 shutdown 本身没有超时，不能为通过测试自行添加退出时间策略。

完整 Go unit／race／90%、最终 verify、同一候选桌面／Web／CLI、Windows 实机、真实 IM／有声语音及正式 120 对 Benchmark 仍未通过或未执行。用户已排除真实 Claude OAuth；没有执行真实登录／刷新。不切分支、不提交推送、不合 main、不发布。
