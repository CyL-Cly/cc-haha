# Go 桌面完整等价：执行进展

更新：2026-10-06。本文件记录附件 D01–D35 的后续实施；完整范围不变，`fullMigrationComplete=false`。原盘点在初始状态发现 25 组缺失资源、175 处前端调用缺少入口。当前 Task REST 接通后为 24 组、170 处调用；数量只说明静态入口缺口，不能作为完成比例。

## 本次实际实现

### D04/D29/D34：插件配置与 Output Style 的真实 Server 消费者（2026-10-06 新窗口）

先新增回归，确认当前 Server 的 native settings 遇到真实插件目录会失败于 `native policy resolver/plugin host snapshot`。现已将 InstalledRegistry、Materializer、ZIP、Loader 与共享 CLI settings state 接入 `NativeProcessSettings`，并复用 `NewDesktopServer` 的实际 storage store。插件设置作为最低优先级，与用户/项目/本地/flag/managed 来源按原 customizer 合并；启停后的失效清理同步作用于插件、配置和输出样式缓存，进程关闭清理归档临时资源。原有限对照 profile 的边界保留；共享状态绑定要求实际 Loader 身份，不能用 absent 标志绕过。

Native settings 新增原五个字段的解析消费者：enabledPlugins、extraKnownMarketplaces、strictKnownMarketplaces、blockedMarketplaces、agent。复用原生 MarketplaceSource schema，保留未知字段，按原版处理记录特殊属性、discriminated union 非对象、continuable refinement 与多错误顺序。33 个输入经实际 SettingsSchema/formatZodError 对照；fixture 没有替换 settings、policy、registry 或 loader 业务函数。

Server 输出样式的 plugin source 已接实际 registry 和共享 frontmatter：默认目录、额外文件/目录、大小写扩展、命名空间、description 标量/正文 fallback、force-for-plugin 和重复路径。9 个原版场景覆盖各配置来源/覆盖、禁用刷新、文件覆盖 manifest 和插件样式；真实 HTTP 回归沿 `GET /api/settings/output-styles`、`PUT /api/settings/output-style`、`PUT /api/settings/user` 完成选择、禁用后消失/拒绝选择、重新启用及未知字段保留。没有启动隐藏 Agent 或模型请求。

当前 Source-enabled 10 包 race 为 774 pass events、8 个既有 fixture/专用捕获用例 skipped、0 failed；之后的首次读前缓存失效修正经 clibootstrap/application/systemprompt 再检查，150 passed、3 skipped、0 failed。数字是 test events，范围有重叠，不相加。Windows/Linux amd64 编译和 persistence-upgrade passed。[本窗口报告](../../artifacts/go-native-plugin-settings/report.json)及原始记录已保存；最终[正式 Go 检查](../../artifacts/go-checks/2026-10-06T02-59-26-095Z/report.json) build/vet、unit/race 各6,319 passed，整体 failed 于75.48%改动覆盖率与原8个映射缺口，1,357输入无漂移。第一次正式检查因期间输入变化被判 stale，未计作当前通过。

**完整工作包仍未关闭。** 这是 Server 的配置/输出样式生产接线；CLI/实际 Agent 的完整 plugin settings、Skill/slash/MCP/Agent/Hooks、管理 API、marketplace 更新/卸载/依赖闭包和活跃会话 reload 仍需完成。24 组缺失 API、170 处缺入口调用没有变化。完整原生 Electron/真实模型/平台实机/Benchmark 本窗口未运行，用户安装未更新。

### D04/D05：原生插件缓存/项目加载与 MCP 组件检查（2026-10-06 新窗口）

扩展实际原版 pluginLoader 对照：17 个隔离场景覆盖本地/文件市场、manifest 补充、无 manifest、启停/缺失项、外部缓存、损坏 known config 与 fail-closed 策略、GitHub/git blocklist 等价、inline 覆盖、managed 锁、builtin 默认/替换/空版本、add-dir 最低优先级和项目安装作用域/依赖降级。原版调用实际 settings/loader/文件系统；Go 的 settings/builtin 生产器在此 fixture 中显式受控，不代表 production bootstrap 已绑定。

