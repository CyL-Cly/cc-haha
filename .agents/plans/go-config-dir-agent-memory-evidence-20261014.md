# Agent memory 当前配置目录与缓存

本批证明同一实际 native Agent factory 的 memory prompt 消费者，普通 Main/GetInitialSettings/设置赋值的完整 host guard 保留。冻结 Source `6d21316ec4635f29ecbaa43e1a5574bfd31d7155` 的完整 loader、定义缓存、`getSystemPrompt()`、`isAutoMemoryEnabled()`、`getAgentMemoryDir()` 和 `buildMemoryPrompt()` 均在私有目录执行；未替换业务函数，没有网络或 secure primitive。对应六个 Source 生产文件 SHA 与冻结提交相同。

九个 profile 包含 user 的 `0` 强制启用、absent env 的原 merged cache on/off、显式 disable、relative/empty ConfigDir，以及 project/local/remote-user scope。普通定义 cache 暖读保持同一对象；配置目录变化后 user prompt 每次读取新 MEMORY.md，物理内容重写也立即消费。Agent clear 更换定义对象、保留 merged settings 对象；原 `resetSettingsCache()` 则使同一定义重新消费新物理 settings，并按 on/off 变化决定读取 memory。相对和空目录保留 prompt 的原 requested spelling，只在实际 FS 调用解析到 process CWD。

旧 Go 在初始完整 prompt 匹配后，同一缓存定义的第二次 prompt 因 `CLAUDE_CONFIG_DIR` 变化被 startup merged guard 拒绝。原 API fail-first 实际执行 0.90 秒（package 2.110 秒）。最近修复为 factory 预先绑定 exact Settings/State/env/native-path 的 Agent memory reader，初始仍要求完整 host admission；之后仅该 reader 允许 CCD 变化，原 getter body、解析器和 cache 不变。快速 gate 也检查其它 frozen host controls。Prompt 入口采当前环境，路径与内容读取不重建 Agent service；原 text builder 抽为共享纯 instructions，避免 Agent prompt 再次多余执行 startup settings gate。

`-p=1 -race -count=1 -json` 实际通过：新九 profile（1 top/10 events，9.081 秒）；same-area + 原消费者 7 tops/25 events（bootstrap 16.787 秒、startup 2.409 秒），含 foreign/retained/late-binding、HOME/USER_TYPE/XAA/SIMPLE 负例、原 memory、first-cold Agent、Source inline merged reader、真实 plugin Agent 子 Read/4 HTTP；原 systemprompt 全包 8 tops/19 events（6.965 秒）。均 0 skip/fail。原新增五阶段 Source 大输出立即 exit 导致 JSON 截断的两份无效 fixture 输出保留；现等待真实 stdout callback drain。共享 provider 声明窗口的一次 build-only 也单列，不计产品 RED。

Ignored 收据：`artifacts/go-migration-20261013/config-agent-memory-evidence-20261014/receipt.json`，含原 RED、修后 JSONL、精确 selector 与当前文件 SHA。此前 first-cold 用户源与 warm discovery 的独立收据仍有效，不能将本批当普通 Main 启动链已开放。

仍 OPEN：Agent 原 Source 的 fire-and-forget mkdir 与 held completion/关闭生命周期（当前 Go 仍同步 mkdir）；主 user context/auto-memory 路径 memo、不同 child CWD/remote-local；SDK `apply_flag_settings` 的真实 inline 入口；公开 CLI/桌面/Web、全门禁 90% 与 120 对 benchmark。same-area vet 尚未执行。

后续同一生产字节四包 `go vet -p=1 ./internal/clibootstrap ./internal/clistartupenv ./internal/systemprompt ./testdata/gochildcoverage` exit 0；raw `same-area-vet-green.log` 已纳入本批收据。此检查不增加模型/公开入口或异步 mkdir 生命周期的证据范围。
