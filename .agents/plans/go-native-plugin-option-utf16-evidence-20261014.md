# 插件选项 UTF-16 保存与实际消费者证据

2026-10-14；继续当前 checkout，未切分支、提交、推送、合入 main 或发布。本文只记录以下边界，完整迁移仍未完成。

## 已复现与修复

- 原 MCP 参数解析及 Skill 文本经过 Go 字符串解码后，孤立 UTF-16 单元变成替换字符。保留 Raw JSON／UTF-16 到模板替换、参数／环境／请求头及实际模型 JSON 边界。
- 插件根目录／数据目录使用原版函数替换语义，数据目录仍按实际引用延迟创建。Skill 目录／会话 ID 保留原版 replacement-string 的美元模式，不能等同于函数替换。
- 完整 Skill Prompt、原 Bash／PowerShell 权限生产者、真实 Shell、结果持久化、并行完成次序、父 Agent 权限与子 Agent cwd 保持在既有路径。普通模型消费者及 Agent 的预加载 Skill 采用原始文本 JSON；旧字符串调用者保留字符串接口。
- 选项名包含孤立单元时，保存流程过去以有损字符串查 schema、删除另一个存储的旧键。现在使用同一个原始属性身份分类、清理及构造设置删除路径；敏感字符串转换沿原版 `String(value)` 的失败语义，失败先于写入。

原版参考文件 `pluginOptionsStorage.ts`、`loadPluginCommands.ts`、`promptShellExecution.ts`、`argumentSubstitution.ts`、`mcpPluginIntegration.ts`、`envExpansion.ts` 与冻结 MAIN 的 diff 为空。对照包含实际完整函数／方法或原模块，不以重写算法作为期望值。

## 已观察结果

所有日志位于 ignored `artifacts/go-migration-20261013/`；测试使用临时 HOME、ConfigDir、凭据文件、fake Keychain 失败原语、loopback HTTP、Bun 1.3.14、Go 1.25.7。未使用真实 Claude OAuth 或真实用户数据。

| 边界 | 实际证据 | 结果 |
| --- | --- | --- |
| MCP 配置解析 | `plugin-option-interpolation-source-red-v1.log`；18 组 stdio／HTTP 配置原版完整 resolver 对照 | 旧解析真实 RED，原始单元丢失 |
| MCP 配置解析及共享协议回归 | `plugin-option-interpolation-source-v5.log` | `-race` protocol 1.550s、pluginmcp 4.213s passed；protocol 的独立文件 fixture 因未配置跳过，不能称该文件 fixture passed |
| 完整原版 Plugin Prompt | `plugin-option-prompt-source-v2.log`；9 种值 × command／skill 两种调用，含敏感遮蔽、未知引用、美元路径／会话值 | `-race` skillcommands 2.991s passed |
| 原版 Shell 匹配 | `plugin-option-all-focused-v1.log`；普通语法、嵌套代码块、原始 UTF-16 命令及前后文本 | 原模块对照 passed |
| 真正保存后消费者 | 同日志；实际 settings／credentials → 已安装插件 → `MainPrint.RunTurn` Slash → 权限内 Bash 实际文件 → 两次真实 loopback 模型请求 | 原始单元、Shell 输出、敏感遮蔽及下一 Human 全部断言 passed |
| 真正旧版反例 | `plugin-option-real-model-original06-red-v1.log`；06 的独立副本，仅增加同一个旧 API 测试 | `-race` 1.859s，真实 RED：`foundRaw=false`，实际 Shell 和敏感遮蔽均 true；旧生产未改 |
| Shell／父子 Agent 回归 | `plugin-option-all-focused-v1.log` | bootstrap 15.944s passed，包含真实 Bash 并行文件、原版 PowerShell 工具物理执行／结构化结果回退、父权限／子 cwd；此为 macOS 上真实 PowerShell，不能替代 Windows 实机 |
| 三个包完整回归 | `plugin-option-packages-full-v1.log` | `-race` protocol 1.527s、pluginmcp 4.294s、skillcommands 8.735s passed；protocol 文件 fixture 同上 skipped |
| 原始属性名存储分类／跨存储清理 | `plugin-option-storage-utf16-source-red-v1.log` | 原版实际物理文件对照 RED：旧 Go 分类错误 |
| 分类／清理修复与旧设置消费者 | `plugin-option-storage-utf16-source-v2.log` | initial-sensitive／swap-stores 两组原版实际文件、原选项 Source 回归、共享设置／凭据、真实 Slash 模型请求，bootstrap `-race` 4.676s passed |
| 原版写前转换失败及同消费者回归 | `plugin-option-storage-conversion-original-source.json`、`plugin-option-storage-utf16-source-v3.log` | 原版 `No default value`，settings／credentials 字节保持原值；Go 同消费者负例及两组存储分类／清理、旧 Source、真实 Slash 回归 `-race` 6.053s passed |
| 普通 JSON 对象的继承属性替换 | `plugin-option-inherited-original-source-v2.json`、`plugin-option-inherited-full-prompt-source-red-v1.log` | 12 个原版继承属性实际 String 结果；18 组完整 Prompt 先真实 RED，旧 Go 保留引用而原版替换 |
| 继承属性修复与三包完整回归 | `plugin-option-ordinary-prototype-source-v2.jsonl`、同名 receipt JSON | protocol 1.647s、pluginmcp 4.196s、skillcommands 8.733s，`-race` passed，280 pass events／0 fail；唯一 skip 为由独立 Bun differential lane 提供输入的 `TestProtocolParityFixture` |