修复第一次启动缺少安装记录时 schema literal 校验的 panic；项目读取的依赖降级现在保留原 enabled 意图；缺失版本按原版回退空 registry，避免误选缓存；市场嵌套文件的权限/Stat 错误不再触发另一文件 fallback；builtin 的空版本与未提供版本保持区别。权限 fallback 的旧决策先运行失败、修后 race 通过，原始前后日志留在证据目录。新增 native 回归覆盖缓存失效、并发发布、全量刷新/缓存隔离、损坏安装记录和权限边界。

| 当前检查 | 结果 | 范围 |
| --- | --- | --- |
| 原版对照与四包 race | passed：142 test pass events，0 skipped | pluginregistry/pluginschema/mcpclient/skillregistry；[报告](../../artifacts/go-plugin-registry-progress/report.json)、[原始 JSONL](../../artifacts/go-plugin-registry-progress/source-race-final.jsonl) |
| 当前 build/vet/gofmt | passed | Go 编译与静态检查 |
| Windows amd64 / Linux amd64 build | passed | 交叉编译，不代表实机运行 |
| 最终正式 check:go | **failed**；build/vet、6,300 unit 与6,300 race passed，分别67 skipped | [冻结报告](../../artifacts/go-checks/2026-10-06T01-42-45-313Z/report.json)：改动覆盖率76.63% <90%，原8个映射缺口；1,335输入无漂移 |
| check:impact | 路由分析执行成功，PR policy blocked | 现有CLI core diff的label/approval要求保留；不阻止已授权本地工作 |
| 完整 selected lanes / verify / UI / 模型 / 平台实机 / Benchmark | not run this window | 不把组件检查计入真实桌面或模型验收 |

**D04/D05 及完整迁移保持未关闭。** 通用来源安装/ZIP/依赖闭包、安装记录迁移与写入、完整schema/错误/并发契约、实际内置注册与Skill命令、原其他MCP传输/认证、配置/策略/凭据/活跃会话消费者、Server和正常默认工具仍需完成。24组整组缺失API、170处缺入口调用保持不变；本窗口没有更新用户安装或调用真实模型。

### D02/D06：共享原生 frontmatter 与 Skill 来源/详情组件（2026-10-06 新窗口）

新增 `backend/internal/markdownfrontmatter/`，接入 `agentcatalog.Frontmatter` 的实际生产消费者，替换原来会拒绝合法 YAML 的部分解析器。现支持多行、Hooks 对象列表、anchor/alias/merge、block indentation、YAML escape、原标量规则和未知字段；保留原版坏 YAML 的二次引用 fallback、非有限数的 JSON 投影与 String/truthiness 区别、UTF-16 正文切片及 key 顺序。实际原版 Bun 解析器确认 surrogate escape pair 也会失败并回退为空 frontmatter，原先只基于假设的 pair 接受测试已修正；合法 Unicode 文本/code point 保留。

新增 `backend/internal/skillregistry/` 的实际文件扫描与 metadata/detail 组件：用户/项目 `.claude` 与 `.agents` 来源、disable 条件、git/nested repo 停止边界、插件直接 Skill/子目录、覆盖/同名处理、symlink、UTF-16 长度、目录树/文件读取预算、原 market metadata 校验、请求错误和刷新读。首个 `fileCount=null` 回归确认无效 metadata 被接受，修后按原版拒绝。

原版 fixture 实际加载生产 frontmatter、skillRoots 和 Skills REST handler，读取同一隔离目录的真实文件/符号链接。解析对照 60 个输入；扫描/详情对照 6 个场景与 38 个原版 HTTP handler 请求。**只有 plugin producer 在这组扫描对照中受控**；这证明文件/根/元数据/错误契约，不证明实际通用 plugin registry 或活跃 Harness 接线。

| 当前检查 | 结果 | 范围 |
| --- | --- | --- |
| 原版对照 + 受影响 5 包 race | passed，196 test pass events；1 个既有模型菜单 Source 用例 skipped | [冻结证据](../../artifacts/go-skill-registry-progress/report.json)、[原始 JSONL](../../artifacts/go-skill-registry-progress/source-race.jsonl) |
| `go build ./...` / 受影响 vet / gofmt | passed | 编译与当前组件静态检查，不代表整包功能 |
| 正式 `check:go` | **failed**；build/vet、6,267 unit 与 6,267 race passed | [2026-10-06T00-47-34-455Z](../../artifacts/go-checks/2026-10-06T00-47-34-455Z/report.json)：改动覆盖率 77.56% < 原 90%，原 8 个 source mapping 缺口仍在；1,311 输入无漂移 |
| `check:impact` | 路由分析执行成功；PR policy blocked | 现有 CLI core diff 仍要求 label/maintainer approval；不阻止已授权本地工作，未声称 PR-ready |
| 完整 selected lanes / 最终 verify / UI / 模型 / 平台 / Benchmark | not run this window | 原完整要求保留 |

