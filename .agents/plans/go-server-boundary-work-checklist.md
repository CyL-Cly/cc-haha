# Go 迁移：Server 独立入口与生命周期待办

更新：2026-10-10。当前任务是继续全部实现与验收。这是 [D01–D35 总表](go-migration-worklist.md)内的细分任务，不另立迁移范围，也不关闭主清单中的 115 条任务。最初的静态盘点保留为历史依据；后续已有 Go 实现和真实模型任务，不能再把当前工作描述为“仅梳理”。

## 当前结论与完成标准

**尚未全部完成。SB01–SB16 均未完成整项闭环验收。** 这不表示每项零实现：健康探针、local/H5 认证、部分文件/静态服务、设置、会话和进程关闭已有实现；原生 MCP 的限定 CLI/Web 对话和管理开关已有证据。它们不关闭本清单的专用入口、全部消费者和生命周期。

唯一目标是：以仓库现有 TypeScript CLI/Server/Harness 的原版行为为基线，完整替换为 Go，保留 React；打开桌面应用即启动 Go，聊天、会话列表与操作、全部设置、Skills、连接器、插件、MCP、Computer Use，以及原发行的其他功能全部正常。CLI/TUI 同样完整迁移，旧 TS CLI/Server/Harness 不承担生产业务。

每项完成须有实际 Go 业务实现、真实消费者接线、原版行为对照及相应 UI/进程验收。影响模型行为的项必须用官方 DeepSeek 正常对话核验实际产物、工具/权限轨迹和后续恢复；不依赖模型的项验证真实接口、原生动作和持久化。最终还须验收同一安装候选的原生 Electron E2E、默认完整工具目录、所选质量门禁和原条件配对 Benchmark。路由存在、返回 200、健康探针或部分聊天成功均不构成整项完成。

当前复核：Computer Use、Plugins、Agents、诊断/Doctor、Token 统计、文件预览/下载、远端设置分流、Claude OAuth callback、连接器和原生 MCP 已有 Go factory/router/生产器接线，各自验收范围见[当前验证记录](go-migration-current-validation.md)。实际签名桌面 Agents 创建与插件读取/禁用/启用/应用、原生 Doctor 已有实际界面证据；真实官方模型 Agent+Write/Read 与原 SID 恢复已有产物。它们仍不关闭完整条目。会话主索引/搜索/Trace/调度、内部协作和共享Inbox已有实际生产绑定；Workflow定义/历史与Conversations旧REST已有实现，原SDK通道和Workflow执行继续完善；Pet限定HTTP/WS已接入同一实际Harness，并通过官方模型任务。Teams、其他OAuth/IM、公开访问、Provider proxy、完整SDK/Workflow等仍缺完整实现或验收；普通默认工具目录已接真实消费者并通过签名原生官方模型任务，文件附件与桌面消息Fork已实际接线/模型续聊通过，图片归一化与条件ImageGen/ImageEdit仍有明确守卫。语音全部API/实际原生识别已接，签名设置下载/就绪与真实Go worker转写通过，麦克风 UI 真实录音与 Go 本地转写传输已验证（用户当场许可），实际返回 noSpeech，尚未验证有声识别准确率，Computer Use元素handle拒绝经实际Source/Go同传输四组对照一致，录制错误对应原helper refetch拒绝；具体locator/祖先成因未证实，原生坐标恢复已有证据，完整跨平台验收仍待做。FileHistory主对话/三种文件工具已接真实state/sink，完整工厂冻结diff/文件还原通过、九包Source/race通过，0983签名新会话真实Edit/Read→GUI撤销恢复物理文件→同SID官方模型Read续聊已通过；继承缺child的旧Fork完整性边界仍单列；session references已接真实存在检查，P786/P787签名原生@选择/ReadSession与官方模型、P801实际Open referenced session已通过；完整候选的全部会话操作仍待验收。普通技能的 Write 误拒绝已修，条件技能/后台/Hook/LSP 等完整生命周期还须落实。

2026-10-10追加真实同包范围：P867签名Go桌面Workflow双子Agent→TaskOutput→Write/Read和即时保存已通过11次官方请求，保存命令复用已通过新签名P892/P893正常输入框命令→双子Read→完成通知/父journal复核（9次官方请求），原SDK数组slash输入已修复；P872计划入口在原有trusted自动权限设置下仍失败，正在迁移真实auto state/classifier/退出附件。P874设置创建/编辑项目Agent后实际子Read/父Read通过5次官方请求；P876精确测试app AX/截图通过3次官方请求。记忆编辑、插件停启/应用、本地麦克风转写、Trace/用量、只读Doctor、包内Go终端已实际运行；IM仅配置页、宠物仅偏好操作，不冒称整项验收。正常退出20个测试包owned进程全部消失。新增Guide11已严格合入且主仓Source/race重复3通过，后续签名官模待跑。这些结果均不关闭SB01–SB16整项，见[当前验证记录](go-migration-current-validation.md)。

