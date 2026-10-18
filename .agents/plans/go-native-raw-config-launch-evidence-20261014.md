# 原始配置目录启动入口：当前有界证据

2026-10-14；当前 checkout 继续实现，没有切分支、提交、推送或合 main。冻结 MAIN 为 `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`。这是中间消费者验证，不是完整迁移完成记录。

## 原版与真实失败

`native-config-launch-environment.fixture.ts` 在原 disposable HOME/CWD/ConfigDir 守卫通过后，实际调用原 `getClaudeConfigHomeDir`、`getCcHahaDir`、settings 文件路径与读取、`getGlobalClaudeFile`。六个 profile 为 absent、empty、relative、relative-parent、decomposed、absolute；文件和当前 CWD 均属于同一私有目录。五份被调用的生产源文件与冻结 MAIN 的 bytes/SHA256 全相同，见 ignored `native-config-launch-source-authority-sha-20261014.json`。

原 `NewProcessNativeBoundary` 把 absent/empty 合并成默认值，把相对目录按宿主 Go 测试进程 CWD 转成绝对值；v1 原函数/实物对照中四个 profile 真 RED，两个绝对路径 profile 通过。原失败保留于 `native-config-launch-source-red-v1.log`。

公开 CLI v4 另真 RED：empty 实际请求使用 absent 的设置头，转录写入另一目录；两个相对路径在 provider 服务的绝对路径要求处退出、零 HTTP。去掉过早的 provider 预解析后，原来隐含依赖被继续暴露：v6 plugin MCP、v8 workflow 根及 SDK host 必须存在 ConfigDir 的有限构造器拒绝。每一轮原日志保留，没有把 body 成功外推为公开入口成功。

## 当前实现

原始环境值保持 absent/empty/relative/decomposed 的区别。`ConfigHomeDirectory` 只为原生文件消费者计算 nullish/NFC/当前 native CWD 对应的物理位置；各现有 store、writer、watcher 和缓存的构造位置、对象身份与生命周期保持。现有文件消费者的配置根使用该 producer，不把物理位置写回子进程环境。

普通 CLI 的 OS snapshot 不再提前调用应用/桌面 provider resolver；saved provider 环境由既有 managed-settings startup producer 读取。全局配置保留原始相对路径与首次 lookup memo，实际 OS 操作在 native CWD 下解析。workflow discovery 的原始 config resolver/cache 保留，原生文件 adapter 使用真实 CWD。

实际 Native Print 可声明原版 absent ConfigDir，且必须与 `OriginalEnv` 的字段存在性及值严格相同。有限 neutral host 的原要求继续拒绝 absent；未知 registry 字段、非字符串目录和合成目录仍拒绝。运行中普通 `CLAUDE_CONFIG_DIR` 修改的前置 frozen-host guard 仍未开放。

## 已执行验证

| 命令范围 / ignored log | 结果 |
| --- | --- |
| 原函数六 profile，v3 | passed：7 pass events、0 fail/skip，bootstrap race 2.986s |
| settings/provider/policy/MCP/Windows 模拟身份，v7 | passed：10 top-level、25 pass events、0 fail/skip，bootstrap race 19.146s |
| 公开 CLI 六 profile + SDK 有限/原生字段边界，v9 | passed：11 pass events、0 fail/skip，CMD race 14.565s、SDK host 1.983s |
| 公开 CLI 六 profile + saved-provider marketplace + 原 plugin handlers + continue/fork/prompt files，v10 | passed：4 top-level、38 pass events、0 fail/skip，CMD race 38.081s |
| 严格加强 absent 身份后，公开 CLI/SDK 边界/原函数/provider/settings，v11 | passed：8 tops、25 pass events、0 fail/skip；CMD/SDK/bootstrap race 13.870/1.910/13.615s |
| native workflow/实际内部 Read/图像/工具 hook/终端附件，v12 | passed：原消费者选择、两包 race，命令与退出见 `native-config-launch-native-consumer-regression-v12-exit.json` |
| 六个受影响包，v13 | passed：`go vet -p=2` exit0/31.109s，仅 build/static validation |
| 冻结 MAIN 为 PR base 的 v14 impact | 路由成功：8413相对base files、14 required checks、Blocked no；不是这些检查已通过 |

公开 CLI 使用真实已编译 Go binary、真实 OS 子进程、私有状态和 scripted loopback provider。每个 profile 核对 actual HTTP 配置头、人类输入、正常退出、真实 JSONL 的 user/final assistant、源 settings 字节不变；build 与每个 child 均接既有 child coverage capture。没有调用真实模型或真实 Keychain。Windows identity 为当前平台上的模拟 primitive 回归，不是 Windows 实机。

旧 v5 两项失败为旧测试把 canonical HOME/ConfigDir 当成原始环境的断言或准备方式；已按原始 authority 改为保留 HOME 字节、使用独立物理根设置私有文件。源 JSON/golden、90% 门槛和各 consumer 时间窗口均未修改。

## 仍开放

这批不证明普通运行中 ConfigDir 更新、其它服务各自的 fresh/captured/memoized 目录消费者、初始 safe/trusted 两段完整排序、relative HOME、完整原版 workflow API 的相对路径呈现，或所有原生生产路径已经完成。后续相关消费者回归及最终完整 `check:go`/90%/`verify` 仍需执行。

同版本桌面/Web/CLI、真实外部机器/账户/有声设备与正式 120 对仍未完成；Claude 真实 OAuth 按用户明确要求排除。

ignored `native-config-launch-bounded-receipt-20261014.json`（SHA256 `e84c74c0dba0cd9195dd7c8b33a7f0716e16353652fcad22aceae95884c819b0`）记录12份原失败/通过日志、78份记录时相关源SHA及冻结MAIN字节比较。它是记录时清单，不是后续仍在编辑的不可变候选。
