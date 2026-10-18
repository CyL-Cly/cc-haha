# Native Vim input evidence — 2026-10-14

This component uses the current branch and frozen Source MAIN `6d21316ec4635f29ecbaa43e1a5574bfd31d7155`. It does not declare the migration complete. No commit, branch change, merge, real Claude OAuth, or live model quota was used.

## Implementation and public consumer

The dedicated `backend/internal/tui/vim_{types,cursor,objects,operators,transitions,input}.go` helpers implement INSERT/NORMAL and all pending command states, counts, operators, logical/visual motions, finds, text objects, registers, paste, repeat, undo, replace, case changes, indentation and line insertion. The TUI owner's central `State` hooks give modal permission/question/picker input priority, then process Vim before ordinary Chat bindings. Config reads/writes the real global `editorMode`; the ordinary footer observes the actual mode.

Vim retains an original UTF-16 field independently of its terminal display projection. Source Esc decrements one code unit, including inside an astral character. `vimSubmissionTextJSON` expands original pasted-text reference slices once without losing lone units. The optional `Input.TextJSON` is a JSON string, and the root owner's existing Session/media producer validates it and admits it through the same Harness. A surrogate rendered as a replacement glyph remains its original unit in the request field.

## Actual Source and deterministic evidence

`scripts/quality-gate/go-parity/native-vim-input.fixture.ts` executes the actual unchanged `useVimInput` body with controlled React storage and base input I/O; actual frozen `transition`, operators, text objects and `Cursor` perform the Vim behavior. This is a hook/state-machine oracle, not a full original Ink PTY. The strict comparator recursively decodes JSON strings to code units, so different lone surrogates cannot alias through Go's ordinary JSON string decoder. Final coverage is **443 scenarios**, including counts, NaN pending states, Number rounding for raw large prefixes, cancellation, narrow wrapping, Unicode and image chips. Earlier progress messages' 422 count was corrected to the observed log count; the final corpus added 12 scenarios after the 431 case checkpoint.

The seven hook/Vim/Cursor files were SHA256 compared with the frozen Git object and matched exactly. Original Source behavior retained includes linewise motion ranges that do not extend `from` to line start, visual-line indexing by join/indent/open/paste, `dd` dot replay's unrecognized motion, single-code-unit Esc, and Source wrapping whitespace. Source production files were not changed.

Observed commands:

```text
bun artifacts/go-migration-20261013/cli-flags/run-focused.ts test -race ./internal/tui ./internal/application -run '^(TestVimInputActualSourceStateMachineOperatorsRepeatsAndUTF16|TestVimTerminal.*|TestActualCLIVimConfigOperatorsUTF16SameHarnessAndNormalPTY)$' -count=1 -timeout=120s -v
```

Final focused race: TUI **passed 3.725s**, application **passed 7.212s**, physical PTY case **passed 5.17s**. The broader TUI race run passed **22.303s** before the final raw-count corpus/parse extension; the extension was exercised by the final focused race. `go vet ./internal/tui ./internal/application` passed, and focused files are gofmt formatted. No full backend or official gate was run by this component.

The public Python PTY starts actual `application.RunCommand`, opens `/config`, persists Vim, performs paste/escape/delete/replace/undo/yank/paste, and compares the actual canonical POST against the original hook's final string. A second turn types emoji, Esc, x, Enter and physically proves request user content `[0xd83d]`. A third turn switches Config back to normal and verifies normal text reaches the same Harness. Unknown global fields survive, requests remain three, and physical terminal state restores. The credential primitive is an explicit private `RunSecurity` substitute and the model transport is fake loopback.

Initial red logs are preserved. Two product discrepancies (linewise range start and wrapping/visual offset details) were fixed in the native engine against original Source. PTY fixture corrections preserve original assertions: the global writer legitimately omits default `editorMode: normal`, and Darwin's existing read-only FIONREAD observation settles XNU PENDIN before the physical termios comparison. No terminal attributes are changed by that observation.

Artifacts remain under `artifacts/go-migration-20261013/cli-flags/vim-*`. Windows ConPTY, full cross-surface acceptance, final quality gates and paired Benchmark belong to final root verification and are not substituted by these component receipts.
