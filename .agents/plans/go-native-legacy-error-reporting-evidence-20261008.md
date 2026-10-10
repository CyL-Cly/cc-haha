# Native legacy API-key error reporting

Scope: P285 ordinary TUI visibly displayed `exit status 44`. Source inspection found that `NativeOpenAIAuth.apiKeySource` printed recoverable legacy Keychain errors directly, while the original `auth.ts` catch calls `logError`. Private injected 44/73 errors reproduce this migration difference; the exact origin of the visible P285/P271 diagnostic was not independently instrumented. This consumer repair does not close the complete Go migration.

The production consumer now uses `internal/errorlog` through the same MainPrint Owner. Native preAction attaches one local sink. An in-process Agent routes immutable error delivery through the existing parent Owner bridge, creates no second logger, and does not close its parent's sink. There is no exit-code special case and no new default capability failure.

Original behavior retained:

- Nonempty `DISABLE_ERROR_REPORTING` / `CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC` and truthy Bedrock/Vertex/Foundry/AzureOpenAI disable error reporting. `DISABLE_TELEMETRY` alone does not disable error memory.
- The original error object is queued before the first sink; memory records its string at log time, keeps the newest 100 rows, and reporting failures do not replace the original recoverable operation.
- An external default process does not write error text to stderr or a local log. Explicit DEBUG/DEBUG_SDK writes the local debug file and latest link. Internal `ant` use also writes the original JSONL field layout, cache path/hash, one-second/deferred buffering, and close flush.
- The original managed-key config fallback and memoized lookup remain; a canceled native primitive propagates cancellation and remains retryable. Foreign Owners cannot deliver errors.
- Go native errors keep their actual message/cause; no V8 stack is manufactured. All tests use private HOME/config, explicit fake Keychain primitives, and local files. There are no real Keychain, user-state, model, or telemetry-upstream calls.

Focused evidence:

- Intended red: `/tmp/go-auth-error-reporting-private-red-first.log`, all ten private 44/73 cases printed to terminal before repair.
- Actual Source privacy/queue/100-row comparison: 31 profiles in `native-error-log.fixture.ts`.
- Actual Source file/sink comparison: 21 profiles in `native-error-sink.fixture.ts`, including real filesystem errors. The original async latest-link physical completion is observed without a sleep or output normalization.
- Twelve actual Source auth/error/config cases; actual MainPrint memoized Keychain failure, fallback, cancellation, foreign Owner, JSONL close, and independent child/parent lifecycle.
- Latest combined scoped race: `/tmp/go-auth-error-reporting-final-current-source-race.log`, errorlog 3.765s, clibootstrap 11.562s, exit 0.
- Sink Source/lifecycle repeated three times: `/tmp/go-errorlog-native-sink-source-tenth.log`, 8.456s, exit 0.
- Eight-package vet: `/tmp/go-auth-error-reporting-full-vet-first.log`, exit 0.

Full affected Source/race was run and **failed**, not reported as complete:

`go test -mod=readonly -race ./internal/errorlog ./internal/clibootstrap ./internal/application ./internal/sdkhost ./internal/openaiauth ./internal/securestorage ./internal/cliglobalconfig ./internal/clistartupenv -count=1`

`/tmp/go-auth-error-reporting-full-source-race-first.log`: errorlog 3.703s, SDK 19.543s, OAuth 3.020s, secure storage 2.878s, global config 5.360s, startup environment 8.265s passed. CLI 539.975s failed media cancellation, pane, and in-progress Ultracode checks. Application 289.028s failed pane and the then-mismatched TUI history Python caller. The parent producer delta and latest tests landed after that full run's compile and have separate focused evidence above. The parent owns final frozen integration verification; this leaf does not relabel those failures green.

Remaining boundary: other generic log/MCP debug callers, OpenAI storage's existing logging callback, Axios enrichment, and CLI debug/hard-fail argument admission are outside this legacy-getter consumer repair. They are not claimed by this evidence. Source Keychain prefetch itself was not implemented here. New signed/native UI stderr validation must use a package built after these changes; P285 remains older evidence.

## Early child-constructor reporting integration delta

A later actual interactive Workflow stage test exposed an error in the first child logger bridge: before a Controller exists, native model identity may fail the real legacy-key primitive and report it. Calling the generic tool-context copy bridge then asks a nil Controller for its native-copy binding and returns `ErrRestricted`. It did not reach parent delivery. The actual red is `/tmp/go-ultracode-actual-child-stage-sixteenth-1791419556.log` (prepare passed; child `NewMainPrint` failed before `StartSession`). This evidence supersedes the earlier started-child-only coverage.

`native_error_log.go` now snapshots only the error's immutable name/message/stack values. It submits real parent `Read` work, evaluates parent metadata/sink on that parent's actual Scope, and awaits its real child-owned Future. No Controller or tool-copy ledger is needed for the diagnostic message. The generic `nativeAgentOwnerCall`, copied context validation, and continuation guards are unchanged. Reporting backend failure is observed as failed delivery and retains original recoverable `logError` semantics; invalid caller/parent identity and child administrative lifetime still remain errors.

The dedicated regression constructs a real interactive independent MainPrint with an actual failing private security primitive, observes the parent diagnostic, retains the original managed-key config fallback and unknown global fields, and starts/closes the child. The parent-continuation stage fixture also now passes. A held parent delivery test verifies the child's actual Future admits its real timer and is released by child Owner close; the original parent `Read` then completes on its legal Owner. A closed parent reporting backend does not become an auth failure. No security success or API-key approval is fabricated.

`/tmp/go-native-errorlog-early-child-lifetime-third.log`: all logging Source/actual child/foreign/close/fallback/lifetime cases passed, 13.804s. Additional Source neighbors/vet are recorded in the final handoff. Root/interactive owner separately own exact Workflow child-count, TeamTerminal137, and the next full frozen verify; this logger delta does not claim those outcomes before their real consumers pass.

Final delta checks: Source/auth model-identity neighbors passed 39.559s in `/tmp/go-native-errorlog-early-child-source-neighbors-fourth.log` (OAuth/secure packages in that filtered invocation had no selected tests). A separate complete foundation Source/race passed errorlog 3.890s, OAuth 2.668s, secure storage 1.838s, ownerasync 2.528s in `/tmp/go-native-errorlog-early-child-full-foundation-sixth.log`. Real early constructor, parent continuation, parent-close reporting, child Owner close, and caller Scope cancellation repeated three times passed 11.957s in `/tmp/go-native-errorlog-early-child-cancel-close-seventh.log`. Caller cancellation leaves the original immutable error queued on its real parent Read rather than executing parent metadata outside an Owner. Seven-package vet passed before the final test-only cancellation addition and is rechecked at final freeze.
