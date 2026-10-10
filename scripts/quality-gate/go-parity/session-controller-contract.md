# Finite actual headless activation and first-turn admission

This package ports the hosted activation and first accepted prompt of the
external bare/simple headless profile. It is not the full CLI, QueryEngine,
SDK protocol, interactive host, or Server session controller. Production
entrypoint switching is still pending. It shares the actual Go Loader,
resume-state orchestration/reducer, ordered message Factory, CLI settings
cache, and requestcontext Prepare/BuildAttempt. No TS production fallback,
Source-selected SID, processed history, or captured request body is an input.

## Real Source caller and order

The oracle imports real `src/cli/print.ts:491 runHeadless`, after disposable
HOME/config and capability checks. It runs the original SDK lines through
`runHeadlessStreaming` and the real `QueryEngine.ask`/QueryEngine. It uses real
`permissionSetup.initializeToolPermissionContext` with original `--tools ''`,
real AppState defaults/store, settings resolution, `config.enableConfigs`,
Project writer, read-file cache extraction and Source provider. All HTTP goes
to an exact loopback origin with fake credentials and a physical SSE response.
The unavailable external SDK type-only query stub is never the oracle.

The migrated finite sequence is:

1. Original SID/continue selection through `resumestate.Service.Load` and the
   independent Loader. This retains skills/plan/file-history restoration,
   genuine no-config/disabled hooks resolution, and non-joined background copy.
2. Optional resume-at slicing; non-fork SID/project-dir switch, then pointer
   reset only when original CLI persistence is enabled.
3. Restore file-history/legacy TodoWrite AppState consumers, then metadata
   cache, then Read/Write/Edit cache extraction before interruption removal.
4. Remove the restored interrupted user and sentinel, and queue the original
   prompt with a newly allocated UUID when the original nonempty environment
   flag enables this Source branch. The string `0` enables that raw check.
5. Original UUID admission against the actual loaded transcript membership and
   the runtime received Set. Historical retries acknowledge and complete their
   lifecycle; received retries only acknowledge. SDK queue operations preserve
   original priority, timestamp and property order.
6. Dequeue the finite plain prompt/pending batch, allocate prompt ID and a real
   user message, mutate hosted history, call the real Project Record boundary,
   then independently build and physically send the first request.

The actual Source observers call the original functions. Wrappers only capture
arguments/returns, deterministic clock/UUID allocation, raw diagnostics, or
hold a precise private OS Link operation. They do not replace business results.

## Original inputs and finite capabilities

`sessioncontroller.New(Options)` requires injected real resume dependencies,
shared settings cache, Factory, clock/UUID, files, Git, scheduler and error/phase
observers. `Activate(Activation)` loads original private files or an original
LogOption. `AdmitOriginalBatch` validates the original finite SDK batch before
any SDK enqueue, `AcceptNext` admits one accepted turn, and `Prepare` consumes
only the controller's actual mutable history. Alternate prepared history and
nonempty tool definitions are refused. Host/model/env are original independent
inputs to the shared request boundary.

Supported SDK inputs contain at most one novel plain-string user input, plus
historical/runtime retries and a restored pending prompt. One input without
UUID allocates its own real Factory UUID. Workload/control/session-inbox/file
attachment/slash command dispatch is outside this finite host profile. More
than one novel original SDK input is explicitly rejected before SDK enqueue
and before HTTP. Activation can already have copied a plan/backups and changed
host state; this is not a rollback or a claim of zero activation side effects.

The original multi-novel `later/now/next` Source profile is a confirmed pending
counterexample: the active first ask is interrupted by `now`, keeps its user
history, and the next ask sends a different first request. Collecting all SDK
inputs then batching is not equivalent. Its Source self replay and old Go raw
mismatch remain artifacts; its current explicit Go capability/no-socket
negative is not counted as host parity. Active-turn cancellation, dynamic input
queueing, multi-turn QueryEngine results, SDK event/permission/control protocol
and all hosted completion/shutdown state must still be migrated.

The release `TRANSCRIPT_CLASSIFIER` feature is enabled in the actual Source
child and native reducer; internal features remain genuinely off. Settings use
the existing finite validated original resolver and empty/disabled policy
profiles. Active hooks/plugins are refused by that resolver, rather than
replaced with an empty hook result. Active new file checkpoints before a novel
turn are explicitly refused; original snapshot restore with no turn is real.
Legacy/local Date.parse/coercion, hashed CWD paths over 200 UTF-16 units, invalid
UTF-16 filesystem paths and unsupported schemaVersion coercion are refused.
The React AppState and all default prompt/tool/permission business are not
replaced by this package.

## State and real persistence

