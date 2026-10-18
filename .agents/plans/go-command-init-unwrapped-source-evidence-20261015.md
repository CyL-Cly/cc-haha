# 普通插件命令的原生沙箱 HEAD 对照

独立运行冻结 MAIN 的实际 `bin/claude-haha → src/entrypoints/cli.tsx`，参数 `plugin marketplace list --json`，新临时 HOME/config/workspace、loopback peer 和 deny-default Seatbelt。没有包装 fetch、替换普通文件 Promise 或注册退出诊断 listener。仅声明明确不存在的外部路径和空的安全存储、Keychain/MDM 外部原语，与原有命令 fixture 的隔离边界一致。

五个原版进程全部退出 0、stderr 为空、stdout 为 `[]`、私有设置字节不变，退出前真实 HEAD 入站均为 1。3148 个实际 Source 文件的内容 SHA、Git blob 与冻结 MAIN tree 逐个相同。首版缺外部 prefetch 原语及精确只读 resolver 配置能力，未到业务，作为无效准备保留；有效第二版没有扩大写入、Keychain 或网络权限。

同一沙箱实测六项外部假文件/符号链接逃逸/假 Keychain 文件/`security` 执行均 EPERM，临时文件读写及 loopback HEAD 成功。Bun 将禁止的非 loopback connect 映射成 ECONNREFUSED，该结果原样保存；独立私有 native C connect 在相同网络规则下返回 EPERM。

复用 CLI 组刚构建的当前 noncover `native_process_fixture` 二进制，SHA256 `13eb557deca934a523504e5b3e4c59a8e96f8bcb1735cbc2f8c9764a88a89236`，没有另构建或修改生产逻辑。五个实际 Go 命令同样退出 0、stderr 为空、stdout 为 `[]`、私有设置不变，但真实 HEAD 全部为 0。因此退出诊断的同步读写不是原 Source 1 / Go 0 差异的充分解释，原严格断言保持，HEAD parity 仍 OPEN。

静态调用链另确认具体未移植的 producer：冻结 `src/main.tsx:4268` 的实际 action 先 await `import('./cli/handlers/plugins.js')` 再调用 `marketplaceListHandler`，当前 Go 直接调用 `nativeMarketplaceListOnOwner`。本批尚未以完整 Source import lifecycle 证明其对物理 HEAD 的因果作用，没有用数字 tick、假文件读取、延时或等待 HEAD 完成补结果。

完整十个实际进程、profiles、边界探针、Source 身份审计与结果在 ignored `native-command-init-unwrapped-bounded-receipt-20261015.json`，SHA256 `4f8e1fe2106d31a472d7a505fec90b010fccc0b97f751c9a76678121687fe051`。这是新的真实入口诊断，不能代替最终门禁、三端、live 模型或 120 对验收。未合入 main，未执行真实 Claude OAuth。
