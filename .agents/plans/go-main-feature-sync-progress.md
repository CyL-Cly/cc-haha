# main → Go 同步实施记录

目标与需求以 [冻结同步计划](go-main-feature-sync-plan-20261012.md) 为准。开始于 2026-10-12；本记录更新于 2026-10-13。状态：**进行中；未达到全部完成定义**。

参考 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`；共同 BASE `6b9d2aec1d9321a4dfff90d16b51f10c1ab984c8`；起始 Go HEAD `64c2fa1c7213e8f39c10e2cba8d7361625449b7e`。实现留在该工作树；没有切换分支、提交、合并或发布。BASE/MAIN 源码快照与逐文件 hash 在 `artifacts/go-main-sync/source-snapshots/`。这些 ignored 输出仅作为本地证据，源码与测试才是交付物。

## 当前状态

“已实现”仅表示代码与生产调用已落地；“局部 passed”不代替最终候选门禁、实际入口和 live 验收。全部 ID 达到目标后才关闭总任务。

| ID | 实现与确定性检查 | 尚需完成 |
| --- | --- | --- |
| D01/D02 | MAIN React/native 最终树已同步，保留 Go 特有逻辑；React、Electron 测试局部 passed | 最终包主题、菜单、语言、键盘与各能力实际入口 |
| D03 | Go 离线市场、分类、detail/file/install已接；新发现的warm技能缓存缺口已补ConfigDir隔离通知/owner队列/首次目录watch；same SID安装→执行→卸载、WS命令列表×3 race passed | 更新后的UI真实模型Skill执行；最终包卸载 |
| D04 | MAIN webview guest/IPC/native 安全与生命周期同步；对应 Electron 测试 passed | 最终包浏览器操作、PDF/下载、picker、Go CU |
| D05 | CSV/TSV、stat、NFC/NFD、checkpoint/watch已接；中间签名Native真实模型生成中文CSV、产物按钮Sheet预览2行实际数据passed | 最终包预览复验 |
| D06 | Go rewind接线与React已接；中间签名Native编辑→确认回滚16消息/文件→真实DeepSeek重跑→Bash两测试/Write/Read新产物passed | 最终包同流程复验及失败恢复 |
| D07 | Go trajectory/prompt snapshot 与生产 Harness 请求接线；MAIN 对照、69MiB/HTTP/race passed | 最终桌面主/子 Agent 浏览、live tail/搜索/定位 |
| D08/D09/D10 | 对应 MAIN 桌面/H5/语音代码已同步；不能据此关闭 | 对应计划完整入口、模型依赖、权限及跨平台验收 |
| D11 | Team UI 已同步 | R07/R08/R15/R16 生产行为与真实多成员任务 |
| D12 | 新设置、Fast/effort 全生产链和索引升级局部 passed | 最终设置界面消费、官方 OpenAI live、CLI/桌面恢复 |
| D13 | Go maintenance admission/validation/activate/recover 实现；HTTP/race 局部 passed | IM drain、managed 路径全 consumer、故障事务与旧 session live |
| D14/D15 | 更新/保留 Tauri Go 入口、打包配置与 MAIN native 同步；Rust 31 项 passed | 最终完整包与所有受支持入口、升级实际结果 |
| R01 | commit/服务器工具判定、分类预算/退避/status、终帧容错、owner watchdog、nonstream fallback与独立usage已接；13项真实MAIN兜底对照、12项SDK公开输出/跨ASK持久化、实际Bash副作用一次回归 race passed | 最终同一候选完整门禁与真实任务；404兜底按MAIN不共享API预算，不能声称所有分支共享 |
| R02 | manual/auto/partial、hooks/文件恢复/共享预算/UTF16 progress、TUI双Esc选择/代码撤销已接；实际PTY与物理模型链通过；Go Agent E2E压缩修复后 passed | 最终桌面操作及真实模型完整压缩任务 |
| R03 | 同 child/iterator promotion 实现；物理副作用一次、输出不丢与 lease race passed | 最终入口 promotion 真实长任务 |
| R04 | inclusive boundary 已下沉原始链；过滤前截断、无 interruption/continuation；24项实际Source/公开CLI初始化失败及正常恢复 race passed，原文件不变 | 非 bare hook 命令、最终桌面/CLI交接和真实旧 session fork |
| R05 | disconnect generation/timer、实际Stop取消/join、进程父存活guard及Windows整树策略已接；真实plan恢复/取消/断线/queued 10场景完整Source+Go race passed | TeamWorker终结审批事件、hard ceiling状态与全局Close最终集成；Windows实机未测 |
| R06 | 有界 locator 早退出与同物理文件判定已实现；locator 与真实 hardlink repository launch regression race passed | 最终候选及受支持文件系统的大小写入口 |
| R07/R08/R15 | mailbox Claim/history、依赖/交接、supervisor/恢复/Stop及诊断已实现；MAIN Source/race与无socket审批→两实际Harness→权限→Write/消息/任务链 passed | 最终桌面及真实模型 Team |
| R16 | 持久化checkpoint/metadata/UUID增量、真实Goal hook/等待、失败重试/邮箱唤醒已接；实际 owner/HTTP/Source/race ×3 passed；普通WaitSessions保持MAIN原语义 | 最终CLI/桌面长时恢复与真实模型任务 |
| R09 | root runtime 域 limiter、配置/前后台/嵌套共享、满额即拒绝；物理及 race 局部 passed | Team 独立域/同域集成、真实并行任务、最终覆盖 |
| R10 | selection→env→official Responses priority→ACK→meta/index→fork 全链实现；v6→v7/parser10 fixtures passed | 最终候选及官方 OpenAI live |
| R11 | 2.1.288/cli、SDK0.128.0 wire 和 env/config account UUID已接；MCP真实HTTP/SSE/stdio和SDK/client-app标识对照 race passed；TLS创建失败数据实际重录后passed | 最终完整SDK候选与真实官方登录/请求 |
| R12 | Go managed metadata worker贯穿实际Desktop DataMigration事务；26项MAIN物理对照、42项desktop迁移回归、persistence-upgrade passed；Read/文件API/ImageGen读取移动路径/坏映射拒绝物理race passed | 最终签名桌面迁移、旧会话/附件live继续任务与候选新鲜度 |
| R13 | 所有 Go IM migration stdin/真实drain与失败ACK/idle/ConfigDir/relocation已接；全量MAIN Source+race、8实际ctor、QQ cold resume ×3与Windows/Linux编译passed | 最终桌面控制与获授权真实平台账户消息 |
| R14 | 默认 vision、明确拒图才重试、成功后30min有界缓存；物理 HTTP、MAIN Source、race passed；诊断回调成功后一次/缓存命中不重复/失败不记录回归passed | 最终诊断UI、CLI/proxy集成、真实支持与拒图模型 |
| R17 | index全局active1/pending8、claim/cancel/rollback、SQLite真实锁和spool清理已接；全Source/race passed | 最终迁移及候选门禁 |
| Q01/Q02 | Go原生Agent E2E全部11场景/19物理上游请求 passed；CI blocking lane/policy passed；冻结MAIN请求contract有raw hash收据 | 最终候选新鲜度、Benchmark配对证据 |
| Q03/Q04 | 动态全package覆盖/strict repository-input freshness已接；policy572与归属静态检查 passed；新版Source goldens实际重录中；保留90%门槛 | 全Go/verify；失败与覆盖debt整改；最终fresh重跑 |
| Q05 | MAIN 46个文档/网站/发布资料同步；Go用法已修正，docs gate与发布脚本测试passed；网站实际播放/语言/窄屏操作passed | 最终集成文档核对 |
| Q06 | provider preset/赞助资料同步，保留旧保存provider；MAIN静态对照通过 | 最终设置新建与旧配置恢复消费 |
| B5最终验收 | 中间签名Native实际迁移130文件/280MB、旧fork真实工具继续/编辑重跑/中文CSV预览passed；语音/MCP/Agent与Skill管理已实际操作；6对原版/Go真实模型预跑12任务/69请求全部产物成功、0runtime/工具错误 | 正式Benchmark仍insufficient 6/120；默认Agent API三额外字段已修，最终包/所有UI/跨端与多Agent尚需完成 |

## 已观察证据

- 桌面第一轮：React 9,060 passed / 8 skipped；Electron 720 passed / 4 Windows-only skipped；Tauri Rust 31 passed；lint/types/build passed。这轮使用 Bun 1.3.12，最终集成检查使用 pinned Bun 1.3.14 重跑。报告 `artifacts/go-main-sync/desktop-report.md`。
- 市场/轨迹：5 包 race passed；真实 shared Harness 请求→snapshot→trajectory；69MiB trace near/at 有界查找、物理 HTTP、冻结 MAIN 投影对照 passed。报告 `artifacts/go-main-sync/market-trajectory-report.md`。
- 迁移：11 项新回归 race×3 passed；Server 全包 passed；Application Team/Plan/Migration 聚焦 race passed；Windows Server 编译 passed。全 Application/Scheduler 尝试遇新版 Source fixture 缺 helper 与 scheduler Source 超时，正在修复；没有登记成 passed。报告 `artifacts/go-main-sync/migration-report.md`。
- Root policy：91 项聚焦 live/mockLlm/change-policy/goBackend 测试 passed；79 项 workflow/runner/quality/test-environment/coverage 测试 passed。Go locator race passed；providerproxy/providerproxytransport 包含真实 HTTP 与配置冻结 MAIN Source 的 race passed。
- Go Agent E2E：第一次10/11，发现compact progress把非文本事件计入节流；修复后完整11/11 passed，上游19请求。此为确定性loopback生产链，不是真实模型。
- DeepSeek官方 `/models` HTTP200，准确model ID `deepseek-flash`。Go CLI真实Read/Edit/Bash修复隔离项目并运行Go测试；首轮达到人为设置的8轮上限，随后同SID恢复得到success。敏感key只保存在私有临时目录，没有写入仓库/日志。这一限定场景通过，不代表全部live目标。
- Web UI通过正常输入框和实际审批执行真实DeepSeek Read/Edit/Bash，修复后1项unittest通过；实际fork增加负数测试后2项通过；同fork手动compact显示进度/summary，再真实Read/Bash确认2项仍通过。`live-web-evidence.json`、`live-web-task.png`明确记录中间Go binary SHA。fork冷读曾遇一次Edit前未Read的普通工具校验，已通过模型正常Read恢复，保留该事实。
- 原生中间包使用实际Developer ID签名，host/Go/helper `codesign --verify --deep --strict` passed。实际UI下载SenseVoice→ready，麦克风录音/停止/识别/空语音返回可用；另用明确英文合成speech经实际Go recognizer准确返回原句，两种证据分开，不能把空录音称为有声准确率。最终Go尚在更新，不能用中间包代替最终验收。
- Go公开CLI的missing-at错误原多了resume失败前缀；已按MAIN `ResumeMessageNotFoundError`区分类型，24项实际Source/Go错误路径及正常路径race 32.772s passed。MCP默认UA原错误沿用local构建版本；现遵循MAIN 2.1.288及SDK/client-app元数据，实际Source三种transport和四种metadata配置race 6.087s passed。
- R01真实原版兜底13场景（含401、503/重试、Stop、consumer return、双usage、共享预算和404例外）race 7.022s passed；实际公开CLI缓冲Bash遇截断重发后nonce物理文件仅写一次，race 3.332s passed。SDK公开system输出12个实际Source案例、当前ASK内存和下一ASK持久化2.273s passed；新版SDK TLS默认/坏CA数据由实际MAIN重新采集后Go2.039s passed，保留原始hash/备份。
- 正式Go门禁第一轮gofmt失败已修；第二轮build通过、unit失败（270 passed packages/13,625 passed test receipts），race未跑。source写入44个变更导致证据stale，仅用于诊断。已观测覆盖59.9843% < 90%，包括原迁移与controlled dependency debt；不降低门槛。完整 `check:go`、`verify`、最终签名桌面、配对Benchmark仍未通过。

- 更新后的原版/Go配对runner已完成6对不计成绩的setup pilot：真实官方69请求，12个完整CLI任务均通过独立产物judge，runtime错误与工具错误为0；部分批次正确返回insufficient。没有把6对外推成120对正式结论。固定协议见 `go-main-paired-benchmark-protocol.md`。逐请求检查发现Go Agent额外暴露name/team_name/mode；已补实际MAIN API过滤/首次缓存行为，5个真实Source缓存变化、本地schema不变、desktop Teams偏好与retry跨ASK持久化race passed。
- 完整诊断v4发现R05 close-restart真实重复cleanup回归，正在修复；旧desktop retry测试错误要求下一ASK不保存system controls，现按真实MAIN QueryEngine consumer证据严格验证retry/recovery各一次且失败assistant不入库。clipboard native child在全instrumented检查1秒admission失败，聚焦实际race×15 passed，保留原始失败，不放宽时间或宣称全门禁通过。
- 中间Web真实UI新安装Pdf技能执行失败（warm会话unknown prompt command），没有model请求；已沿市场通知→每会话owner→缓存/命令注册→WS消费者修复，standalone首次目录watch也已补；原版实际Source捕获确认其缺失首目录watch差异，记录为实际用户路径改善，未修改Source期望。

- 最后新增Agent API修复经过独立审查与实际Source对照：背景禁用启动flag下local parser/API/compact预算同一schema；full toolschema race13.068s、builtintools22.671s passed。新候选实际Go Agent E2E再次11/11、19上游请求passed。真实CLI普通双Agent同一assistant并发声明、两份实际sidechain Read/Write、主Agent不可改Python judge成功，原输入/测试未改，公开JSON无坏行；binary与SID见 `live-parallel-current.json`。
- full policy首次605/606，失败为Benchmark回归测试5个dead imports，正在删掉无引用import后重新验收，没有关掉检查。Root fresh runtime/Application/SDK完整race保留15分钟watchdog，Source大包正常参考耗时clibootstrap约13分钟、application约10分钟，未提前中断或改超时。

- 新鲜policy在删掉5个无引用import后完整606 tests / 7699 assertions / 0fail passed（41.57s）。Windows审计确认是implementation gap：builtinwrite.New平台guard在首次聊天premodel拒绝Windows，后续还有tools、GitBash/PowerShell/cwd、CU SDK拦截；`Windowscompile passed`只证明构建，不能把这一组标accepted。W1文件/路径/identity、W2Shell/terminal/permissions、W3CU已按生产调用链分配；完成真实Source边界与生产接线后才解除总guard。

- 市场warm安装执行真实验收passed：更新Go Web保持同一SID，正常Read建立warm owner→UI卸载Pdf→UI重装→不reload直接/pdf→真实DeepSeek选择可用pdftotext/pdfinfo→4次普通Bash审批→原生Read产物；1页/28字节/GO_SKILL_PDF_LIVE_20261013。输入PDF601B/原mtime与MD5保留，Python库缺失由模型正常改用已安装工具。详细receipt `live-warm-market-skill-current.json`。
- Native→CLI→Native同一已迁移SID passed：正常Cmd+Q并确认所有隔离host/Go/helper退出→Go CLI读取旧proof/中文CSV、运行两unittest、Write/Read CLI_DESKTOP_SHARED_SID_OK→正常启动Native同SID新轮/产物preview可见。原文件与测试未变，CLI公开JSON无坏行；当前仍中间签名包与9d08CLI，最终包需同一fresh hash再验。
- Root完整fresh race：clibootstrap794.769s passed、sdkhost24.160s passed；application591.323s failed，只有QQ/WeChat生产工厂cold-resume两项超时失败。保留原日志，QQ/微信owners正沿实际sessionStore→ServerClient→MainPrint cold恢复查因，不修改deadline或忽略失败。

## 连续执行与约束

2026-10-13 续轮新增核验：

- W3 的 Windows Agent Computer Use 生产链已补：原始 22 个像素工具、捕获时坐标锚点、多显示器/重试/zoom、一次全应用授权后的输入、剪贴板恢复、batch、同 SID 全局锁、取消/join 与持有鼠标清理，接入 Windows MCP/技能/MainPrint，保留 macOS JavaScript 面。实际 frozen MAIN 原工具/执行器对照 212 调用用例及 24 个显示/截图场景通过；取消前后通过真实原 binder 的同源对照。完整 CU/MCP/mcpcli race 分别 38.116s/2.373s/14.221s passed，原 macOS MainPrint 回归 8.084s passed；这不是 Windows 实机证据。
- 原 `--computer-use-mcp` 原始入口先前未分派，现复用 Go JSON-RPC transport，按实际 Source SDK 保留两平台 catalog/0.1.3/logging metadata 与无 session 的明确 Call 错误。四个实际 Source SDK profiles、两个坐标模式全 schema 对照 passed；无 fixture build tag 的真实 Go CLI child/stdio/禁用后空 catalog/无 ASK、无输入锁通过 8.792s。两平台 bundled skill 的完整声明/提示词、8 配置 gate 与参数追加 Source 对照 passed 3.079s。
- 完整 Windows MainPrint 的新测试仍待两个 file/tools 总 guard 闭合。首轮 `native permission startup platform` 诊断经复核是测试把公共 Go 平台写成 Source 的 `win32`，不是实际 `runtime.GOOS=windows` 的生产入口失败；已只修正该测试字段，CU service 的 Source 平台仍为 `win32`。原失败日志保留且不再据此宣称新生产缺陷。W1 的实际 Windows 追加目录/UNC/PWD 别名/失败语义确有宿主 filepath 依赖，已用共享纯 lexer 和实际 Source startup 对照补齐。
- 冷 session 已有 Read 未进入文件工具缓存的真实回归已复现（直接 Write 报未 Read、文件仍为 before）；已将 Controller restored ReadCache 接给实际 owner 的文件缓存，冷恢复直接 Write 及 resume/fork/截断负例通过。另按真正的 Source `forkContextMessages` 条件核对 Agent cache 继承；普通首次 Agent 必须空缓存，不能用父 Read 绕过独立 Agent 校验。
- Windows PowerShell 使用实际安装的 pwsh，并与真实原 PowerShellTool.call 对照 8 条物理命令、文件/cwd/下一请求上下文，race 11.521s passed；完整权限 decision、路径、AST/6 模式正在收敛。`priority: now` 的实际非重复 admission/interrupt cause 正在补齐；默认 queued Submit 和 Stop 的行为分别保留。
- 电脑控制工具要求临时启用 H5 局域网或 Computer Use 访问时当场确认；相关问题已提出，尚未收到回复，未启用。这两项实际 UI 验收与 Windows 实机、正式 120 对 Benchmark、90% 整体门禁仍开放。

当前并行工作：桌面Agent补Windows W1原生路径/文件/恢复与identity，并继续最终包UI；设置/运行时Agent补Windows W2 Git Bash/PowerShell/terminal生产链；市场Agent补W2 PowerShell权限/AST判定并完成W3 CU拦截审计与Q03真实缺口；主Agent负责真实模型/UI/跨端恢复、剩余集成、完整检查与正式Benchmark。确定性测试使用临时home/config与loopback；live使用独立profile，不接真实用户配置。

真实模型凭据：用户此前提供的DeepSeek key已通过官方HTTP200验证，可继续使用。隔离key路径不作为公开文档内容；生产配置与日志均不嵌入key。官方OpenAI/OAuth、真实IM与Windows实机证据分别需要对应环境，不能用DeepSeek/编译结果代替。

2026-10-13 续轮新增 Auto/内部设置引用验收（仍非整体完成）：

- Auto 实际入口原有 guard 已逐个闭合：CLI 显式/保存默认启动、Source cached circuit breaker、初始危险规则移除、fresh gate/plan kick-out/规则恢复、真实 classifier 与运行时 model authority；`--enable-auto-mode` 只增加可选入口，首次交互确认仍执行。TUI 保留原 400ms 首次确认、默认模式选项、可信 user/flag/policy consent、拒绝回退/退出和 Shift+Tab 继续前进；确认前不提交模型/执行工具。
- 原版函数未修改：54 startup profiles race3.087s、224 active/inactive/compiled cycle observations5.248s、42 startup/plan/fresh gate profiles3.183s、16 settings synchronization profiles（含 active Auto + UTF-16 rules）2.395s passed。完整 Session 真实确认→保留未知设置→独立 classifier request→实际 Bash 写文件通过6.780s，TUI choice/advance2.108s、MainPrint CLI/savedAuto3.291s。最初失败日志保留：未接启动、两个 preflight mode guard、内部保存后 settings identity、active Auto settings guard。恢复后 Bash(*) 按原 rule parser 规范成 Bash 是测试预期修正，不是恢复丢失。
- 使用真实官方 DeepSeek Flash/Anthropic Messages，当前中间二进制 `0ae372ad83ba051e732fde8440e0b298ebeaeda9f77c2275a4fc8a6454aeecf0`：普通 print CLI 默认19 tools → 主模型3请求 + 独立 classifier1请求全部HTTP200 → Bash/Read →实际 AUTO_REAL_DEEPSEEK_OK、success/exit0/JSON无坏行（6.520s）；实际 Go TUI/TTY默认交互catalog → 原始确认/保存默认Auto/未知字段保留 →键盘提交正常任务→同样真实4请求→Bash/Read→AUTO_REAL_TUI_OK/真实最终assistant/exit0（5.368s）。`live-auto-current.json` 与 `live-auto-tui-current.json`。两个先前TTY收集器错误（提前单Ctrl-D/未drain、EOF空bytes未停止）保留失败receipts；真实任务本身成功，不能把driver failures算为通过。最终同一候选仍须重跑。
- 新真实 PS TUI 第二轮暴露内部记住权限更新使 ASK settings reference 失效。现在只在原 Owner 执行的权限持久化/model/fast 变更上保留严格 lineage，权限 scope 外未知字段、env、禁用设置、半代理变化仍拒绝；保留 in-memory session rules，不用全 settings reload 覆写。真实 TUI reader/记住编辑前缀/localSettings/第二Human输入 + Auto 确认联合race application12.302s、SDK2.135s passed。`get_settings` 原版主动reset cache且内容未变的独立刷新也已接只读证明；改变内容仍需原 notifier/实际内部编辑，SDK输入最终回归在进行。
- W3 独立审查捕获并修复实际 `--computer-use-mcp` 未设置CLAUDE_CONFIG_DIR启动错误；临时HOME默认 `.claude` 与显式目录catalog一致、真实生产child/stdio/no-model/no-lock回归8.780s passed。另原始UTF-16到Windows native输入及MCP回显不再换成U+FFFD，236原Source调用场景用原JSON比较避免双侧解码掩盖，21额外UTF16/Unicode场景2.787s；完整CU/MCP race38.351s/2.722s、Mac回归6.545s和Windowscrosscompile/vet passed。Windows error-envelope原始错误保真和shell hints/图片下游仍在闭合。

- W1/W2 必要路径/执行/permission/SDK producer 闭合后，仅解除 builtinwrite/New 和 builtintools/New 两个 canonical Windows 总入口 guard，unknown platform 与所有具体绑定 guard 保留。Root 完整 Windows MainPrint→模型 SSE→22 CU 工具/截图/输入→第二 Human 轮→同 SID 锁收尾无 macOS worker 的 graph race16.669s passed，`w3-windows-full-harness-open-guards-v4.log`；native OS primitive 受控且在 Mac 执行，仍不是 Windows 内核实机证明。W2 actual Source MCP 全声明（PS30000/strict/outputSchema）+bare/普通 realpwsh/保留cwd/image/mandatorysandbox12.224s passed。
- Root后续真正React UI→最新中间Go Server（62903）→真实已迁移fork SID warmRead→原 Auto 确认→配置 opt-in →Bash/Read/实际 WEB_GO_AUTO_REAL_OK→最终消息通过，receipt `live-auto-web-current.json`，binary `b9fb44d8acd297f0210cb938c37deba006160f070063bf4d410d7ac217586970`。该UI检查同时复现可见未闭合项：ContextUsageIndicator 的 includeContext/contextOnly inspection返回500 INTERNAL_ERROR；新失败已分配独立隔离旧history回归与修复，整个UI不标通过。
- SDK实际标准输入 now/defaultqueue/重复UUID ACK/真实HTTP取消/同workload prompt合批/一次Write/下一Human/checkpoint/EOF/coldresume fresh race4pkg通过13.165/4.081/2.938/1.742s。原 test 误以为相邻now与queued分别执行两ASK；frozen MAIN实际合并为 now\nqueued，已修测试按原合批事实核验3次HTTP，未改production排序/超时。

- 2026-10-13 继续执行：Policy 两次失败保留；原因是 package script 的子 `bun` 经 PATH 仍解析为 1.3.12（即使用绝对 1.3.14 调用父进程），违反原 manifest runtime 断言。通过完整 pinned PATH 调用，当前 606 tests / 7,705 assertions / 0 fail（39.97s）passed，没有改 fixture 期望或门槛。
- 独立 SDK review 找到真实内部 settings 认证缺陷：已认证 JSONObject 可变，baseline 与 current 同指针导致无关 language 字段自我认证。Root 已保留 identity 并新增 owner/base 不可变完整 raw JSON snapshot，各 permission/model/fast/get_settings 生命周期共用，ASK 同时检查 identity 与值，reload/notify 清理证书。原物理两 ASK 用例扩展成四个独立实际进程（language、nested shared reference、raw lone UTF16、AppState base alias），全部拒绝未消费变更且第三请求不开；fresh sdkhost race 4.329s passed。扩展测试首次复用已失败的同一 MainPrint 导致后续 serial ASK lifecycle 错误，已按真实终结生命周期改为独立进程，保留初失败，不改变生产终结行为。
- 原版 retained attachment context 对照发现 text/truncated/directory/image/plan 六处缺绑定，修复已由 W2 实际 Source 对照 passed；另发现旧 PDF mapper 及实际新 Read PDF producer 尚未实现，继续补齐，不能把旧附件可检查等同于 PDF 工具完整可用。真实 UI footer 500 仍等待 fresh candidate 复验。
- 当前中间 Go Web binary b9fb44d8... 的真实旧 session Auto 回合已补齐去重 trace：3 个主 ASK stream 与 1 个 auto_mode classify_result 请求，官方 deepseek-flash 全部 HTTP200。正在从正常输入框验收 TeamPlan 审批与两个实际 DeepSeek 成员；不把该中间 binary 当最终安装候选。
- SDK认证独立复核 passed 后又收紧 reload 的生命周期：同时清除 legacy model/fast marker；新增旧 fast cache 不可在下一生命周期被接受的 consumer 回归。fresh actual Source GetSettings、4个物理两ASK负例、Auto/权限/model/fast三包 race passed（sdkhost4.821s/clibootstrap10.606s/application6.692s），独立审查者复验亦passed。
- 真实 Web TeamPlan：正常 composer 选择 Agent Teams→实际 DeepSeek 产出二成员计划→UI 查看两名成员 deepseek-flash 并确认启动→alpha/beta 分别写正确JSON→lead Python judge `TEAM_REAL_GO_OK`。三份输入/judge SHA保持不变。模型初次把lead设为任务owner以及遗漏owner遭两次正常schema校验拒绝并恢复，收据保留，不登记0工具错误。实际暴露收尾缺陷：双方shutdown批准后，Go Server仍保留live worker引用，supervisor因而认为连接存在，UI收尾中/工作中、TeamDelete失败。原版有意拒绝process型shutdown_approved，必须沿实际worker self-close→server严格identity retirement补接；不能放宽TeamDelete或mailbox schema绕过。W3正在fail-first生产回归和修复。此项任务产物通过、完整Team生命周期尚未通过。
- 当前 pinned Bun 1.3.14 `check:desktop` 完整通过：545 files / 9,063 passed tests / 8既定skipped，lint/typecheck/生产build passed（42.55s测试，最终Vite1.11s），日志 `desktop-pinned-fresh-current.log`。仍不证明Go生产候选或native全部完成。影响面路由已使用用户明确授权的CLI核心重写范围设置本地 `ALLOW_CLI_CORE_CHANGE=1`，当前blocked=no，保留全部14个required checks；未设置missing-tests或coverage exception，未改仓库labels/settings/发布。


2026-10-13 续轮：CLI 公开入口、context/PDF 与原版完整 server 检查（仍非整项完成）：

- Context 旧会话冷读 500 已由 W2 补 retained attachment mapper/plan 默认消费者；新中间 Web `f4eb125d…` 62904 在 copied disposable profile 上 cold footer 4%，后续真实 Read/PDF warm 仍正常 6%。`live-context-ui-candidate.json`、`live-pdf-pages-web-current.json` 仅证明这个中间版本，不替代最终签名包。
- 原 Read full PDF 真实两请求 HTTP200，但 DeepSeek 返回 Unsupported Document；实际冻结 MAIN 同内容也如此，`live-pdf-source-current.json` 保留任务失败。后续 Helvetica 未嵌入 PDF 的 Source/Go/独立 pdftoppm 均渲染纯白，模型未编造文字；不计通过。新嵌入 Arial 的独立三页 PDF 经正常旧会话 composer→Go Read pages=1-3→3 张原生图片→真实官方两请求 HTTP200，准确返回两处验证码及 17+23-5=35，只有 Read、没有 Bash/技能或文件修改。frozen MAIN 同一个文件/任务也成功（3.823s，两请求，输入哈希保持）。两版 live receipt 分别 `live-pdf-pages-web-current.json` / `live-pdf-pages-source-current.json`；full-document 与无字体环境限制仍分开记录。
- Team self-close 原 Server live 引用未退休已沿 strict runtime identity 回调补齐；实际多成员 question/task/dependency/mailbox/shutdown→supervisor terminated→TeamDelete race passed。另 Source conversationService 只在 canonical transcript 实际存在时 resume；empty file=true，missing=false。Go 旧缺失 worker transcript 路径现在保持同 SID fresh start；五种真实 Source findSessionFile 模式及完整 application/server cold restart race passed（3.154s/12.287s）。下一次 fresh Web/native 还要真实模型复验；先前 live TeamDelete 失败收据保留。
- 公开 resumeSessionAt 原过滤后重复校验导致合法 UUID 消失的错误已修；24 Source 错误 profiles、原默认 CLI 四项测试、两种实际 Source/Go tool/attachment 过滤物理进程 passed，父 transcript 保持原字节。详见 `cli-resume-at-report.md`。
- Root manual compact 原公开 CLI 两项失败找到缺失的 QueryEngine system/local_command dispatch ACK。现在在 async 本地 work 前生产原 marker，保留 peer literal input、sensitive/prompt/JSX/unknown 排除和原 UTF16/空格/alias name。actual QueryEngine 原块+真正 findCommand/parseSlash/format 对照（含 alias 30 rows），物理原 compact/Stop/public loader 7 tests passed；owner/requestretry/sdkhost focused race2.216s/1.668s/2.571s passed。
- 默认 `check:server` 之前消费 Oct7 的 ignored CLI 二进制导致 stale evidence；package script 现在先 standard build:cli，再运行原 server runner。原 quality-contract 路由回归 12 tests/183 assertions passed；未减 check/env sandbox。当前 pinned PATH/Bun1.3.14 完整 server lane **600 files / 7,136 tests / 0 failed files passed**，`server-full-pinned-with-cli-rebuild-v1.log`。后续 Go 还在修改，不能据此声明最终候选全门禁通过。
- 公开 CLI 原语法81 options 不等于业务闭合。实际审计43个 accepted-but-capability 参数/动作及 Source 路径见 `cli-public-open-surface-audit.md`。三组继续做producer→runtime：W1 continue/system prompt files/name/prefill/rewind/init，W2 deny tools/agents JSON/slash/betas/dynamic strict MCP/permission prompt tool，W3 fallback/USD/task budget/workload/auth events。Source --task-budget 是 first-party output_config token budget（不是本地 token 上限），disable-slash 不关闭模型 Skill 全局catalog，主 agent tools 不覆盖调用本身 tool 选择；按真实 Source 修正审计/断言。
- Root auto-mode defaults/config/critique 公开 actions 已使用同一 MainCommand settings/credentials，critique 用明确 owner-purpose sideQuery，不构造 ASK/Controller/工具/转录。19实际原 handler 输入/输出/原 prompt/options profiles race2.686s、7实际完整 Source CLI/Go CLI进程含401/首text/空text/no-custom/rules保持/no ASK profiles race11.280s passed；新typed owner用途/foreign/read callback/失效scope回归也 passed。第一次 Source wrapper 忘记原 TRANSCRIPT_CLASSIFIER 编译feature导致空defaults/未注册 action，已按原现有parity相同 --feature profile修正，仅修fixture启动条件，不改Source/期待。
- 真实 Go auto-mode critique 首次未发模型即失败：saved autoMemoryEnabled=false 触发 Native settings schema guard。发现现有 memory enable/prompt/AgentCatalog/文件发现消费者已在，但 Native schema名单遗漏。Root 正补 bool验证/顺序/旧字段保留，并以真实 Source schema和含该字段的public command回归；不删除 live 配置绕过。首次 `live-auto-critique-current.json` 尚未 passed。
- 正式120对、最终同一SHA/signature desktop/Web/CLI、90% Go改动覆盖/full check:go+verify、所需真实Windows/官方OAuth/IM外部条件仍开放。目标 active，未commit/push/merge，未发布，未用live或局部绿替代确定性门禁。

- Native autoMemoryEnabled schema遗漏修复后，Source18schema profiles passed1.841s；actual7 publicCLI profiles（含memory true/false）passed11.608s，full systemprompt/usercontext Source+race passed1.383/1.993s，7pkgvet exit0。真实官方 critique重新构建候选21255fe795db8f7ec7990a899c18959d9b52a18374bafcb0fd53e11836b2ff52后，1个nonstream DeepSeek Flash HTTP200、14.614s/exit0，针对自定义规则返回真实评审；settings原样、无ASK/工具。原模型未发出时的startup失败保留在live-auto-critique-startup-memory-failed.json，不删除测试字段或改期待。详见cli-auto-mode-report.md。

- 新21255fe7…中间Web62905通过正常旧会话冷恢复→实际TeamDelete→普通Bash审批→python3 team-judge.py，真实官方3请求HTTP200/0toolerrors，UI已完成2/2并恢复Run，旧团队与tasks目录已删除，三输入/judge及两原产物SHA不变；初B9失败场景/配置未覆盖。live-team-cold-cleanup-current.json只证明冷恢复收尾；新二成员依赖/handoff/各自批准shutdown/self-retirement完整live复验正在开始。


2026-10-13 继续：安装更新生产入口与真实团队恢复（尚未整体完成）。

- Go `install` / `update` / `upgrade` / `doctor` 已接入 MainCommand 的 settings/credentials/Owner。安装只接受校验 SHA256 与 Go build identity 的 release artifact，稳定通道筛选预发布，保留全局未知字段和 native 保护标记；错误 checksum/旧 TS artifact 不替换原可执行文件。实际编译后的 CLI 安装、执行安装版本、更新 alias、只读 Doctor 与真实 loopback MCP initialize/tools/list 测试 passed（`cli-installation-doctor-process-v6.log`，10.346s）；Source 11 target、3 XDG 环境及 17 通道/配置 profiles passed。版本生命周期锁与旧版本清理正在补齐，此段不表示全部安装工具完成。
- 发布构建沿用各平台最终签名 sidecar，新增独立 CLI asset 和六平台 manifest。只有构建测试；未发布 release，公开 GitHub 目前缺少该新 Go manifest 时安装明确失败。manifest/staging/workflow 回归 302 passed、0 failed；4 包 vet 和 Windows CLI/installer crosscompile exit0。actionlint 不可用，最终 policy/整体门禁仍需重跑。
- 实际 Web Team v2 复现：子成员审批先于 lead complete 会从 UI 消失，浏览器重连的同值 set_runtime_config 还会误重启活跃 runtime。已按 frozen MAIN 的 completion replay、normalized same-config ACK-only 接上严格身份与事件排序；真实 DesktopFactory + 模型 peer + 实体文件 + 原 Source 对照 fresh race passed（`team-runtime-replay-physical-final-v2.log`，9.067s）。不同配置仍按原 deferred restart，已 resolved/retired 的审批不复活。
- 新中间候选 a8ab48f0… 62906 使用隔离旧团队同 SID 继续。首次错误继承 NODE_ENV=test，在认证 preflight 就被原 CI 规则拒绝，0 模型请求；改为 production 且清除 CI 后真实官方模型 HTTP200。alpha 原会话确实恢复并完成 Read，继续到 Write；新的成员 Write 审批仍未显示，正在跟进实际 parent/child 生产链。未重建团队、未代写产物、未修改输入或 judge；此恢复任务仍 failed/pending，不能据此标 Team 全通过。
- 用户已批准本次隔离 H5 局域网与 Computer Use 开启/授权/测试/恢复关闭，正在安排实际 native 验收。用户没有 Windows 实机或虚拟机，明确要求继续其他项目；Windows 内核实机验收延期，Source/compile 结果不记为实机通过。凭据保留在私有隔离文件，不写仓库或日志；模型继续冻结 DeepSeek Flash / 官方 Anthropic Messages，显示名称不冒充 provider model ID。

- 旧版本清理新增真实 frozen MAIN 的实体目录/符号链接/7天 mtime lease/active PID/过期下载与临时文件对照，race passed2.476s（初次fixture null-array driver失败保留）；安装后已接真实 cleanup consumer，actual CLI child安装/执行/更新/Doctor/MCP及Source cleanup联合 fresh race cmd18.484s / installer2.749s passed。不得把 Source注释的30天误当实际常量（实际7天）。生命周期 mtime lease 有原 stale/2 心跳和 release/join，replacement inode 不被旧lease删除。NPM清理/其他Doctor/包管理器分支仍待实现。
- 原生已实际打开H5设置→当场已批准的enable→确认dialog→局域网监听/随机token/QR可见；正常QR导航到同机LAN POSTverify返回403 CORS origin not allowed，服务端Source与Go均按空allowedOrigins拒绝同源。已关闭H5。Root仅修改桌面设置操作：显式enable/改址/rotation按API返回的effective QR URL登记exactHTTP(S)origin，保留用户existing origins；不改ServerCORS/Token/local-control policy。旧空配置升级、proxy origin/path、idempotent、badURL/credentials与persist failure fail-first4失败后82同区域回归passed；生产React/Electron build passed。
- 新原生中间包已正常Developer ID同证书构建，codesign --verify --deep --strict exit0；Go sidecar SHA1734c29d…，private profile启动PID29562。初次两构建未找到helper稳定签名、第三build隔离HOME使codesign找不到既有identity；保留原失败，使用生产packaging的正常证书查找环境和既有CC_HAHA_SIGN_IDENTITY后通过；没有创建证书/修改权限/发布或notarization。运行时与测试配置/cache仍隔离。新的H5/CU真实操作待执行，不是最终Benchmark候选。
- Web v2冷恢复alpha已完成真实Read；新的Write审批在原warm UI隐藏。保存截图和snapshot后，通过正常浏览器reload恢复了原pending，正常允许alpha写入，继续捕获下游实际帧。保留warm丢失的失败，不把reload当修复；原完整Team/handoff/judge/shutdown收尾尚未通过。


2026-10-13 续轮：H5真实旧会话、团队完整收尾、大工具结果缺口（整体仍未完成）。

- a8ab48f0… Web Team v2 在保留首次 warm Write 审批消失的失败后，正常 reload→允许原 pending→alpha实际Write/Read/SendMessage交付→beta依赖解锁后Read/Write/Read→lead实际Python judge `TEAM_SELF_CLOSE_REAL_OK`→双方shutdown_response批准→TeamDelete完成。原输入/judge哈希未变，任务产物sum15/count3、mean5/verified_total15，团队/tasks目录消失，实际27个官方DeepSeek Flash请求HTTP200。`live-team-v2-current.json`仅将“正常reload后的产物与收尾”标passed；首次warm审批丢失仍open，不能把恢复操作当fix或完整性能Benchmark。
- 新签名native1734c29d…通过实际H5设置enable/原同意→exact QR origin持久化→正常LAN QR验证200/连接旧session→正常composer真实Read calc.py→两官方Flash请求HTTP200/准确报告return a+b。结束后Native正常关闭H5，原链接reload进入禁用连接门。`live-native-h5-current.json`仅证明这个中间版本的连接/真实工具/关闭；冷inspection三次500保留，warm footer恢复3%。
- H5冷500在新的saved-provider-only profile上已定位：只读TranscriptPricing误用需要进程直接凭据的model selector，改为原pure parsing；实际模型的认证保持原严格路径。78行原失败snapshot fail-first500，fix私有回归及永久 Source无凭据账单/context/limits/public cold/warm老文件不可变回归race通过（server4.775/application5.394/modelaccounting1.813s）。签名native fresh cold复验待下次候选。
- Native真实CU回合失败，SID0590cf8d…：名字/bundle碰多实例时被正确拒绝；数字PID参数不属于原App API，模型随后自行选了另一个安装路径，偏离隔离目标，不能登记通过。Root通过正确full-path native绑定停止回合，重新观察“已停止”，并正常Settings将Computer Use关回off。该次任务入口未提供完整隔离路径，复测须明确；不增加per-app授权/allowlist。原始输出只留私有目录，公开failure证据去除另一个实例的UI内容。
- CU同时暴露Go queryloop真实未实现large-tool-result persistence，19个失败调用中11个命中此capability。Root新增实际 Source per-tool持久化：50000全局clamp/原工具Read Infinity与Grep20000/feature override优先级、图片原样、UTF16大小与2000预览、独占创建不覆盖、可选IO失败保留原block。串行/并发/post-hook重映射四条实际查询路径经SDK/MainPrint同Owner接入，只有大结果启动原native future/file IO，普通结果保持纯处理。原Source14实际FS profiles race2.571s通过；MainPrint普通catalog+真实HTTPMCP大结果→完整.json→模型正常Bash/Python真实读取尾部→下一请求结果回传，两readonly profile race6.676s通过。最初test把JSON text里的escaped换行当物理行4004，Read当然找不到尾部，失败保留；按原输出.json格式使用实际解析器读取，未改尾部期待或生产存储。
- Root AutoDream纯叶已与实际Source锁11profiles/完整prompt2/任务完成失败kill与30turn上限/同一实体目录native顺序对照，并增加38组实际getConfig/isGateOpen配置优先级及Kairos/remote/autoMemory disabled边界，联合race3.317s通过。Source full memory override为CLAUDE_COWORK_MEMORY_PATH_OVERRIDE，REMOTE_MEMORY_DIR只决定base；首次fixture路径/guard/目录跨实例顺序错误保留。runtime fork/权限/任务UI/实际模型仍由独立owner继续闭合，纯叶绿不表示该feature整体完成。

- 大结果新消费链路五包focused Source/physical/race完整passed：toolresultstorage2.374s/queryloop6.311s/builtintools22.255s/sdkhost10.252s/clibootstrap312.846s（toolresult-storage-consumer-source-race-v1.log）；新增15组Source真实feature阈值/Infinity优先级对照passed3.841s，五包vet exit0。AutoDream纯调度实际FS测试含current排除、失败后599999ms throttle/600000ms retry、5.5 sessions及24小时精确边界，联合实际Source锁/配置/任务/完整prompt最终race3.778s passed。完整门禁尚未通过，不以focused替代。
- 新正常Developer ID签名包current-build-v2，GoSHA3a1a03aa528d4a994768d7f283a3d66e9497d7fc3801b103664c150d0eade0a5，codesign deep/strict exit0；cold普通Native sidebar打开原fork，ContextUsageIndicator3%正常，未发送新Human。公开inspection精确HTTP和转录哈希证据见live-native-cold-context-v2-current.json。
- 真实CU full-path复验被原native进程identity guard拒绝（SID99d15717…），仅三次getApp、没有点击/输入，恢复开关off。read-only AppKit实机诊断证明direct-executable测试启动的bundleID与exe正确，但NSRunningApplication.launchDate=nil；guard按原源码应拒绝。用正常LaunchServices open按同一私有HOME/config/user-data-dir启动后，PID72516具有实际launchDate813559616.158443，无身份/全应用授权规则放宽。此为driver启动方式纠正，不作为Go生产fix；同SID通过正常composer继续原任务，仍待实际点击/截图/关闭验收。

- 正常LaunchServices启动的签名3a1a03aa…同SID真实DeepSeek复测，通过完整path绑定/PID72516状态→Native网址输入和Enter→真实Click fixture坐标点击→实际MCP截图确认Q06 clicked→最终assistant/Run恢复；八个官方Flash请求均HTTP200，七次mcp__computer-use__js、没有Bash/HTTP/DOM。Root实际检查模型最后截图与Native AX heading/URL，不只采信模型陈述。结束后正常Settings关闭CU并AX验证off；H5保持off。live-native-computer-use-v2-current.json仅将实际导航/坐标点击/截图任务标passed。
- 该复测保留两次opaque元素定位错误“No snapshot element 245; call get_app_state”（并非一次类型参数错误）。原native helper确会对display-only/ambiguous snapshot path拒绝索引refetch，Source原App API也接收gN:id字符串；需要原版同实际进程对照才能区分Source公共限制与Go缺陷。坐标成功不将所有CU入口判passed，不修改定位/身份guard，也不将driver进程启动修正计成生产fix。

2026-10-13 续轮：CLI旧安装清理实际消费（非整体完成）。

- 新 `cliinstallation/legacy*` 对齐冻结 MAIN 的 `cleanupNpmInstallations`、`cleanupShellAliases`、真实 execa no-throw primitive；5个Source文件实际SHA逐项相等。npm全局清理仅指定原包与MACRO package，ENOTEMPTY仅unlink launcher、node_modules保留；空错误/E404不误报；旧local路径仍HOME/.claude/local；原expanded alias才删除，ZDOTDIR/三shell/NFC/UTF8/CRLF与权限位保留。
- 显式公开 `install` 接 MainCommand 的实际 native worker，先原安装成功再清理，warning/alias输出回CLI；`update`/Doctor不触发。实际Source+私有physical npm/files 8profiles race4.176s、完整installer Source/race6.040s、公开CLI安装/执行/更新/负向artifact/Doctor消费者race14.120s通过。无模型依赖，因此此项不消耗live请求。
- Fixture shared guard只授予精确disposable-home executable，必须显式私有env/cwd/stdout/argv批准；新10个正负边界与原HTTPS10个边界共20pass，已加入正式Go parity corpus；外部/目录grant、错误PATH/home/cwd/argv、shell和替换symlink均拒绝。首轮误用Bun非path filter扫描ignored snapshots导致失败记录保留，最终精确./路径passed。
- 3包vet、Windows amd64 installer test compile exit0；Windows真实OS未跑；公开GitHub未发布Go manifest，原版安装/升级公共release访问能力仍待授权发布后的实际验收，不用loopback冒充。新收据 `cli-legacy-install-cleanup-current.json`。最新impact选中14个lane，最终完整verify/90%覆盖和120配对Benchmark仍未通过。

2026-10-13 续轮：AutoDream 真实调用链与TUI修复（正式live仍待执行）。

- 原 native fork/authority/cancel路径fresh相关Source/race：autodream3.526s、builtintools2.302s、clibootstrap7.640s、tui2.437s通过；sdkhost/queryhooks该筛选无直接测试，不能把其 no-tests-to-run 记为Source覆盖。既有真实HTTP Write/拒绝越界/父prefix保留/skipTranscript通过；独立dialog kill、桌面Stop、RuntimeClose真实HTTP cancel+join+lease rollback通过。
- Root实际原 `DreamDetailDialog` 组件与useElapsedTime/formatDuration只绑定React render/clock/keyboard hook primitive，对照6种details/keys和15duration边界。原Go缺失elapsedsubtitle在fail-first-v3实际重现，补到TUI实际Tick/渲染后fixed-v4 race2.089s通过；Source正文和业务组件未改。新public默认完整catalog PTY覆盖3profile仍在修测试驱动，不以内部MainPrint通过替代公开入口。
- 新公共PTY已实际显示任务、写memory、complete/kill及memory_saved，但第一轮立即Esc后发整串输入丢失；后续改为等待实际编辑器恢复，先修Python bytes replacement错误及真实prompt尾随space匹配，所有原fail log保留，生产键盘逻辑未改，待公开复验定性。
- 官方Flash live隔离profile已准备：5份原版真实pilot父会话按字节复制并冻结hash，提供重复memory笔记供实际整理；当前未发模型请求。Windows实机仍不可用；完整任务/模型/最终benchmark尚未验收。
- 最新完整policy `policy-legacy-dream-current-v1.log`606pass/0fail/7735assertions；该时点非最终候选，后续更改依适用性复测。

- Public AD PTY v6已定位真实跨边界失败：completed→memory_saved写入原父history后，下一轮被requestretry admission拦为unported history class，模型未请求。Root补精确原MemorySaved factory类的Native admission，保留原record交由已迁移API normalizer过滤，非任意system通行；6正负/immutable回归race2.186s通过。公开完整路径v7正在复验，官方模型尚未启动。

- Public AD固定后v8与公开安装/update/Doctor合跑race16.532s通过：全默认≥19tools、真实Write/次请求、kill明确等待实际HTTPopened后再x、disabled无fork、下一ASK、父历史/先前5session不可变、lease与终端恢复。v7 cancelled-before-HTTP原失败保留，改等待实际边界加强cancel证明，未放宽计数/timeout。
- 随后production-tag中间Go binary12f1bba9…执行官方DeepSeek Flash真实CLI/TUI自动后台整理：5份真实原Source pilot历史复制、父源与副本hash均保留，22request全部200（19auto_dream），3memory文件实际改写，当前MEMORY索引已合并到一份规则，后续普通Read→回复与termios/exit0成功。**进一步tool轨迹审计确证7个unique错误，1个Go capability“large Read actual token-count API”未迁移、6Bash readonly拒绝待Source对照。因此driver初判task-completed不能升级accepted，整项live验收改failed。**原preliminary结果私有保存，公开脱敏`live-autodream-cli-v1-failed.json`，不删除失败或用后续成功掩盖。
- W2已接largeRead实际owner token-count await/已有nativeTokenCounter生产接线与Source/fallback测试；Root对6条实际Bash命令作原Source权限对照并补强live结果判定。最终新候选再跑，此12f1仍旧失败候选，不用于正式Benchmark。

- Root将真实Bash输入按原始JSON抽取，保留全部实际quoting/description，仅隔离路径归一化，新增permanent6case并固定USER_TYPE=external。最新actual Source→Go比较通过3.418s；进一步明确Source6case也必须deny的回归在v6收取中。只读权限不得为了让这次model好看而放宽。live driver今后会解析完整工具result并拒绝任何capability/unported，原v1preliminary弱判定已保留并标失败。

- 精确liveBash corpus v6进一步assert6条Source也均deny，race2.575s通过；6个拒绝确认为原版权限行为而非Go额外错误，不放宽权限。仍需补并复验的是1个largeRead tokenCount cap。真实live收据保持acceptance-failed，22官方HTTP200与完成产物只证明任务恢复成功，不掩盖缺失能力。

- `check:persistence-upgrade` pinned1.3.14 最新完整lane全部passed（persistence-upgrade-legacy-dream-current-v1.log），包含原Go持久化actual consumers、旧provider/team/UI/index/trace/connector升级与未知字段保留；有筛选的子测试按lane原设计filtered/skipped，不当作全surface模型证据。后续代码新鲜度与最终完整verify仍待关闭。


2026-10-13 续轮：官方大文件 Read 与后台整理复验（限定范围 passed，整体仍未完成）。

- 新 production Go CLI `7e9aef400b69453f579ec9c28d9b3da11e85d063fff91fbf0d1177d99dbc232c` 使用与失败 v1 相同的五份真实 Source pilot 历史，按 SHA 恢复三份原始 memory 输入；正常 CLI/TUI 自动后台整理→真实 Write/索引整理→正常下一轮 Read→exit0/termios restored。17 个官方 Flash 请求均200（14个auto_dream），0runtime capability/unported，父Source与五份副本不可变，lease为空。两条Bash拒绝也已作为精确JSON语料与实际原Source对照，8条v1/v2拒绝均为原版权限行为；不放宽只读权限。
- 此次模型选择分段 Read，不能据此证明新大文件分支被实际使用。另用同一生产 CLI/同 SID 普通resume明确完整 Read 39317-byte原JSONL，不传offset/limit、不用其他工具：实际官方 `/anthropic/v1/messages/count_tokens?beta=true` 一请求200、准确model deepseek-flash；真实Read结果非error；下一模型请求正确报告首记录queue-operation/末记录last-prompt，独立文件解析相等、原hash保持、exit0。脱敏收据 `live-autodream-cli-v2-current.json`。原v1 capability失败与其初判结果继续保留；这不是完整Benchmark或最终安装候选证据。
- 新隔离 Web Go Team v3从旧fork普通composer/团队计划/模型确认启动两名实际成员，首次Bash及首次Write审批当前正常显示；完整server WS和实际browser CDP帧均捕获中，正在验证leader完成时pending重放、交接与关闭。未使用reload或Stop恢复，不提前判整项passed。


2026-10-13 Git 跨机交接（用户要求先提交/推送，停止新增功能）：

- 交付独立 `feat/go-migration-20261013` 分支；完整目标仍未验收完成，不合 main、不发布。跨机启动与可直接交给新 agent 的指令见 [交接文档](go-migration-cross-machine-handoff.md)。原冻结 MAIN 保持 6d21316ec，交接时远程 main 已到 bb80a84d1，未集成。
- 真实 Team v4 中 alpha Write 首次 warm 审批正常可见；普通 composer 排队消息→立即引导→真实 Flash lead 完成 `LEAD_PENDING_V4_OK` 后，同 request ID 的审批仍在。实际 frame 为 message_complete→pending replay→SDK idle notification，无多余 session_state。alpha/beta 都已真实 Write/Read/报告，独立读取两份 JSON 与预期相符；用户要求先提交时 lead judge Bash 尚待审批，已正常 Stop 和关闭隔离 server，因此 judge/self-close/TeamDelete **not run**，不声明全 Team passed。安全局部收据 `live-team-v4-handoff-partial.json` 留本机 ignored artifacts。
- 独立前台 user lifetime 已补 startup window 与 result 前 settlement。原 Source SDK-only CLI running 仍对应用户 idle/turnActive false；两个旧重连断言按实测 Source 修正。当前 server 聚焦 race 4.422 秒、application 聚焦 race 12.659 秒 passed，完整 33 项在新 server 补丁后尚未重跑。仍有 connect 多发 snapshot 与 Stop 后旧 queued input 执行缺口，交接保留。
- Doctor 已完成公开真实 PTY Enter/Esc/实际保持 HTTP 中取消、Source/race、vet/Windows crossbuild；真实 Windows 不存在。CU 正式隔离 Settings consent 已完成，新签名只读诊断 40/40（31 refetchable、9 duplicate_fingerprint_and_topology）；v1 fixture 路径别名误报保留，v2 不回溯旧 g1:245。CU 已恢复关闭。
- 当前静态审计和脚本交付，原历史台账不改。普通 TUI local-jsx/default actions/媒体输入、七个 CLI root options 仍未闭合；本轮没有写 /help 或 --file 半成品。全量 compile-only 暴露 builtinwrite 内部测试引入 sessioncontroller 后产生 import cycle，提交前修测试边界并保留真实 Writer/Write/ledger/Owner 断言。
- 本轮敏感信息扫描 11,727 个 tracked/untracked 文件，提供的实际 API key 零命中；token-pattern 均为冻结 Source 的假凭据、例子或名称。生成 inventory JSON、构建/私有数据与 logs 不提交；跨机需要重新采集证据。全量最终 verify、90% 门禁与正式 120 对 Benchmark 未完成。

- 提交前当前全 Go compile-only（203 个 package 输出）已修复并通过；新增 Write 外部 test race 1.776 秒 passed，保留共享 pending ledger 与 async Mkdir/foreign scope/实际 flush=1 断言。审计/配对 runner/CLI launcher 聚焦 33 tests/0 fail；文档站已有依赖 build/check 31 tests passed，未重跑 npm ci。原 compact control 模板两处行尾空格属于 frozen Source 提示词字节，路径级 whitespace 属性保留它们，diff 可见且 Source exact prompt 回归单独复验；没有改提示词、模型参数或 coverage 阈值。
