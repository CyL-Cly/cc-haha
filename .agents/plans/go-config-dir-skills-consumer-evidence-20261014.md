# Skills 当前目录与缓存消费者证据

2026-10-14。本批仅完成实际 `NativeProcessSettings.SkillCommands` 工厂、`skillregistry.UserRoots`、`skillcommands.Local` 的目录采样、完整命令元数据和 watch-list 投影。普通 Main 的动态 `CLAUDE_CONFIG_DIR` / `HOME` admission 仍有 guard，不能把下面的 lower-consumer 证据写成产品整项或最终迁移通过。没有访问 Claude 真实 OAuth、真实 keychain 或模型。

## 原版行为与修复

冻结 MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155` 的 `envUtils.ts`、`skillRoots.ts`、`loadSkillsDir.ts`、`markdownConfigLoader.ts`、`skillChangeDetector.ts`、settings/cache 共七个业务文件与当前文件逐字节相同。使用固定 Bun 1.3.14，加载原业务函数，在独立 HOME 写真实 Markdown/settings 文件；只隔离外部能力并暂停真实 filesystem completion，不替换目录、缓存或命令业务。最终八个 Source profile 的原始 JSON/stderr 已保存，blocked/secureReads 均为零。

- 目录切换后，直接 roots/watch 查询读取当前路径；同 cwd 已暖的 skill 命令缓存继续返回旧定义。显式清 skill/legacy 缓存后再从当前目录加载。Go 原 factory/legacy/watch 固定在启动路径；v2 真 RED 保存，最近修复用 native 当前环境 callback，不重建服务或全局清缓存。
- 空、相对、父目录相对路径在原版元数据/watch 投影中保持相应路径表示；NFC 归一化后，真实 OS 读取相对于实际 native CWD。v4 的三个路径 profile 真 RED 保存，使用独立文件系统 adapter 完成修复；watcher 在实际启动边界解析物理路径。
- `.agents/skills` 的 user root 跟随当前非空 HOME（Windows 对应 USERPROFILE）；project traversal 仍使用原进程 home。v11 HOME profile 真 RED 保存，再绑定该 user root 的原 getter 行为。
- 暖的 `disableAgentSkillsDirectory` 设置在目录切换、清 skill 缓存后继续有效。v13 真 RED 保存；root settings getter 读取同一既有 session settings cache，普通 startup guard 仍负面。
- 原版在独立 filesystem loads 完成前已采样 user roots 和 legacy directory。真实 readdir 完成被暂停、期间切换目录，原版本次仍返回 old skill + old legacy；旧 Go 返回 old skill + next legacy（v7 真 RED）。在 `loadLocal` 开始采样两者后修复，后续暖缓存仍保持该次结果。

代码涉及 `native_skills.go`、`native_config_home_directory.go`、新增 `native_skill_directory_files.go`、`native_skill_watcher.go` 与 skillregistry/skillcommands 的 roots、legacy、watch。回归新增 `native_config_dir_skills_source_test.go` 和 whole-Source fixture。没有改变原版业务、90% 阈值、individual deadline 或 benchmark 协议。

## 实际运行结果

所有命令使用记录的私有 HOME/TMP、Go 1.25.7/offline module cache、固定 Source Bun、假凭据或无模型入口。

v14 的准确 selector：

```sh
go test ./internal/clibootstrap -run '^TestNative(CurrentSkillDirectoryPreservesWarmCacheAgainstWholeSource|SkillsCaptureRootsBeforeHeldFilesystemCompletionAgainstWholeSource)$' -race -count=3 -json
```

| 运行 | 结果 |
| --- | --- |
| 两项动态目录/held-FS 新回归（v14） | passed；2 个独立 top、每个重复 3 次，27 test pass events，0 fail/skip，包 14.440s |
| `go test ./internal/skillcommands ./internal/skillregistry -race -count=1 -json`（v15） | passed；22 tops / 86 pass events，0 fail/skip；10.729s / 3.256s |
| 原 discovery/conditional、实际文件动态 skills、物理 watcher、附件/转录/legacy resume、slash/no-query、下一普通 human 请求及 raw-launch 六 profile（v16，准确名字见 JSONL） | passed；8 tops / 14 pass events，0 fail/skip，11.513s |
| 三包 `go vet`（v17） | passed，exit 0；静态检查，不代替执行验收 |
| whole Source 手动八 profile | passed；七目录/cache profile + 一个 held-real-FS profile；0 blocked/secureReads |
| `git diff --check` | passed |

最新 `bun run check:impact` 使用准确 `PR_BASE_REF` 指向 frozen MAIN：8434 个相对基线文件、14 项检查、blocked no。它只证明路由，不证明这些检查通过。v18 的 `PR_BASE` 并非脚本读取的变量，其默认比较 1648 个文件结果单独保存；v19 校正了基线变量。

v1/v2 Source setup guard 失败、v1 Go 的错误独立 environment guard 断言、v5 文件名误撞造成的 build failure 均保留，不能冒充业务 RED。原 `native_skill_files.go` 已按不可变 checkpoint-11 文件恢复（与 HEAD 相同，SHA `f0f1bffb3b5880f0ea0990522614448d9cf0d58525d16afe4095674201fe351d`），新增 adapter 使用独立文件。v2 Go 是校正实际 `p.environment` 后的消费者真 RED；v4/v7/v11/v13 是其余真实差异。

ignored 收据 `artifacts/go-migration-20261013/native-config-dir-skills-bounded-receipt-20261014.json` 保存十个代码/fixture 文件的当前 SHA、原始失败/成功日志及 Source authority。收据不冻结整个 repo，其余并行实现发生后不能当最终同版本证据。

## 仍未闭合

普通设置 env 导致目录变更的完整启动/turn admission、冷 merged settings、plugins/bundled/model resolver、已启动 watcher 的跨进程行为、Desktop roots/market detail 对应 Server getter 均不从本批外推。当前既有 watcher capture lifetime 保留。

完整 check:go/90%/verify、最终同版本 CLI/桌面/Web、真实外部平台（Claude OAuth 按用户要求排除）、正式 120 对 Benchmark 未由本批执行或证明；迁移仍未完成，没有合入 main。
