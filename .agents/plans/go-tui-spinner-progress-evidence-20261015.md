# Spinner 流事件与计时引用的有界证据

本批完成 native interactive root 的专用流事件观察和终端计时引用，尚未完成完整 Spinner、TeammateSpinnerTree 的画面迁移。不能把本批通过当作最终候选、全产品一致性或质量门禁通过。

`sdkhost.Host` 在既有 RequestStart 过滤之前，仅调用实际 native interactive Print 的 typed observer。通用 OnQueryYield 的 RequestStart 过滤保持不变；观察结果没有进入 SDK、模型请求、转录或持久化 JSON。MainPrint 使用已有同 Owner/真实 Native settings binding 检查，Session 通过现有有序事件通道发布不可变数据；State 只消费当前 turn 或已有 background 通道的数据。

模式来自原 `handleMessageFromStream`；text、thinking、input JSON 的响应增量按原 JavaScript UTF-16 length 计算，signature 不计入。计时引用独立于 mounted Spinner，当前可表示的原 REPL focus 条件包括工具批准、输入抑制、message selector、AskUserQuestion 和普通 local JSX。下一模型轮的 RequestStart 不清响应长度；真正完成与下一查询分别沿原重置边界处理。

## 实际命令与结果

所有 Go 命令实际使用 `-p=1 -race -count=1 -timeout=90s -v`，默认 vet 保留；测试内部 15 秒请求窗口与 3 秒取消/join 窗口未增加。详细 selector、原始日志和 SHA 在 `artifacts/go-migration-20261013/tui-task-list-source-evidence-20261014/spinner-typed-progress-bounded-summary-v6.json`。

- oldHost v1：ENOSPC 链接失败，未执行业务；保留 build-only 证据。
- oldHost v2：构建期间 Root 新增 nativemodule import，输入不稳定导致 vet importcfg 失败；未执行业务，未关闭 vet。
- oldHost v3：唯一当前逻辑路径 overlay、实际 2 次模型 HTTP 和 TaskUpdate 成功，但 RequestStart 为 0、响应增量为 0/60；真实 RED。Host 物理文件前后 SHA 一致。
- current v4：TUI race 44.828 秒通过，包含原 stream callback 16 profiles、原完整 REPL timing/focus 5 profiles/34 refs、旧 TaskList 47 profiles/223 frames 和晚事件/重置检查。其余失败是新增取消测试误假设 RunTurn 必须返回 error，以及新增事件收据未对并发 inbox callback 加锁；原失败保留。
- current v5：bootstrap 3 tops race 4.149 秒、application 1 top race 3.057 秒通过。验证真实 2 HTTP 的 requesting 2、UTF-16 60 units、通用观察器不增加 RequestStart、SDK 不增加内部事件；实际 Session 3 HTTP 的同 Harness 下一 Human 与 closed sink；foreign/nil/retired/finite/closed 拒绝；真实 held provider 被取消并在原 3 秒窗口内结束。

依赖缓存仅统一私有 GOMODCACHE 路径。两个任务私有缓存的原锁定 55 模块、14,669 source files 均按既有 manifest 比较，零缺失/差异；原 GOFLAGS/off/local/临时目录/编译缓存不变，源种子未删除。单 Host overlay 没有 clone 路径问题。

## 仍开放

完整 Spinner cells/styles/位置、共享动画 clock、stall/glimmer/glyph、thinking 的 mode cleanup 与 remount timer、TeammateSpinnerTree 的完整画面和实际进度、compact/stop-hook override、公开 PTY 的 header/批准暂停/下一 Human/termios 路径尚未闭合。原组件已有轻量完整挂载观察，但本批没有声称它与 Go 完整画面相等。

原普通 build profile 的 KAIROS/KAIROS_BRIEF/TOKEN_BUDGET 为 false 只适用于已观察的锁定 Source profile；不能推广到其它 build、账号或 gate。foreground child 与 remote 专用传播也不从本批 native root 证据外推。
