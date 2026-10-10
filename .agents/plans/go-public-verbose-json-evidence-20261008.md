# Public Go CLI verbose JSON and original replay behavior

This change implements `--print --verbose --output-format json` through the existing application/MainPrint/SDK Host. The original CLI prints a filtered full SDK message array; ordinary JSON retains only the final result. No separate Agent, provider loop, transcript store, or TypeScript production fallback was introduced.

The original `src/cli/print.ts` retention predicate and JSON switch case are extracted structurally and executed in `native-public-print-json.fixture.ts`. Forty-six original-output profiles are replayed twice and compared byte for byte with Go: all control/stream/keep-alive/streamlined/prompt-suggestion events; five late SDK-only system events; ordinary and future envelope types; missing/non-result final messages; success/error results; nested property order, lone UTF-16 surrogate and U+2028. Nonverbose output does not retain the full array. The existing JavaScript-compatible serializer supplies original `JSON.stringify` bytes.

An actual original public Commander/MainPrint/QueryEngine run exposed a shared Harness bug: Go always emitted an initial user replay, while Source defaults replay off. Host now samples the actual Controller option on its owner before acknowledging. Original `conversationService` always supplies `--replay-user-messages`, so DesktopFactory now binds that option explicitly. A true physical desktop factory regression first failed with zero replay messages, then passed with the original single acknowledgment and identity. Existing replay-enabled SDK Source tests remain unchanged.

The actual original CLI also rejected four format combinations before any provider request. Go now preserves their message and original stderr line-ending distinction through the real entrypoint. This includes partial output outside print/stream-json, replay without both stream-json formats, stream-json input with JSON output, and terminal `--no-session-persistence`. No new permission or persisted configuration shape was added.

## Preserved failures and observed checks

- `354` intended red: public verbose JSON was rejected before Harness startup; single-result JSON remained valid.
- `355` Source fixture setup failed because an empty Go slice serialized as null; corrected only the test input to `[]`.
- `356` original-byte comparison failed on U+2028 escaping; fixed actual output with the existing JavaScript serializer.
- `358` an invalid Source test argv combined partial output and JSON; Source correctly rejected it. That prompted the real format-combination regression instead of changing Source behavior.
- `359` actual original CLI exposed the extra Go replay and missing/mismatched format rejection messages. Original errors made zero physical model requests.
- `362` actual DesktopFactory exposed the missing explicit desktop replay binding after the shared Host fix.
- `357` affected application/formatting and prior text/stream result regressions passed, 4.863s.
- `360` Source/application focused 43.856s and SDK Host 9.505s passed.
- `361` latest exact stderr rendering + Source/application focused 40.168s and SDK Host 8.005s passed.
- `363` real Go executable: verbose array and five early rejection profiles passed, 20.159s. Rejections left private config absent, stdout empty and provider requests zero; stderr bytes matched Source. Successful execution used the real shared Harness against a physical private peer.
- `364` latest Source/physical application, dedicated SDK socket, desktop replay and shared file/resume regressions passed, 11.914s.
- `365` entire SDK Host Source/race package passed, 20.243s.
- `366` actual CLI/application/SDK Host vet exit0.
- `367` entire CLI Source/race package passed, 65.356s.

Every deterministic test used disposable HOME/config and a private model peer or pure original output consumer. Original debug CLI fixture was reused read only for actual Commander startup/query/output. These checks do not constitute official-model, signed GUI, complete migration, complete verify or Benchmark-score evidence. Root still must rebuild the candidate and verify model-dependent behavior with official DeepSeek.

## Changed files

Production: application `command.go`, `command_json_output.go`, `command_error_output.go`, `desktop.go`; shared SDK `host.go`; real CLI `main.go`.

Regressions: `command_json_source_test.go`, `command_verbose_json_test.go`, `desktop_replay_test.go`, `main_public_json_output_test.go`; original fixture `scripts/quality-gate/go-parity/native-public-print-json.fixture.ts`.
