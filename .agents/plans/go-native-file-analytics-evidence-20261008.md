# Native file-operation Analytics evidence · 2026-10-08

This leaf removes the real default Write boundary by binding its original optional file-operation reporter. It does not disable Analytics, suppress provider-managed environment keys, or return a fake event-export success.

## Production boundary

- `backend/internal/analytics/`: original privacy/sample/hash/proto serialization; real queued background batches, authentication retry, current and old JSONL spool, bounded 100MB tail reads, quadratic retry/drop, private files and 500ms HTTP cancellation/settlement.
- `clibootstrap/native_file_analytics.go`: one actual process producer. Agent owners deliver immutable attribution to the parent producer. Its native I/O ledger is separate from transcript writes; telemetry does not block normal ASK or canonical writer joins.
- Actual `Write`, `Edit`, text/notebook/image `Read` callbacks use the same tool producers and live file cache. Read emits its original `message.id`, not canonical envelope UUID. Dedup emits the original dedup event.
- MainPrint final close drains background children before the optional producer, then closes helper/settings/Owner. Source endpoint, privacy and trust remain intact. `AnalyticsHTTP` substitutes the physical event endpoint only for private maintainer evidence.
- Original fork has an empty Datadog token and GrowthBook client key; no substitute outbound sink or fabricated credentials were introduced. External cached GrowthBook features do not override this fact. Original ant override precedence remains.
- Source process metadata cannot describe a Go process as Node/V8: Node version/counters are omitted, rather than fabricated. Actual platform/architecture/terminal/deployment and original sparse event layout are retained. Repo-remote hash, full platform inventory and the original process-counter producer are not claimed by this leaf.

## Deterministic evidence

Commands ran under private HOME/config/tmp, offline Go dependencies, actual Source Bun, isolated loopback transports and fake credentials. No public telemetry endpoint was used by these tests.

| Evidence | Observed result |
| --- | --- |
| 75 original privacy/hash/UTF16 profiles + 5 original proto/PII records | passed |
| Original exporter 10 profiles: batches/401/503/old files/legal primitive rows/partial failure/retry/drop | passed, `/tmp/go-file-analytics-old-spool-source-first.log`, race 2.541s |
| Original Read 7 profiles: CRLF/BOM/empty/range/session flags/extension/notebook/dedup/message id | passed, `/tmp/go-file-read-analytics-actual-source-fourth.log`, race 2.769s |
| 127 original terminal/deployment profiles + actual Main/reporters/helpers/old continuation regression | passed, `/tmp/go-file-analytics-runtime-facts-source-twelfth.log`, race 12.044s |
| Real Owner/concurrent export, immutable 401 fallback, 503 spool, privacy, foreign owner, 100MB tail, 0600 files | passed |
| Full Analytics / builtintools / builtinwrite | passed, `/tmp/go-file-analytics-full-domain-race-seventh.log`, race 3.015/14.594/2.045s |
| Final Source/real Main narrow delta | passed, `/tmp/go-file-analytics-current-source-sixteenth.log`, race Analytics3.558 / CLI4.640s |
| Six affected package vet | passed, `/tmp/go-file-analytics-final-vet-seventeenth.log`, exit0 |
| Final Analytics/reporters delta incl platform separator | passed in `/tmp/go-file-analytics-final-narrow-eighteenth.log`: Analytics2.506 / builtintools1.695 / CLI5.378s; application runtime-switch regression reproduces separately, failed20.01s |
| Full CLI/application after test-mode fixture repair | failed, `/tmp/go-file-analytics-full-CLI-application-fourteenth.log`: relocation subprocess saw transient SDK undefined `h`; CLI suite hit total5min during still-running Plan cases; application runtime-switch history test failed at20s. Full suite green is not claimed. |

The earlier `NODE_ENV=test` shared fixture change incorrectly selected Source test persistence/auth behavior, producing canonical/helper failures. It was replaced by `DISABLE_TELEMETRY=1` solely in the shared test fixture; the explicit Analytics fixture removes that flag and redirects events to loopback. First failures are retained.

## Official model evidence

