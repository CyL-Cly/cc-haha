# Public Go CLI debug arguments → existing native sink (2026-10-08)

Scope: public `--debug`/`-d`, `--debug-file`, `--debug-to-stderr`/`-d2e` now reach the existing native error/debug writer through the actual public application parser and MainPrint. No new logger, Agent executor, SDK output consumer, registration change, or host/inbox/schema change. Original TS production files were read only. All runs used disposable HOME/config/tmp, fake credentials, and private loopback model transport. No signed package, live provider, real UI or complete verify was run by this owner.

## Original contract and implementation

Source `src/main.tsx:974` registers debug and debug-file with boolean coercion; `src/utils/debug.ts` independently reads raw process.argv. The Go command retains a cloned raw argv in PrintArguments.DebugArguments and marks DebugFormatted only for noninteractive JSON/stream-json, corresponding to original main.tsx:2623. initializeNativeErrorLog passes those fields and the existing Error writer to NativeSink. The ordinary startup producer (`Error log sink initialized`) and actual legacy authentication error producer use the same previously attached errorlog.Service/NativeSink and existing Close path. The actual early-child immutable-error ownerasync bridge remains unchanged.

`--debug=api,hooks` is a filter; separated `--debug api` is not a filter. Attached `-dapi` is accepted by Commander but does not match original raw argv debug-mode detection. `-d2e` does match original stderr detection. The first raw debug-file occurrence wins, even though Commander stores a boolean and subsequent occurrences still parse. Stderr takes precedence over a file. DEBUG/DEBUG_SDK truthiness, external default disablement, NODE_ENV=test except stderr, minimum verbose/debug/info/warn/error, mixed filter fallback, uncategorized exclusion, MCP/server categories, secondary categories, and multiline JSON formatting match original producer output.

NativeSink now applies all five Source levels (fallback minimum debug), first-file priority, and Source MCP category extraction. Explicit debug is synchronous; ant default remains buffered with original one-second/100-line cutpoints and Flush/Close drains. CLAUDE_CODE_DEBUG_LOGS_DIR retains Source's file-path meaning. append failure in explicit mode aborts startup; buffered/error-reporting failure retains the existing Source catch semantics. The latest symlink remains best effort and once per sink. A real regression found os.Remove could remove an empty latest directory although original fs.unlink fails and preserves it. nativeErrorLogFiles.Remove now preserves directories and returns EISDIR; the actual original CLI and Go CLI both retain that directory while completing their query.

The native diagnostics JSON segment writer is a separate Source facility controlled by CLAUDE_CODE_DIAGNOSTICS_FILE; debug flags do not fabricate or mirror that JSON stream. Its whole-package regression remains green. Existing native log-file privacy modes (0700 directories / 0600 files) remain in place.

## Changed paths

Production:
- backend/internal/application/command.go: bounded debug admission and raw argv/formatted bind.
- backend/internal/clibootstrap/main_print.go: typed PrintArguments fields and same raw bind in the existing print parser.
- backend/internal/clibootstrap/native_error_log.go: same sink Arguments/Stderr/Formatted options; original unlink directory behavior.
- backend/internal/errorlog/native_sink.go: reused Debug API, five levels, first-file selection and MCP categories.

Regressions:
- backend/cmd/claude-haha/main_debug_process_test.go: 20 actual Go CLI profiles and 12 actual original CLI profiles; real private HTTP request/result, files/symlink/append failure/stdout/Close.
- backend/internal/application/command_debug_test.go: seven Commander-coerced argument forms, raw clone identity, text/JSON formatting, preserved unrelated deprecated MCP guard.
- backend/internal/clibootstrap/native_debug_test.go: two actual MainPrint→legacy auth primitive failure→same sink consumers, error category and newline JSON formatting in file/stderr modes.
- backend/internal/errorlog/native_debug_source_test.go: 28 original debug writer/category/level/raw argv profiles with physical before/after files, symlinks, Flush/Close and stderr byte comparison.
- scripts/quality-gate/go-parity/native-debug-cli.fixture.ts: original public main/Commander/startup/sink/QueryEngine/result; only private peer and original external machine primitives isolated.
- scripts/quality-gate/go-parity/native-debug-sink.fixture.ts: actual original debug/error sink implementations, fixed clock and physical filesystem completion barrier for latest, no sleeps.

