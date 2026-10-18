# 动态配置目录：MCP 认证失败 cache 与 refresh lease

本批只闭合实际 native producer 下的 MCP 认证失败 cache 和 refresh lock 路径采样。普通 Main/RunTurn 的 `CLAUDE_CONFIG_DIR` frozen host guard 仍拒绝运行时变更；这属于当前 Go 未完成的能力边界，不是 Source 产品限制。本批不声称普通动态配置目录入口完成。

## 原 Source 完整消费者

`native-config-dir-mcp-auth-cache.fixture.ts` 导入冻结的完整 `services/mcp/client.ts` / `auth.ts` 原模块。仅追加私有函数观察出口，未重写业务 body；文件/Date/不可用 Keychain 是明确私有原语，所有 filesystem 操作实际执行。七个 profile 使用 Bun 1.3.14、临时 HOME，均 `blocked=[]`、`secureReads=[]`、stderr 0；原业务四份文件 SHA 与 `6d21316ec4635f29ecbaa43e1a5574bfd31d7155` 字节相同。

- `getMcpAuthCache` 在创建共享读 Promise 时调用路径 getter；warm Promise 保留旧数据。原 `getMcpToolsCommandsAndResources` 等待 writeChain 后清 read Promise，下一冷读才取当前目录。原完整 discovery reset caller 以空 configs 执行，未声称真实 MCP 网络连接。
- `setMcpAuthCacheEntry` 和 `clearMcpAuthCache` 都在入队前采路径。旧实际读完成阶段被 held 后，next 入队操作随后即使环境切 third，仍读/写/删除 captured next；旧 first 仍写 old，third 未被错误修改。Mark/target Clear/all Clear 的完整未知字段和三份文件逐项比较。
- `ClaudeAuthProvider.refreshAuthorization` 在第一处异步 mkdir 前采一次 claudeDir。创建 provider 后切 next，实际 lease 在 next；held 真实 mkdir 完成阶段后再切 third，lease/释放仍在 next。实际 proper-lockfile 目录存在且最后移除。原未来假 MCP token 返回完整 token 结果，无 provider HTTP/真实 Claude OAuth 刷新。

## Go 真 RED 与最近修复

新回归首先使用旧 API 跑实际 `newNativeMCPAuthenticationCache` 构造器、MainPrint 的 canonical environment、同一 cache 对象与真实 FIFO 文件描述符。五个 profile 均真 RED：新 batch 仍读 old；排队 Mark/Clear 错写/删 old，Source 是 captured next。原配置 guard 负例保持正确。日志 `original-production-red.jsonl`。

refresh 回归在声明新 native callback/真实 mkdir 原语后、消费点仍保留原 fixed ConfigDir 时跑完整 `RefreshAuthorization`，两 profile 真 RED：实际 mkdir 和物理 lease 都在 old，Source 在 next；假 token、无 HTTP、原 guard、lease 释放均通过。该 RED 与全旧代码快照区别明确，日志 `refresh-fixed-directory-consumer-red.jsonl`。

最近修复：

- `AuthenticationCacheOptions.NativePath` 仅真实 native 构造器绑定。读 Promise 只在首次创建时采路径；warm Promise 不失效。Mark/Clear 在 enqueue 前采一次，链内所有 Read/Mkdir/Write/Remove 复用该值。原 discovery invalidate 时点、串行链、TTL/JSON 合同保持。
- `mcpauth.Options.NativeConfigDirectory` 仅实际 NativeProcess 构造器绑定；finite profile 保留静态 ConfigDir。refresh 在 mkdir 前只采一次，所有 retry/heartbeat/release 保留目标。`MkdirAll` 原语缺省仍真实 `os.MkdirAll`；回归 wrapper 执行真实 mkdir 后只 hold 完成，不伪造文件系统结果。
- native auth 构造器的原 Options 提取为 `nativeMCPAuthOptions`，生产仍只构造原服务一次；回归复用同实际 Options/共享 private Secure/environment。没有重建既有服务、清所有 cache 或开放普通 guard。

## 当前复验

两个顶层回归、七个 profile，`-p=1 -race -count=3 -json`：27 pass events、6 top executions（两独立 tops 各三轮），0 skip/0 fail，bootstrap 27.798s。原 5/10/20s consumer/Source 窗口保持。旧缓存、caller wait 取消后仍完成原排队 Clear、当前 cold read、实际 native lease 目标/释放、无 provider HTTP、原 host guard 均通过。

原 AuthCache 实际写链/target clear/new batch、refresh stale lease/替代者归属/取消、20 个并发实际 token 请求共用真实 refresh、原 Source backoff TTL/discovery/real probes 和 external clear/explicit reconnect/凭据 supersession 五个顶层回归：`-p=1 -race -count=1 -json` 全通过，19 pass events、0 skip/0 fail；mcpruntime 1.568s、mcpauth 1.567s、bootstrap 16.173s。未声明 Native callback 的有限 profile 沿原 fixed Path/ConfigDir；没有扩大其能力。

收据和原日志：`artifacts/go-migration-20261013/config-mcp-auth-current-directory-evidence-20261014/receipt.json`。Source 原 profiles 保存在 `config-mcp-auth-directory-source-original-v1`；最终 matched held-read profiles 完整 stdout/input/summary 在 `config-mcp-auth-directory-source-final-v2`。

其它 plugins/skills/MCP 配置 memo/普通目录入口及不可变整体质量门禁、90% 和 120 benchmark 仍另行验收，不从本批数字外推。
