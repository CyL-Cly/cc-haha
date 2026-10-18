# checkpoint13 原 verify 当前运行

这是 immutable intermediate diagnostic，最终迁移未完成。所有组暂停非 ignored 写入时，当前 tracked/untracked 13,013 输入 before/copy/post 类型、模式、SHA 一致，捕获 7.902 秒；manifest SHA256 `6127d301f5bfe8a3cbe14ebcb6105154e1994ef9e8dabb11be2a689b6fceb136`。五份依赖树与实际 runtime preflight 使用前一份13准备证据。没有切分支、合 main、执行 Claude 真实 OAuth。

实际冻结13 `bun run check:impact` 通过 4.287 秒，14 required lanes / 2,202 外部 importing files。随后实际执行原 `bun run verify --artifacts-dir <private artifact directory>`，没有 only/skip/allow-live 或阈值变更。进程由 ignored `run-checkpoint-13-verify.py` 执行，session 53580；当前仍 running，完整日志与退出回执在 `artifacts/go-migration-20261013/checkpoint-13-verify-*`，不能称已通过。

已观察的阶段：原 policy 38 files / 620 tests 通过 55.92 秒。desktop 原 lint/typecheck已走到 Vitest：545 suites / 9,069 tests passed、11 skipped，另一个 storage relocation suite 在 beforeAll 失败，实际原因是构建环境 helper 未传仓库根，artifact Poppler 在私有 TMPDIR 下被正确拒绝。Root已在当前树给实际 Bun helper补真实 rootDir；原13不修改，修后实际worker仍待复跑。

server build 在同类 CLI helper rootDir 遗漏处 setup failed。Go lane完成平台与完整包/Source inventory，原 gofmt argv 因长绝对路径触发 E2BIG；未运行 unit/race/正式90判定，不能写为业务失败或 coverage通过。正在当前树修实际调用边界和保持完整 Source 集合的按字节分批；原13错误与日志不删。后续 parity 与其他 lanes 自然继续，最终完整 report 未出。

Root 的实际修正 helper 诊断 v4 已通过：与 storage worker 相同的 Bun helper、private TMPDIR 内 HOME、真实 55 module offline hydrate 与 `go mod verify`，exit 0 / 4.410 秒，Poppler 实际仍在已校验 artifact 根。仅环境准备通过，不称实际 worker或整 Electron通过。v3 诊断错误地在该进程 TMPDIR 外建立 HOME，被原 guard 正确拒绝，作为 invalid setup 原样保留。raw/result 为 ignored `root-storage-worker-env-actual-v3/v4.*`。真实 production CLI build、Go persistence、Go matrix 的同类 rootDir 调用遗漏与 UTF8 argv 分批已有独立修复组接手；不能放宽路径 guard 或减少 Source 集合。

当前工作区已完成 CLI build、Go persistence、Go matrix 两分支的真实 rootDir 传递；原 strict guard 与快照13字节相同。旧 CLI/persistence 各保留实际 Poppler guard RED，修后同区及环境负例共12项 Bun tests passed。新的 matrix 实际 Bun CLI 回归仅替代 Git process primitive，确实执行严格环境/物理文件快照/closure validator，并仍以原90%门槛拒绝六个缺失的 native target。该测试已接入原 check:policy；Root 的 quality-contract + matrix 原完整两文件实际10 tests/156 expects passed（0.190秒）。Matrix 旧代码 fail-first 未单跑，fake persistence 不称实际 Go 验收。完整收据为 ignored `hermetic-root-callers-evidence-20261015/receipt-v1.json`，SHA256 `c8749885e60d79aea7aef81ad1cb6ffb8d6b86e7f157da93d3959e0b76693a77`。

gofmt 仅改为保留全量顺序的 UTF8/NUL 参数预算分批（每批≤96KiB）；不改 build/vet/unit/race、源码声明或90%门槛。长 Unicode 801 实际临时文件在旧执行器路径先触发有效 E2BIG RED，修后5项聚焦与12项原 fake-lane 回归通过。Root另用当前生产 batching helper，在快照13全部5,935条原长绝对路径实际执行物理 gofmt：11批全 exit0、无格式错误、1.914秒，原未分批 argv 为1,010,839字节，所有 Source 与 helper SHA 前后一致。只读快照13不修改；该通过不代替 Go build/vet/unit/race/coverage。真实回执为 ignored `root-physical-gofmt-v1-result.json`；独立回归收据 `cli-flags/gofmt-argv-batch-receipt-v1.json` SHA256 `ed6bd19ce9c9f02bd19a457b15fe08e01a478e9ed16bf09bd19c3ad81c314393`。