## Red → green evidence (logs preserved)

- `/tmp/go-debug-public-cli-intended-red-first-1791431139.log`: intended red public --debug-file, equals-file and repeated-file syntax rejected by application guard; exit1 / zero private HTTP requests / no stdout.
- `/tmp/go-debug-source-and-public-physical-second-1791431261.log`: Source writer already green; two public tests incorrectly placed optional -d/--debug immediately before the prompt, so the original Commander-compatible parser consumed it. Corrected test argv order without changing parsing. An ant public-CLI attempt encountered the existing internal schema host guard; that distinct profile remains guarded, not broadened by this work. The ant sink's actual original buffering/flush comparison remains in the writer profiles.
- `/tmp/go-debug-source-public-private-full-third-1791431381.log`: Source writer3.390s; original public CLI9 + physical public CLI18 profiles24.036s PASS.
- `/tmp/go-debug-native-auth-actual-fourth-1791431448.log`: first constructor test reached secure OAuth lookup rather than legacy auth-error reporting; kept red. The repaired test invokes actual native binding.apiKeySource on its real Owner, never a synthetic log injection.
- `/tmp/go-debug-native-auth-actual-sixth-1791431548.log`: actual MainPrint legacy auth-error producer file/stderr PASS2.522s.
- `/tmp/go-debug-source-actual-failure-and-symlink-fifth-1791431498.log`: actual original append-error branch green; Go latest-directory regression red because os.Remove deleted that directory. Preserved red before narrow unlink fix.
- `/tmp/go-debug-final-source-physical-seventh-1791431619.log`: Source errorlog5.008s and bootstrap7.324s green; CLI dynamic compilation reported `imregistration/qq_http.go:7 could not import errors (open : no such file or directory)`. This transient shared build failure was not counted as a debug business regression or a pass; fresh full run below is authoritative.

## Final checks, all exit0

Pinned Bun: `/var/folders/sx/hl8zlmxd7gx9b58_1t2zhp5r0000gn/T/cc-haha-pr1441-bun-6exhle0_/bun-darwin-aarch64/bun`.

`BUN /tmp/go-current-test.ts ./cmd/claude-haha ./internal/errorlog ./internal/cliargs ./internal/diagnostics`
- `/tmp/go-debug-final-cli-errorlog-parser-diagnostics-full-eighth-1791431735.log`
- Actual Source + race whole packages: CLI64.614s, errorlog9.037s, cliargs6.062s, diagnostics1.564s. Includes final 20 Go CLI/12 original CLI/28 Source writer profiles and physical latest-directory preservation.

`BUN /tmp/go-current-test.ts ./internal/application ./internal/clibootstrap -run 'Debug|LegacyKeychain.*AgainstActualSource|NativeErrorLog|NativeCommand|PublicJSONSchema'`
- `/tmp/go-debug-final-app-bootstrap-boundaries-ninth-1791431736.log`
- Source/race focused: application6.803s and clibootstrap13.705s PASS. Includes actual legacy Error producer and prior error/reporting owner consumers.

`BUN /tmp/go-ultracode-vet-private-1791420065619366000.ts ./cmd/claude-haha ./internal/errorlog ./internal/cliargs ./internal/diagnostics ./internal/application ./internal/clibootstrap`
- `/tmp/go-debug-final-six-packages-vet-tenth-1791431737.log` exit0.

`git diff --check` and no-index whitespace checks for six newly added Go/Source files passed. Shared existing changes preserved; no branch/commit/PR. Whole application/bootstrap packages, complete verify, signed package and live model were not run here; Root owns final integration/official verification.

## Boundaries and freeze

This item enables these public flags and proves their real startup/auth error consumers. It does not claim migration of every original API/hooks/MCP debug message callsite or runtime `/debug` producer. The existing internal USER_TYPE=ant CLI host-profile guard remains; its debug writer default/buffering contract is independently proven against actual Source, without expanding that unrelated host capability. No guard is relaxed for any unported consumer. Production, tests, fixtures and this document are frozen after the final results above.
