# checkpoint13 依赖准备

仅复制完整检查所需依赖与运行时，尚未捕获生产源码，也未运行新全量 Go 检查。APFS 实体副本来自未修改的 checkpoint12：根/desktop/adapters 三棵 node_modules、Go 依赖种子、私有运行时。五棵树共 92,028 个条目的普通文件 SHA、模式、类型和链接目标在复制前、副本、复制后逐项相同；耗时 59.493 秒。

独立新临时 HOME/config/cache 下的 preflight 九项全部通过：三棵实体依赖树及内部链接、运行时哈希、Bun 1.3.14/tmux 3.5a/PowerShell 7.6.6/Poppler 26.05 真实版本命令、实际模块解析与 sharp native 加载、当前 Go/fork lock 与 55 模块种子匹配、实际 hermetic environment 保留明确的 MCPB SDK、空私有缓存且没有真实凭据或公共代理。Go 种子必须指向带 lock key 的子目录；正式 hydration 仍须逐文件验证并写入新空私有缓存。

ignored 目录 `artifacts/go-migration-20261013/checkpoint-13-preparation/repository` 目前只有上述依赖，没有 `.git` 或源码快照。完整收据 `checkpoint13-dependency-preflight/receipt.json`（SHA256 `149fb7d813711cf4b57daf2367b5788ba558930b1d2c867aec1231f4567064f3`）仅证明准备结果，不是 check:go、90% 或 verify 的通过证据。

下一步在当前修复稳定后捕获全部 tracked/untracked 输入，保存 before/copy/after 身份与冻结 MAIN/BASE；用全新 compiler inventory/default-tag 分母和 child counter session 跑原门禁。当前严格 cold title、MainCommand HEAD、Spinner/workflow 消费者和最终同版本三端/120 对尚未完成；Claude 真实 OAuth 已排除，未合 main。

文档安装步骤另准备私有 npm 缓存：独立空 project 只复制 site 两个锁定 manifest，联网安装仅用于准备依赖（3.403 秒），随后另一个全新空 project 用实际 `npm ci --offline` 和默认依赖安装脚本通过（217 packages，2.803 秒）。两次 package.json/package-lock.json 前后 SHA 相同，npm 用户/全局配置均为新临时 HOME 中的空文件，没有访问个人 npm 配置。收据位于 ignored `site-npm-dependency-preparation-20261015/receipt.json`；`checkpoint-13-offline-private-env.json` 仅补充私有 npm 配置与 offline 开关，不修改上述九项预检的原环境收据。尚未运行 site build/check 或完整 `check:docs`，安装不是质量门禁通过。
