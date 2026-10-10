# Query terminal hooks: finite external empty resolver

This is a component service for the actual empty/suppressed hook branch. It is
not evidence that the full CLI hook executor, startup, or outer query is ported.
The production Go query loop obtains this service through its external Host;
`queryhooks` imports `queryloop`, while `queryloop` does not import `queryhooks`.

## Original inputs and lifecycle

`queryhooks.New` requires the shared `clisettings.Service` and process-retained
`clisettings.State`, original SIMPLE environment, main session ID, shared UUID
allocator, and an explicit `external-empty-hook-resolver-v1` host profile. The
profile declares absent managed policy, flag settings, and plugin base. They
are host capabilities, not snapshots captured from Source. No parsed Source
hooks or hook decisions are accepted.

The service independently reads user/project/local original settings through
`GetEditableSource`. The finite shared schema accepts `hooks: {}` and known
hook event keys containing empty arrays, plus the original boolean settings.
Settings parse/source cache, merged hooks, snapshot capture/reset/update,
registered callback descriptors, and session hook descriptors have independent
lifecycles. `UpdateSnapshot` resets the shared settings cache. `ResetSnapshot`
does not reset it, and can return the identical cached hooks object. It also
clears the SDK callback registry through Source resetSdkInitState, while keeping
session/programmatic hooks. This profile starts with initJsonSchema=null;
non-null shared SDK init-schema reset remains a startup integration boundary.
Retain one
Service for the process; allocating one per request does not prove the Source
process lifecycle. Returned snapshot/cache references are borrowed and must be
serialized or mutated by the owning event-loop host, not raced by consumers.

`StartPostSampling` and `StartStopFailure` synchronously enter their actual
resolver before returning an already-completed Task for the admitted empty
branch. They do not spawn a fake asynchronous executor. An active programmatic
registry is rejected. StopFailure tests existence using the main SID before
SIMPLE and trust gates; Stop/SubagentStop uses the main/agent SID. Session event
presence is truthy even with an empty array, unlike a registered empty array.
Normal Stop allocates its tool-use UUID before SIMPLE/trust/matching discovers
there is nothing to execute. SIMPLE uses ECMAScript trim, including FEFF and
excluding NEL, rather than Go's `TrimSpace`.

`StopHooks` saves the real history plus accepted live assistant references for
`sdk` and `repl_main_thread`. Other query sources cannot overwrite that process
cache. System/user/system-context/tool-context references and live assistant
envelopes are retained. Original raw user records are immutable in this profile:
queryloop.Message.Record is currently a JSONObject value, so later addition of
a user-record property needs the shared host record-identity seam before it can
be claimed equivalent. Existing record fields and model bytes are not replaced
by Source output. The
service requires inactive CU/goal/team host state. `AbortCleanup` resolves only
the original dormant CU state (overlay, cursor badge, hidden apps, and lock all
inactive); cancellation does not invent a failure. Subagents skip the process
CU resource. Active CU cleanup remains an explicit capability.

## Actual Source oracle

The child calls the real exported `hooksConfigSnapshot`, `registerHookCallbacks`,
`executePostSamplingHooks`, `executeStopFailureHooks`, `handleStopHooks`,
`cleanupComputerUseAfterTurn`, and `getLastCacheSafeParams`. Only the primitive
UUID/clock and external capabilities are controlled. No business hook function
is replaced. Original settings files, history bytes, environment, registry and
session descriptors are shared inputs; expected outputs never enter Go.

There are 28 original profiles: 25 strict Source/Go empty or genuinely
suppressed comparisons, and 3 actual active-execution Source observations whose
Go outcome must be a typed capability. These three are **not** Go-equivalence
passes. The default focused suite also runs actual Source self-replay, five
intentional observation mutants, and import-tripwire isolation guards. The
trusted native golden contains only 25 profiles after two equal Source runs.
The captured golden is an expected-output oracle, not a production input.

Each Source child first validates disposable HOME/config, raw input shape, and
operation whitelist, then installs the shared preimport capability guard.
Network, child processes, native credentials and outside filesystem operations
are denied; only exact absent managed paths and absent ancestor `.git` paths
are represented as ENOENT. The capability and raw stdout/stderr evidence is
saved separately in ignored artifacts. No provider/socket/model is needed.
Bun 1.3.14, TRANSCRIPT_CLASSIFIER, NODE_ENV=production and Go 1.25.7 are pinned.

## Verification and limits

Run the focused suite using the task-local pinned Bun with `--no-env-file`:
`bun test scripts/quality-gate/go-parity/query-terminal-hooks.test.ts`.
Native golden/lifecycle/alias/capability/concurrency tests use private HOME and
the verified offline module seed; build, vet, race, gofmt and the existing 90%
changed executable-line gate apply unchanged. The fixture records synchronous
entry, promise return/completion, snapshot identity, ordered raw JSON, cache
references, registry/session state, and UUID/Date allocations.

A real Source-first counterexample is retained: SIMPLE containing whitespace
around mixed-case `YeS` originally caused Go to enter the execution capability
while Source skipped it. The corrected implementation passes that original
profile. A second original counterexample showed snapshot reset retaining Go registered
callbacks while Source cleared them. That lifecycle is now independently ported.
The earlier fixture TMPDIR setup and incomplete live-envelope content
adapter failures are recorded separately from production behavior evidence.

Active configured matchers, command/prompt/HTTP/agent hooks, actual callbacks,
plugins/managed customization, hook input payload execution, asynchronous
progress/barriers/timeouts, goal/team/background services and active native CU
resources remain unported. They must not be accepted as a no-op. Outer
query/modelyield hooks, abort sentinel allocation, consumer cancellation,
commands-completed analytics and startup registry initialization require their
own physical Source/Go caller tests. This component suite does not replace
those integration tests or prove full Harness scores.
