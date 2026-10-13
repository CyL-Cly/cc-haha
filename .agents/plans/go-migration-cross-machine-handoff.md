# Go 迁移跨机器交接

2026-10-13。分支 `feat/go-migration-20261013`。用户要求先提交并推送到 GitHub，停止增加功能，交给另一台电脑继续。**这是工作检查点，完整迁移未完成；不合入 main，不发布 release。**

## 目标与边界

从应用图标打开桌面即使用 Go，所有可见功能都具有与原版一致的实际业务结果。完整 CLI/TUI、Server、Agent Harness、工具、权限、会话、设置、skills、connectors、MCP、插件/市场、团队/后台任务、轨迹/文件/检查点、H5、Computer Use、语音、IM、更新与迁移恢复全部属于目标。

React UI、Electron/native 宿主继续保留。桌面/Web/H5 通过 HTTP/WebSocket 访问 Go Server；普通 CLI/TUI 直接使用同一 Go application/Harness，不强制经过 HTTP。保留 TypeScript 参考实现与对照测试，不能启动 TypeScript CLI/Server 回退来补缺失功能。

完成标准是正常用户输入 → 实际 API/事件/配置 → 执行/持久化/生命周期 → 用户结果。编译、路由存在、单元测试、部分聊天或模型口头报告不代替整项验收。

## 先读这些文件

- 根 `AGENTS.md` 和改动区域的嵌套指令。
- [MAIN 同步计划：38 项 D/R/Q](go-main-feature-sync-plan-20261012.md)、[实施记录](go-main-feature-sync-progress.md)、[当前入口与缺口审计](go-current-surface-inventory-20261013.md)。
- [原 Server 边界清单](go-server-boundary-work-checklist.md)、[桌面 API 清单](go-desktop-api-checklist.md)、[CLI/TUI 清单](go-cli-tui-work-checklist.md)、[原迁移验收记录](go-migration-current-validation.md)。原 115 项和 SB01–SB16 目标仍须满足。
- [正式 Benchmark 协议](go-main-paired-benchmark-protocol.md)、[CLI 构建说明](../../scripts/cli/README.md)。

冻结 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，BASE `6b9d2aec1d9321a4dfff90d16b51f10c1ab984c8`，本轮起始 Go HEAD `64c2fa1c7213e8f39c10e2cba8d7361625449b7e`。交接时远程 main 已到 `bb80a84d116906444e4dcc7998a0b4ae88f4dc93`；没有集成到本分支。先继续冻结计划；后续跟进新 main 要另列增量及基准，不能悄悄改变对照版本。

## 另一台电脑取得与启动

```sh
git clone --branch feat/go-migration-20261013 git@github.com:NanmiCoder/cc-haha.git
cd cc-haha
git branch --show-current
git rev-parse HEAD
git status --short
```

已有 clone 用 `git fetch origin` 取得分支，并在干净 checkout 中跟踪它；不要 reset/clean 原工作。需要冻结 MAIN 的对象时可 `git fetch origin main`，不执行 merge/rebase。

工具版本：Bun **1.3.14**、Go **1.25.7**，以及 Electron 开发脚本使用的 Node。macOS helper 需要 Xcode/Swift；签名 Computer Use 验收还需要可用签名与 OS 权限。Windows crossbuild 不代替实机。

```sh
bun install --frozen-lockfile
(cd desktop && bun install --frozen-lockfile)
bun run prepare:go
bun run build:cli
node scripts/cli/launch.cjs --version
```

`prepare:go` 是依赖联网准备步骤；后续构建/检查使用校验后的隔离依赖种子。不要把本机 `/private/tmp` 的缓存路径当作跨机配置。

- 普通 CLI/TUI：`node scripts/cli/launch.cjs`。
- 桌面开发：在 `desktop/` 执行 `bun run build:sidecars`，再执行 `bun run electron:dev`。
- 本地目录包：在 `desktop/` 执行 `bun run electron:package:dir`，脚本使用 `--publish never`。
- Web：先在 `desktop/` 执行 `bun run build`；再在根目录执行 `node scripts/cli/launch.cjs server --host 127.0.0.1 --port 3456 --h5-dist /absolute/path/to/cc-haha/desktop/dist`，访问 `http://127.0.0.1:3456/`。

