# CLI v11 migration sequence compatibility

This component implements the synchronous external/classifier release sequence
in `src/main.tsx:328-354`, using the real CLI global-config and editable-settings
services. It does not dispatch normal CLI startup or install the version marker
before the admitted sequence completes.

## Production API and host boundary

`climigrations.New(Options)` requires existing enabled
`cliglobalconfig.Service` and `clisettings.Service` instances, an absolute original
working directory and config directory, a clock, and the explicit
`ExternalEmptyOAuthProfile`. `Host.EmptyCredentialStore` must be true. The only
admitted environment controls are `CLAUDE_CODE_DISABLE_LEGACY_MODEL_REMAP` and
`CLAUDE_CODE_DISABLE_1M_CONTEXT`; model override is an original quoted JSON string
or null. Defaults are not inferred from a fixture or from Source output.

`Run() (*ChangelogTask, error)` executes the ten synchronous functions in their
original order, saves numeric version 11 only afterward, and starts changelog
migration even when version 11 was already present. A fatal synchronous failure
returns without running later steps or starting changelog. Ordinary settings
writer return-errors remain ignored where the original function ignores them;
the original catch sites and telemetry order are retained. Unsupported capability
errors stop execution instead of silently stamping a partial migration.

`ModelOverride()` and `DisableAutoUpdater()` expose the corresponding host-state
changes for a future startup adapter. This component does not mutate the process
environment or bootstrap main-loop model state. `ChangelogTask.Wait()` is an
explicit completion barrier for callers that need it; normal startup must not
wait. Its error is observable, while the Source startup call swallows the
background promise rejection. Observers may receive background events and must
support that concurrency.

The project key is independently derived from the original working directory and
filesystem. A discovered Git directory/file is rejected before MCP migration;
no Source-resolved project key is supplied to Go. Current local settings writes
support real files outside Git. Git-ignore updates and Git project canonicalization
remain pending.

## Actual Source oracle

The fixture compiles the actual complete `src/main.tsx` with pinned Bun 1.3.14
and `TRANSCRIPT_CLASSIFIER`. Acorn selects the complete `runMigrations` and version
constant spans, discovers the original import bindings, and binds the real
exported functions. It does not re-author the call sequence. Raw records include
the main and compiled SHA-256 hashes, exact spans, bindings and extracted function
text. Startup initialization outside that function is not executed or claimed.

The bound functions are the ten actual exports under `src/migrations/`:
auto-updates, bypass acceptance, MCP approvals, Pro default reset, Sonnet 1m,
legacy Opus, Sonnet 4.5, Opus 1m, remote-control preference, and auto-mode opt-in
reset. The actual asynchronous function is
`src/utils/releaseNotes.ts:migrateChangelogFromConfig`.

In the declared empty-credential profile, the Source subscriber predicates are
false; subscriber-only Sonnet 4.5 and Pro default branches are not admitted. In
this external fork the GrowthBook client key is an empty literal, so cached
`enabled` payloads still resolve to the actual opt-in guard. Those Source guard
results are exercised, without substituting no-op migration callbacks.

The fixture guards HOME/config and original input keys before any production
import. The capability adapter returns an observable empty secure-store snapshot
and exact ENOENT for absent managed policy and external ancestor `.git` paths.
Native credential processes, public network and external filesystem access are
denied. Each run allocates and removes its own private HOME. Real private files,
global lock leases, backup files and editable settings writes are used.

## Proof and original inputs

There are 47 original profiles: 41 migration runs and six raw settings-schema
inputs. Source self-replay and independent Go runs compare:

- ordered migration, updater, telemetry, diagnostic and physical-write effects;
- synchronous, completed-background and final config snapshots;
- raw file bytes, modes, actual backup contents and lock presence;
- original UTF-16 unknown fields and MCP identity/order, plus the exercised
  ASCII Sonnet model override;
- strict numeric 11 versus string `"11"`, completion flags and repeated runs;
- returned settings errors, caught global errors, fatal later writes and final
  marker failure, including actual locked fresh reread/fallback updater counts;
- changelog exclusive-create behavior, existing files, failed writes, truthy
  non-string values, lone-surrogate terminal bytes and background save failure.

The async fixture uses a native mkdir promise followed by an explicit completion
gate. It separately compares the snapshot before that gate and after the actual
Source promise completes. It does not globally await away the startup async
boundary or prove complete JavaScript/Go scheduling equivalence.

Six comparator mutants demonstrate that changed file bytes, marker ordering,
updater count, UTF-16 identity, return-error continuation and async snapshots are
rejected. Three import tripwires prove invalid HOME, config and undeclared input
are rejected before production code loads. These negative tests are distinct
from the 47 positive profiles.

The settings extension ports the migration-consumed booleans and MCP string
arrays, plus the rule-free permissions object: default mode, disable-mode enums,
additional directories and genuinely unknown ordered fields. Active permission
`allow`/`deny`/`ask` fields and permission arrays remain capability errors, as do
other unported active settings families. The existing env/model/effort parser,
cache clone/error-reference behavior and writer semantics are preserved.

## Reproduction and evidence

Ignored helper `artifacts/go-parity/cli-migrations-native/run-parity.ts` allocates
a private parent HOME, removes capture flags by default, and runs this test with
`--no-env-file`. `run-native.ts` allocates independent Go HOME/cache through
`createGoBuildEnvironment`, verifies the pinned seven-module seed, and uses Go
1.25.7 with offline `GOPROXY`/`GOSUMDB`. Run the pinned Bun binary with
`--no-env-file` against these helpers; do not invoke fixtures from a real user
HOME. `--source --capture` is the explicit golden-capture operation only.

Every child run records the exact command, sandbox environment, exit status and
literal stdout/stderr bytes in ignored `artifacts/go-parity/cli-migrations-raw/`.
Only declared HOME allocation bytes are replaced at the comparison boundary,
using byte-preserving replacement before re-encoding base64. Stored raw evidence
and actual files are unchanged. Goldens are actual Source observations;
ordinary parity execution does not write them.

Current focused results: 103 tests passed (47 Source self, 47 independent Go,
six mutants, three guards), zero failures. Native unit and race each passed 211
cases across `climigrations` and `clisettings`; three fixture-only entry tests
were intentionally skipped without their explicit environment flag. Vet/build
passed. Statement coverage is 92.5% for `climigrations`, 98.5% for `clisettings`;
the unchanged 90% changed-executable-line gate passed at 1113/1214 = 91.6804%.
Logs and coverage reports live in ignored `cli-migrations-native/`.

Retained failed logs distinguish the deliberate unimplemented Go red, a real
missing settings diagnostic/enum suggestion, and fixture construction or
observation mistakes (feature folding, double changelog spying and relative-path
projection). No Source production behavior was changed to make the comparison
pass. The earlier 15-file settings snapshot is historical; this extension has a
new owner manifest. The prior 18-file global-config snapshot remains unchanged.

## Remaining scope

The normal CLI preAction/init dispatcher, first-start record, environment/model
startup adapter, managed/flag policy, native OAuth/subscriber state, cloud model
providers, enabled auto-mode deployment, Git/worktree handling, full permission
rule filter and remaining active schema families are not covered. The Source/Go
fixture observes ASCII model overrides; arbitrary UTF-16 override host adapters
are not proved by this corpus. Full CLI,
Server and Harness migration are still incomplete. These proofs establish this
finite real persistence/migration component, not complete startup equivalence.