当前 Root storage worker 原 Electron Node `tsc -p electron/tsconfig.json` 实际通过（2.130秒），测试文件前后 SHA一致。worker 的物理 Go build/迁移测试、修后完整桌面/Server/Go lane及完整当前 policy 仍未运行。原13 verify session53580在 Source corpus 阶段继续实际运行，最终 report 尚未出现；当前修复未混入冻结13，最终同候选三端/真实模型/正式120对仍未完成。真实 Claude OAuth 按用户要求排除。

当前另发现普通短命令 preAction 缺少原 `utils/sinks` 模块与实际接收端。新增 whole Source main→Commander→init→initSinks→plugin handler 试验实际通过1 test/17 expects（1.184秒），同步 error 队列与异步 analytics 队列保持原对象引用和原 Promise，真实 private debug append成立；observer 不作 cold timing 权威。最终 Source 收据 `cli-flags/native-command-sinks-preaction-receipt-v2.json` SHA256 `57f95b46c331d4ba9f3743b1718aef27636b44b2b499984adec730f7aeea57de`。新 Source test 已接当前 parity 原 corpus，冻结13不改。

Root实际使用已有稳定 v52 binary（SHA256 `c739470e48a0f73e2997a00b3d7536533fe2c82a8a0ab5d141d8c27905cd8b72`）做公开旧Go对照：七个当前 Command consumer 与该完整 build manifest相同，八个Source/fixture输入开始/结束SHA不变。两边 ordinary `plugin marketplace list --json` 都[]/exit0/stderr空、settings不变，Source物理初始化debug记录1，Go没有debug文件，形成有效旧业务RED（2.544秒）。完整收据 `root-command-sinks-public-red-v2.json` SHA256 `35c4aea9bfb7f53fde030fbb543a9143037e83d403b4c7725a1fea2251fcbfd1`。这一有界instrumented pair两端HEAD各1，不能关闭无包装coldHEAD/标题P0。v1诊断错误地假定nonessential流量设置会禁Source HEAD，作为invalid保留，不改生产。Go新强消费者 `native_command_sinks_test.go` 已声明/gofmt解析但未运行；typed sinks与实际队列/后端近修继续，旧binary不是当前最终候选。

本机只读远程入口观察见 ignored `root-external-access-observation-v1.json`：现有设备标签WINDOWS11显示在线，真实OS尚未确认；应用另显示服务器2003错误，桌面与终端都出现接管现有会话提示，均取消并保留原连接。没有Windows OS/应用测试通过证据；该事实是连接观察，不是缺少用户授权。

原13 parity 后续观察到一个实际失败：`session-controller.test.ts:118` 的 `multiple-sdk-original-priority-batch-and-last-uuid`。生命周期组核对实际断言后更正最初诊断：108行的Source self-replay先通过，118行是 `expect(go).toEqual(source)`，实际为Go↔Source的raw transcript用户/queue-operation/last-prompt记录顺序及尾部last-prompt差异。不是两Source自重放失败。完整 lane尚未退出；不排序或删除记录，不改原Source/golden。current fixture/test/cases/capture四个SHA与13相同，原完整失败片段已保存，SHA256 `f6c6efe35cef8a18921c15f77f2f5231ac0c36685757d6497ec40cdd499624dc`。该test的beforeAll有真实Go构建，不能将带这个hook的filter执行称Source-only；生命周期组先继续原始调用边界审计，原13只读。

随后原 parity lane 实际结束：65 files /3,166 pass /1 fail /54,117 expects，总3,360.04秒。唯一失败仍为上述Go↔Source比较，manifest保存原13,009个corpus输入。原log2278的五次真实native file/promise优先级观察通过2.936秒，五份raw SHA全相同 `176e3284c8af991d5111c35f112d996cfdffac3898e38a565dd7bee43f17ee05`，末lastPrompt均now original next original，完整用户/queue顺序也相同。因此不能修Source fixture去吞Go差异，cold P0仍OPEN。原完整verify继续其他lane，尚无全项结束报告。

