# CLI global configuration persistence contract

This component ports the release/production global-config persistence service.
It is not wired into normal CLI `preAction`, `init`, v11 migrations or settings
writer dispatch. The desktop recoverable-JSON store is not a substitute for
this service. No model, network preconnect or full startup is exercised here.

## Source and production API

The independent literal default recipe is from `src/utils/config.ts:596`.
The behavior sources are `saveGlobalConfig` at line 810, auth-loss guard at
796, legacy project history cleanup at 978, watcher at 1010, initial cache load
at 1057, locked fresh reread/write at 1166, enable/strict parse at 1347,
`getConfig` at 1434, project access/update at 1620/1650 and first start at 1790.
The native symlink/mode/flush/fallback writer is
`src/utils/file.ts:362`. The lock lease ports pinned `proper-lockfile@4.1.2`
`lib/lockfile.js` and `lib/mtime-precision.js`; sync locking uses no delayed retry.

`cliglobalconfig.New(Options)` requires an explicit absolute HOME. Options pass
resolved OAuth filename suffix, optional config directory, clock/PID, real
filesystem/watch/lease timer capabilities and diagnostic sinks. Construction
does not enable or read config. `Path` memoizes the Source precedence: existing
legacy `.config.json`, then the resolved suffix filename. `Enable`, `Get`,
`Save`, `RecordFirstStartTime` and `Close` are the host integration seam.
`GetProject`/`SaveProject` take the host's already resolved canonical project
key; this service does not run Git discovery or invent a project key.

Updates use `Updater(*protocol.JSONObject) (*protocol.JSONObject, error)`.
Returning the passed pointer skips all persistence. A distinct ordered object
requests a write. Own nil slots represent undefined, preserve insertion slots
and are omitted by stringify; explicit `null` remains different. Raw JSON
retains unknown UTF-16 field/string identity and numeric lexemes until the
actual JavaScript stringify boundary. Locked failures perform a true fresh
reread/unlocked fallback and can invoke an updater twice. There is no identity
updater/no-op initialization installed by default.

## What the differential observer proves

The Source child imports real production `config`/`file`/`lockfile` code after
the disposable HOME/config guard and external capability adapter. The adapter
returns null secure storage, exact ENOENT managed-policy paths and absent Git
ancestor paths; it blocks real native/network/external file access. Bare and
`node:` fs imports use the same checked real primitives. Business factories,
parsers, merge/filter/cache/auth-loss/backup/lock/writer functions are not mocked.
Watch polling, resolved suffix, Date/PID and standalone lease timer readiness
are explicit host capabilities. A `poll` step drains the actual filesystem
Promise and existing Source reaction instead of assuming setImmediate is an
I/O barrier. Separate `pollPending`/`releasePoll` profiles hold only the native
read's completion, prove that Get remains on the old cache while pending, and
prove either the completed refresh or rejection after a later write-through.
There is no global drain that removes the pending-state observation.
The callback executed by the timer is the real
proper-lockfile callback. A synchronous Save frame cannot renew its lease in
the middle of a blocking updater/fsync; Go intentionally preserves that limit.

Each scenario shares only original file bytes, mode, symlink/backup seed,
neutral operations and fault inputs. Go constructs its defaults independently.
Source output is never supplied to the Go constructor or updater. The observer
compares per-step returned ordered JSON strings, error type/message, strict
parse payload, updater invocation count/current input, successful file effects
and native write-flush call order, stderr diagnostics, actual config/backup/temp
bytes (including rawBase64 in addition to the UTF-8 text projection), mode and
retained symlink. `<HOME>` is the sole allocation-root rewrite, including the
same exact allocated UTF-8 HOME sequence inside rawBase64 project-file bytes;
embedded JSON, numeric/source lexemes, line breaks and UTF-16 escape strings are
not canonicalized away. Each child also writes its original stdout/stderr bytes
and allocation root to ignored `artifacts/go-parity/cli-global-config-raw/`
evidence before comparison; its literal rawBase64 fields remain unchanged there.
Ordinary tests never rewrite the Source contract file.

Startup cache memory hits, background external update rejection/acceptance,
write-through's second mtime check, enabled-before-strict-error behavior,
auth-loss refusal, stale/held mkdir leases, released-lock renewal/compromise,
backup throttle/retention/corruption deduplication, native write fallback and
legacy project cleanup are covered by actual Source recordings. Source led to
repairs for reserved lodash pickBy keys, backup Number grammar, array/string
project preservation, null project TypeError/updater repeat, cleaned-object
`__proto__` assignment, initial uncached second read and legacy allowedTools
UTF-16 JSON text, backup filename UTF-16 sort order and decoded-UTF-8 corrupt
backup equality while preserving the different original byte sequences.
Comparators have explicit semantic mutants for file bytes, raw bytes,
updater count, auth-loss side effects, unknown UTF-16 values, effect order and
stderr order. Source guard failures are counted separately from positive replay.

## Limits that remain explicit

The updater is a typed ordered-JSON callback, not an arbitrary JavaScript
function/object runtime. Accessors, toJSON, prototypes, mutable nested JS
reference graphs and aliasing a project projection across calls are outside
this API. It preserves same-reference skip for the passed callback object;
it does not claim arbitrary nested-reference identity. The typed project API
requires object project records and rejects unrepresented primitive records
with `ErrProjectShape`; global saving preserves the original JSON collection
shape and actual null-entry Source error instead of silently coercing it.

The explicit suffix and resolved project key must come from real future host
policy. OAuth lookup, Git resolution, settings writer, v11 migrations, CLI
init/main dispatch, auto-mode startup and zero-model HEAD preconnect remain
separate integration work. Production diagnostic sinks are a host dependency;
Source debug/telemetry files outside the selected config/backup/temp observation
are not claimed to be byte-equivalent. Arbitrary platform-native filesystem
error strings beyond the pinned tested parse/lease/fixture-fault branches,
cross-platform ACLs, invalid filename byte encodings, adversarial symlink races
and OS crash/fsync fault injection are not proved by successful ordinary files.
Go performs real `os.File.Sync`; the Source recorder observes the production
`writeFileSync(..., {flush:true})` call and resulting files, not kernel syscalls.
This component's passing corpus does not establish complete CLI/Harness parity.

## Reproduction

Use Bun **1.3.14** and Go **1.25.7**, the existing verified offline dependency
seed and the shared sandbox environment. Run the focused differential file:

```sh
/tmp/cc-haha-go-migration-tools.x6y1OL/bun-darwin-aarch64/bun --no-env-file scripts/quality-gate/go-parity/cli-global-config.run.ts verify
```

An explicit recording operation is separate from ordinary verification:

```sh
/tmp/cc-haha-go-migration-tools.x6y1OL/bun-darwin-aarch64/bun --no-env-file scripts/quality-gate/go-parity/cli-global-config.run.ts capture
```

The runner allocates a private parent HOME/config, filters inherited state and
provider variables with `createSandboxedTestEnvironment`, and invokes Bun with
`--no-env-file`. The test allocates separate private children, validates them
before production imports, and builds Go with `createGoBuildEnvironment` from
the verified offline seed (GOPROXY/GOSUMDB disabled). Capture and source-only
controls are not inherited into ordinary verification.

The native consumer reads the actual Source contract and executes the real
private-filesystem service. Same-area native tests also cover startup/cache
concurrency, delayed watcher read versus write-through/close, native polling,
lease precision/transient failure/racing replacement/sync frame, and protected
files under persistence failures. Final evidence is reported with its exact
source hashes, corpus count and changed executable line gate; full repository
native/impact/parity checks are owned by the parent checkpoint.