MCP 的 18 组中，三种有效字符串各经实际 Go Save→Load 后解析；数组、数字、null、对象仅是直接 resolver 的语言转换对照。曾把不符合保存 schema 的值误当成保存前提，`v3` 保留为失败记录，不能说全部九种值已经过有效保存。完整 Prompt 对照将实际原版方法的 Shell 回调连接原模块；其未含嵌入 Shell 的语料不能代替物理 Shell 证据，后者由同一原生 Harness 的模型请求与 Bash／PowerShell 测试另证。

后续完整 Prompt 的 18 组均增加 12 个继承属性引用，MCP 的 18 组也增加 constructor／toString／__proto__ 配置引用。`GetOrdinaryPropertyStringJSON` 仅表示普通 JSON 对象上的实际属性读取后 `String` 转换，保留 own 值／own undefined 的遮蔽、原始 UTF-16 与转换错误；原 `GetPropertyJSON` 仍只返回 own 数据。原版固定 Bun 1.3.14 中 Object constructor 与其他 native 方法的字符串格式不同，均由实际原版方法对照，而非把函数写入配置或执行函数。

## 保持开放

- `compiler-cache-cleanup-v4.json` 记录所有 agent 暂停新编译、OS Go/compile/link 进程为零之后仅清隔离 GOCACHE；原 0700 目录、依赖种子、证据及 lockfile hash 保留。可用空间 7.56GB→39.69GB；之后完成上述新增负例。
- 本文不证明被外部 JavaScript 代码主动修改的自定义原型、所有直接非有限数字的保存行为、所有传输拒绝诊断或所有 Unicode 字段名的 UI 输入。不能从普通 JSON 对象的继承属性和有限对照外推整个插件功能完成。
- 新鲜全量 Go／race／90% 门禁、最终 verify、同一候选桌面／Web／CLI、真实 DeepSeek Flash、正式 120 对 Benchmark 均未因本文而通过；Windows／其他外部验收条件单列。

新生产边界主要在 `protocol/javascript_templates.go`、`protocol/object_properties.go`、`protocol/ordinary_property_string.go`、`pluginmcp/resolve.go`、`pluginoptions/service.go`、`skillcommands/{definition,fields,prompt_shell}.go`、`clibootstrap/native_skill_{invocation,shell}.go`、`clibootstrap/native_agent_preset.go`。新增 API 是进程内原始文本／回调，不新增持久化字段或设置 schema。

## 插件 Agent 的实际合并存储与子模型消费者

沿下游发现 `native_agent_plugins.go` 只从 settings 读取选项，并经 Go 字符串替换；原版完整 `loadAgentFromFile` 调用共享 `loadPluginOptions`，私有 secure store 优先。现 AgentCatalog 接收同一实际 PluginOptions producer，普通 MainPrint、`agents` CLI 与 Doctor 都传入对应共享 secure store；没有创建另一个凭据所有者。Skill／Agent 的敏感遮蔽复用 `pluginoptions/content.go`，Agent 原始提示保存在 Definition 的私有内存字段中，返回 metadata 的修改不能改变捕获的提示，memory 仍在真实调用时追加。

