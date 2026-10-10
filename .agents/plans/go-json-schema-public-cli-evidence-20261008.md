# Public CLI --json-schema → StructuredOutput

## Original behavior and scope

The original `src/main.tsx` registration already recognizes `--json-schema <schema>`. Its action parses the string only for a noninteractive session, creates the original `SyntheticOutputTool`, and adds that tool after normal `--tools` filtering. Invalid schema compilation logs a failure and continues without installing the tool; malformed JSON rejects before the model runs. Parsed falsy schema values do not enable structured output.

`src/tools/SyntheticOutputTool/SyntheticOutputTool.ts` validates through pinned Ajv with all errors, always allows its read-only tool, preserves its dynamic input JSON schema, and returns an actual `structured_output` attachment. `src/QueryEngine.ts` captures that attachment for this ASK's successful `result.structured_output`. The capture resets each ASK. Its Stop function hook (`src/utils/hooks/hookHelpers.ts`, `messages.ts`) requires the latest StructuredOutput call to have a non-error result; SIMPLE keeps its original hook skip behavior. Retry limiting reads the current mutable history, subtracts the count before the current ASK, counts one assistant message containing a StructuredOutput call (not each block), and checks after every user yield. All calls count, including a valid call at the threshold. The terminal is `error_max_structured_output_retries`, with no success result or structured output. The original default is `parseInt(MAX_STRUCTURED_OUTPUT_RETRIES || '5', 10)`.

This batch implements that public flag only. It does not modify CLI registration, coverage declarations/statistics, the inbox consumer, provider routing, or introduce another Agent or validator runtime. Actual Go CLI tests build a disposable private CLI binary with the existing `native_process_fixture` external OS boundary; this is not a desktop/package build. No real user configuration, keychain, account, provider quota, UI, signed package, or whole repository verify was used by this child task.

## Concrete changes

- `backend/internal/application/command.go`: admit the registered flag and pass its original string through to MainPrint, including inline `--json-schema=...`. Parsing is deferred so interactive mode ignores it as Source does.
- `backend/internal/clibootstrap/main_print.go`: schema argument/state declarations, bounded normal print parser admission, initialization, and the shared Host binding.
- `backend/internal/clibootstrap/native_json_schema.go`: parse the CLI string, compile through the existing pinned Ajv primitive, construct the existing synthetic tool after normal filters, and emit the original structured-output analytics event. Detached/workflow child schemas retain their own existing binding.
- `backend/internal/sdkhost/native_json_schema.go`: actual parent compiler/tool identity and owner binding, per-ASK watermark and output capture, current-history retry limit, real terminal result with accounting/checkpoint, and original Meta Stop feedback through the existing query loop consumer.
- `backend/internal/sdkhost/public_print.go`: typed schema field and strict parent installation dispatch; ordinary unbound sessions do not expose StructuredOutput.
- `backend/internal/sdkhost/result.go`: project the actual current-ASK attachment on successful result only.
- `backend/internal/sessioncontroller/synthetic_tools.go`: explicit compiled public-parent binding branch with native/public/root/owner constraints; preserve the original workflow-agent branch and canonical tool registry.
- `backend/internal/sdkhost/host.go`: four narrow integration calls were applied by the owning diagnostics agent: begin per-ASK state, observe actual yields, check retry limit after SDK user yield, and pass the original Stop emit callback. This child did not edit that file or its concurrently owned inbox branch.

Regressions: `backend/cmd/claude-haha/main_json_schema_process_test.go`, `backend/internal/application/command_json_schema_test.go`, and `scripts/quality-gate/go-parity/native-json-schema-cli.fixture.ts`.

## Original Source and physical evidence

The new fixture awaits the original exported `src/main.tsx.main()` with the original Commander argv/action, startup, QueryEngine, tool implementation, validation, attachment/result consumer, transcript and process shutdown. It uses a private Bun HTTP peer, fake explicit API key, disposable HOME/config, and the existing deny capability boundary. The only absent/fake external primitives are OS managed settings, empty keychain prefetch/store, an exact ioreg machine identity command, and the exact read-only IDE process scan returning an empty private process list. Every other process/network/user-state operation stays denied. Awaiting the original exported main observes its malformed-schema rejection without a fixture HTTP server keeping the unawaited CLI bootstrap promise alive. No Source algorithm is replaced or copied.

Both original Source and actual Go public CLI cover these eight cases:

