# Original-input resume state contract

This is a finite, production Go port of the noninteractive
`src/utils/conversationRecovery.ts:loadConversationForResume` orchestration. It is not
the complete CLI host, desktop history API, or a replacement for a transcript
writer. No production TypeScript fallback is used.

## Production chain and host API

`resumestate.New(Options)` requires explicit private/runtime paths, original
working directory, current temporary session ID, environment and arguments,
real filesystem and stat implementations, a shared `clisettings.State`, policy
paths, the real `sessionhistory.Loader`, real `sessionresume.Reducer`, independent
date parser, UUID/clock, and diagnostics/error/phase callbacks. Missing
dependencies and interactive hosts are rejected. The filesystem interface
performs actual reads, writes, directory operations, hard links and copy
fallbacks. No restore callback supplies a selected log or a prepared history.

`Load(Input)` independently selects from original inputs with Source priority:

1. Absent Source selects CLI `--continue`, even when a JSONL path is supplied.
2. Present Source plus JSONL path calls the actual JSONL loader and bypasses the
   original LogOption state-restore branch.
3. An original ASCII UUID string selects `getLastSessionLog` semantics.
4. An original LogOption can hydrate its empty messages from its original
   `fullPath`, then restores plan, starts file-history copying, checks the last
   consistency checkpoint, filters malformed attachments, restores invoked
   skills and one-shot skill-listing suppression, runs the real reducer, and
   resolves the finite SessionStart profile.

SID selection uses newest non-sidechain message of any supported transcript
type. Lite/full-log selection uses the newest valid user/assistant leaf.
`--continue` scans actual regular UUID JSONL files, independently stats and sorts
them, reads bounded 64 KiB head/tail metadata, and filters Source sidechain/team
patterns. The existing Loader reconstructs raw parent/branch/compact history;
it is not fed a Source-selected SID, tip, chain, or serialized Source result.
The narrow `sessionhistory/export.go` wrapper exposes that same chain and
removeExtraFields operation, and actual metadata maps without converting
undefined to JSON null. Raw object/array observers cannot acquire a persisted
object reference identity.

`State()` clones caller-visible raw values. `AwaitBackground()` is a separate
completion boundary: returned state before a held real hard link is compared
separately with state after completion. Backup destinations use the temporary
session ID captured at restore, preserving original backup bytes. Successful
copies buffer real startup entries without making a metadata-only transcript.
Native tests verify actual hard links, independent copy fallback, copy/read/
mkdir faults, settings reset during held copying, and pending-entry ownership.
The Source fixture observes real `recordFileHistorySnapshot` calls rather than
inspecting Project's private writer queue. Those calls and queue entries agree
for the captured default persistence profiles; writer materialization, flush,
queue reset and other host persistence modes remain unproved.

`resumestate.Prepare(result, originalContext, dependencies)` accepts only the
actual restored messages and rejects any alternate `Context.History`. It calls
the shared independent request-context preparation; retries reuse that
preparation through `BuildAttempt`. Supported request fixtures then use the
real provider client and an actual loopback HTTP socket.

## Proven profiles and refusals

The frozen corpus has 86 original-input profiles, replayed through the actual
Source caller twice and independently through Go. It compares exact serialized
result property order, undefined omission versus null, unknown message and
metadata fields, UTF-16 values, plan and backup bytes, phases, allocations,
diagnostics, errors, before/after state, and original-file preservation.

Checkpoint coercion includes null/bools, decimal and radix strings,
ECMAScript BOM/non-ASCII whitespace, arrays, NaN/infinity and own noncallable
`toString` failures. Real Source red evidence also covers missing/null plan
snapshot content, null message property-access priority, missing/null
assistant API message destructuring, and the quoted negative lite PR branch
without later hydration masking its value. BOM and NEL parseInt prefixes are
compared in metadata-only files, preserving ECMAScript whitespace semantics.
An exact second-read fault is also compared through the full continue caller.
Three separate actual portable-helper profiles compare tail-read failure,
close failure and zero-byte head, including read/close order and unchanged
original file bytes; every failure clears both fields and zero head skips tail.

Four SID profiles probe `doesMessageExistInSession` with UUIDs supplied in
original fixture inputs, comparing exists/missing and zero additional transcript
reads against Go's actual primed cache. A Source-only clear-cache control causes
one real reread and verifies that the read observer is effective. A native
rewrite case proves that later getLastSessionLog calls do not replace a primed
cache; native state observers cannot mutate its UUID bytes. Other cache/reset/
writer lifecycle semantics are not implied by these checks.