产品验收必须使用临时 HOME、ConfigDir、Electron profile，核对实际 Go sidecar 身份，不使用开发者真实配置。脚本构建成功不证明整项产品可用。

真实模型：DeepSeek 官方 `https://api.deepseek.com/anthropic`，Anthropic Messages，准确 model ID **`deepseek-flash`**。不要把 `claude-opus-5-5` 等 UI 别名直接作为 DeepSeek 请求 model ID。用户在新电脑单独提供私有凭据文件/隔离配置；凭据、真实转录和日志不进入 Git。Claude/OpenAI/Grok 官方 OAuth、真实 IM 验收需要对应获授权账户，DeepSeek 不能替代。

## 确定的剩余工作与顺序

| 顺序 | 当前事实 | 下一步 |
| --- | --- | --- |
| P0 门禁 | 最终全量 `check:go`/`verify` 未通过；早期 Go 改动覆盖约 64.9%，不是最终值 | 从此分支重新跑 `check:impact` 所选检查，修复失败，以实际边界/消费者测试满足 90% 门禁；不得降阈值、删声明或加豁免 |
| P0 会话生命周期 | 多发普通结束快照导致团队审批消失已修，Source/聚焦 race passed；startup 有独立 user registration | 最新完整 33 项消费者回归重跑；处理 connect 多发 `session_state` 的差异、Stop 后旧 queued user 仍可能实际执行；验证取消/队列身份/后台 SDK/重连 |
| P0 团队 live | v4 正式审批→alpha Write 审批→lead 立即引导并回复后同一审批仍在→alpha/beta 实际正确产物完成 | 本轮按用户要求先提交，停在 lead judge Bash 审批前，已正常 Stop；judge/self-close/TeamDelete 未完成。新隔离任务重跑完整流程，不靠刷新/停止恢复权限显示 |
| P1 TUI | `/help /permissions /tasks /skills /agents /mcp` 等 local-jsx 完整消费者缺失；没有写 `/help` 半成品 | 按 Source 注册/gate/真实配置与任务消费者逐项实现，公开真实 PTY 打开/操作/退出/下一轮，不能发命令给模型冒充面板 |
| P1 输入/快捷键 | toggleTodos、toggleTeammatePreview、killAgents、fastMode、imagePaste 五个默认 action 缺消费者；CLI 图片/附件输入拒绝 | 完整动作/状态/媒体链及 true-gate custom keybinding loader/hot reload；不只补键名或隐藏入口 |
| P1 CLI flags | `--file`、`--chrome=true`、`--remote/--teleport` 和三个 deep-link metadata 参数未闭合 | 沿真实 Source 实现。`--file` 5 并发/3 retries/Bearer ingress，启动异步，仅 resume 等待；`--sdk-url` RemoteIO 已做不代表 remote/teleport 已做 |
| P1 CU/Doctor | Doctor 真实 PTY/Source/取消已局部通过；CU 新只读诊断 40/40（31 可定位、9 重复 fingerprint/topology 拒绝） | 最终签名桌面真实模型完整 CU 任务与失败恢复。内部诊断不是模型工具；保持统一授权/目标/快照有效性边界，不能倒推旧 `g1:245` 原因 |
| P1 自动记忆/Read | Flash live 17 次请求（14 auto_dream）与独立完整大 Read/count_tokens passed，均为中间 binary | 最终候选重新验收整理/Stop/lease/内存内容与旧 session 继续；保留旧失败证据 |
| P2 所有产品表面 | React/native、设置/市场/会话/工具大量实现仍为局部或中间版本验证 | 按 38 项计划及原 115/SB 清单逐项真实入口，设置保存后下一次执行实际消费；旧 session 不变复制后 fork，加新场景，CLI/桌面/Web 同行为 |
| P2 外部条件 | Windows 无实机；OAuth、真实 IM、发布环境有未验收条件 | 提供对应平台/账户后实测，crosscompile/loopback 状态单列；不能写为外部真实通过 |
| 最终 Benchmark | 正式 120 对未执行；6 对 pilot 仅 setup 验证 | 确定性门禁通过并冻结同一 Go/React/native 候选后跑完整 120 对，按既定产物、错误、tokens/成本/耗时与统计阈值判断，不能外推 pilot 或宣称绝对零变化 |