**D02/D06 保持未关闭。** 实际通用 plugin loader/credential/policy、完整 Skill command/Skill tool/Hooks/fork/条件加载、compiled/bundled/legacy slash 与 mentions、Server API 和正常四端消费仍需完成。`skills`/`connectors` Server 仍在 unsupported，24 组/170 处入口缺口不变。生产运行不调用这些 TS fixture；没有修改用户配置、模型 quota、已安装应用或发布状态。

### D01：managed plugin bridge、cache/registry 与 settings（新增窗口）

新增 `connectors/plugin_artifact.go`、`plugin_bridge.go`、`plugin_store.go`。实现 native/Skill/remote 包装指令、远程 MCP manifest/配置、受控 credential 占位符、managed marketplace 所有权、安装时 disabled 发布、cache 复制和 user-scope installed registry、停用与卸载、旧 v1 记录迁移及其他 scope 保留。`settings.Service.UpdateEnabledPlugin` 复用原 settings writer 的同路径事务和 cache invalidation，nil 只删除选中 plugin key，false 保留 disabled 意图；未知字段保留。

`Installation` 增加仅内部的 env key 顺序记录及兼容 JSON 编解码，持久化形状不变。原 DingTalk 环境的顺序进入 Skill 指令，避免 map 枚举改变模型看到的内容。账号目录与真实凭据未写入本窗口测试。

已验证：

- 55 个原 catalog Skill 包装文本、43 个远程配置及额外非法 URL，与原生产 renderer/buildRemotePlugin 对照通过。
- 原 cacheAndRegisterPlugin、addMarketplaceSource、removePluginInstallation、orphan marker 实际文件行为，与 Go 本地 cache/registry 在同一隔离路径比较通过；没有路径或输出归一化。
- 并发准备保留 marketplace/cache 各成员；冲突和 force-disabled policy 拒绝；key 不落入 Skill；卸载只移除 user scope，保留 project scope 和未知 metadata；最后一个 scope 的缓存保留并按原 epoch-millisecond marker 标记。
- Source-enabled `connectors` + `settings` race：100 个 pass events，0 failed / 0 skipped，见[冻结报告](../../artifacts/go-connector-bridge-progress/report.json)及[JSONL](../../artifacts/go-connector-bridge-progress/source-race.jsonl)。build 与受影响包 vet 通过。

**范围仍是组件。** loader/managed marketplace policy/credential deletion/active session reload 使用必需的共享服务接口，测试中的这些宿主消费者受控，尚未接入 production factory。不能把文件存在、loader callback 或 Source 组件对照算作 Agent 正常使用成功。Server 的 Connector 路由仍是 unsupported，当前缺口仍为 24 组/170 处。

下一步仍需实际 Go plugin/Skill/MCP registry 与各服务绑定、活跃会话刷新、credential/OAuth adapter、Server 全 API 以及默认 Harness 的消费，之后再验证正常桌面、真实账号/模型和 Benchmark。当前普通二进制早于新文件，保持其原范围。

本窗口整包 [Go 报告 2026-10-06T00-09-19-536Z](../../artifacts/go-checks/2026-10-06T00-09-19-536Z/report.json)：build/vet、6,257 unit 与6,257 race通过；整体仍 failed（77.67% 改动覆盖率，原要求90%，8个source mapping缺口）。1,304个输入文件核对无漂移。Source-enabled受影响包证据和默认整包检查分开；全部selected lanes、最终verify、原生UI、真实模型和Benchmark未在本窗口完成，没有提交、发布或改用户安装。

### D01：Connector 组件与原版契约（当前新增窗口，仍未接通桌面）