The hosted cache keeps actual restored Read results after Source reminder/line
cleanup, Write input even when its result is an error, and successful Edit's
real current file bytes/stat. It preserves lone UTF-16 in content, UTF-16LE BOM,
CRLF handling, invalid ISO timestamp NaN→JSON null, and the real headless
caller’s 100-entry / 25-MiB LRU limits. Standalone `ExtractReadCache` retains
the Source helper’s independent default of 10 entries; the headless caller
passes 100 explicitly, and cache clones preserve the selected capacity and
observer ownership. Original 12-Read and 101-Read JSONL profiles prove full
history selection, MRU order, retention above 10 and eviction at 100. This
corrects the prior finite documentation’s incorrect 10-entry hosted-limit claim.

The Project writer preserves real lazy materialization, prefix-only parent
chain/dedup, compact-boundary logical parent, sourceToolAssistantUUID override,
startup queue consumption, metadata property order, unknown original fields,
JS write serialization, last meaningful prompt, persistence policy checks,
private descriptor provenance/regular-file/Dev-Ino identity, and replacement
or deletion discard without resurrection. It uses a real 100-ms scheduler and
real append-only file writes. Native fault/state regressions exercise owned
failure causes, partial writes, policy transitions, clone ownership and races;
these are not all actual Source fault/error-stack parity profiles.

`Writer.AdoptSessionFile` ports only `Project.adoptSessionFile`'s pointer step.
Four separate real Source public-wrapper profiles with original absent metadata
prove existing/missing/delete/replace queue/drain bytes. The public
`adoptResumedSessionFile` also does reAppendSessionMetadata(skipTitleRefresh):
its nonempty-metadata/--name behavior is not implemented by this narrow method.
Tail metadata refresh's full Source regex/pathology surface also remains
unfinished. These helpers are separate from headless activation evidence.

The optional `resumestate.Options.OnSnapshotRecord` sink connects a genuinely
successful background backup copy to the real writer startup queue. Legacy
component callers without the sink retain prior behavior. A private Link
barrier proves resume returns before snapshot recording/copy completion.
The fixture explicitly releases the barrier at cache extraction and awaits
tracked real background work only at its closing observation boundary, after
activation/first HTTP. It does not force the production activation path to
await copy, or claim real CLI shutdown always joins background work.

## Raw transport and isolation

The controller fixture calls provider.OpenMessagesStream with the default
Go net/http client after the real host activation and shared Prepare. Application,
SDK, fake auth, SID, trace, content headers and raw body are observed; Source and
Go socket headers are retained intact. This is not requestretry's explicitly
configured Raw HTTP/1 path. Native defaults differ: Source Bun sends
`Accept-Encoding: gzip, deflate, br, zstd` and `Connection: keep-alive`, while the
current Go transport sends gzip and omits Connection. Compression, pooling,
raw response metadata and these defaults remain provider integration work;
they cannot be ignored by the final complete migration gate.

Guarding happens before production imports. HOME/cwd/allocation root/config,
USERPROFILE, BUN_OPTIONS, canonical paths and known input/output leaf/ancestor
symlinks are checked independently on both sides. Source secure-store is a
recorded exact empty snapshot, native/network capabilities are denied except
an exact loopback fetch origin. Only the seven original canonical ancestor
`.git` leaves are virtual ENOENT; their actual reads are recorded. No parent
read/write grant, real Git/native spawn, real keychain, public network, user
config, real model or quota is used. These are JavaScript capability guards,
not an OS process sandbox guarantee. Five pre-import guard profiles preserve
external temporary sentinel bytes.

All raw Source SDK output, allocation stream, phases, socket headers and error
stacks remain ignored artifacts. The finite strict comparator checks migrated
history, AppState/read cache, UUID confirmations, queue/replay order, first raw
request, exact persisted/copied bytes and actual background snapshot records.
Seven comparator mutants independently prove those fields cannot drift
silently. Source-only extra SDK UUID/event allocations and stack equivalence
are pending, rather than normalized away or fed into Go.

## Evidence and capture

The controller corpus has 23 supported original-input profiles: 22 physical
first-HTTP profiles and one genuine checkpoint restoration/no-turn profile.
The additional multi-novel input is a separately classified pending negative.
Four Source Project helper profiles, five guard cases and seven comparator
mutants are separate counts. The resume-state request corpus now has eight
positive HTTP profiles, including retained Skills, replacing the old
seven-positive/one-negative scope.

Ordinary tests write only ignored artifacts. Explicit offline golden capture
requires `CC_HAHA_SESSION_CONTROLLER_CAPTURE=1` and is run before freezing:

```
bun test scripts/quality-gate/go-parity/session-controller.test.ts
CC_HAHA_SESSION_CONTROLLER_CAPTURE=1 bun scripts/quality-gate/go-parity/session-controller-capture.ts
```

Native replay reads the frozen actual Source contract. Unit/race/vet/build and
>=90% changed executable line evidence are in
`artifacts/go-parity/session-controller/final-evidence.json` for the prior
checkpoint and `artifacts/go-parity/session-controller-capacity/final-evidence.json`
for the caller-capacity correction. Native macOS is
observed; Linux/Windows host runs and full activation APIs remain pending.
Windows currently refuses unavailable native descriptor Dev/Ino identity,
rather than inventing path/mtime identity. Build-only is not host parity.
