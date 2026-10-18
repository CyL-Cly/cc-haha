# 动态配置目录：Agent 首次冷 settings 读取

本批继续前一份暖缓存证据中的首次冷读 OPEN。修复实际 native catalogue 的 source reader 后，首次 old user Markdown 读取完成阶段切目录，仍返回采样的 old Markdown，同时 builtin overrides 在后续冷 source getter 中读取 next user settings。普通 Main/RunTurn、普通 settings getter 和配置赋值的完整 host guard 保留；不把本批称为普通动态配置目录入口完成。

## 原完整 Source 与真 RED

`native-config-dir-agent-cold.fixture.ts` 使用冻结 MAIN 原完整 Agent loader、builtin override resolver、settings source getter/schema/parser 和真实私有文件。原 `getInitialSettings()` 仅自然加载 merged/parser cache，没有调用 source getter 预暖。实际 old user `readFile` 执行后 hold 完成；在该点 user/project/local/flag 都确实 cold，policy 被原 markdown restriction getter 自然加载。切 next 后释放，完整原调用继续运行。

五个 Source profiles（valid、schema、relative、flag file、flag inline）exit 0、stderr 0、`blocked=[]`、`secureReads=[]`。它们证明：

- next user settings 的真实路径、原 model trim/effort catch/未知字段和 UTF16 JSON 保留；project model 覆盖 user model，但保留 user effort；local 与 flag/inline 仍按原顺序覆盖。
- 当前目录的 Markdown 不会替换已经采样的 old load；同 cwd warm Result identity 保持，完整 Agent clear 才采 next Markdown。
- cold source getter 只加载一次；随后物理重写 next settings，再完整清 Agent caches，仍保留旧 parsed/source 值与 cached object/null identity。没有重建 settings service、清总缓存或注入缓存值。

新实际 native factory 回归先使用原 Settings 委派执行。四个 profile 都只在首次 cold builtin source getter 被 `changed filesystem/schema host CLAUDE_CONFIG_DIR` 拒绝，3.838s；before-hold 的真实 source 冷状态和完整 Source 执行均通过。原 RED 保存为 `first-cold-original-delegation-red.jsonl`，SHA `732f9928040a96fddc05d620ba3c08202809c027237d17aba265d71030de76b7`。此前一次构建被 TUI 的 unused import 阻断，单列 build-only，不计产品 RED。

## 最近修复与绑定边界

`NativeAgentSettingsReader` 在实际 catalogue 构造、原 `InitialSettings` 完整 admission 之后绑定现有 startup source body。它要求 resolved native profile、Settings/State/environment 的精确实例 identity，以及 `UsesNativeUserSettingsPath` 的 NativeProcessProfile/精确 state/真实 native path callback。有限或 fixed-path services、foreign/missing pointers 和已变更环境下的迟到绑定均拒绝。

绑定后的 reader 每次仍检查除本批已证明的 `CLAUDE_CONFIG_DIR` 外所有 frozen controls，并验证 source name，再委派原 `getSource`。因此 cold user/project/local 仍用实际 `GetEditableSource`/path/schema/parser；flag file/inline/policy 复用原分支与 source cache，没有复制其业务实现。catalogue 原 owner 检查、缓存、插件和 memory 路径保持。

普通 `GetInitialSettings`、`GetSettingsForSource`、settings environment assignments 的 guard 没有改变。相对 user path 仍由 Root 的 `nativeUserSettingsPath(n.CWD, p.environment)` 解析，源是同一个 canonical process environment；metadata 仍保留原请求拼写。

## 当前验证

新 cold/warm 两项在接线后初次 race：9 pass events、2 tops、0 skip/fail，7.232s。

五个独立 tops（cold 四 profile、warm 三 profile、严格 reader identity/host 负例、inline 完整值/merge/identity、finite/fixed/path-state binding 负例）`-p=1 -race -count=3 -json`：36 pass events、15 top executions，0 skip/fail；settings 1.700s、startup 3.406s、bootstrap 16.340s。Source 20s/native 10s 窗口保持。

原相关 native Agent 13 tops（Main agents readonly/cancellation/provenance/guard/close、hardlink/ripgrep、完整原 memory prompt、Plugin raw options→child model、CLI/SDK JSON Agent→真实 child Read、SDK definitions 和 parent command memo）`-p=1 -race -count=1 -json`：22 pass events，0 skip/fail，26.857s。三包 `go vet -p=1` exit 0；本批 diff 检查通过。没有跑整个 Go/CLI suite。

收据与 exact selectors、原日志、SHA 位于 `artifacts/go-migration-20261013/config-agents-first-cold-evidence-20261014/receipt.json`；Source 五项原完整 stdout/input/env 保存在 `config-agents-first-cold-source-v2`。前一份暖缓存台账的 cold OPEN 是修复前事实，本批只在上述具体绑定下关闭它。

## 未闭合

inline 是原完整 Source 和实际 native source service 的独立消费者证明。`NativeProcessSettings` factory 当前只接 FlagSettingsPath，没有 FlagInline 输入；本批四个 factory profiles 不冒充该产品输入已接。完整 memory、cold policy 变更、非空插件动态目录、cross-owner process memo、普通 Main 动态目录入口，以及同版本桌面/Web/CLI、完整质量门禁、90% 和 120 对 benchmark 均仍需独立验收。
