# Go 普通非模型 CLI 命令：2026-10-08 分项证据

本记录只关闭已验证的命令分支，不关闭完整 CLI、桌面、16 项 Server 边界或 Harness Benchmark。

## 实现

- `clibootstrap.MainCommand` 使用现有 NativeProcessSettings、真实 managed/plugin/settings producer、同 Global writer 与 secure store，按初始化与 preAction 顺序读取设置、enable Global、应用安全环境和 CA 配置。没有创建 ASK、模型会话或第二套凭据存储。
- 普通 Go 入口实际分派 `auth status`、`auth status --openai`、`auth logout` 与 `auth logout --openai`；`--text` 保留原文本语义。JSON/文本、字段顺序、账号来源和退出码来自对应原业务规则，不使用固定成功响应。
- 普通 `agents` 原生 factory 已接同一 NativeBoundary/MainCommand，再使用实际 Settings.AgentCatalog。原离线子 profile 保留，实际发行路径不再无条件报普通 agents OS startup 未迁移。
- 原 `/login` API key 删除入口属于同 secure store 的独立 legacy identity。一般退出按原规则清理 primaryApiKey、secure record、remote settings cache 和 oauthAccount，保留未知配置/已完成 onboarding。OpenAI 退出保留其他账号槽，选定 file-backed 路径只移除该文件并登记原 marker。
- `native_process_fixture` 的 Keychain primitive 显式 unavailable，仅用于隔离测试；release 编译不包含此替代，测试不访问开发者真实 Keychain。

## 实际运行结果

| 检查 | 状态和证据 |
| --- | --- |
| 233 初始 compile 与既有 native 身份邻域 | passed，cliauth1.884、clibootstrap27.781s；cliauth selector 无匹配不是完整包通过 |
| 234 原 handler/native 40 profile | failed；Globals 实为 optsWithGlobals，text/openai 被误判 root flag；两条 inline settings fixture 参数顺序也未进入原路径 |
| 235 修正后40 profile | passed，7.378s；仅检查真实 root default keys，按原 eager settings 准备后解析，fixture 将 settings 放在 root 前；没有放宽未知 root 参数 |
| 236 Source/agents/production factory 邻域 | passed，clibootstrap8.546、securestorage1.772（selector无匹配）、CLI4.228s；不是三个完整包通过 |
| 237 独立退出及生命周期 | passed，clibootstrap2.226s；独立file/共享槽、取消/foreign Owner/Close拒绝；CLI selector无匹配 |
| 238 原退出四状态与40状态合验 | passed，8.120s；实际原 authLogout/performLogout、原 OpenAI storage、cache/marker/remote file 和 Global updater 对照。只有外部secure/config adapter及空telemetry flush/cache通知替代，未声称完整账号/遥测生命周期 |
| 239 实际普通 Go CLI 子进程 | passed，CLI16.996、Source退出2.993s；真实编译native_process_fixture，仅替外部managed/Keychain primitive，实际Parser/初始化/存储/命令/stdio执行。匿名status exit1；OpenAI text exit0；logout删除token/marker；重读exit1；一般logout清理文件、保留未知global；agents读私有Markdown并正常列出；没有项目ASK记录 |
| 240 六包完整Source/race | failed：cliauth1.570、openaiauth2.672、securestorage2.528、agentcatalog5.327、CLI19.790s全passed；clibootstrap477.888s中两原公共Write/max-turn测试被ErrRestricted拒绝。原因是新增Bedrock绑定在inactive分支前检查NativeProcess/AWS producer，污染旧普通CLI；新认证/退出自身测试无失败。不能称六包全通过 |
| 241 check:impact | passed/selection only；所选门禁仍必须执行，不能以选择成功替代 |
| 修复后旧公共CLI/媒体邻域 | passed：Cloud501原两测试+inactive/foreign/active-negative2.413s；Diagnostics `/tmp/go-public-write-media-owner-regression-second.log` 6.599s，包含图片/PDF HTTP/SSE/取消后续聊。原fixtures未改，仅修实际inactive消费者顺序，Owner guard保留 |
| 243 完整clibootstrap Source/race复验 | failed，523.017s，仅原iTerm React fixture的固定500ms轮询提前调用select.onChange；两原公共Write/max-turn已无失败，认证/退出也无失败。保留原异常，不算完整通过 |
| 242 docs 与244六包vet | passed，两条命令实际exit0；docs构建108页、31tests/0failed，六包vet无错误 |
| 245 签名build 与246实际包检查 | passed/build only，Go SHA `87e7a28f9d6a75b1bdd3a0d088cb475d75af3528e0d9a5c295f165cd75cacdf6`，strict codesign0、package-smoke PASS。未启动GUI或调用模型；之后MCP bare/Unicode修复不计入该包 |
| 247/248 iTerm fixture实际settlement修正 | passed，19.134s / 26.703s；等待原state setter及install/verify结束，原业务断言不变；248新增650ms真实私有探测回归，两侧仍比较实际callbacks/命令/配置/结果 |
| 249 完整clibootstrap稳定复验 | passed，476.909s，`/tmp/go-current-nonmodel-integrated-stable-full-source-race-249.log`，包括保留原断言的7个实际iTerm React场景 |

所有凭据是假值，HOME/config/temp 与文件均隔离。这些命令无需模型请求；此前官方 DeepSeek 的图片/Write/Read/冷恢复属于 P231 实际签名 SHA，不能把本节新源码计入该候选。

## 仍需完成

- CLI OAuth login、浏览器/手动登录、refresh-token 快捷路径、账号/角色/first-token-date 安装及强制 org 校验。
- Status 的 descriptor/CCR/managed source consumer 已补齐并通过40新增 Source profile与实际继承pipe，见[凭据状态补充](go-native-cli-credential-evidence-20261008.md)。模型请求的 descriptor 分支、内部 Homespace schema bootstrap、完整 startup globals、异常credential原stack和全部平台仍开放。
- MCP、plugin/marketplace、doctor/install/update、剩余 TUI/stdio 动作和全部模型相关任务；MCP 独立实施中。
- 六包完整结果、稳定最终 verify、最新签名桌面整包和原条件配对 Benchmark。
