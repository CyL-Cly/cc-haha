# 冷启动标题：原版 CLI 原生沙箱控制（2026-10-15）

本轮只做独立诊断，没有修改产品代码、旧 Source controls、golden、时限或排序。冷启动标题 P0 仍 OPEN。原版五次实际 CLI 在没有普通 JS 文件/`fetch` 包装的条件下，落盘 `lastPrompt` 全部为 `now original next original`；之前当前 Go v39 的五次实际 CLI 全部为 `later original`。这排除了同步 JS 路径验证开销是充分原因的猜测。

原始收据在 `artifacts/go-migration-20261013/cli-flags/cold-public-source-cli-v1/seatbelt-cold-diagnostic-v42/receipt-v42.json`。3148 个实际 `src/`、`bin/`、preload、package/tsconfig 生产输入的 Git blob 与冻结 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155` 完全相同。原版入口保持 `bin/claude-haha → src/entrypoints/cli.tsx`，同三个原始 SDK stdin 输入、同父历史和同 35/40 秒窗口。每次实际子进程退出 0、stderr 为空，真实 loopback HEAD/POST 共用 TCP；两个原父历史记录和后来两条真实用户记录的 UUID、parent 和文本保留。没有真实 Claude OAuth，也没有 live 模型请求。

外部系统边界由 deny-default Seatbelt 保护。临时 HOME/config/工作目录与 SDK pipe 隔离，只有临时 HOME 可写；系统设备标准 IO 是明确例外。负向探针实测拒绝外部读写、符号链接逃逸、私有假 Keychain 文件和 `/usr/bin/security` 执行（EPERM），正向临时文件读写和 loopback HEAD 成功。Bun 把禁止的非 loopback connect 映射为 ECONNREFUSED，因此没有用此异常名作为拒绝证据；单独私有 native C `connect(192.0.2.1:9)` 在相同网络规则下返回 errno EPERM。追加能力仅为 Source 的精确只读依赖/配置解析，写入、网络和 Keychain 规则未扩大。

原版 preload 只替换明确声明为不存在的外部路径与空的外部安全存储原语。普通文件操作返回原始 native Promise，`fetch` 不包装；没有替换 query、队列、记录、cleanup、clock 或模型正文。为了消除额外 stderr，精确只读 Root tsconfig 已与冻结原版逐字节核对；Root package 的模块解析字段也与冻结原版相同，Go 质量命令等 scripts 差异单列记录。依赖真实路径和系统动态加载器/时区读取能力均列入原始 profiles。

准备阶段的 profile 编译错误、dyld/JSC 启动拒绝、LLDB 仅私有目标的失败/超时、先前带 resolver stderr 的业务运行全部保留，没有把它们当成合格正向证据。正式迁移结论仍需未变的严格冷启动对照、全部质量门禁、同版本桌面/Web/CLI 和 120 对 Benchmark；本诊断不能替代这些要求。

下一只读调查是固定 Bun `1.3.14+0d9b296af` 的原生 warmed ESM import Promise 构造、adoption 和 settlement 链。已有 actual import identity/probe 显示 warm import 原 Promise 在八个竞争 microtask probe 后结算，但不允许把数字八变成 Go 的延迟或 tick shim。必须定位可移植的实际语义操作，冷模块物理 I/O 与纯 Promise reaction 分别列证。