`backend/internal/connectors/` 的服务、状态恢复、native 安装/认证 adapter、Skill bundle adapter 已增加实际原版对照和文件/进程验证。当前 Server 的 `connectors` 仍在整组 `unsupported` 分支，不能据此关闭 D01 或减少 24 组/170 处缺口统计。

本窗口先观察到失败，再修复：

- 旧 schema 数值按原 JSON number 判断，支持 0.0/1e0 等原可接受表示；损坏 JSON 使用原错误，未来 schema 与无效 records 不覆盖原文件。
- `installed`/`operation` 的 truthiness、installedVersion 的 nullish fallback 和解码后的字符串比较与原版一致。
- 状态写入使用原 JSON.parse/stringify 的 number、UTF-16、字符串和 key 规则；内存 records 保留 undefined 的插入位置，避免后续赋值改变原磁盘顺序。未知字段保留。
- 取消进度在拿到操作锁后重新检查；adapter constructor 失败不提前 detach；catch 中写入失败仍执行原外层 disable/reload，失败顺序不多执行原版跳过的 reload。
- native 认证 URL 使用既有 WHATWG URL 实现；账号标签保留原 trim/control removal/UTF-16 slice，包括截断后的 lone surrogate。

原版 fixture 直接加载生产 ConnectorService/ConnectorsPersistence/CLI parser/catalog，比较 19 个动作场景、磁盘字节、调用顺序、错误及全部 55 个 catalog 定义。Service 的 adapter/bridge I/O 被控制，只证明这些业务契约；不是第三方账号或实际插件运行验证。

另外实际执行了 loopback HTTP 下载/checksum、tar 解包、受哈希约束的隔离可执行文件及状态命令、篡改拒绝、只删除自有目录、真实子进程树取消/输出上限、Skill 文件校验和 symlink 拒绝。所有 HOME/config/files 为临时目录，没有第三方网络、真实账号或模型调用。

| 本窗口检查 | 结果 | 范围 |
| --- | --- | --- |
| 初次新增回归 | failed | 确认旧数值/URL/标签/JSON/版本 fallback 差异，原输出保留在本对话工具记录 |
| 当前 Source-enabled Connector race | passed，33 个 test pass events，0 failed / 0 skipped | [冻结报告](../../artifacts/go-connector-progress/focused-report.json)、[原始 JSONL](../../artifacts/go-connector-progress/focused-source-race.jsonl) |
| `go build ./...` | passed | 当前全部 Go 编译；不是完整功能验收 |
| `go vet ./internal/connectors` | passed | 当前 Connector 静态检查 |
| Windows/amd64 `go test -c` | passed，compile-only | 未执行 Windows 原生进程或桌面 |
| `check:persistence-upgrade` | passed | 原升级 lane 与新 Go 组件旧 fixture 分别记录 |
| `check:impact` | 路由分析执行成功；PR policy 显示 blocked | 现有 CLI core diff 要求 maintainer label/approval；不阻止已授权的本地迁移，不能宣称 PR-ready |

仍需完整实现：managed plugin bridge、manifest/cache/settings/registry、MCP/OAuth/credential adapter、活跃会话 reload、生产 factory 与 Server 全 API 接线、默认 Harness 的 Skill/MCP 消费及正常桌面/真实账号任务。当前安装候选哈希和旧 6,218/6,220 测试窗口均先于新 Connector 代码，保持旧范围。

当前整包 `check:go` 已执行：build/vet、6,250 unit 和 6,250 race 通过，整体仍 failed（77.95% 改动覆盖率低于原 90% 要求，8 个 source mapping 缺口）。报告：[2026-10-05T23-40-24-395Z](../../artifacts/go-checks/2026-10-05T23-40-24-395Z/report.json)。第一个全量窗口因新的实际安装 fixture 的 5 秒总期限在 atomic 全覆盖环境超时失败，保留[首次全量报告](../../artifacts/go-checks/2026-10-05T23-37-08-832Z/report.json)和[日志](../../artifacts/go-connector-progress/whole-check-go.log)；只将这个 fixture 的总期限改为 30 秒后重跑。生产下载、认证、命令时限和门禁阈值没有更改。

Source-enabled 的限定 Connector 验证与不启用 Source runner 的整包质量结果分开记录。最终 `verify`、所有被选 lane 的完整新窗口、生产 Connector API/registry/MCP 接线、真实模型、原生桌面和平台/Benchmark 均未关闭。完整 migration 与 Benchmark 状态仍为 false / 未验收。