原13完整verify现已实际终态：Root继续同session53580收到exit1/4,151.606秒，原report `2026-10-14T20-05-00-591Z/report.json` finishedAt为2026-10-14T21:14:11.267Z。含impact报告为9passed/6failed/0skipped；原14 required lanes为8passed/6failed/0skipped。通过：policy、provider-contract、chat-contract、agent-flow（8场景）、agent-e2e（11场景/19 mock请求）、adapters（797tests/61files）、docs、原JS/component coverage。失败：desktop、server、Go、Go parity、native、persistence。原JS coverage lane通过429.157秒，不代表Go changed-line90%已测；Go unit/race/正式90仍因先前gofmt E2BIG未运行。

native失败已核实为本机工具链先决条件：当前只有CLT/Swift6.2.3/SDK26.2，无完整XCTest framework/module/runner，`xcrun --find xctest`实际exit72，现有compat dylib依赖缺失XCTest.framework。实际Swift私有env选择同一路CLT，没有存在的Xcode被DEVELOPER_DIR/SDKROOT过滤丢失；生产helper链接只是build-only，测试与后续Electron/packaging门禁没有通过。只读诊断收据 `swift-toolchain-audit-20261015/receipt-v1.json` SHA256 `61e191e87e996315105ed350dec7fd8488a256006189588109c6da98170d75e0`。未安装/改系统工具链、未删测试或放宽guard。persistence仍是已在当前树修复的rootDir caller原失败。全部原日志保留，当前修复和新消费者尚待新候选复验。

生命周期组独立终态复核通过：14lane状态/各log SHA/首失败分类/完整report SHA保存在 `checkpoint13-independent-final-lanes-audit-20261015/final-required14-lanes-receipt-v1.json`，SHA256 `e115176012a4f27dab83e386761e58e60a994a83151e2a2e15a2ceb4f52cada2`；13,013个源码/配置输入结束后身份全部匹配，四个None仍不存在。post identity回执SHA256 `a4ce752fbc4edc31a55ed0b390d0bb1586f85a1e9f0964c5c50faf18243a0e59`。原依赖目录中的Vitest生成results.json自然变化另记，不能混为源码变化或继续使用旧依赖全部不变声明。

原coverage实际desktop log另证明物理storage worker3 tests通过（50.225秒，其中成功迁移用例1.441秒），包含真实Go build、unknown fields/source不变、失败不发布、取消join；该coverage wrapper使用其自己的隔离环境，因此没有触发前述strict桌面lane继承Poppler根错误。它证明冻结13 worker的实际迁移消费者，不称修后current worker/严格desktop lane/新namespace的完整通过。原Source输入与当前环境修复测试不同，证据不合并成最终同版本候选。

freeze 后当前树新增 user instructions test，故 pool fresh CLI另做新的 6,619 输入稳定身份；Source/Go普通 absent 与 explicit-zero Agent 两个实际入口通过，当前 binary SHA256 `c739470e48a0f73e2997a00b3d7536533fe2c82a8a0ab5d141d8c27905cd8b72`。它不混作冻结13或最终候选，详见 [pool消费者](go-agent-process-http-pool-audit-20261015.md) 和 [native zero域](go-native-plain-env-zero-evidence-20261015.md)。普通短plugin物理HEAD仍0/5、cold title仍0/5；完整TUI、最终同版本三端与正式120对继续未完成。

当前 policy 的原完整命令已复验：39 files /625 passed /1 skipped /0 failed /8,017 expects，51.288秒，package及全部39测试文件SHA前后不变；私有HOME/TMPDIR，无真实凭据或Go编译。唯一skip是未提供可选MCPB路径，随后使用本任务已验证的私有MCPB SDK，原同一测试聚焦实际通过（1 passed /48 filtered /0 failed /2 expects，0.103秒）。两次证据分别保留，不能改写第一次为626通过。收据在 `root-current-policy-20261015/result.json` 与 `mcpb-focused-result.json`；原完整policy日志SHA256 `7b2abc6eaacbd2aec52347288340b48d2bd0bc7a652567dfcd3d9ee5ec85372a`。这是当前策略修复的有界证据，不替代当前完整Go/race/90%、最终verify或产品候选。
