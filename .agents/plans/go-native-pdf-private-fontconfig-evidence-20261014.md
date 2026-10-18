# 私有 Poppler 字体配置与原 PDF 入口窗口

处理 checkpoint 11 `TestActualNativeMCPPDFReadBareAndOrdinaryFactories/bare=true` 的原 20s context 失败。没有修改 PDF、Owner 或工具超时生产逻辑；保留原未嵌入 Helvetica 的有效三页 PDF、两次 Read、data-only MCP mapper、同 cache 的空 content/offset 1 断言。

## 原失败与实际耗时

Root 原窗口重播：bare 27.74s failed，ordinary 18.16s passed，两次 Read 已返回物理结果，之后同 context 的 cache Owner.Run 报 deadline。原 log `checkpoint11-mcp-pdf-original-window-replay-v1.log` 保留。

测试仅临时加入分段单调时钟及私有进程 observer：observer 直通原真实 Poppler argv/stdout/stderr/exit、继承相同 process group，没有预跑命令/codec。原 20s 开始位置和断言未改。原依赖一轮 bare full 0.956s/page 10.060s，ordinary 0.458s/7.339s；cache Owner admission 23/44µs。真实 `pdftoppm` 分别占 9.616/6.910s，主要时间不在 Owner。

独立新 HOME 下同 Poppler/原 PDF 实物：`pdfinfo` 确认 3 pages、612×792；`pdftoppm` 17.906s，stderr 为 `Fontconfig error: Cannot load default config file: File not found`。诊断对照的有效 embedded Type3 PDF 0.042s、无 stderr。Type3 仅用于识别字体依赖，未替换原验收 PDF。

私有依赖 launcher 只设置 DYLD fallback，Fontconfig 编译默认仍指向旧 build prefix，因此默认配置未加载。`fonts.conf` 本身存在于私有 bundle。

## 最邻近依赖修复

两个私有 launcher 从自身位置解析：

- `FONTCONFIG_PATH=$POPPLER_ROOT/etc/fonts`
- `FONTCONFIG_FILE=$FONTCONFIG_PATH/fonts.private.conf`
- `XDG_CACHE_HOME=$HOME/.cache`

新增 `fonts.private.conf` 保留原配置全部 dir/match/include/config；只将旧 build-machine/legacy cachedir 替换为 xdg `fontconfig`。XML 非 cache 树逐项比对相同。没有执行 `fc-cache`、字体枚举或预热；每个 profile 新 HOME，调用前 cache 不存在，实际 Poppler 随后生成仅该 HOME 下的 cache。

Root 的 snapshot 12 runtime scaffold 已是旧副本，须刷新 2 个 launcher 和 `poppler/etc/fonts/fonts.private.conf`，保持相对 layout。没有 RootART/tmpbuild 绝对引用；不得复制验收 HOME 的 cache。原 Fontconfig、conf.d、Mach-O binaries/libs 不改。

## 当前证据

- 同原 20s 的带进程诊断两 profile passed，bootstrap 8.798s；进一步逐文件 private cache SHA 一轮 passed 10.863s。Profiler 已移除，原测试调用和路径已恢复。
- 无 profiler 原 `TestActualNativeMCPPDFReadBareAndOrdinaryFactories`，`-p=1 -race -count=3 -json` passed：3 top/9 pass events、0 skip/0 fail，package 15.945s。六个独立 fixture HOME 的 bare 2.70/2.00/1.99s、ordinary 2.16/2.09/2.19s；原 20s context、Helvetica、两次 Read、MCP data-only 与 cache 断言均未变。
- 原 `TestActualMainPrintPDFReadNativeFilesPagesMessagesAndWriteGuard`，`-p=1 -race -count=1 -json` passed：1 top/6 pass events、0 skip/0 fail，package 9.422s。五种原场景实际完整 Read → 后续真实 HTTP → 真实 Write 或禁止 Write，原文档/page/error 消费者全部通过。
- 原 `readPDF`/count/extract 工具函数体以及完整 `FileReadTool.call`/`callInner`，使用真实私有 FS、原实际 Poppler、原 `imageResizer`/Sharp：bare 两次 Read 2.943s，ordinary 1.950s。full `pdf` document 1、page `parts` image 1，full cache 的空 content/offset 1 在第二次 Read 后保留，均无 throw。每个 HOME 起始 cache absent，实际产生 178 个 private cache 文件并记录 SHA。
- 最后限定实际 bin realpath 的 guard 编辑后，两项独立新 HOME 复播 bare 2.788s、ordinary 1.926s，仍各 178 个私有 cache 文件、stderr 0、相同完整 Read/cache 断言。所用七份 Source 业务/codec 文件与冻结 `6d21316ec4635f29ecbaa43e1a5574bfd31d7155` bytes 全相同。首个重播启动遗漏 CWD/BUN_OPTIONS/allocationRoot，被原 disposable-home guard 在业务执行前拒绝；raw 保留为 invalid fixture，修正启动边界后才计上述通过。
- Source fixture 仍把 app-state/model/telemetry 原语作为输入，不是完整公开 MCP server；公开 MCP factory 的 Go 原测试单独验收，不互换证据。
- 新 physical 分支之后，原 controlled `TestActualSourcePDF*` 五个 Source 顶层测试在 `-p=1 -race -count=1 -json` 下通过，96 pass events、0 skip/0 fail，pdfread 1.973s；原页范围、工具异常、cache/补充 block、session promotion 与普通 tool abort 合同未回归。
- 第一次字体准备证据目录随后缺失，原因未建立；Root 确认未移动/删除。第一次 Source raw 未存成功且不可恢复，未计 passed。当前 raw 独立重播，原 launcher 用现值去除新增 exports 重建，其 SHA 与此前记录完全一致；该区别写入收据。

收据/raw：`artifacts/go-migration-20261013/pdf-fontconfig-relocation-evidence-20261014/receipt.json`。Fontconfig 原 SHA `2fc368d496384829bbd29f67c13682a6e1b2bdc0a207e661c9bdddb39a9b1a42`；private SHA `59733bfb6629ef61a9a5c2fb97b77c315c38a84e2b5e3ac4565e68e0ddd22b16`。

本证据限定实际 macOS 私有依赖和原消费者。没有真实 Claude OAuth/模型、真实用户 HOME 或 OS 设置写入，不替代完整门禁/90%/120 benchmark，也不声称 Windows 实机通过。

本批原 PDF 20s 失败已经在上述未插桩消费者中复验通过。snapshot 12 必须携带三份修复后字体依赖文件，再由 Root 的不可变完整门禁复核；checkpoint 11 原失败与缺失证据状态仍保留。