Private CLI adapter invokes actual `application.RunCommand` and its single Harness. Only OS managed-policy/Keychain primitives and event HTTP target are isolated. User-authorized official DeepSeek uses the Anthropic endpoint; no credentials are saved or printed. Neither `NODE_ENV`, `DISABLE_TELEMETRY`, nor `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` is set.

- `artifacts/go-migration-integration/real-deepseek-file-analytics-default-interval-receipt.json`: SHA43a427af163f345c6c626c3cf7e1bafe665211db97ff1a767c4435aed77b832c, SID4f9253d6-5bd7-4743-96b2-7e0be74dac37. Eight real rounds: Write→Read→Edit→Read→Read(dedup)→Write→Read→result. Both files contain actual markers; cold resume reads both successfully. Default10s interval sends its first batch at shutdown, receives503, and leaves a spool. Resume's constructor scan precedes activation of the resumed SID in this run, so new events send204 but the old file remains. This is not claimed as retry proof or repaired by guessing a phase.
- `artifacts/go-migration-integration/real-deepseek-file-analytics-active-retry-receipt.json`: same binary SHA, fresh SIDf3d168f2-a5d0-4687-b2c1-c0cf220228ca, original supported `OTEL_LOGS_EXPORT_INTERVAL=20`. Same seven tools plus normal cold-resume Read, both result success. Ten actual loopback requests: first503, second204 with the identical event ID, later204; no remaining spool. Model tools continue normally.

The later optional beta/build-time/version-base metadata binding has separate deterministic Source evidence; these receipts do not claim that later code or a signed desktop SHA. Public Anthropic event-server availability, full Node/V8 process-counter equivalence and latest complete signed desktop readiness remain outside these live receipts.

## Exact checks run

Private environment file: `/tmp/go-openaiauth-final-environment.json` (private HOME/config/temp; locked offline module cache). No credential value is present in this file.

```text
go test -mod=readonly -race ./internal/analytics ./internal/builtintools ./internal/builtinwrite -count=1 -timeout=120s
go test -mod=readonly -race ./internal/clibootstrap ./internal/sdkhost -count=1 -timeout=180s
go test -mod=readonly -race ./internal/clibootstrap ./internal/application -count=1 -timeout=300s
go test -mod=readonly -race ./internal/analytics ./internal/clibootstrap -run 'TestActualSourceAnalyticsTerminal|TestActualMainPrint.*Analytics|TestActualSource.*(Privacy|FileOperation|FirstParty|Exporter)|TestActualConcurrentExport|TestActualOldJSONL|TestActualPrivacy' -count=1 -timeout=90s
go test -mod=readonly -race ./internal/analytics ./internal/builtintools ./internal/builtinwrite ./internal/clibootstrap ./internal/application -run 'Analytics|TestActualSourceExporter|TestActualOldJSONL|TestActualConcurrentExport|TestActualOwnerAnalytics|TestActualPrivacy|TestDesktopRuntimeSwitchWaitsForActiveTurnThenRestoresHistory' -count=1 -timeout=120s
go vet -mod=readonly ./internal/analytics ./internal/builtinwrite ./internal/builtintools ./internal/clibootstrap ./internal/sdkhost ./internal/application
git diff --check
```

Full root `check:impact`/`verify` and a new signed desktop smoke for this final Analytics leaf are root integration checks; not run by this subtask. Final whole-suite evidence must use a frozen current graph, rather than reusing the earlier metadata/live SHA.

## Follow-up resolution of application runtime-switch failure

The apparent history-loss failure was a stale test setup: its saved index had already selected the new service before the first native startup, so the old service was never entered. Original Source's startup active-provider producer and the corrected actual dual-HTTP test confirm this. The initial default now remains first until the explicit second override; every original history/credential assertion stays intact and both Owner-close/canonical-answer checks were added. New Source/actual default-versus-explicit route profiles passed race10.896s; latest full application passed race244.984s in `/tmp/go-runtime-switch-full-application-eighth.log`. See `go-native-runtime-switch-evidence-20261008.md`. The old red evidence remains; no production consumer was disabled or bypassed.
