# 使用真实旧 session 验收 Go 迁移

更新：2026-10-06。验收方式采用用户要求的正常 Agent 对话：旧 session fork 续跑与新任务并行覆盖。**真实官方 Flash 已通过 Web UI 基础工具与子 Agent、一个旧 session 的 CLI fork/重启续聊、Web UI 续聊、实际 macOS TUI 任务，以及原生 MCP 文本/结构化工具的 CLI 和 React 任务。完整迁移与 Benchmark 尚未验收。**

## 每项能力的真实验收规则

按用户最新要求，涉及模型的能力必须在对应实现与离线回归通过后，用官方 DeepSeek 做正常 Agent 对话，独立核验工具调用、任务产物及后续对话。不能把编译、Source 对比、模型 fixture 或另一项任务的成功算作本项在线通过。每项分别记录实现、离线验证、在线验证；需要在线验证但尚未运行的项保持未验收。

不依赖模型的设置、配置持久化、发现、HTTP 状态与原生动作使用实际 API/UI/进程核验；一旦改变活动会话的工具、权限、上下文或模型请求，继续用真实对话验证其效果。每份证据绑定生产构建哈希、实际响应 model、入口、SID 和执行范围。

## 本轮原生 MCP 在线任务

- 普通 Go CLI：官方 Flash 实际调用私有 MCP 服务 `inspect_task`、`submit_result`，服务校验 nonce 与计算结果后写出文件，退出 0。候选 SHA256 `4e14ff061e86ae4a224ab5c71863a66f725479dd330646242e4a25f6def9c7ad`，收据 `artifacts/go-migration-integration/real-native-mcp-cli-receipt.json`。
- 原 React Web UI：普通 Go Server、同一个实际会话、两轮官方模型任务。第一轮在 MCP 审批等待时刷新页面，恢复原审批后批准读取和提交，真实文件独立校验通过。第二轮故意提交错误 nonce/answer，实际工具错误回到模型，模型重新读取并提交正确结果，回复 `MCP_ERROR_RECOVERY_OK`。总计 5 次实际 MCP 工具调用，响应 model `deepseek-flash`，没有 401。候选 SHA256 `ed86f4a9f31050ecf43b8feb86f3978db0c30ecf2c444a9cb0384c3a84fb1736`，SID `e939d9ec-98d9-4962-a164-566a951ed65d`，收据 `artifacts/go-migration-integration/real-native-mcp-react-receipt.json`。
- 新增离线回归：真实 DesktopSession 的 MCP 重连替换下一轮工具目录、未知服务报错、持久化禁用后刷新并移除工具；按名称查询配置的真实 Source 对比含四模式/九个名字。相关 race 与 vet 通过。动态控制尚未通过管理页在线验证，不能混算为已验收。
- 本轮仍使用显式空 builtin 工具列表，只验证实际 MCP 工具链。MCP 管理 API、媒体/大输出、原生 Electron 窗口、完整默认工具目录和配对 Benchmark 不在本轮通过范围。初始无激活 Provider 时发现 `models standalone/Claude OAuth resolution` 尚未实现；通过实际 Provider API 创建并激活隔离官方配置后完成任务，不将此准备称为 Provider UI 验收。

后续同会话的管理接线与真实任务（不能与上面的候选混算）：

- 普通候选 `d33e6750143a716a792b1b651e5b948c29d07ade2a04aefdb16b09e738da143f` 重启后，从 React 恢复原 SID，读取新的 MCP nonce、提交新的正确计算结果，回复 `MCP_RESTART_CONTINUED_OK`。实际 `/mcp` 列表与连接状态通过；浏览器宿主按原版使用简化设置，未注入桌面 host。
- 同候选通过真实 HTTP API 禁用 MCP，`sessionSync.applied=true`，MCP 服务没有收到调用；但空工具目录下模型输出了 DSML 文本，未给出预期正常回答。保留首个失败 `artifacts/go-migration-integration/real-native-mcp-empty-catalog-first-failure.json`，原版在线对照未运行，原因未确认，不修改 Harness 解析或以另一场景替代该失败。
- 最新普通候选 `2a498d121395cd48e64f81b0c4a23d0b3479c78363a6d0839f0938992bb22c47` 使用显式 `Read,Write,Edit,Bash,Glob,Grep` 加实际 MCP。相同 SID 的三轮真实任务通过：MCP 关闭时审批 Write，实际文件严格包含 `MCP_DISABLED_WRITE_OK\n`；HTTP API 开启后 `sessionSync.applied=true`，模型实际调用 inspect/submit，服务核验新 nonce 与计算结果；再次 HTTP 禁用后 `sessionSync.applied=true`，模型审批 Write 后用 Read 核验 `MCP_HTTP_DISABLE_OK\n`，MCP 调用数未增加。收据 `artifacts/go-migration-integration/real-native-mcp-management-receipt.json`。
- 最后一轮浏览器驱动误等了第二个审批：Builtin Read 在原版权限路径下直接通过，实际任务已完成，故等待超时。真实 transcript、文件和工具调用单独核验成功；驱动 exit1 不记作浏览器命令通过。
- 管理 API 的 34 个隔离真实 HTTP 场景与原 Source handler 对比通过，包括父目录 raw 配置保留、跨项目冲突回滚、toggle/status/reconnect、重试探针以及实际 initialize/tools-list 次数。实际 WebSocket 会话的 HTTP toggle 已验证下一轮推理工具目录，Source/race 与 vet 通过。配置 hash 的 Scope/键顺序/UTF-16 比较通过原 Source 对照，探针不能清理被新重连替换的连接。