2026-10-10 最新追加 P956–P995：新签名 P961 的原生市场安装→真实官方 Skill/Read/Write/Read→卸载→新 SDK 命令目录同步已经验证；统一 Computer Use 的 AX/应用截图和设置重新检测已实测。普通计划入口/当前 provider 批准恢复已通过，但原生切换 provider 的审批仍明确失败，隔离原版→Go 双 provider 恢复链与共享 128 行消息投影正在完成最终生命周期验证，未合入或原生复验。Windows setup/status 已严格合入并通过主仓限定回归，应用列表原 501 已补齐并合入，主仓整包复验进行中；Windows 完整工具/SDK 仍缺实现，未称 Windows 实机可用。P966 全 verify 的 Go race 失败已保留，后续检查中止，全部 10694 冻结输入零漂移；不能关闭完整质量门禁或 SB01–SB16。详情与精确范围见[最新记录](go-migration-current-validation.md)。

2026-10-10 最新 P1030–P1042：Owner 合法 ASK 空闲排队修复已合入并通过主仓限定 Source/race 和签名构建；正式包两个独立计划审批跨 provider 自动续跑、同 SID 下一轮、旧用户消息 Fork、普通 CLI 恢复并回显桌面已有真实官方证据。团队实际创建/提交/GUI 批准/成员 Read/inbox 回报通过，但团队卡片 disabled、工作台未显示，原 TeamWatcher 事件生产器缺失正由私有 P1043 实现，尚未合入或新包复验。覆盖率流式读取已合入，原 P1013 SIGTRAP 失败保留，完整 verify P1036 的 Go race 三包失败已记录、后续 lane 继续；不关闭 SB01–SB16、全 UI 或 Benchmark。精确收据和限制见[最新验证记录](go-migration-current-validation.md)。

2026-10-10 最新 P1052–P1061：旧 Fork 正常重命名/官方 Read/重启恢复/正文搜索通过；旧团队归档报告实际缺 reader 和任务。Go TeamWatcher 八文件已严格合入，主仓原 Source/实际 HTTP-WS/生命周期三轮 race 与 vet 通过，新签名包构建及真实团队 UI/归档待跑。私有 Application 三轮总时限失败保留，一轮原预算复核进行中；P1036 整验收失败后剩余 lane 已停止 143，不能称完整质量通过。见[最新范围](go-migration-current-validation.md)。

2026-10-10 最新 P1062–P1085：新包真实团队工作台、成员/任务/执行过程与成员下一轮 Read/inbox 已通过 15 次官方 HTTP200；直接消息 UI 重复 pending/思考中仍失败，共享 React 对账已回归修复且完整桌面 8096 pass/4 skip，新包复验与归档待跑。Windows 传输、Mac fixture 退出和 Git 同 Owner 就绪/真实工厂已合入并通过主仓限定完整包检查；Windows SDK 仍待完成。CLI stdin 修复继续私有校验，完整质量/Benchmark 不关闭。见[最新记录](go-migration-current-validation.md)。

2026-10-10 最新 P1098–P1104：模型/Trace 共享响应体的真实物理读取阻塞在原一秒测试中自然复现；修复后物理 Provider/Trace 与六种原版计划审批/断连生命周期 Source/race 三轮通过，三包 vet 通过。P1096 完整 Application 原预算失败仍保留；新签名桌面及官方模型复验、完整门禁、全 UI 与 Benchmark 仍开放。团队、Windows、批量成员消息和 CLI 原始 UTF-8 等独立缺口见[最新记录](go-migration-current-validation.md)。

