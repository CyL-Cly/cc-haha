# Native Go Slack producer and shared MainPrint evidence — 2026-10-08

## Scope and production consumers

This implements Slack, the second of the eight IM runtimes after Telegram. It reads the original desktop AdapterSettings → manifest/token/pairing config PUT → SidecarManager `adapters --app-root ... --slack` → original launcher → `adapters/slack/index.ts` → shared `adapters/common/chat-runtime.ts` call graph. Original TS production remains unchanged.

Production bindings:

- `backend/internal/imruntime/config.go`: adds platform selection around the already validated Telegram config loader; existing LoadConfig remains a Telegram wrapper. Slack environment/file bot token, app token, workspace, roots, server/local authority use the original document and precedence.
- `backend/internal/imruntime/slack_api.go`: actual bot/app authority, JSON POST/ordered GET, original ok:true test (HTTP status alone is not the success test), 30/120-second contexts, literal files.slack.com HTTPS/port/userinfo constraints, private file fetch and full reserve URL → raw-byte POST without bearer → complete upload protocol. The original upload code uses POST despite its PUT comment; Go follows executable Source.
- `backend/internal/imruntime/slack_payload.go`, `slack_manifest.go`, `mime.go`: full DM/bot/edit/hidden/file_share filtering, mention/channel/link and ordered entity decoding, replay identity/thread/file fields, exact ordered pretty manifest and URLSearchParams form encoding, original MIME helpers.
- `backend/internal/imruntime/slack_socket.go`: actual trusted WSS, ACK before dispatch including hello/disconnect, fresh apps.connections.open URL on each reconnect, unconditional unbounded retries with 1-second exponential/30-second cap and OPEN reset, malformed packet handling, disconnect close/reconnect, cancellation and owned join.
- `backend/internal/imruntime/chat_runtime.go`: ports the actual 784-line ImChatRuntime shared consumer. Pair/auth runs inside per-chat FIFO after dedup and before commands/session/deferred downloads; default-mode session create/recovery, all common command aliases, existing history picker, permission snapshots/resolutions/allow/always/deny, status/git/tasks, invalid-thinking rebuild, complete/error/clear exactly-once response closure, lazy buffers and safe image dispatch follow original behavior. Telegram keeps its dedicated original handler.
- `backend/internal/imruntime/slack_runtime.go`: actual 292-line index behavior: auth.test identity, 500-entry insertion-order display-name cache including unpaired senders, last inbound thread context before dedup, 3500 UTF-16 chunks, 1200ms edit throttle, exact failure pending-suffix recovery, deferred media limit checks before staging, image inline refs/file atomic staging, partial/all failure notices, safe outbound uploads, lifecycle ownership. Original fresh-session clear drops reply thread; restored-session clear is not invented and preserves the incoming thread.
- `backend/internal/adapterconfig/service.go`: Slack unbind shallow merge removes undefined token properties while preserving workspace/unknown fields and clearing allowed/paired arrays; uses existing atomic store/write/modes.
- `backend/internal/server/adapters.go`: bound manifest/unbind dedicated routes, original 404 for unknown Slack tail/method, typed original config errors including actual write failure. Other incomplete account routes retain guards.
- `backend/internal/server/adapterlauncher/launcher.go`, `backend/cmd/claude-haha/main_slack.go`, `main.go`: actual configured Slack credential gate, source launch/env/caller directory, default Go factory, owned process Wait/Close. Existing Telegram callback remains bound.

No new persisted JSON schema is introduced. Old/future platform/global fields survive masked PUT and unbind; tokens are omitted from stored JSON on unbind.

## Actual original Source comparisons

Each fixture imports or runs the original source. Private index/route fixtures append exports with Bun.plugin and replace only transport/startup dependencies. They do not supply a replacement parser or response algorithm.