### D07：Task 管理服务与 Agent 的实际磁盘任务互通

新增 `backend/internal/server/tasks.go` 并接通全部原版 Task 管理入口：

- `GET /api/tasks`
- `GET /api/tasks/lists`
- `GET /api/tasks/lists/:taskListId`
- `GET /api/tasks/lists/:taskListId/:taskId`
- `POST /api/tasks/lists/:taskListId/reset`

读取复用同一配置目录的真实 Task 文件，不启动另一套 Agent。保留原版 desktop TaskService 的 legacy 读取投影、内部任务过滤、缺省/可选字段、数字 ID 排序、String 强制转换及 UTF-16 内容。通过原 Source 文件读取器发现并修复了 Go `os.ReadDir` 默认排序导致的列表顺序差异；现在保留原文件系统枚举顺序。

Reset 复用 Task Store 的生命周期锁和 list lock。保留 ID high-water mark 与修订，删除旧任务后下一次 TaskCreate 不复用已提交 ID。并发下仍保持原版有限次数锁重试；耗尽时返回原错误，不偷偷延长原契约。保留隐藏状态文件及未知 metadata。

新增和扩展的回归实际执行 Task Store 与共享 Agent 工具，检查 TaskCreate/Update 提交后桌面 API 可读出相同数据，最终完成状态一致。模型响应来自 loopback，只证明确定性运行边界，不计入真实模型证据。

### D34：运行配置落盘与 native CWD 别名

新增回归先确认首轮消息前关闭会丢失已确认运行配置。原因是元数据写入使用 CLI 的 lazy Project pointer：它在首轮记录前为空，数据只进入内存缓冲。

`sessioncontroller/runtime_metadata.go` 现在按原桌面 SessionService 的语义，向已存在的会话日志通过共享 Writer 入队，等待实际写入完成后返回；不提前 materialize CLI 的 lazy pointer。缺少物理日志与禁用持久化仍不新增日志。原日志字节及未知字段保留。

整体检查又确认了另一个路径问题：桌面把 `/var/...` launch 目录 realpath 成 `/private/var/...`，native Agent 仍使用原别名，编码成两个 project ID。`clibootstrap/process_native.go` 现与原 `src/bootstrap/state.ts` 一致：realpath 成功时使用物理路径，失败保留原路径，并规范为 NFC。隔离 Source 进程验证了从符号链接启动时的实际原版 originalCwd；新增 native alias 回归。

完整数据升级、所有字段/失败分支与真实 UI 重启交接仍在 D34 待验收中。

## 验证与范围

证据根目录：[本次原始结果](../../artifacts/go-desktop-progress-20261006/)。首个失败和中间失败均保留。

| 检查 | 当前结果 | 能证明的范围 |
| --- | --- | --- |
| 首次 Task API 回归 | failed（修复前 501）；修后 passed | 从原缺失路由到真实 Task Store / reset 业务 |
| 首轮前关闭的配置回归 | failed（修复前丢失）；修后 passed | 等物理 append、关闭、缺少日志与禁用持久化 |
| 原版 TaskService 文件读取对照 | passed | legacy 数据、排序/过滤/字段/数组、实际文件读取；不规范化数组顺序 |
| 受影响 application/clibootstrap/taskstore/server race | passed | 当前 native alias / 元数据与 Task 跨层实现；`current-affected-race.log` |
| sessioncontroller Source/race | passed 的限定包证据 | 共享 Writer / 原差分；`alias-runtime-tasks-source-race.log`，最终 metadata 代码相同，native CWD 的后续 NFC 修正另经当前受影响包检查 |
| 正常 Go 二进制与真实 HTTP Server | passed | 普通 build，无 fixture tag；不传工具子集正常启动 Server；六个真实 HTTP 请求、reset 文件/水位、version、正常进程退出；不证明聊天默认工具全集 |
| `check:chat-contract` | passed | 原 CLI/SDK/桌面聊天契约；桌面部分 422 项 |
| `check:persistence-upgrade` | passed | 原持久化升级检查；未改变存储 schema |
| 当前 `check:go` | **failed** | build/vet、6,218 unit 和 6,218 race 通过；改动覆盖率 **78.54%**，8 个 source mapping 缺口仍未关闭 |
| 原版对照启用的当前整包 race | **passed：80 packages、6,220 passed、53 skipped** | `full-source-race-current.jsonl`；当前 Go 输入与正式报告无漂移。旧窗口的 Git 多余 CWD/HEAD 读取失败仍保留，未因一次通过宣布原稳定性缺口已关闭 |
| `check:impact` | passed 的路由分析 | 全 diff 仍选择原要求的各门禁，不等于所选门禁全部已通过 |
| 原生 Electron / Web UI 新候选 E2E | not run | Ego Lite 211 当前为 agentDelegatedToUser；重新接管已请求确认。原窗口/真实模型证据仍绑定其旧候选 |
| 完整默认工具、真实模型配对 Benchmark、全部平台 | not complete / not run | 保持 D32/D35 的全量要求 |

