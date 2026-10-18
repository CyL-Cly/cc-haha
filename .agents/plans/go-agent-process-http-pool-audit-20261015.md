# 普通后台 Agent 的 HTTP 连接池消费者

2026-10-15。冻结 Source MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`。本项仅关闭普通父会话 → 后台 Agent → 父会话这一条物理资源链，不代表冷启动标题、全部门禁或迁移完成。

## 真实入口与失败

使用冻结原版 `bin/claude-haha` 的普通非 bare SDK stdin 入口，临时 HOME/ConfigDir/工作目录、fake API key、私有 loopback 模型服务。Source 进程由实际 Seatbelt 限制写入私有 HOME、网络仅 loopback、Keychain 禁止；只替代明确的外部空 Keychain/MDM 原语与明确不存在路径，没有普通 fs/fetch 的异步 observer。

模型实际返回后台 Agent 工具调用。子 Agent 的第一个 HTTP 响应保持到父拿到真实 Agent ID，父请求随后通过实际 TaskOutput 等待子结果；服务端在父这个响应中发送 `Connection: close`。子 Agent 实际读取私有文件，下一子请求包含真实文件字节；子完成后父下一 HTTP 包含 TaskOutput 的成功和子结果。

Source 明确 `CLAUDE_CODE_SIMPLE=0` 五次均正常退出、stderr 空，HEAD 1 次、父 POST 4 次、子 POST 2 次。每次父恢复请求复用子已完成请求的 TCP 连接。另一次两端变量 absent 的普通默认入口同样通过 Source。Source 的 3,148 个生产文件重新逐个 SHA 对照冻结 Git blob 清单，无差异。

旧 Go 普通默认入口业务同样完成，却在子第二请求使用端口 55076 后，父恢复请求另连 55077。每个独立子 MainPrint 创建自己的物理池并在子 Close 关闭它，导致父无法消费该 idle socket。旧 Go 明确 SIMPLE=0 还会在模型 HTTP 前被 API presentation 的 bare 环境 guard 拒绝；此独立缺口由 Root 修复，原失败保留。

最初 `--bare` 探针会让真实 Source 强制 SIMPLE=1、从而无 Agent，正常入口首次还被未声明的 Keychain 启动原语阻止。这两次保留为无效准备记录，不能作为连接池失败。

## 迁移

父实际 Scope 签发 `providernative.PoolTransfer`，仅保留不可变的物理资源及源 handle；不保存 Scope、请求、凭据或设置。原子 Agent options producer 将票据交给实际子构造。子在自己的现有 Scope 同步绑定独立 handle，没有新增 await、native I/O、timer 或 microtask。

父 Pool/旧 Scope 仍不能供子 Owner 直接调用。票据来源、foreign/expired/nil/unminted、handle 关闭和根资源关闭均受校验。子 Close 撤销其 handle，根 Close 负责实际所有 idle/active 连接；子取消只结束该子请求。sticky keepalive 禁用仍由原实际 client producer 采样，并作用于共同物理资源。原 authority/proxy/TLS key、CA 对象及请求级授权不变。

本项资源范围是一个根 MainPrint 的实际独立子后代；没有据此宣称不相关根 MainPrint 或 Server 多 session 已共享全局池。

## 当前证据

- 普通首次 3 项聚焦通过，实际 package 0.618/1.398 秒，wall 50.557 秒。
- 最终 4 项 `-p=1 -race -count=1` 通过，实际 package 1.681/4.040 秒，wall 86.183 秒；7 个生产/测试文件 pre/post SHA 相同。含真实子取消保持父 idle、根关闭取消 held 子 body 并 join、独立授权、sticky 状态及 Owner 守卫。
- 新普通 CLI 两个公开 profile（SIMPLE absent/0）都 HEAD 1 + POST 6、真实工具及文件成功、父恢复复用子连接（61434/61437）、exit 0/stderr 空。
- 新 CLI build exit 0，完整 6,619 输入 pre/post 相同，binary SHA `c739470e48a0f73e2997a00b3d7536533fe2c82a8a0ab5d141d8c27905cd8b72`。第一次 build 成功但 inventory 发现另组新加的 `_test.go`，已严格拒绝并保留记录，没有将它写成稳定成功。

完整 raw 与 SHA 收据位于 ignored `artifacts/go-migration-20261013/cli-flags/cold-public-source-cli-v1/agent-http-pool-source-v52/agent-process-pool-receipt-v52.json`。当前公开 binary 包含检查点 13 后新增的独立测试文件，不能冒充完整 immutable13 的同一源码清单；对应迁移生产文件的 SHA 另列于各聚焦收据。

原 CA/HEAD/Source 邻近集合和修改后 vet 尚未跑，等待 Root 的资源窗口。严格冷标题五对仍 OPEN，旧 0/5 结果不改；正式 90% 门禁由 Root 在 immutable13 执行，120 对 Benchmark 未跑。没有真实 Claude OAuth、live 模型或真实用户状态验收。
