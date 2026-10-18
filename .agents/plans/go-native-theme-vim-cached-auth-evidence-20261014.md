# Theme, local Vim command and cached subscriber request evidence — 2026-10-14

This closes these focused consumers on the current branch. It does not certify the entire migration, whole changed coverage, cross-machine acceptance, or Benchmark 120. No branch, commit, merge, live model call, Claude login or token refresh was performed.

## Frozen Source and Theme

Nine relevant Source files match MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155` byte for byte. SHA-256 receipts are in `artifacts/go-migration-20261013/cli-flags/theme-vim-source-shas.json`.

`native-terminal-theme-picker.fixture.ts` executes the actual ThemeProvider and compiled ThemePicker component bodies, retaining their state and compiler memo caches across rerenders. Its external React/context/store primitives are controlled, disposable fixtures. It also executes actual StructuredDiffFallback, wrapText, diffWordsWithSpace, and getTheme. This is a component-body oracle, not an original Ink PTY simulation.

The Go Source regression covers 48 picker scenarios and two environment-rerender scenarios: six palettes and six terminal widths; focus-only preview; syntax toggle and cancellation; the two Source persistence calls on selection; environment falsy values including JavaScript FEFF whitespace; mount-time color reason and theme-keyed syntax-theme memo; and the optional AUTO_THEME build feature. The optional feature case explicitly injects the feature into the original component body. The actual normal distribution and public Go entrance expose six options, with AUTO_THEME disabled.

The normal frozen Bun resolution points `color-diff-napi` at `stubs/color-diff-napi.ts`. Its renderer returns null and syntax-theme result contains the raw theme with null source. Go retains this actual shipped boundary and renders the original word-highlighted fallback demonstration. It does not claim the unused native-ts grammar implementation as shipped behavior. Source role colors, dimmed gutter, changed words, spaces, wrapping and dashed preview borders feed the real TUI. The custom syntax shortcut uses the existing keybinding parser, pending chord state and displayed binding.

The actual application consumers read existing global/startup/user settings on the canonical owner. Focus previews do not write the theme. Ctrl+T writes the existing user settings key and refreshes the active settings consumer. Child cancellation keeps the independently changed syntax preference; outer Config cancellation restores its original snapshot, including deletion when the field was absent. Selection preserves both Source save calls and publishes preferences to the same terminal state. Unknown user/global fields remain intact.

`TestActualCLIThemePreviewSyntaxRollbackCommitAndNextPromptPTY` runs public RunCommand under a real PTY. It observes the light RGB fallback palette, verifies global/user files before and after focus/toggle/cancel/commit, submits a restored stashed draft and a subsequent human input to the same Harness and physical loopback model, observes the committed ANSI bright-red prompt role, and verifies real slave termios restoration. Its color observations decode combined SGR parameters; the physical renderer may combine adjacent style escapes. The Go global writer omits default `dark`, so the fixture reads the actual default instead of demanding a redundant persisted field.

## Actual local /vim consumers

The new command reads and saves the existing global editor mode, implements Source truthiness and legacy `emacs` handling, returns the exact Source text, reports the original analytics event through the existing reporter, and publishes live editor-mode changes. The descriptor is local and unsupported in noninteractive sessions. Native interactive input processing and registry publication also admit the real local executor; the command does not query a model.

`native-terminal-vim-command.fixture.ts` executes the unchanged Source descriptor and call body for 11 old/normal/Vim/unknown/falsy/truthy inputs. Application tests compare exact returned mode/text, retain unknown global data and reject the headless entrance. The actual owned local-input test verifies canonical caveat, command input and local stdout in the existing conversation.

Three public PTY flows remain separate: Config switching, standalone `/vim`, and a `/vim` entered while the first real model POST is held. The queued case observes `1 queued`, verifies the mode remains normal before queue execution, releases the actual HTTP response, then observes the same native owner toggle and live mode publication. It continues actual operators, yank/paste/undo, astral Esc→x and the lone high surrogate reaching canonical model HTTP, then switches back to normal input. These use the existing 443-case actual Source Vim oracle and same Harness. No queue, FIFO, timer or golden sorting rule was changed to obtain the result.

## Cached subscriber request

The positive fixture has only fake future-expiry cached `.credentials.json` and private account metadata; no API key or environment auth token. Source client construction chooses the cached access token regardless of the normal loopback base URL, omits x-api-key, and accepts ordinary `USER_TYPE=external`. The independently owned `terminal_usage_cached_client_source_test.go` executes actual original getAnthropicClient and the SDK HTTP request for subscriber and external-key branches.

The Go defects were separate real consumers:

- The shared sealed NativeClaudeAuth binding previously admitted only desktop entrypoints, so ordinary CLI fell through to the API-key helper.
- Native request construction rejected every nonempty USER_TYPE, although Source considers only exact `ant` internal. Non-ant native requests are now admitted; internal and finite-profile negative cases remain guarded.
- Request admission rejected shared TLS options on an HTTP endpoint even with actual subscriber/TLS producers. The client already captures the original process TLS state for this branch.
- Native SDK HTTP rejected those captured TLS options on plaintext, unlike Source fetch and the existing RawHTTP1Exchange/native redirect paths. Plain HTTP now ignores a copied TLS configuration, without mutating the shared exchange; HTTPS and owner/transport guards remain intact.
- The cached client emitted an empty x-api-key header rather than the Source null omission. Both normal and validation cached-token branches now omit it.

`TestCachedSubscriberActualNativePrintSameHarnessHTTPAuthAndSharedTLS` proves real ordinary NativePrint→Harness→POST with `Authorization: Bearer fake-usage-token` and no x-api-key header, while retaining the private TLS settings required by Usage. Physical transport tests cover verified/resolver/both TLS fields on HTTP and confirm no resolver call or shared configuration mutation. Existing TLS trust, foreign transport, internal identity and finite-profile guards are still exercised.

The new `ObserveOpenFailure` seam observes a final failed Open exactly once and cannot change its returned error/request. The existing per-attempt `ObserveFailure` retains its raw SDK error pointer and retry semantics. A same-area test verifies both pointer identities and silence on a real successful request. Initial failed probe logs preserve the actual underlying boundaries; no fake token was added to bypass them.

The formerly unreachable Usage global-close POST fixture exposed an unread POST-body server issue; that fixture and its independent public Usage acceptance are owned by lifecycle. The focused evidence here does not relabel an older failed close run as passing. Real Claude OAuth and official cloud remote/teleport account acceptance remain unperformed under the current user boundary.

## Observed verification

The final focused race command was:

```text
test -race ./internal/tui ./internal/application ./internal/requestretry ./internal/providernative ./internal/requestcontext ./internal/sdkpermissions ./internal/sdkhost -run '^(TestThemePicker.*|TestTerminalTheme.*|TestActualCLITheme.*|TestTerminalVimCommandActualSource.*|TestActualCLIVim.*|TestInteractiveNativeVimLocalInput.*|TestCachedSubscriberActualNativePrint.*|TestOpenFailureObserver.*|TestNativeHTTPSharedTLSOptions.*|TestNativeBridgeFiniteConfigurationGuards.*|TestNativeHTTPSCAFailuresRetainNativeSDKRecordWithoutPOST|TestNativeExternalUser.*|TestThemeSyntaxPreference.*|TestActualSourceCommandDescriptionAnnotations.*)$' -count=1 -timeout=120s -v
```

It passed with exit 0: tui 4.645s, application 31.396s, requestretry 2.114s, providernative 3.699s, requestcontext 5.330s, sdkpermissions 6.105s and sdkhost 5.111s. The application run includes all three Vim PTY profiles. `vet` passed for application, tui, clibootstrap, sdkhost, requestretry, requestcontext, provider, providernative and sdkpermissions. `git diff --check` passed. Logs: `theme-vim-cached-race-final.log`, `theme-vim-cached-vet-final.log`; earlier transient shared-tree compile failures and fixture diagnosis logs are preserved beside them.

All commands use the task-private Go/Bun environment, pinned Source Bun 1.3.14, ready controlled dependency seed and temporary user state. The task's stopped 3.7 GiB compiler cache was cleared with `go clean -cache`; the runner now reuses the approved root compiler cache, which was not cleaned or modified by cleanup. These are focused deterministic Source/physical/race results, not the parent's final immutable quality gate.