- `native-slack-leaves.fixture.ts` / `slack_leaves_source_test.go`: raw DM/mention/entity/file/filter matrix, exact manifest/deep link, API error/status authority, app vs bot token, upload bytes and absence of upload authorization. Go comparator uses a real private HTTP peer; production file host validation remains literal files.slack.com and the transport alone relocates the already validated authority.
- `native-slack-handler.fixture.ts` / `slack_handler_source_test.go`: original complete index SlackResponse, collectAttachments, replyThreads and resolveDisplayName; append/throttle/finish, failed update suffix and re-post, over-limit UTF-16 chunks, notices, inline/file media, unsupported MIME, empty preferred URL vs fallback, unsafe URL failures, 501-name eviction and actual 3-step image upload. Go performs actual private HTTP/file writes.
- `native-im-chat-runtime.fixture.ts` / `chat_runtime_source_test.go`: original shared class with typed dependency ports; unauthorized media, replay, help/status/projects/new/stop/clear/cancel, model init and tool stream, permissions/snapshots/resolutions, discard/finish/rebuild and binding/stream/state ordering. Go comparator executes its real HTTP and WebSocket bridge against private peers.
- `native-slack-socket.fixture.ts` / `slack_socket_source_test.go`: original SocketMode class and actual Source ws client connect to the same trusted private TLS WS peer. Source and Go exact ACKs, fresh opens, envelopes/retry/disconnect logs and two physical socket closes agree. Private cert is supplied at Source startup via original NODE_EXTRA_CA_CERTS; TLS verification is enabled.
- `native-slack-config-routes.fixture.ts`, `native-slack-route-failure.fixture.ts` / `server/slack_adapters_source_test.go`: original private route and adapterService, manifest extra-tail behavior, masked config/unbind, unknown endpoints/methods, file mode and preservation; actual directory-at-adapters.json rename failure preserves the original 500/code/message.

Source SDK fixture maintenance: the three existing Telegram fixtures now resolve `grammy` via the original adapters/telegram package location using Bun.resolveSync, rather than importing an internal node_modules filename. Installed and executing version remains 1.42.0. Dependency graph checks can resolve the fixture module without treating omitted generated node_modules files as repository sources. No SDK/mock/allowlist substitution occurred.

## Physical desktop / public CLI / shared Agent

`cmd/claude-haha/main_slack_test.go` runs the public Go `run(adapters --app-root ... --slack)` with the production default factory. Only the native API HTTP endpoint/client is relocated. Auth → app-token connection URL → verified WSS → ACK → original pairing refusal → reply → cancellation/socket/launcher join execute; old config remains exact.

`application/desktop_slack_mainprint_flow_test.go` starts actual NewDesktopServer with its bound local listener and native process factory, then GETs manifest and PUTs masked Slack configuration over real authenticated HTTP. It launches the real adapterlauncher credential path with SlackRuntime and a private verified WSS sender. A repeated envelope is ACKed twice but produces one shared Agent turn. The real default-mode MainPrint prompts for Write; a private incoming `2` sends the original always-permission response. Real Write/Read create and verify the exact artifact; file variant downloads only once after auth/dedup/session binding and the real Read tool reads the staged file. Real assistant markdown drives an actual external upload reserve/raw POST/complete sequence. Canonical history contains actual tools and final answer. Adapter restart reattaches the same persisted SID, carries actual prior Write history into the next Read, retains the new thread, and closes both sockets. Close joins launcher/socket/bridge/buffers/MainPrint owner.

This is private fake IM transport, with only the model HTTP endpoint faked in deterministic runs. No real Slack credentials, account, workspace, external message or user home is used.

## Observed red → green and commands

All logs are unique and retained.

- PASS `/tmp/go-slack-leaves-first-1791409716.log`: original leaves + physical HTTP, imruntime1.668s.
- PASS `/tmp/go-slack-runtime-declarations-second-1791409890.log`: production declarations compile and leaves, imruntime1.672s.
- FAILED `/tmp/go-slack-common-source-third-1791409998.log`: stub fixture work path was not canonical while real HTTPClient canonicalizes it; fixture input normalized to the same real path.
- FAILED `/tmp/go-slack-handler-common-fourth-1791410075.log`: actual shared comparison found Go sent attachments:null on a no-attachment user message, while Source omits the field. Production sender now emits attachments only when nonempty; no Source algorithm weakened.
- FAILED `/tmp/go-slack-full-source-socket-fifth-1791410158.log` and `/tmp/go-slack-socket-private-cert-sixth-1791410233.log`: actual Source Bun ws client did not trust a CA set only as runtime options. Startup NODE_EXTRA_CA_CERTS supplies the private CA. No insecure TLS setting was added.
- PASS `/tmp/go-slack-full-private-ca-seventh-1791410287.log`: original shared/index/leaves + actual trusted WSS, imruntime6.209s.
- PASS `/tmp/go-slack-cli-routes-eighth-1791410400.log`: Source/private HTTP/WSS/public CLI/rest routes; imruntime6.590, adapterconfig4.552, server2.752, CLI2.333s. Launcher package compiled; that focused filter had no launcher tests.
- PASS `/tmp/go-slack-actual-mainprint-ninth-1791410524.log`: actual desktop/launcher/MainPrint/default permissions/Write/Read/files/reply/image/Close; application5.751, imruntime6.396, server5.150, CLI3.443s.
- PASS `/tmp/go-slack-telegram-full-owned-leaves-tenth-1791410617.log`: full imruntime8.218, adapterconfig4.056, adapterlauncher2.176s; all existing Telegram Source regressions run with the same 1.42.0 production package entry.
- PASS `/tmp/go-slack-telegram-shared-consumers-eleventh-1791410673.log`: shared desktop Telegram text/file/JPEG/same-SID restart preserved alongside Slack and both public CLI factories; application11.138, server3.307, CLI3.507s.
- PASS `/tmp/go-slack-file-failure-thirteenth-1791410730.log`: actual Source + physical config write failure, server3.642s.
- PASS `/tmp/go-slack-actual-restart-file-failure-fourteenth-1791410800.log`: actual Slack same-SID/history/thread restart, file/MainPrint/Close and original error consumer; application6.233, server3.780s.