2026-10-10 最新 P1105–P1116：合法 `statusLine` 已修复并通过定向 Go/race、Source↔Go parity 与 vet；check:native 的签名候选通过 Swift 518、Electron 615、package smoke 及 Developer ID 严格验签。实际 Electron + Go 页面和 Go H5 UI 已启动，健康、会话、设置、服务商、语音、技能、连接器、Agent、模型目录接口多数返回200；直接浏览器缺 Electron 访问令牌的两个接口仍403。获准的隔离 profile 已保存 Computer Use 统一应用授权；P1114 使用的 ad-hoc 包被 helper 正确拒绝，未授予 macOS 辅助功能/录屏权限，也未冒称 Agent 截图/点击通过。SenseVoice 下载并校验成功，单次真实 UI 麦克风录音→Go 本地转写返回 noSpeech，识别准确性**未通过**，临时 profile 与模型已清理。P1115 当前冻结输入上的完整 Go parity 通过：63 文件、3150 tests、0 fail、53763 assertions，10724 个输入前后快照一致；这只证明列入语料的 Source↔Go 行为边界，不是整套 Harness 或 Benchmark 结论。P1116 当前完整 Go unit 与 race 各14,011 tests passed；`check:go` 仍因改动行覆盖率60.27%低于90%门槛失败。旧60秒超时只属于更早候选，`bun run verify` 尚未通过。P1115 manifest 在P1116测试与文档更新之前生成，不是当前工作树快照。Go TUI statusLine 执行、完整功能和 Benchmark 仍未完成。`check:impact` 因 CLI core label/maintainer approval 显示 blocked。真实 DeepSeek未调用，待凭据轮换。见[当前验证记录](go-migration-current-validation.md)。

## 为什么单列

2026-10-10 最新 P1007–P1023：计划跨 provider/effort 重启与 176 行原版消息投影已严格合入，主仓限定应用回归、整 Server 三轮 race、vet 及新签名原生包通过。真正从原生桌面使用官方模型复验时，provider 已切换，但自动继续和同 SID 后续轮次仍被 Owner 活动标记拒绝；产物不存在。首次后台读取等待时序和残留 active 的调度缺陷正在修复，完整 Go 门禁 P1013 正在运行。此真实失败保留，不关闭 SB05/SB13/SB16 或全部迁移；精确证据见[当前记录](go-migration-current-validation.md)。

原版 `src/server/index.ts` 在普通 `/api/` 分派之外还处理专用认证、SDK、OAuth 回调、文件、代理、公网 listener 和后台服务。页面有路由、HTTP handler 有代码、Go 服务能启动，均不证明这些入口已经接通。以下按原入口、当前 Go 接线和实际业务结果列待办。

状态中的“明确缺失”来自源码的拒绝或缺少专用注册；“部分/待验收”表示已有生产代码，需要补齐原生命周期或核对同一完整候选。下表保留初次盘点的具体依据，逐项关闭必须追加当前实现与实际验收证据。

## 16 项具体工作

