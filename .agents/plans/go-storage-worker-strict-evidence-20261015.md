# 桌面存储 worker 的严格环境复验

本项通过的是冻结13加当前 `storageRelocationWorker.test.ts` 环境修复的有界候选。不是当前全部生产改动的最终候选，也不代替完整 desktop/native/persistence 或最终 verify。

原全量 verify 中 desktop worker 的 beforeAll 因 Go 隔离环境遗漏 repositoryRoot 失败。当前测试在独立 Bun 子进程中调用相同 `createHermeticGoEnvironment(buildHome, process.env, false, repositoryRoot)`，继续校验原55模块离线种子。Node/Electron 编译不直接导入 Bun 专用工具。生产 guard、原 dependency/build 120秒、beforeAll 130秒和业务20秒期限均不变。

Root独占窗口实际执行：

```text
<owned Bun 1.3.14> run test -- --run electron/services/storageRelocationWorker.test.ts
```

Vitest 3.2.4 实际结果：1 file /3 tests passed，0 failed /0 skipped；Vitest29.88秒，包含输入复核和依赖post复核的runner48.326秒，terminal exit0。

三个消费者均实际完成：

- DataMigration正常迁移调用真实 Go worker，quiesce后重写connector管理路径、保留未知字段、重新生成skill、保护原数据，再发布profile指针。
- 不支持的真实connector schema使Go worker失败；DataMigration恢复原指针、不发布stage，并保留原settings字节。
- 真实Node child被取消并join后才reject；歧义TypeScript backend被原边界拒绝。

候选13,013源码输入before/copy/post相同。五份Node/Go seed/runtime依赖副本保持原类型、模式和SHA；运行后的生产与runtime文件SHA仍相同。原Vitest读取并重写的唯一生成结果缓存 `desktop/node_modules/.vite/vitest/da39a3ee5e6b4b0d3255bfef95601890afd80709/results.json` 事先声明为输出，实际变化与完整内容单独保存；不将它算成生产依赖变化或测试豁免。

ignored证据：`artifacts/go-migration-20261013/root-strict-storage-worker-20261015/` 下 `preparation.json`、`strict-storage-worker-v1.log`、`strict-storage-worker-v1-result.json`、`vitest-generated-results-output.json` 与五份依赖清单。result SHA256 `402d992395bc37751d19066c3ef3c625659a8e59a6af4dd17c327215a6c6c1c7`，log SHA256 `0e6916b8531584577fc64113a25446845102e236d7c2625e5a14cd5a71fe655a`。

本轮无真实用户配置、凭据、OAuth、模型或外部平台调用。最终同版本三端、全量90%/verify与正式120对仍待完成。