`native-agent-plugin.fixture.ts` 的新 profile 运行完整原模块、真实私有 settings／credentials 与原 `plainTextStorage`，并调用返回的 `getSystemPrompt`。该绑定替换隔离能力桩默认的空 secure store，不接触本机 Keychain。原 Source 行为仍是合并存储和完整 Agent 加载器，不把准备好的 Source definition 输入 Go。

- `plugin-agent-merged-options-physical-source-v4.log`：实际已安装插件 → 原生 MainPrint → Agent 子任务 → 私有文件 Read → 子模型消费真实 Read 结果 → 父任务消费子结果，父／子各两次实际 loopback HTTP，race 3.683s passed。严格对照原版捕获的提示前缀直到调用者追加的 `Notes` 边界；每个 UTF-16 单元、secure 覆写与敏感遮蔽相同，不声称两份不同 cwd 的完整环境段相同。
- `plugin-agent-merged-options-original06-red-v2.log` 及 test-only receipt：仅向旧 06 独立副本复制相同测试和 fixture，未改旧生产，race 2.774s 真实 RED。旧子 Agent 仍执行实际 Read／四次 HTTP，但提示前缀与 secure 优先不同。
- 前面的 v1 编译错误、v2/v3 空 secure 桩及过强整块比较失败均保留，不能作为正确原版反例或通过证据。
- `plugin-agent-options-three-packages-v1.jsonl`：agentcatalog 4.265s、skillcommands 9.718s 完整 race passed；当时 pluginoptions 尚无本包测试，明确 skip。随后增加七组原版完整 content helper 对照（缺失、继承、严格 sensitive、先遮蔽再转换、原始键、转换失败、无递归替换）。
- `plugin-agent-options-all-focused-v3.jsonl`／receipt：四包聚焦 race 全通过、26 test events／0 skip／0 fail，agentcatalog 2.848s、pluginoptions 2.885s、skillcommands 3.643s、bootstrap 19.372s。包括捕获提示与可改 metadata／返回 buffer 分离、memory 单次追加和错误身份，以及既有实际 Agent、CLI agents、插件管理、父模型 Read counter、默认 SDK Agent 与 Worktree 消费者。v2 的非法 Go Unicode 字面值导致编译失败已修正并保留原日志。

此证据不替代 LSP 激活、所有持久化 Agent 恢复路径、完整插件菜单、最终质量门禁或真实 DeepSeek 验收。

## JSON Agent 的 CLI／SDK 提示捕获

继续沿同一 `PromptJSON` 下游发现 JSON Agent 使用 listing 的 `exactString` 校验提示，导致 Source 合法的孤立 UTF-16 提示被整个丢弃。`ParseJSONDefinition` 现在按原版 Zod 的 JavaScript 空白规则裁剪原始单元，保留私有捕获及 `rawSystemPrompt` metadata；`Body`／字符串接口仍为显示投影，没有新增持久化字段。

- `agent-json-raw-prompt-source-red-v1.log`：原版完整 `parseAgentFromJson` 和实际 `getSystemPrompt` 接受三个孤立单元 profile，Go CLI／SDK 两个真实 catalog producer 均丢弃，真实 RED。
- `agent-json-raw-prompt-source-v2.log`：七组有效／无效输入 × CLI／SDK、metadata 修改不能改变捕获值，以及旧 JSON／CLI map／插件 memory 回归，四个 top-level 测试 race 5.886s passed。原版私有能力 guard 零 blocked。
- `agent-json-raw-prompt-physical-v1.log`：两个真实 `MainPrint` 入口分别经 CLI agents map、SDK initialize 启动 Agent；父／子各两次 loopback Messages 请求，实际子 Read、子结果交回父任务，race 5.032s passed。对照原版提示到调用者 `Notes` 边界的所有 UTF-16 单元，不用 Source definition 驱动 Go。
- `agent-json-catalog-full-v3.jsonl`：整个 agentcatalog 包 race 5.903s passed，38 test events、0 skipped／0 failed。
- `agent-json-raw-prompt-evidence-v1.json` 保存完整命令、输入／日志 SHA 与范围。测试没有调用任何外部模型。Agent 名称／描述的孤立单元、team-worker 字符串提示消费者及所有恢复路径不在这份有限证据中；不能因此宣称整个 Agent／迁移完成。