上述收据只关闭各自注明的限定路径。Electron 管理页按钮、MCP prompts/resources/media/大输出与其他原发行传输、默认完整工具目录和全量 Benchmark 继续未验收。

## 已准备的真实样本

只读盘点发现 182 个 `/tmp` 相关主 session 文件。原始历史保留在原位置；选取的 JSONL 按原字节复制到临时 HOME/config/workspace，原设置、服务商和凭据不复制。旧日志不加入仓库或 CI fixtures。

普通生产构建的 Go CLI 已执行以下三个真实历史副本的 `--resume SID --fork-session -p /cost`。三例 exit0、新 SID、fork 日志存在，原始文件与隔离副本的原日志均未变。这只是实际恢复及本地命令证据；这三例本地 `/cost` 不计在线任务；后续在线证据单独记录。

| 原 session | 历史内容 | 后续正常对话任务 | 本轮证据 |
| --- | --- | --- | --- |
| `2a212c53-54aa-40c8-bb8f-c9323247fa56` | DeepSeek 论文 PDF 下载，Bash 工具历史 | 依据历史说明原任务与目标目录，在当前隔离目录生成续测说明文件，再通过工具读取、验证 | CLI 恢复/fork、真实模型 fork 与新进程续聊、Web UI 审批刷新、实际 TUI 任务与 exit0 passed |
| `dd1c53b8-cb2e-40ee-8c00-a0e14a8de16f` | 文件创建与权限等待 | 延续原文件任务，在当前隔离目录创建续测文件；通过正常 UI 批准，再读取确认；另做拒绝分支 | CLI 恢复/fork passed；真实续聊 not run |
| `3cf28381-292d-431c-8536-c2d666f4fa5d` | 后台 Bash 与任务通知 | 运行短后台任务，等待完成通知，读取实际产物并结束当前回合 | CLI 恢复/fork passed；后台执行仍是未完成迁移项 |

原始历史及私有 stdout/stderr 的位置保存在隔离目录；脱敏验证收据为 `artifacts/go-migration-integration/actual-old-session-fork-local-receipt.json`。该候选 SHA256 为 `8c419046e22d6737c4ebe9d2781ee1d630d95155e39f72abb8b0646bfe9dce3c`。

## 执行与判定

1. 每个入口独立复制相同原始历史和任务工作区；首次 fork 使用新的 SID，后续恢复使用这个 SID。保留原历史哈希，结束后再次检查。
2. CLI 通过公开启动器正常输入；桌面端和 Web UI 通过 Ego Lite 操作实际 React 界面。记录页面、Go 进程、构建哈希和真实 provider response，不能通过直调工具或注入 assistant 消息代替聊天。
3. 三端使用相同模型、Prompt、权限模式、工具目录、预算和上下文设置。当前 CLI 的显式工具子集可用于定位故障，最终验收必须去掉子集限制、使用原来的完整默认工具目录。
4. 检查任务产物和工具执行：文件内容/路径、命令退出状态、批准或拒绝的效果、子 Agent 结果、后台完成通知。模型说“完成”不算独立的任务结果。
5. 每例继续第二轮、刷新或重连、关闭并重启后再次续聊。验证历史和 fork 身份、工具结果、运行模型选择、权限等待和错误恢复一致。不得把另一个会话的成功记录算作本例通过。
6. 除三端独立运行外，再用同一 fork 做跨端交接：CLI → Web UI → 桌面端。单独记录 Go 桌面 branch/side-chat API 的缺口，不能把离线手工复制称为 UI fork 完成。

