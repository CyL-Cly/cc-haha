# Go Telegram runtime: Source and physical evidence (2026-10-08)

Scope: the existing Telegram adapter, desktop adapter configuration API, native launcher and CLI entry. Original TypeScript production files are read-only. Other platform runtimes and account-specific REST routes remain explicit capability errors.

## Production paths

- `backend/internal/adapterconfig/service.go`, `schema.go`: actual existing adapters.json shape, all platform patch fields, secret masking, shallow platform/pairing merge, unknown-field preservation, 0600 atomic writes. Original patch mutation on masked-secret preservation is retained.
- `backend/internal/server/adapters.go`, narrow `server.go` Options.Adapters/route; `backend/internal/application/server_adapters.go` and server initialization: actual desktop GET/PUT config consumer.
- `backend/internal/imruntime/`: shared original config/workspace boundaries, live disk pairing, shared adapter-sessions.json refresh, HTTP catalogs/session safety, WebSocket reconnect/auth/handler FIFO, session restoration/selection, UTF-16 text limits, permission reconciliation, message dedup/buffering, Telegram API polling/menu/commands/delivery/media/runtime.
- `backend/internal/server/adapterlauncher/launcher.go`: typed Telegram runtime factory, original credential gate and CALLER_DIR/app-root/env inheritance, stdout startup producer, Close/Wait ownership.
- `backend/cmd/claude-haha/main_telegram.go`, narrow main.go adapters binding: real native Telegram factory. Unbound launcher factories still fail explicitly; the production CLI supplies the complete Telegram runtime.

No new persistence schema marker or global migration is introduced. Existing shapes are retained. Pairing clears the whole original pairing object on use, while desktop PATCH retains unknown fields. Attachment staging deliberately uses the original HOME/.claude/im-downloads path independently of CLAUDE_CONFIG_DIR. Download/size-check/dedup/authorization order follows the actual Telegram entrypoint, including staging duplicate incoming media before deduplication.

## Original Source execution

Fixtures in `scripts/quality-gate/go-parity/` execute actual original functions/classes. Private parser/entry functions are exposed by Bun.plugin appending exports only. Telegram entry handlers use the documented grammY API transformer to route the original API through a real private HTTP grammY client; platform business logic is not replaced.

- `native-adapter-config-service.fixture.ts`: original AdapterService and private desktop config parser; legacy/unknown fields, all platforms, masks, masked patch mutation, stored JSON/mode.
- `native-im-common-state.fixture.ts`: actual original pairing and SessionStore, 5-failure/5-minute exact boundary, expiry equality, one-use pair code, strict allowlist/numeric paired-user identity, shared binding updates/deletes/unknown records.
- `native-im-recovery-bridge.fixture.ts`: actual WsBridge/AdapterHttpClient/session recovery with private HTTP/WS, Bearer/query auth, user/always-permission/stop packets, offline retention, safe-session rejection and stale deletion.
- `native-im-config-workspace.fixture.ts`: actual loadConfig/resolveAdapterWorkspace, GUI cwd '/', env precedence, canonical/symlink/explicit roots, relative/missing roots, legacy primitives.
- `native-telegram-delivery.fixture.ts`: original formatting, permissions and TelegramStreamDelivery; UTF-16/lone units, table/fence formatting, streaming sealing/final plans, 429/network retries, permanent edit fallback, failed final send and resume without replay, empty completion.
- `native-telegram-api-poll.fixture.ts`: real grammY API/long poll, parallel initialization, setup retry and 429, menu DELETE/SET, sequential updates and offsets, first allowed_updates, stop acknowledgment and real blocked HTTP abort.
- `native-telegram-media.fixture.ts`: original TelegramMediaService, AttachmentStore, ImageBlockWatcher and outbound-image safety; real HTTP downloads/files, MIME/size boundaries, forbidden remote/symlink/outside images, base64, actual sendPhoto multipart filename/content type/bytes.
- `native-telegram-entry-runtime.fixture.ts`: actual index.ts bot handlers over 43 actions: sender/private gate and dedup, thinking/tool-separated streams, forged/expired permission buttons, always/text deny/snapshots, status/providers/models/skills, history/resume/stop/clear/new and exact selection TTL equality/expiry. Model-list/current-model requests are independent Promise.all operations; only that pair's physical arrival order is canonicalized in the comparison. State snapshots preserve original observed post-action values and clock semantics.