1. Invalid tool input → original schema feedback → valid tool input → successful final JSON result with actual `structured_output` (three model requests).
2. Ordinary nonbare print initially ignores the schema → original Meta Stop feedback → valid StructuredOutput → successful result (three requests). The Go peer asserts the original feedback enters the second real API request.
3. One actual SDK/CLI process with three sequential ASKs: first captures structured output; second returns plain text without stale output; third gets two invalid calls and the proper retry terminal. Inputs are released by observed prior results, without sleeps or changing Source's in-flight input contract (five model requests, three results).
4. Two invalid calls reach the custom retry maximum; exit 1, exact error subtype, actual count, no `structured_output`.
5. A valid call at maximum 1 still hits Source's retry terminal; exit 1, one model request, no `structured_output`.
6. Invalid schema compilation installs no synthetic tool, performs one model request and returns normal success text.
7. Parsed `false` schema is ignored and returns one normal success request, with no synthetic tool/output.
8. Malformed JSON exits 1, performs zero model requests, has no result stdout, and reports an error.

The application regression verifies original string propagation, separate/inline syntax, interactive invalid JSON deferral, and falsy schema admission. Existing real Workflow schema agents, parallel schema tools, agent hooks, and normal text/JSON/SDK input regressions are included in the neighborhood validation.

## Red to green and commands actually run

- Intended production red: `/tmp/go-json-schema-public-cli-intended-red-first-1791426156.log`, actual public Go CLI recognized syntax but rejected `CLI option --json-schema`; success/retry/invalid-schema paths had zero requests. This is preserved and is not a synthetic-tool-only test.
- Source fixture setup failures are preserved separately. The second/third Source runs reached sandbox/OS primitives and the unawaited malformed-JSON bootstrap promise; subsequent runs bound only the specific external primitives described above. No Source guard was removed or general subprocess capability granted.
- `/tmp/go-json-schema-parent-declarations-stable-fourth-1791426659.log`: coherent declarations plus existing real application text/JSON/SDK paths PASS 3.529s; SDK host compiled (no matched tests). Earlier root/diagnostics activity snapshots with missing fields are preserved as transition compile failures, not claimed as business reproductions.
- `/tmp/go-json-schema-original-public-source-private-machine-seventh-1791426833.log`: original public Source CLI success, validation retry, invalid schema and malformed JSON PASS 5.585s.
- `/tmp/go-json-schema-public-source-and-physical-eighth-1791426930.log`: original Source plus actual public Go CLI first four full paths PASS 19.934s. The preceding sixth run's empty text output came from a test peer missing `text_delta`; it was repaired to the real streaming protocol, with assertions unchanged.
- `/tmp/go-json-schema-nonbare-and-per-ask-source-probes-fourteenth-1791427557.log`: simultaneous SDK inputs were coalesced/interrupted by the original in-flight input behavior. The regression now uses actual result barriers. Its successor fifteenth run passed both Source and Go sequential SDK queries; only the distinct nonbare OS IDE discovery primitive remained blocked, then was bound explicitly.
- `/tmp/go-json-schema-full-source-actual-eight-profiles-seventeenth-1791428115.log`: all eight original Source and actual Go CLI profiles PASS 23.231s, including current-history/per-ASK count, valid-at-limit, nonbare Stop feedback, and final structured-output/error result behavior. Command: pinned Bun `/tmp/go-current-test.ts ./cmd/claude-haha -run 'JSONSchema'` (`go test -p=4 -race -count=1`, private offline environment).
- `/tmp/go-json-schema-parent-worker-and-entry-neighborhood-eighteenth-1791428219.log`: application PASS 6.072s, clibootstrap PASS 14.760s, SDK host PASS 1.779s, JSON Schema validator PASS 5.031s, queryloop PASS 5.501s. Controller compiled with no matched tests and is covered by the whole-package run. Command: same wrapper `./internal/application ./internal/clibootstrap ./internal/sdkhost ./internal/sessioncontroller ./internal/jsonschemavalidator ./internal/queryloop -run 'JSONSchema|Synthetic|Structured|Workflow.*Schema|AgentHook|NativeCommand'`.
- `/tmp/go-json-schema-final-eight-packages-vet-twentieth-1791428292.log`: hermetic `go vet` exit 0 for public CLI, application, clibootstrap, SDK host, sessioncontroller, JSON Schema validator, CLI args, and queryloop. Uses the existing private vet wrapper.

- `/tmp/go-json-schema-public-cli-five-whole-source-race-nineteenth-1791428291.log`: full Source/race, process exit 0. Public CLI PASS 45.586s, SDK host PASS 17.888s, sessioncontroller PASS 39.264s, JSON Schema validator PASS 9.605s, CLI args PASS 5.715s. Command: pinned Bun `/tmp/go-current-test.ts ./cmd/claude-haha ./internal/sdkhost ./internal/sessioncontroller ./internal/jsonschemavalidator ./internal/cliargs`.

Final `git diff --check` and owned new-file whitespace review passed. Existing shared changes were preserved, and no registration or coverage file was edited. All files owned by this batch (production, tests, Source fixture and this document) are frozen after this entry. Root owns subsequent signed-candidate/official model evidence, selected quality checks and repository-wide verify. No broader flag capability or all-feature completion is claimed here.