| ID / 归属 | 当前证据 | 要干的活 | 后续完成条件 |
| --- | --- | --- | --- |
| SB01 · D33/D35 健康探针与 ready | Go 有 `/health`，完整安装候选待验收 | 保留探针与业务初始化的独立性；Electron 的 ready、URL、版本和服务身份来自实际 Go 进程；端口冲突、启动失败与超时回报准确 | 从应用图标启动，健康探针不等待索引/模型/迁移；仅在实际服务 ready 后呈现可用桌面；错误、重启和退出均可观察 |
| SB02 · D13/D21–D24/D26/D34 索引启动 | 主索引/全文/Trace/活动统计/调度已绑定NewDesktopServer，各自Source与实际进程证据已追加；完整安装候选和损坏恢复闭环待验收 | 接会话主索引、全文、Trace、定时运行和活动聚合生产器；迁移、重建、watch/reconcile、损坏恢复和主索引优先于全文的启动顺序；保留旧文件与数据库兼容 | 冷启动、增量变更、重建和中途退出后的数据正确；侧栏、搜索、统计与 Trace 读取同一真实状态；后台索引不阻塞健康探针 |
| SB03 · D15–D17/D30/D31/D33 凭据分流 | local/H5/Pet/协作已实际分流，SDK开始消费同一注册/撤销凭据；整包与公网设备边界待闭环 | 分别接 local、H5、内部 SDK、协作会话、Pet 和公网设备的原认证入口；保持原 bearer/query、Origin、preflight 和认证失败语义；凭据各自进入实际消费者 | 正常桌面和各专用客户端能访问原允许路径；原拒绝路径及字段仍被拒绝；刷新/撤销/重连使用正确凭据 |
| SB04 · D26/D31/D33 Pets 投影 | Go原范围白名单、9会话投影/成员检查/偏好、WS严格输入/脱敏与单Pet切换已接真实消费者；Source、实际HTTP/WS、共享Harness和官方DeepSeekWrite/Read通过；签名设置开启与浮窗互动恢复主窗通过，默认完整Agent目录/整窗口任务路径尚未验收 | 迁移原 Pet HTTP 白名单、最多 9 个会话/offset=0 的投影、成员检查、pet 偏好；WS 只接原允许消息与脱敏输出；保持单个活动 Pet 连接切会话的关闭行为 | Pet 可读原限定会话、正常文本追问/停止/同步；无法发 slash、附件、审批或读取完整敏感消息；脱敏字段和会话切换与原版一致 |
| SB05 · D11/D31/D32 原 SDK 通道 | `/sdk/:sessionId` 专用认证/升级/重连和原输出projection已接；foreign控制/权限/关闭完整消费者继续实现 | 接原 session token、连接身份、sessionId 校验、SDK message/control 转换、回调/应答、重连和关闭；与桌面 WS 使用同一实际运行会话 | 原 SDK 客户端经原路径连接、输入、审批、停止和恢复；令牌失效/会话结束及时关闭；不能用 SPA HTML 或另一个 `/ws/` 名称代替 |
| SB06 · D12/D31 内部会话协作 | 五工具/内部API/宿主credential与真实MainPrint共享Inbox已接；完整Source、实际共享Harness、官方DeepSeek新child/追问/原SID恢复通过；整项GUI/关闭/并发全候选验收待闭环 | 接 POST list/read/create/send/wait；使用 `x-session-id` + Bearer 会话凭据、无 Origin 的原内部认证；接宿主注册/解除、等待取消与实际消息投递 | 原 Agent 协作工具和 UI 候选共用真实服务；鉴权失败、超时、取消、投递和结果可核对；关闭宿主后不遗留等待 |
| SB07 · D15/D16/D29 OAuth 回调 | Claude/OpenAI callback 与自管 listener 已绑定；2907 的实际登录入口失败保留。当前7a0bf5fc签名包已含CLI缓存身份/过期刷新/Responses普通工具链，官方DeepSeek原UI续聊通过；随后新加OpenAI activation/auth-status/models/WS选择通过原Source对照，实际远端catalog与新包登录UI继续实施，官方OpenAI账号/Grok仍开放 | 接 `/callback`、`/auth/callback`、`/callback/openai`，复用相应登录事务/state、错误、凭据提交/刷新和页面结果；保留原 provider 的回调差异 | 从原登录入口到浏览器回调再到真实请求完整贯通；重复/过期/取消/失败回调不写错账号；回调不落入 H5 SPA fallback |
| SB08 · D28/D34 Workspace 预览文件 | Go `/preview-fs/`已接实际文件、范围/Range/MIME/HTML处理；原生旧候选真实Agent生成HTML及点击预览已有证据，最新整包待复验 | 接 session 工作目录的实际解析、原预览文件服务、认证/CORS、路径/符号链接边界、Range/MIME/cache 和文件缺失错误 | HTML、图片、文档及音视频按原 URL 打开；资源、Range、编码路径和工作目录恢复正确；不能以 shell HTML 代替请求文件 |
| SB09 · D28/D33 本地文件服务 | Go `/local-file/`已接实际登记root/文件读取与认证，实际边界回归通过；完整原生多媒体打开验收待做 | 接原 absolute local file 路径、HOME/tmp/已登记 root 范围、认证、响应和资源读取；由实际工作区变更登记访问 root | 原 AI 产物和本地文件链接可打开；跨平台/中文/编码路径正确；目录范围、缺失/拒绝响应与原版一致 |
| SB10 · D29/D31 Provider 代理 | `/proxy/` 专用HTTP已接真实工厂/保存Provider/network/TLS/Trace，三协议WS→Agent Write物理测试通过；新签名真实模型/CLI全入口和自动提问其他协议尚须验收；OpenAI OAuth 专用SDK fetch/auth/zstd/TLS/流式/turn-state已接真实CLI，Source及物理工具链已过。OpenAI activation/env/model projection最新Source通过，ImageGen/Edit同Owner实际OpenAI TLS/落盘/编辑已通过隔离物理测试；远端catalog Source/真实私有HTTPS已通过；通用代理媒体235/Chat请求432/响应136/流166和73组物理请求已对照，完整HTTP/保存Provider/Trace及新签名UI继续验收，不关闭通用proxy | 接 `/proxy/v1/messages` 与 provider 专用路径，原协议/媒体转换、实际所选 provider 凭据、headers/beta、流式数据、重试和错误体；绑定真实 ProviderService | 原支持 provider 的请求与工具/媒体往返可用；流式结束/错误/停止、模型切换和重启配置一致；基本 CRUD 不关闭此项 |
| SB11 · D30/D33/D34 PublicAccessServer | Go专用control、独立listener、设备迁移/配对/cookie/限流/实际WS/Close已绑定；原Source、actual消费者与完整factory确定性Write/Read通过；05f010新签名原生设置/官方WriteRead及同二进制配对Cookie WS官方WriteRead/撤销关闭通过；真实ngrok UI闭环继续 | 接独立控制与公网 listener：status/enable/origin/disable、pairing approve/reject/revoke、远端 session/pair/claim；保留设备/私有配置文件、cookie、过期、限流和 socket 清理 | Electron 正常开启/配对/批准/撤销；listener、设备记录与重启恢复正确；关闭/撤销使对应访问失效，不拿 H5 token 流程替代 |
| SB12 · D29/D30 远端细粒度能力 | Go已接原远端路径/方法与字段投影/补丁校验；来源对照和持久化回归有证据，整H5候选待闭环 | 迁移原远端路径/方法矩阵、Provider 投影、通用设置读写与补丁校验；隐藏兼容扩展字段保留，API key/image key 脱敏和 hasApiKey 字段保持原语义 | H5 原 providers/general 操作可用；可编辑字段保存，隐藏字段不被覆盖；原禁用路径和敏感信息继续受保护 |
| SB13 · D11/D26–D31 请求断开 | 原 API 用 `settleResponseOnRequestAbort` 返回 499；Go 的完整操作边界待核对 | 逐业务接请求结束/监听清理，区分 HTTP 等待结束、WS 断开、Agent 停止与已经提交的副作用；原函数只竞争响应，不自动取消已创建的业务 Promise | 客户端断开后的响应、写入和操作状态符合原版；不能把所有断连改成 Agent 取消；不会因重试重复已完成副作用 |
| SB14 · D28/D30/D33 H5 静态资源 | Go 有 H5 静态服务，完整包/专用路径分流待验收 | 接打包 dist/asar.unpacked、GET/HEAD、MIME/cache/资源404；保留非秘密 shell/assets 在认证前可加载；专用服务路径先分派，不能误用 SPA fallback | 无 token 可加载原 bootstrap，再完成二维码/认证；真实包内资源和刷新可用；SDK/回调/文件 URL 得到其业务响应 |
| SB15 · D29/D32/D33 CLI launcher 启动 | Go异步启动安装、实际HTTP状态、原PATH/包装器已接；Source与实际factory通过；707c签名包原生终端同Go CLI版本/官方bareTUI执行通过；平台更新/卸载及整CLI动作仍待闭环 | 接实际 bundled Go 二进制的安装、检测、PATH/启动关联、版本、修复/卸载和平台路径；启动安装错误可观察且不阻塞健康探针 | 从独立终端及桌面 Terminal 均使用同一普通 Go CLI；更新/卸载/路径有空格及权限失败保持原流程 |
| SB16 · D08/D09/D12/D30/D33/D34 启停所有权 | 原 Server 启动 Team watcher、cron、索引及协作宿主；Go 已有部分 session/进程 close | 将全部后台服务纳入实际 Go 生命周期：启动一次、避免重复调度/监听；关停索引、watcher、cron、协作等待、公网 listener、会话/工具子进程及未完成写入；保留信号和清理顺序 | 启动/重启/退出/失败后无遗留进程、端口、订阅或重复任务；已完成写入可恢复，正在执行的任务按原语义结束或恢复 |