新增任务覆盖多文件修改与实际测试、工具拒绝后改用可行步骤、停止后续轮、Agent 分工、长上下文压缩、Skills/MCP/Hook 和后台任务。这些任务使用隔离的小项目，验收条件在发送提示前确定，失败保留并修复相应实际入口。

旧 session 检查恢复兼容性，新任务检查完整执行能力。要证明 Harness Benchmark 没有回归，还需旧 TS 与 Go 在相同历史、工作区、模型配置及任务上的配对运行，并比较实际产物、工具/权限轨迹和任务评分。回答文本与请求耗时允许模型随机性；固定响应契约仍要求零语义差异。

## 模型与当前阻塞

官方 Anthropic Messages base URL 为 `https://api.deepseek.com/anthropic`，客户端模型配置沿用用户要求的 `deepseek-flash[1m]`。正式运行保存实际请求及响应的 model 字段、配置和时间；不能仅以界面名字冻结模型版本。

官方文档目前将 `deepseek-flash` 指向 V4.1 Flash，旧 V4 Flash 的名称也路由到新版。因此这些新验收应称为当前官方 Flash 验收，不能标作已退休 V4 的同版本结果。[官方更新日志](https://api-docs.deepseek.com/updates/)、[官方 Claude Code 接入说明](https://api-docs.deepseek.com/quick_start/agent_integrations/claude_code/)。

前一把凭据在独立官方认证诊断返回 401 `authentication_error`。用户后来提供的另一把官方密钥已从实际 React UI 保存并在 Go Harness 中认证成功；本轮未再观察到 401。真实响应 model 为 `deepseek-flash`，请求配置仍为 `deepseek-flash[1m]`。密钥只保存在隔离临时文件和配置，不进入本计划、仓库或收据。

本轮在线任务证据：

- 实际 React Web UI 三轮：Write；Read/Write/Read/Edit/Bash 的 Python 内容断言；Agent 子 Agent Write、父 Agent Read。权限通过实际 UI 批准，三个产物均按字节核验。候选 SHA 为 `8c419046e22d6737c4ebe9d2781ee1d630d95155e39f72abb8b0646bfe9dce3c`。
- 原论文 session 的私有 fork `2446152d-0ae7-4396-94f1-2ce437b28630`：公开默认 Go CLI 正常模型对话创建、读取续测文件；另一个 CLI 进程恢复相同 SID，实际退出 0。原始用户历史与副本原日志哈希未变。
- 原字节私有 fork 导入隔离 UI HOME 后，通过聊天界面继续同一 SID，Read/Write 完成；Go Server 在任务前重启，审批等待时刷新页面再批准，实际文件包含历史标题和原 PDF 路径。该准备是私有历史导入，**没有验收桌面 UI fork/branch API**。
- 使用同一个隔离 UI config 与 SID，在真实 macOS PTY 中执行公开 Go TUI。终端显示 Write 审批，批准前文件不存在，按 y 后完成实际写入与模型回复，正常退出 0；Go Server 随后重启，UI 显示 TUI 的历史。首次 PTY 退出探针挂在测试脚本的未排空终端输出，保留为失败，不计退出证据；修正探针后另一个新任务通过。
- CLI 正常 `-p` 首次真实续聊暴露无人响应的权限等待。回归先复现失败；实现沿用原版无 permission prompt tool 时的决策返回，未批准工具作为错误返回模型，交互 UI/TUI 仍正常审批。application/clibootstrap/sdkhost 三包 Source-enabled race passed。

后三项使用最新普通候选 SHA `61f9c2296656f5904cfa3613d19b7b003b5e1a78209fa6feeaf1594b0f173631`。所有运行仍为显式工具子集，不能称全目录等价；本轮尚未操作原生 Electron 窗口完成同样模型任务。收据位于 `artifacts/go-migration-integration/real-ui-tools-and-agent-receipt.json`、`actual-old-session-live-cli-receipt.json`、`actual-old-session-live-web-receipt.json` 和 `actual-old-session-live-tui-receipt.json`。

当前默认工具全集、部分 CLI/TUI、桌面 branch/side-chat、后台任务等尚未全部迁移；完整门禁也没有通过。恢复入口通过不改变“全量迁移未完成”的状态。
