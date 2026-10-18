# API init HEAD 消费者证据

2026-10-14，当前迁移分支；冻结 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`。本批只证明普通 `RunCommand` 的 detached HEAD producer 和生命周期，迁移未完成。

## 原版与实际入口

冻结 `src/utils/apiPreconnect.ts` 的完整原模块与当前保留参考字节一致，SHA256 `a58850e9dbc0b4b02d47a4a2e6c7adc198339b4bf63d57111b5a0328b0424850`。`src/entrypoints/init.ts` 在 settings env、CA、mTLS/agents 设置之后调用它。原模块先消耗 once 标记，再判断四个云 provider、大小写 proxy、unix socket、client cert/key；未跳过则向实际 base URL 发无鉴权 HEAD，调用时创建 10000ms abort signal，吞并 detached fetch rejection。`CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` 本身不跳过它。

新增实际原模块 fixture 的 33 个 profile 比较目标地址、JS truthy/raw-string gate、一次调用、HEAD、signal、10000ms、void 返回和拒绝处理；没有改原模块或原 golden。Go 的同一 NativeMainPrint 在真实 `RunCommand` 普通及 structured stdin 路径捕获 settings 环境，用实际 TLS client 发 HEAD。其独立 lifetime 不受普通 ASK 取消影响，全局关闭取消并加入物理 worker。

原 public RunCommand 测试先 RED（HEAD=0，model POST=1）；实现后真实 loopback 保持 HEAD 未完成，下一共享 Harness 的模型 POST 仍到达。另一个实际 Session 测试在 HEAD 与模型请求均 held 时关闭，观察两个真实服务端 socket 取消和 Submit/Close 完成。settings.json 的 env 覆盖 launch base 后，实际 HEAD 与 POST 都到配置后的 peer。

## 聚焦验证

- `preconnect-final-focused-v6.log`：两包 `-race` 通过，clibootstrap 20.727s、application 20.040s。包括 33 Source profiles、实际 once/10s/错误/关闭、公开 HEAD/POST、held Session close、settings env、原两个 keybindings PTY、MCP prompt、print/JSON/SDK/stream fault、媒体入口。原 individual deadline、请求正文、model 次数和 termios 断言保留。
- `preconnect-existing-public-red-v4.log`：保存原 MCP prompt 与 keybindings 失败，原因是新增 HEAD 被旧私有 handler 当作模型 POST（2 次）。对应模型 handler 只增加 HEAD→204 的独立分支，先于原 counter/body 消费；没有削弱模型断言。首批 74 文件/84 handler 的原文件与插入后 SHA 在 `preconnect-existing-handler-edits-v5.json`；另四个 wrapper fixture 同样只加此分支。
- `preconnect-private-redirect-guard-v7.log`：`-race` 通过 2.257s。隔离 primitive 仅准许明确 127.0.0.1/端口/http(s)/无 userinfo 的 HEAD，逐跳拒绝外部重定向；错误方法、外部 origin、localhost、file URL、userinfo 在实际传输前拒绝。release 的 nil primitive 使用实际 HTTP；没有把私有 guard 加到产品业务条件。
- `preconnect-final-vet-v8.log`：application、clibootstrap、cmd/claude-haha 的 vet exit0；空日志是实际成功输出。diff-check 通过。

完整 ignored 收据：`artifacts/go-migration-20261013/cli-flags/api-preconnect-bounded-receipt-v9.json`，包含原 RED、当前结果、生产/测试/参考 SHA 和命令。早期 SIMPLE fixture 启用插件矛盾的 constructor 失败独立保留，仅修私有 disabled-plugin 输入。

## 尚未通过

这不是模型连接池复用证据：当前模型 HTTP1 transport 仍打开新连接，HEAD client 本批关闭 idle connection；不能宣称预连接性能目标通过。其它非模型 CLI subcommand 的 init 调用点也未由本批验收。叶级 NewSession 构造不执行普通 CLI init 的该 detached effect。

严格 cold SDK 标题五对仍 RED（Source `now original next original`，Go `later original`），原 raw/golden 未改。完整不变 90% gate、verify、同候选桌面/Web/CLI 验收和正式 120 对 Benchmark 均未完成；未进行真实 Claude OAuth 登录/刷新，也未合入 main。
