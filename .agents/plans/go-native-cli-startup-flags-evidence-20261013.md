# CLI startup flags — intermediate evidence, 2026-10-13

This evidence covers the current uncommitted candidate, against Source MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`. It does not seal a release candidate or declare migration complete. No branch changes, commits, live provider quota, or main merge occurred.

## Implemented consumers

- `--file`: actual Files API GET with session ingress Bearer, shared FD credential cache, five workers, three attempts and original non-retry statuses/backoff, upload paths, physical file writes, runtime cancellation and join. Downloads start asynchronously. Print/fresh/continue do not await. Interactive resume/from-PR/teleport await before REPL, and the no-ID resume chooser awaits before it can render/cancel. Supplied `--session-id` does not replace Source's earlier download bootstrap SID; remote session environment override does.
- `--deep-link-origin/repo/last-fetch`: parser metadata survives the shared startup arguments; nonfinite timestamps remain undefined. The frozen normal build has LODESTONE off, so there is no gated banner or automatic submission. External prefill stays a draft.
- `--chrome`/`--no-chrome`: explicit/environment/global precedence, actual Claude subscriber eligibility, original empty bundled MCP transport, prompt/skill hint, native wrapper/manifests and Windows registry targets. Background extension detection persists positives only and preserves old global fields. Reconnect selects the first available compatible browser. Native host is separate from browser MCP tools.
- `--chrome-native-host`: production binary first-argument fast path implements little-endian length framing, Unix socket/Windows overlapped pipe, ping/status/error, client notifications, tool request forwarding, response/notification broadcast, permissions/stale Unix socket cleanup and cancellation/EOF join. The shipped `--claude-in-chrome-mcp` fast path preserves the frozen empty server stub. Source does not ship real browser tools in this profile.
- Interactive MCP startup publishes completed servers asynchronously, with Source's independent three-local/twenty-remote worker pools and parallel tools/prompts/resources settlement per peer. Rendering and the first query can proceed while other peers remain pending. Actual owned transports and fake-account catalog metadata cancel/join before the native ledger's final Host join; independent child close preserves borrowed parent producers.
- Classic MCP prompt commands: actual schema-validated `prompts/list`, recursive metadata sanitization, independent 20-entry LRU/single-page behavior, programmatic identifier and user-facing `(MCP)` alias, literal-space argument zip into actual `prompts/get`, rich content flattening into the existing Skill/slash producer and same Harness model request. Audio/resource blobs persist through existing physical tool-results; images reuse existing native image conversion. Actual SSE `prompts/list_changed` refreshes the prompt catalog/menu without invalidating resource caches. Capability-gated interactive `tools/list_changed` invalidates only its tool LRU and publishes the re-fetched actual tools to the same model catalog. Cancellation sends the real MCP cancellation notification and joins owned producers on shutdown. Frozen `setupSdkMcpClients` supplies clients/tools only, so ordinary prompt discovery skips SDK peers even when they advertise prompt capability. Classic prompts do not acquire a `loadedFrom: mcp` Skill marker.
- `MCP_TIMEOUT`/`MCP_TOOL_TIMEOUT`: actual shared mutable environment consumers now match Source decimal `parseInt` prefixes, exact whitespace, NaN/zero defaults, negative/oversized timer clamping and original error values. The ordinary connection Promise timer captures its deadline but re-reads the environment for the fired diagnostic. Each actual tool call reads its timeout anew; the external tool error remains distinct from the SDK request timeout/abort and its physical cancellation notification. Existing 60-second request/HTTP windows remain intact, and SDK initialization has no ordinary `MCP_TIMEOUT` timer. The raw SDK request retains a native reference after an earlier external timeout result. Actual settings-file hot reload updates the next tool call on the same MCP connection/Harness.
- Interactive disable/reconnect retires only the target server's pending startup publication. A prior completion cannot overwrite the immediate disabled state during the actual disconnect await; other servers continue discovery.
- `--remote`: interactive creation uses organization policy and actual OAuth authority, canonical smallfast auxiliary model/title JSON schema, Git remote preflight/source selection or tracked-WIP bundle upload, temporary seed refs cleanup, environment selection, session events/context and original created/view/resume output. Print ignores this flag like Source. API key alone cannot enter CCR.
- `--teleport`: account/policy, explicit or cloud session selector, repository host/slug and tracked dirty-Git validation, known checkout selector/stale mapping cleanup, paginated/legacy logs, branch fetch/checkout/upstream and nonfatal branch notice, then sanitized history/meta into a fresh local fork. Cloud is not mutated. Startup downloads and ingress cache transfer into the local application without a duplicate download.
- The CCR policy startup promise shares old `policy-limits.json` shape/ETag and stale-cache fallback. Eligible sessions poll every hour; runtime close cancels and joins HTTP. Tests substitute only the clock interval.

## Observed checks

All commands ran through `artifacts/go-migration-20261013/cli-flags/run-focused.ts`, using `createGoBuildEnvironment`, pinned Bun 1.3.14, the prepared Go dependency seed and disposable HOME/config/cache. Saved OAuth/Keychain inputs are fake; production addresses are redirected only through the existing private CONNECT/TLS fixture.

| Check | Result |
| --- | --- |
| Actual Source file parser/path/download fixture plus public RunCommand async print/resume/file cleanup | passed; initial clibootstrap 2.149s/application 1.475s |
| Public RunCommand remote title→OAuth→create output, teleport history→model→new local transcript and staging cleanup, print ignores remote, account denial | passed |
| No-ID resume chooser cancellation waits actual download | passed, application 0.962s |
| Chrome eligibility/wrapper/manifest/idempotence and old global positive-only cache | passed |
| Actual frozen Source native host framing/validation/socket broadcast/cleanup | passed, 0.645s; only socket-location OS primitive replaced |
| Production Go binary Chrome native host ping/status/EOF and empty MCP fast path | passed; cmd package 6.510s intermediate |
| Actual Git/HTTP CCR creation, tracked WIP bundle upload/oversize cleanup, repository/branch, pagination/fallback/401, old policy cache/retries/poll close | passed |
| Canonical auxiliary title request and parent history unchanged; foreign/read/stale authority rejection | passed |
| Earlier eight-package focused race across CCR/Chrome/bootstrap/application/cooperative/sessioncontroller/MCP/cmd | passed: 2.910s/1.550s/5.371s/9.106s/1.436s/2.789s/3.003s/11.016s |
| Actual Source prompt metadata/argument/cache/content comparison and physical SSE changed list→slash→model→MCP cancel | passed; clibootstrap 5.139s/mcptools 1.848s focused race |
| Public RunCommand `-p` prompt list/get→audio file→same canonical model→text output | passed; application 2.752s race |
| Actual concurrent prompt LRU/schema rejection/reconnect/execution policy and blocked-list physical close/join | passed; mcpruntime 2.914s race |
| Interactive first actual query completes with pending MCP, late fast tool→real tools/call→next model POST, shutdown cancels slow peer | passed; clibootstrap 3.160s race |
| Expanded ten-package focused race including original Skill/MCP resource/SDK consumers | passed: CCR 2.661s/Chrome 1.557s/bootstrap 14.415s/application 11.038s/cooperative 1.576s/controller 3.142s/client 5.619s/runtime 3.717s/tools 6.240s/cmd 18.449s |
| Held fake-account organization catalog metadata cancels before Host join; child close preserves parent's process memo | passed after reproduced RED; bootstrap 6.628s/application 3.151s race |
| Latest prompt/tool SSE update, literal-space Source comparator, public audio/CLI, SDK tools-only Source receipt, held metadata and runtime cache/close focused race | passed: mcptools 5.085s/clibootstrap 6.223s/application 4.449s/mcpruntime 3.292s |
| 2026-10-14 actual Source timeout getters/type/message/timers, decimal/default/negative/oversized/overflow, actual cancellation reasons, disabled pending completion; native queued retirement and public `/mcp` next execution catalog, race count=5 | passed: mcpclient 9.472s/mcptools 1.987s/application 6.324s |
| Public RunCommand MCP timeout→`is_error` tool result→next same canonical model POST→output | passed; application 2.873s standalone race and later affected-boundary repetitions |
| Actual settings-file environment reload, one physical MCP handshake, two tools/four model POSTs/same Host | passed; clibootstrap 5.258s standalone race |
| Expanded affected MCP prompt/SDK/resource/rich-file lifetime/environment/public CLI and panel consumers | passed: runtime 1.791s/tools 3.965s/bootstrap 18.915s/application 10.755s; bootstrap/app rerun after peer saved status compile checkpoint |
| Final 2026-10-14 complete mcpclient race and client/runtime/tools vet | passed: mcpclient 14.049s; vet exited 0 |
| Windows native-host package crosscompile | passed; build-only, not Windows runtime evidence |
| Full final quality gates, same-version desktop/Web/CLI live acceptance, 120 paired Benchmark | not run by this component owner; root owns integration |

Initial eight-package focused race command (before later MCP consumer repairs):

```text
bun artifacts/go-migration-20261013/cli-flags/run-focused.ts test -race ./internal/ccrsessions ./internal/chromenative ./internal/clibootstrap ./internal/application ./internal/cooperative ./internal/sessioncontroller ./internal/mcpclient ./cmd/claude-haha -run 'TestCCR|TestChrome|TestPublicChrome|TestCLIChrome|TestFrozenChromeStub|TestCLIStartupFiles|TestCLIFileAndFrozenDeepLinkMetadata|TestPublicCLIFileDownload|TestPublicCLIResumeWaitsForFile|TestPublicCLINoIDResume|TestPublicCLIRemote|TestPublicCLITeleport|TestPublicCLIPrintIgnoresRemote|TestCLIRemoteTitle|TestCLIKnownRepository|TestRemoteTitleAuthority|TestRemoteCLIStartup' -count=1 -timeout=180s
```

Expanded focused race command (all ten packages passed; the later metadata-close/tool-notification deltas then passed the focused command below):

```text
bun artifacts/go-migration-20261013/cli-flags/run-focused.ts test -race ./internal/ccrsessions ./internal/chromenative ./internal/clibootstrap ./internal/application ./internal/cooperative ./internal/sessioncontroller ./internal/mcpclient ./internal/mcpruntime ./internal/mcptools ./cmd/claude-haha -run 'TestCCR|TestChrome|TestPublicChrome|TestCLIChrome|TestFrozenChromeStub|TestCLIStartupFiles|TestCLIFileAndFrozenDeepLinkMetadata|TestPublicCLIFileDownload|TestPublicCLIResumeWaitsForFile|TestPublicCLINoIDResume|TestPublicCLIRemote|TestPublicCLITeleport|TestPublicCLIPrintIgnoresRemote|TestCLIRemoteTitle|TestCLIKnownRepository|TestRemoteTitleAuthority|TestRemoteCLIStartup|TestActualMCPPrompt|TestActualMCPBlockedPrompt|TestNativeMCPPromptCommands|TestInteractiveMCP|TestPublicCLIMCPPrompt|TestPublicCLIChromeSaved|TestActualSDKNameset|TestSharedSkillSlash|TestActualInlineSkill|TestSharedMainPrintSkillInvocation|TestNativeSkillResourceListChanged|TestSkillResourceMentions|TestMainPrintConcurrentResource' -count=1 -timeout=180s
```

Latest affected-boundary focused race command:

```text
bun artifacts/go-migration-20261013/cli-flags/run-focused.ts test -race ./internal/mcptools ./internal/clibootstrap ./internal/application ./internal/mcpruntime -run 'TestInteractiveMCP|TestNativeMCPPromptCommands|TestActualSDKNamesetRebuild|TestPublicCLIMCPPrompt|TestActualMCPPrompt|TestActualMCPBlockedPrompt' -count=1 -timeout=90s
```

Timeout and pending-disable Source/physical/public repetition:

```text
bun artifacts/go-migration-20261013/cli-flags/run-focused.ts test -race ./internal/mcpclient ./internal/mcptools ./internal/application -run 'TestActualSourceDiscardDisabled|TestInteractiveMCPDisableRetires|TestTerminalMCPPanelAndSlashToggle|TestSourceToolTimeout' -count=5 -timeout=90s
```

The timeout fixture executes the original private getter declarations unchanged and invokes actual frozen `connectToServer`/`fetchToolsForClient` plus the real disable-completion guard. Its SDK timeout/abort notifications are joined by physical loopback receipts before fixture process exit; this fixes a fixture teardown race without moving any production deadline. The native fixture records immutable wire bytes to avoid sharing lazy JSON views across HTTP handlers. Actual Source SDK abort reaches the tool wrapper as `TelemetrySafeError` with `MCP error -32001: AbortError: The operation was aborted.`; it is not converted into the unused plain `AbortError` catch branch. Source overflow diagnostics use the canonical JavaScript number string and SDK JSON preserves nonfinite timeout data as null.

The root checkpoint02 public `/mcp` test reproduced a real catalog race: status was set disabled, then the awaited disconnect admitted an older connected discovery result which restored the tool. The per-server generation repair passed the same public panel test without a wait-for-ready fixture change; the new held physical discovery test separately confirms final disabled status and empty execution catalog after every native producer settles.

Initial red evidence was retained in work messages: file Source HTTP guard adapter, canonical macOS transcript path, title owner/source authority, missing synthetic notice timestamps, long Unix fixture socket path, Source fixture HOME/cwd guard, and raw formatted JSON assertion. Temporary undefined symbols from simultaneous peer edits were identified as interrupted compilation, then rerun after consistent checkpoints.

Public `--chrome` with a saved fake subscriber additionally reproduced blocking interactive MCP discovery and Host.Join preceding transport cancellation; actual first-query/late-tool/public `/exit` regressions now cover the repairs. A held fresh fake-account `/v1/mcp_servers` request reproduced a 1.5s shutdown deadline because catalog metadata was closed after Host.Join; the owned/unborrowed catalog now closes before join. No real OAuth login/refresh or cloud account was used. The initial prompt cancellation fixture incorrectly awaited per-HTTP-request disconnection; actual Source-compatible cancellation travels as MCP `notifications/cancelled`, and the corrected physical peer observes the request ID and stops its pending producer.

## Remaining acceptance boundaries

Actual official Claude account login/refresh/cloud remote creation/recovery are **not authorized in this run and unverified**, per the user's latest instruction not to perform Claude OAuth. Fake account/loopback tests cannot replace those receipts. Windows pipe/registry/browser behavior is build-only here. Chrome browser tool operation is outside the frozen shipped empty MCP profile; no invented tools or live browser capability was claimed. The full final diff, quality/persistence policy, final candidate artifact and requested paired Benchmark must pass before declaring migration complete.

The canonical auxiliary-model adapter now admits exactly `teleport_generate_title` and the delegated lifecycle owner's `rename_generate_name`; both retain the existing actual-owner/current-scope gates. Arbitrary purpose strings remain rejected. Focused deterministic requests use fake loopback model transports; this component did not spend live model quota. Live acceptance still requires official DeepSeek `deepseek-flash` where a model is needed.

The normal-profile root argument static audit finds only `--tmux` outside `application.ParseCommand`, and it is handled by the existing worktree fast path. Advisor and the remaining internal/feature-gated options are not registered in the frozen normal profile. This static audit is not product acceptance of every historical checklist row.