## 原版与 Go 源码依据

- 原专用入口、启动与关停：[src/server/index.ts](../../src/server/index.ts)。
- Pet 限制和投影：[petAccessPolicy.ts](../../src/server/petAccessPolicy.ts)、[sessions.ts](../../src/server/api/sessions.ts)、[ws/handler.ts](../../src/server/ws/handler.ts)。
- 协作认证：[sessionCollaborationAuth.ts](../../src/server/sessionCollaborationAuth.ts)。
- 远端可用路径与字段：[remoteBrowserPolicy.ts](../../src/server/remoteBrowserPolicy.ts)。
- 请求结束：[requestLifecycle.ts](../../src/server/requestLifecycle.ts)。
- 公网 listener：[publicAccess.ts](../../src/server/publicAccess.ts)、[Electron manager](../../desktop/electron/services/publicAccess.ts)。
- Go 当前实际生产绑定：[application/server.go](../../backend/internal/application/server.go)、[server/server.go](../../backend/internal/server/server.go)、[server/auth.go](../../backend/internal/server/auth.go)、[server/static.go](../../backend/internal/server/static.go)、[provider environment](../../backend/internal/server/providerconfig/environment.go)。

本索引按源码列出后续工作。Go 已有的认证、静态服务、设置和会话关闭继续复用；不能将其笼统登记为零实现，也不能把已有构造器当作整包验收。