The finite settings profile validates each original file and each explicit
cached merged/policy snapshot before effects. Unknown or unsupported fields,
invalid recognized values, unsupported UTF-16 property keys, invalid hooks,
plugins and active CLI plugin/settings flags refuse explicitly. It is not the
complete SettingsSchema, managed-policy/MDM discovery, remote settings or flag
resolver. Only plansDirectory, disableAllHooks, allowManagedHooksOnly,
nonnegative integer cleanupPeriodDays, the validated finite SessionStart
command structure, boolean plugin enablement and empty marketplace settings
are parsed. Active hooks/plugins are not executed. Genuine bare,
managed-disabled, managed-only-empty and no-config/no-matcher paths resolve from
the original inputs; no hook-result stub pretends to be an implementation.

The request file now contains eight positive actual first-HTTP profiles (JSONL,
SID, continue, plan recovery, copied backups, no-config hooks, managed disablement
and retained Skills). The Skills case reuses the original duplicate/empty-name
payload through the real shared attachment projection/merge and replaces the
previous seven-positive plus one-negative checkpoint scope; these counts must
not be added together. This proves this request boundary, not the full CLI host.
The headless controller separately connects an optional real snapshot-record
sink to the shared restore service. Existing component callers without that sink
retain their previous behavior, with direct same-area regression coverage.

String SID selectors are restricted to original ASCII UUIDs. Lone UTF-16 SID
selectors reject before lookup; they are not silently replaced. Raw LogOption
and metadata UTF-16 values are retained in proven profiles. Date parsing uses
the independent Go ISO/RFC UTC/explicit-offset parser; legacy/local-timezone or
object Date.parse coercion is not complete. CWD sanitization exceeding 200
characters explicitly refuses because Bun's long-path hash is not migrated.
Lite PR proof covers captured numeric/string prefixes including BOM and NEL;
other extraction/pathology profiles, ties across parallel native stat completion, and all
malformed noncanonical snapshot/backup shapes are not claimed equivalent.

## Isolation and raw evidence

Both fixtures reject unknown top-level inputs and preprocessed history, require
canonical disposable HOME/cwd/allocation-root/.claude/USERPROFILE/BUN_OPTIONS,
and reject known input/output leaf or directory symlinks before importing
Source business modules. Five parent guard tests use malformed private settings
as an import tripwire and verify external temporary sentinel bytes are unchanged.
Five strict comparator mutants change real result bytes, phase order, copied
bytes, allocation or metadata and must fail the same toEqual comparator.

The Source capability layer is installed before production imports. Secure
storage is explicitly empty, native and public network operations are denied,
and filesystem access stays inside the private home. State-only profiles assert
zero secure/absent reads. Request profiles grant only their exact loopback
origin and use fake env API credentials. The approved literal
`/home/claude/.claude/remote/.oauth_token` path is virtual ENOENT only; writes
remain denied. Actual reads are asserted: bare request profiles have one empty
secure read and no virtual OAuth read; nonbare profiles have two empty secure
reads and one `readFileSync` virtual OAuth read. Nonempty CCR/FD/OAuth credentials
and complete auth resolution are not proven.

Raw Source errors/stacks, stderr, SDK clock/yield/prefetch observations, original
inputs, results and captured socket requests remain in ignored
`artifacts/go-parity/resume-state/`. Stack equivalence is pending; raw differences
are not normalized away to pass parity. This checkpoint is native Darwin
evidence. Windows/Linux execution, native filesystem error text and final host
cross-process behavior require their real platform matrix.

## Reproduction and remaining host wiring

Use pinned Bun 1.3.14 and Go 1.25.7. Build fixtures with
`createGoBuildEnvironment` so verified locked modules are copied into private
caches and tests run with GOPROXY/GOSUMDB disabled. Focused commands are:

```text
bun test scripts/quality-gate/go-parity/resume-state.test.ts
bun test scripts/quality-gate/go-parity/resume-state-request.test.ts
```

Ordinary tests write actual Source goldens only under ignored artifacts. To
replace the native replay contract, separately run pinned Bun with
`scripts/quality-gate/go-parity/resume-state-capture.ts
--capture-original-source` after the full ordered focused suite. The capture
checks profile identities and count; native tests replay frozen testdata without
regeneration. Go unit/race/vet and fixture compilation are offline/private;
changed executable lines use the existing 90% AND-block coverage rule.

The caller still must wire complete host init/global config/auth, session-ID
switch/reset/continue caches, active SessionStart hooks/plugin execution,
worktree restore, file-history writer materialization and complete query/print
or REPL execution. This package provides concrete restoration state and a finite
restore-to-first-request chain. It does not establish full CLI resume, desktop
history projection or production Server switching readiness.