## 交接时证据边界

当前 server 聚焦 race passed **4.422 秒**（Source SDK-only idle、pre-send running、startup/prewarm/Stop、32 条 queued input 与退出）；application 聚焦 race passed **12.659 秒**（终帧对照、Grok Stop、权限重连、SDK mailbox 真实写入/关闭）。较早 33 项是 32 pass/1 fail；旧 SDK-only running 断言按 Source 修正并聚焦复测，最新全部 33 项未跑。

Doctor 四包 Source/race、公开真实 PTY Enter/Esc/保持中 HTTP 的取消、vet、Windows CLI crossbuild passed；Windows 实机 not run。CU 签名只读 fixture v2 completed，未执行 click/input/model；v1 fixture `/tmp` 别名比较误报保留，生产 guard 未放宽。正式隔离桌面 CU 已恢复 off。

审计解析器 5 tests/12 assertions passed。本次全 Go 包 **compile-only passed**（`go test ./... -run '^$' -count=1`，修复测试 import cycle 后）；审计/Benchmark runner/CLI launcher 聚焦 33 tests passed、0 fail，文档站用已有依赖 build/check passed（31 tests），交接链接检查 passed。敏感信息扫描、diff 检查通过；compile-only 不冒充全量测试，文档检查未重跑 npm ci。全量最终 `verify` 与正式 Benchmark not run。

`artifacts/`、私有 `/tmp` 构建/日志、依赖缓存、凭据、会话和截图不提交。历史文档的本机 ignored 证据在新电脑可能不存在，应重跑对应测试；缺失不能当通过。大型静态 JSON 报告由脚本重生成，提交脚本与 Markdown 台账。

## 直接交给新 agent 的指令

```text
在 feat/go-migration-20261013 分支继续完成 Go 迁移，不切 main，不把本分支合到 main，不发布 release。
先读 AGENTS.md、.agents/plans/go-migration-cross-machine-handoff.md，以及其中列出的同步计划、当前审计、原 Server/CLI/桌面清单和 Benchmark 协议。

目标：从图标打开 React 桌面即走 Go，所有可见功能与完整 CLI/TUI、Web/H5、Agent/工具/团队/后台、会话、设置、skills/connectors/MCP、Computer Use、语音、IM、更新和迁移恢复都具有与冻结原版一致的实际结果。两端共用 Go Harness，不使用 TS CLI/Server fallback。

先复现当前检查与 P0 缺口，再分工实现非重叠完整用户链路。无模型需求的做确定性/真实系统测试；需要模型的用用户在本机提供的隔离凭据，调用 DeepSeek 官方 Anthropic Messages，model ID deepseek-flash，验证实际工具产物。缺对应账户/平台的准确保留未验收，不能用 mock 或 DeepSeek 替代该外部服务。

测试只用临时 HOME/ConfigDir/profile，保护真实配置、凭据和 transcript；旧会话不可变复制后 fork，不改父历史。浏览器交互遵循本机 AGENTS.md 的 ego-browser 指令。

按 bun run check:impact 选中的门禁检查，最终 bun run verify，保留 90% 门禁。冻结同一 Go/React/native 最终候选，完成 CLI/桌面/Web 真实验收和协议中的全部 120 对 Benchmark。持续更新实施记录，区分 passed/failed/skipped/blocked/not run；所有目标真实通过才声明迁移完成。再次提交/推送按用户的新授权执行。
```