## Deterministic physical consumers

- Public Go CLI `run(... adapters --app-root ... --telegram)` executes the default native factory with only its Telegram HTTP endpoint/client relocated to a loopback peer. Real getMe/deleteWebhook/menu/getUpdates/incoming/pairing/sendMessage/long-poll cancellation/Close execute. Unknown legacy config remains unchanged; remaining platform gates are tested explicitly.
- Real NewDesktopServer GET/PUT config -> native launcher -> Go polling -> authenticated real server HTTP/WS -> shared MainPrint. Duplicate incoming messages yield one model turn. Real Write approval buttons feed an actual always permission response, Write changes a private file, Read returns that file to the same model, and Telegram receives final editMessageText.
- File variant downloads and stages the actual incoming document before API handoff; the same MainPrint executes Read on the downloaded path and the subsequent model request contains its real content.
- Image variant downloads an actual JPEG, passes it through the real native image binding, and the model request contains the image block/MIME.
- Restart creates a new adapter runtime and reattaches the exact persisted session ID; the model observes prior Write history and a new Read result. No new transcript/session is invented.
- Close cancels the actual private long poll, joins launcher/poller/WS/delivery/timers and closes the desktop MainPrint owner.

Key observed logs (unique, old failed logs retained):

- /tmp/go-telegram-common-source-ninth-1791402159.log: imruntime 2.027s + adapterconfig 4.401s PASS.
- /tmp/go-telegram-entry-and-leaves-twentythird-1791404395.log: actual entry + leaves PASS (imruntime3.723s, adapterconfig3.948s).
- /tmp/go-telegram-mainprint-physical-twentyfifth-1791404754.log: actual config/launcher/MainPrint/permissions/Write/Read/reply/Close PASS.
- /tmp/go-telegram-native-default-cli-twentyseventh-1791405066.log: public default CLI + physical chain PASS.
- /tmp/go-telegram-attachment-mainprint-twentyeighth-1791405211.log: application file chain PASS; actual sendPhoto Source found MIME divergence application/octet-stream vs image/jpeg; fixed production header.
- /tmp/go-telegram-full-consumers-twentyninth-1791405339.log: MIME repair + Source/runtime + physical text/file + CLI PASS.
- /tmp/go-telegram-six-packages-source-physical-thirtieth-1791405510.log: five packages PASS; CLI transitional unrelated undefined runAuthCommand, subsequently completed by its owner.
- /tmp/go-telegram-image-default-entry-thirtyfirst-1791405986.log: image/public entry/Source PASS.
- /tmp/go-telegram-real-restart-image-thirtysecond-1791406115.log: actual restart/image/text/file + CLI/Source PASS.
- /tmp/go-telegram-final-deterministic-before-live-thirtythird-1791406392.log: application8.608s/imruntime3.534s/CLI2.422s PASS; live opt-in skipped when credentials are absent.

## Evidence limits

Telegram runtime guard is removed only in the production bound Telegram path after the consumers above passed. Feishu, WeChat, DingTalk, WhatsApp, WeCom, QQ and Slack runtimes remain unavailable in Go, as do their unfinished account-specific REST producers. This does not establish complete IM migration.

No real Telegram account, public Telegram API call or message to another person was used. No real user home/config, keychain or running desktop instance was touched. No branch/commit/PR was created. The current signed desktop candidate predates the final Telegram binding; these checks are not a signed-GUI smoke claim. Public CLI transport relocation is a native test seam, not proof of real Telegram credentials/Internet service.

