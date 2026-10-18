# `/clear` 完整消息渲染与公开 PTY 复核

本批修复 checkpoint 11 中公开 ClearAliases 的旧测试期待，不改 clear 生产业务、canonical 记录、超时或模型断言。

原 `native-terminal-clear.fixture.ts` 已调用实际 `processSlashCommand`、clear local implementation 和 `normalizeMessages`。新增 optional `renderCaller` 将其真实三个记录直接传给原 `Message` React/Ink 组件，读取实际 front screen。原消息和 API normalization 保持不变；不以静态文案替代消息组件。

冻结 Source `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`，Bun 1.3.14，实际 132 columns，与公开 PTY 宽度相同：

- `clear` / `reset` / `new` 无参三个 profile 都显示 `❯ /clear`，随后 `⎿ (no content)`；不会把 `<command-name>` 或 `<local-command-stdout>` 标签直接画到终端。
- 同三个别名带 `kept command args` 都显示 canonical `/clear kept command args` 和空 stdout 的 `(no content)`。
- 两组所有 profile exit 0、blocked 0；原 Source 假 legacy primitive 调用仅为 `owned-empty-legacy`，没有实际 Keychain、Claude OAuth 或外部访问。
- 保存/重播的三个 canonical records 仍有原始标签；显示转换不改变记录，也不删除下一 ASK 的原 local tags/stdout。

旧公开 driver 在当前 VT screen 要求原始标签，因此 checkpoint 11 的失败是旧显示期待。修为原 Message 的两个实际显示项，继续检查 logo 存在、旧 human/assistant 消失；原 32s driver / 40s Go context 未改。

实际执行：

```text
go test -p=1 ./internal/application \
  -run '^(TestActualSourceClearFullSlashCallerCanonicalLocalReplay|TestActualPublicCLIClearAliasesNextHumanAndRestoredPTY)$' \
  -race -count=1 -json
```

通过：2 top / 5 pass events、0 fail / skip；application 25.159s。公开 `RunCommand` PTY 部分 9.93s，保留实际四次 loopback 模型 POST、四个不同当前 SID、同 Harness 下一输入、旧父 transcript 不变和 Go/真实 slave termios 恢复断言。

Raw Source：`artifacts/go-migration-20261013/clear-full-message-source-current-v1`、`clear-full-message-source-args-v1`。聚焦 raw：`test-1791972868366.log`，收据及副本在 `artifacts/go-migration-20261013/clear-full-message-pty-current-evidence-20261014`。

这份证据不替代完整 Go 门禁或同版本桌面/Web/CLI、120 benchmark；它也不扩大其余 clear 生命周期尚未验收的 profile。
