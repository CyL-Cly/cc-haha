# 当前入口与能力台账（2026-10-13）

这是冻结 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155` 与当前工作树的**静态业务接线审计**。它不会执行模型或界面，也不把找到 route、函数、测试文件或者历史 live 收据等同于验收完成。原 Oct6 三份台账保持原字节；其 missing 131/240 HTTP、60 rejected root options 等数字不得作为当前缺口数量。

完整机器可读结果由下方脚本在本机生成到 `go-current-surface-inventory-20261013.json`；该大型生成报告不随代码提交。每个 HTTP 调用记录当前与 MAIN 的模块哈希、方法、符号化路径、直接导入者、实际相同符号的调用者、可能到达的可见组件链、Go dispatcher 和 handler；native 通道记录 channel→renderer caller→Electron handler；WS 发送记录实际 discriminator；特殊文件 URL 独立记录。所有 38 项 D/R/Q 需求保留原表行，全部闭环状态仍需最终候选证明。

## 结果的使用边界

- 路由存在只能证明分派入口；不能证明某个 API 的全部方法、子路径、payload、权限、错误、取消和持久化语义。
- `NewDesktopServer` 的 `server.Options` 实际构造参数单独列出，不能把裸 `server.New` 测试中的 nil service guard 当作正常桌面缺口。connectors/skills/market/voice/OAuth/workflow 等现在已有生产服务绑定，历史“整组未实现”不能继续引用。
- import graph 只给出潜在可达路径；`exactSymbolConsumers` 是相同导出对象方法的实际 AST 调用。动态 import、运行时别名、组件条件与数据驱动菜单仍需实际入口检验。
- Go guard 给出实际函数、行和外层条件。guard 数量包括参数校验、测试用离线隔离、owner/provenance 防护、未接 service 和真实缺口，不能作为缺失功能数量。统一授权后的 Computer Use 不因该审计新增任何 per-app 限制。
- safe scoped receipt 只索引仓库忽略目录顶层 JSON 的允许 summary 字段与文件哈希。未匹配最终 binary/React/native 输入的收据保持 `unmatched-to-final-candidate`。不读取原始凭据、转录、终端、网络和其他应用截图。
- current 输入逐文件记录哈希；多人实施期间不是全树原子快照。最终候选冻结后必须重生成并跑门禁、真实 UI、相关 live 和配对 Benchmark。

## 当前已经确认的可达差距

| 能力 / 触发 | MAIN 的实际业务入口 | 当前 Go 边界 / 后果 | 状态与下一步 |
| --- | --- | --- | --- |
| 普通 CLI `--file file_id:relative_path` | `src/main.tsx` 的 fileSpecs→session ingress token→parseFileSpecs→downloadSessionFiles；启动异步下载，仅 resume 分支等待，fresh/print/continue 不统一等待 | `application.ParseCommand` 未接收 `--file`，提前 capability error；没有该实际下载 producer | 未完成：迁移原异步时序、校验/文件副作用/错误、5 并发/3 retries/取消；public CLI 与真实文件边界验收 |
| 普通 CLI `--chrome` true | MAIN 正常注册 Chrome integration flag；原 Chrome MCP/runtime 路径 | ParseCommand 只允许 false/no-chrome；true 提前拒绝，未找到 integration consumer | 未完成：不能用 CU 代替 Chrome 原入口；保持 Source 平台、设置和 entitlement 边界 |
| 普通 CLI `--remote` / `--teleport` | MAIN 在所有构建注册 hidden flags；managed `allow_remote_sessions`、OAuth/CCR 创建、仓库校验、checkout/resume | ParseCommand 提前拒绝；这与 `--sdk-url` RemoteIO 的物理 WS/hydration 链不是同一功能 | 未完成：原实际云操作、策略/认证与 TUI 恢复；官方账号 live 单列 |
| 三个 `--deep-link-origin/repo/last-fetch` | MAIN deep-link provenance/仓库与 fetch metadata 消费 | ParseCommand 提前拒绝；hidden 不等于 Source 未支持 | 未完成：沿实际桌面 trampoline→CLI 恢复接线并对照 |
| TUI Ctrl+T / Ctrl+Shift+O / Ctrl+X Ctrl+K / Meta+O / 图片粘贴键 | MAIN default keybindings 有 toggleTodos / toggleTeammatePreview / killAgents / fastMode / imagePaste；Source 对应组件/hooks 接收 | 当前 `State.HandleKey` 在普通 Global/Chat 匹配后转 `actionInvoke`；`Session.InvokeAction` 未处理这五个 action，返回 UnsupportedAction。teammate 的 Shift+Up/Down/Enter/f 不能替代 Ctrl+Shift+O | 未完成：移植动作本身、原触发条件与 UI 状态；Source 快捷键 resolver 通过不代表 handler 已实现 |
| 普通 TUI `/help /permissions /tasks /skills /agents /mcp` 等 local-jsx 命令 | MAIN `commands.ts` 正常外部表及各 index→load；availability/isEnabled 决定当前条件 | 当前 `compiledPrintCommandDeclarations` 明示有限表，仅 14 builtins；application 额外 doctor/ide/model，TUI exit intercept。动态 skill/plugin/workflow 已有 producer，但不补原 local-jsx 面板 | 未完成：每项按 Source 注册、gate、完整 panel 与实际配置/运行时消费者迁移；不能通过隐藏缺失入口或把命令文本发给模型通过 |
| CLI 图像/附件输入 | Source imagePaste→clipboard image→pasted content→实际模型媒体内容 | `Session.submitContent` 对 `Input.Attachments` 和非 text `PastedContents` 提前 capability error | 未完成：保留真实媒体与权限/生命周期；不能只增加键名 |

上述是具体接线缺口，不是穷尽所有未验收功能的结论。新增实现后这些行保留为发现记录；修复状态、Source 对照与 public PTY 收据追加到实施记录，再重生成当前 JSON。

## 已有但未达到整项验收的边界

| 表面 | 可观察生产接线 | 仍要证明 |
| --- | --- | --- |
| 桌面默认 Go 入口 | `sidecarBackend/sidecarManager` → Go `server`；`application.NewDesktopServer`→实际服务；CLI launcher→同一 Go | 最终安装包图标、ready/错误、重启/退出，native/React/Go 版本一致，保留 Tauri 的可用平台验证 |
| settings、skills、connectors、MCP、市场 | current HTTP caller→server handler→实际构造 services；菜单与设置导入链在 JSON | 实际 scope/gates，保存后下一次 runtime 消费，市场 owner/安装/卸载，connector 真实授权；既有有限 Source profiles 不扩展为整项通过 |
| trajectory、文件、检查点、编辑重发 | current renderer API 与 server 专用 handler，prompt/trace 等生产 package | 相同候选上的实际产物/字节定位/live tail/主子 scope、rewind 和错误恢复；Special URL 真实资源不可用 SPA 页面替代 |
| H5 / PublicAccess / Pet / SDK / collaboration | 专用 transport 已分流，不能拿普通 /api route 数代替；native 与 HTTP public-access 分开列出 | 对应认证、脱敏、平台禁用、断线/撤销、生命周期；历史 H5 LAN 实测不是公网账户验收 |
| Computer Use | Go computeruse/native REPL/MCP 与 Electron 设置消费者 | 当前 opaque snapshot handle 拒绝的原 helper 解释，以及最终签名包完整任务；不能削弱 snapshot/path/process validity guard；真实 Windows 尚无机器 |
| voice / updater /迁移 / IM | 原 native 服务与 Go runtime/worker/adapter constructor 有绑定 | 有声准确输入、Windows compiled worker/安装 helper 实机、完整迁移恢复、获授权真实 IM 账户；没有账户/机器的项保持未验收 |
| Teams /普通 Agent/自动记忆 | 已有 controller/worker、default catalog 与 task/UI/Source/物理/live 收据 | 新候选 warm permission/reconnect、完整公共 TUI/后台面板、各 Source gate；旧 live AutoDream Read capability 失败要新候选重测 |

原115项、SB01–SB16 和 B0–B5 仍以总迁移/边界/同步计划为验收来源。静态扫描不会把这些任务勾选完成。全量 `verify`、90% 改动行覆盖率和最终120配对 Benchmark 仍需 root 汇总真实结果。

## 重生成和验证

不运行产品、不调用模型、不读取真实 HOME：

```sh
# 从仓库根运行；已有 Go/Bun 与 desktop/node_modules/typescript 即可。
# 输出和编译缓存均隔离；不依赖本机旧 /tmp 文件或真实用户配置。
taskAuditDir=$(mktemp -d)
mkdir -p "$taskAuditDir/home" "$taskAuditDir/go-cache" "$taskAuditDir/go-mod-cache"
env HOME="$taskAuditDir/home" USERPROFILE="$taskAuditDir/home" \
  GOCACHE="$taskAuditDir/go-cache" GOMODCACHE="$taskAuditDir/go-mod-cache" \
  GOTOOLCHAIN=local GOWORK=off GOPROXY=off GOSUMDB=off \
  go run scripts/audit/go-surface-ast.go . > "$taskAuditDir/go-ast.json"
bun scripts/audit/current-surfaces.mjs "$taskAuditDir/go-ast.json"
bun test ./scripts/audit/current-surfaces.test.ts
```

本轮解析器回归：5 passed、0 failed、12 assertions；覆盖动态变量、path builder、原方法和 consumer 符号、native registerHandler/bridge、不把注释当代码、WS discriminator 与特殊文件 URL。AST 构建 exit0。没有执行 UI、模型、全量 verify 或 Benchmark，因此这里不声称这些通过。

## 本次交接状态

用户随后要求先提交迁移工作并 push 独立分支，暂停新增功能实现。本审计分工没有开始 `/help` 业务编辑：没有半写 `HelpOpened/InvokeHelp/HelpPanel` hooks；上表仍为发现记录。`go test ./internal/application ./internal/tui -run '^$'` 两包编译通过（cached / no tests to run），只能证明当时可以编译。五个审计文件的 token-like 扫描零命中；不包含真实模型凭据或 raw transcript。历史 live 临时目录不是跨机运行依赖；收据缺失时脚本只索引现存顶层 summary，不因此认定验收通过。