Live official DeepSeek with private fake incoming is a separately gated maintainer check, launched only after deterministic evidence passed.

## Official live model and final checks

- **PASS** /tmp/go-telegram-official-deepseek-live-real-port-thirtyseventh-1791407215.log: real official DeepSeek model task 4.59s, application package 6.438s. Actual session ID `81c797f2-3d6c-499b-b552-dbfee2b4a532`; actual Write/Read, exact private file, 1 real always-permission callback, fake model request count 0, same MainPrint final reply and cancelled Telegram long poll/Close. Canonical data contains no credential.
- Safe receipt copied on explicit parent request to `artifacts/go-migration-integration/real-official-telegram-shared-mainprint-receipt.json`. It contains only allowlisted scalar metadata/tool names; no credential, authorization header, request body or provider object. Raw private artifact/canonical evidence remains in /var/folders/sx/hl8zlmxd7gx9b58_1t2zhp5r0000gn/T/go-telegram-official-live-0fvl5jks/.
- **FAILED, retained** /tmp/go-telegram-official-deepseek-live-thirtyfourth-1791406586.log: live helper used a saved third-party provider but NewDesktopServer.Port=0 while ProxyReady=true. The model failed to connect before any tool/permission. Repair was confined to the test harness: reserve a real loopback listener, pass its port to NewDesktopServer, start that same listener. The original local provider proxy remains in the successful live production chain; no proxy bypass or fake model reply was introduced.
- **PASS** /tmp/go-telegram-real-port-fixture-thirtysixth-1791407106.log: actual deterministic desktop flow rerun with the correctly pre-reserved port.
- **PASS** final six-package Source/physical/race /tmp/go-telegram-final-sixpkg-source-physical-thirtyeighth-1791407249.log: imruntime4.233s, adapterconfig3.249s, adapterlauncher2.592s, server2.337s, application8.393s, CLI2.032s. The run includes Source parser/config/workspace/permission/recovery/media/polling/entrypoint, public default factory, real file/image/same-session restart/MainPrint/Close. Separate live opt-in skipped in deterministic runs. Full owned leaf-package tests also passed: /tmp/go-telegram-owned-full-leaf-packages-thirtyfifth-1791406904.log (imruntime3.626s, adapterconfig3.691s, adapterlauncher2.738s).
- **PASS** `bun run check:persistence-upgrade`, /tmp/go-telegram-persistence-upgrade-1791406676.log: all checks passed using disposable fixture paths.
- `bun run check:impact` exited 0, /tmp/go-telegram-check-impact-1791406675.log: shared 4076-file migration diff selects broad lanes and reports missing allow-cli-core-change label/maintainer PR metadata. This does not block already authorized local implementation/tests. No PR/label/repository setting was changed. Full verify/PR-ready status is not claimed.
- Final git diff --check passed; git status and own source changes were reviewed, preserving the shared worktree’s unrelated changes.

## Subsequent Slack batch and Source SDK import repair

Slack was implemented and separately accepted after the Telegram batch above; see `go-slack-native-runtime-evidence-20261008.md`. Remaining guarded platforms are now Feishu, WeChat, DingTalk, WhatsApp, WeCom and QQ. Three Telegram Source fixtures resolve the same installed grammy1.42.0 production package entry from adapters/telegram using Bun.resolveSync, replacing internal node_modules filename imports that degraded module-graph routing. No Telegram runtime behavior changed. PASS `/tmp/go-telegram-grammy-production-package-thirtyninth-1791410208.log` imruntime3.370s; full owned Source/leaf regression `/tmp/go-slack-telegram-full-owned-leaves-tenth-1791410617.log` and shared text/file/JPEG/same-SID restart/public entry `/tmp/go-slack-telegram-shared-consumers-eleventh-1791410673.log` also passed.