当前普通二进制 SHA-256：`cb0003397f24802cd78c883fa28d2194588471c5aa870e4a5f73522c761f2ddc`。只有普通构建、version 和限定 HTTP 进程烟测属于此候选；用户已安装的应用与已有真实模型窗口未被替换。

当前正式 Go 报告：[2026-10-05T22-19-53-934Z](../../artifacts/go-checks/2026-10-05T22-19-53-934Z/report.json)。源码快照 1,281 个输入无漂移；原始命令、失败与覆盖率详见报告。此前报告与当前范围分开，不用旧候选或单例复跑替代整包证据。

## 剩余工作

D07 的 REST 和数据业务已接通，完整桌面/正常默认工具联调未关闭。D34 的两个具体落盘原因已修复，完整升级/恢复未关闭。其余 24 组缺失 API、完整 WS/SDK、全部工具/CLI/TUI、native 整包、平台与 Benchmark 继续按[完整待办](go-desktop-parity-todo.md)推进。

下一实现批次继续完整管理/扩展 API 的生产器、运行时与 UI 消费者，并持续关闭现有门禁失败。不能用返回成功或已存在的目录，替代真实业务完成。

## 修改文件

| 边界 | 文件 |
| --- | --- |
| Task REST | `backend/internal/server/tasks.go`（新增）、`server.go`、`tasks_test.go`（新增）、`server_test.go` |
| Task 文件与重置 | `backend/internal/taskstore/desktop.go`、`desktop_test.go`（新增） |
| 共享 Writer 元数据 | `backend/internal/sessioncontroller/runtime_metadata.go`；`backend/internal/application/runtime_metadata_test.go`（新增） |
| 实际 Agent → API | `backend/internal/application/task_tools_test.go` |
| 原 native CWD | `backend/internal/clibootstrap/process_native.go`、`process_native_test.go` |
| 原版读取对照 | `scripts/quality-gate/go-parity/native-desktop-tasks.fixture.ts`（新增） |
| 计划/账本 | 完整桌面待办、逐入口索引/JSON、执行进展、总计划/下一步、当前验证与 CLI/Server 账本；完整完成状态仍为 false |

未运行/未关闭：最终 `verify`、所选门禁的全量新窗口、真实模型新候选任务、原生桌面整包、所有平台与正式配对 Benchmark。旧通过证据保持原范围；没有提交、推送、发布或更新用户安装。

## 2026-10-06：Native Skills API 接线

实际 NewDesktopServer 复用 NativeProcessSettings、plugin loader 与共享 store，提供用户/项目/插件 Skill 的列表/详情；.agents 开关和 plugin disable 更新实际读取。Source handler 的 11 个 HTTP 对照、41 个 SettingsSchema case 和 58 个参数场景通过；Source-enabled 四包 race 通过。完整 command service、mentions、Skill/slash/Hooks/fork 和默认 Harness 接线尚未完成。

正式当前候选 build/vet/unit/race 通过，unit/race 各 6320 pass；总门禁因 74.53% 改动覆盖率及 8 个映射缺口失败。[当前正式报告](../../artifacts/go-checks/2026-10-06T03-41-34-532Z/report.json)，[接线范围](../../artifacts/go-native-skills-api/report.md)。完整所选门禁/verify、UI/native smoke、真实模型、平台与 Benchmark 未运行。整组缺失 API 23 组，确定缺入口调用点 168 处；全部 35 工作包/115 主待办保留。