Command for deterministic Go runs: pinned Bun `/tmp/go-current-test.ts` with the listed package/filter scopes, internally `go test -p=4 -race -count=1`, private HOME/caches and offline dependency hydrate. Builds alone are not substituted for Source/physical evidence.

## Separately authorized official model

PASS `/tmp/go-slack-official-deepseek-live-twelfth-1791410686.log`: actual official DeepSeek at https://api.deepseek.com/anthropic via saved isolated provider and actual local generic provider proxy. Private incoming Slack event → same MainPrint → real default-mode always permission → actual Write/Read/exact artifact → private Slack final reply → owned Close. Actual model task3.64s/package5.556s; SID `7ecb9f15-da32-4dd7-9a09-327527b56a39`, permission callback1, WSS ACK3, fixture-model requests0, no credential in canonical.

Safe receipt: `artifacts/go-migration-integration/real-official-slack-shared-mainprint-receipt.json`; only allowlisted safe metadata/tools, no credential, header, request body or provider object. Private receipt source: `/var/folders/sx/hl8zlmxd7gx9b58_1t2zhp5r0000gn/T/go-slack-official-live-o7vb5b_m/receipt.json`.

IM delivery in this live check still uses a private local peer. It sends no real Slack account/workspace message and spends no messaging-platform quota. Real-provider quota was explicitly authorized separately and used only after deterministic green.

## Remaining scope and candidate limits

Telegram and Slack are bound. Feishu, WeChat, DingTalk, WhatsApp, WeCom and QQ runtime/account producers remain explicit capability errors. This is not complete IM migration. Current P250 signed desktop predates this Slack implementation; none of the checks above claims signed-GUI Slack operation. Root owns the next full Go/selected verification/signed candidate snapshot. No branch, commit, PR, repository setting, real account/config/keychain or running desktop was changed.

## Final integration checks

- PASS final six-package Source/physical/race `/tmp/go-slack-telegram-final-sixpkg-fifteenth-1791411032.log`: imruntime8.799, adapterconfig3.812, adapterlauncher1.734, server4.100, application7.099, CLI2.666s. Includes Slack original credential/workspace precedence, original complete index/common/socket/config routes, actual write failure and same-SID/history/thread restart, all Telegram consumers and 1.42.0 SDK Source paths. Separate live opt-ins skip in deterministic runs.
- PASS `bun run check:persistence-upgrade`, `/tmp/go-slack-persistence-upgrade-1791410930.log`: all checks passed (desktop migration regression27tests included).
- `bun run check:impact` exited0, `/tmp/go-slack-check-impact-1791410929.log`: shared4161-file migration selects broad policy/desktop/server/go/parity/provider/chat/agent/adapters/native/persistence/docs/coverage lanes. CLI change label/maintainer approval metadata is missing; this is PR metadata, not a block on already authorized local implementation. No label/PR/repository setting was changed. Full verify/PR-ready is not claimed by this platform handoff; root owns global checks.
- `git diff --check` passed; tracked shared diff and own untracked production/fixture files plus `git status --short` reviewed. Existing unrelated changes preserved.

- PASS source import/dependency graph regression `bun test scripts/pr/module-graph.test.ts`, `/tmp/go-slack-telegram-module-graph-sixteenth-1791411171.log`: 11pass/0fail, including resolution of every repository-local module specifier; Telegram internal SDK filename repair fixed the actual policy graph without broadening any allowlist.

- FAILED/blocked transient shared compile `/tmp/go-slack-actual-attachment-context-final-seventeenth-1791411269.log`: sdkhost/host.go874 referenced abortCleanupHandler before its owner completed the declaration; no Slack production delta. After owner confirmed complete declarations, PASS `/tmp/go-slack-actual-attachment-context-final-eighteenth-1791411365.log` application5.377s: strengthened assertion proves the actual first model context contains the inbound staged attachment name, plus text/file/approval/Write/Read/image/same-SID history/thread restart/Close. Original 15 full-six-package evidence remains retained; no failing log overwritten.

All owned production, tests, Source fixtures, receipts and evidence documents are now frozen for the parent full verification corpus snapshot.
