# 当前文档离线门禁

在独立私有目录执行实际 `bun run check:docs`，使用新临时 HOME、空 npm 用户/全局配置和已验证的私有缓存，`NPM_CONFIG_OFFLINE=true`。原命令中的 npm ci、site build、typecheck、文档校验与 Node tests 全部通过：217 packages，108 文档页，31 tests/0 fail/0 skip，总耗时 8.372 秒。没有修改原门禁命令、阈值或依赖锁。

用于本项的 401 个当前 site/docs/README/root manifest 输入在 Root 复制前、副本、Root 复制后及门禁结束后类型/模式/内容身份一致。副本以未改的 checkpoint12 其余文件作为只读基础，旧 site/docs 输入残留为零，生成产物仅写副本。没有把该混合基础目录称作最终同版本 Go/React/native 候选；完整 check:go/90%/verify、最终三端与 120 对仍须另行完成。

完整命令、日志、401 输入清单和前后审计保存在 ignored `artifacts/go-migration-20261013/docs-current-offline-v1/receipt.json`，SHA256 `cc3b892f827e374ede61060e7a34fd7a6a5392667c7dc64690cab47049c0ecc9`。现有 Root 用户改动保留，源码目录与原 checkpoint12 没有被构建步骤修改，未提交/推送/合入 main。Claude 真实 OAuth 仍按用户要求排除。
