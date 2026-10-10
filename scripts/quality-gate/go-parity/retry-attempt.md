# Request-attempt scheduler boundary

`backend/internal/retrypolicy.RunAttempts` ports the actual Source `withRetry`
generator for the declared release direct-relay profile with empty OAuth,
external user type and no cloud/internal/model-override capability. The client
factory and attempted operation are external dependencies; they provide faults,
not retry decisions. The production Go scheduler decides client reuse/rebuild,
delay, budget, fallback, fast-mode effects, yield and terminal ownership.

The corpus independently executes the pinned Source generator twice and Go for
82 input profiles. It records factory calls, attempt context, helper cache
invalidation, sticky keepalive disablement, state notifications, consumer yields,
sleep and terminal results. The original retry notice JSON is compared as well
as its diagnostic fields. Guards and production-import tripwires reject invalid
HOME/config/cwd before business imports. Each child has disposable settings and
empty credential/managed-policy adapters. No live provider or real user state is
used. The loopback HTTP creation path is still a separate integration task;
these cases inject external factory/operation failures rather than HTTP calls.

Profiles cover SDK error identity, HTTP policy/header precedence, 401 and revoked
client rebuilds, a failed rebuild retaining its previous client, stale connection
codes, foreground 529 classification and repeated-overload fallback, legacy
context overflow, count/Retry-After numeric behavior, fast overage/org/cooldown
transitions, lazy cooldown expiry, caller abort and consumer termination.
Production unit tests additionally execute the real abort-aware timer and a
blocked yield barrier to prove that sleep/resubmission cannot advance early.

Source comparison found and fixed three differences: failed auth rebuild
overwrote the old Go client; Go's regex did not accept Source ECMAScript whitespace;
Go's Unicode-insensitive matching treated Kelvin sign as ASCII K whereas Source
non-Unicode `/i` does not. The raw wire extension also exposed Bun's Headers JSON
serialization; nonempty one-header values now survive the notice instead of
becoming `{}`. SDK payload numbers, original UTF-16 escapes, undefined/null,
request IDs and enumerable native cause fields retain Source JSON behavior.

The notice serializer rejects multiple original header names until the HTTP
metadata adapter preserves Bun's original JSON enumeration. A sorted Headers
iterator or Go map cannot recover that order. Unknown native causes without
their enumerable properties and invalid SDK payloads also fail explicitly.
Such rejections are capability boundaries, not Source compatibility passes.
The existing `Connection: close` metadata mismatch remains pending separately.

Fast-mode persistence/notifications and keepalive/cache effects are explicit host
callbacks. Fixtures compare the real Source's private settings/signals to the Go
host adapter's effects; this does not prove production Go settings/signals are
wired. OAuth refresh, cloud providers, unattended retry, native fault property
adapters, stream consumption/retry/watchdog, query failure recovery and CLI host
integration remain pending. Real timer scheduling preserves the observed Bun
numeric delay behavior; Bun runtime warning text on overflow is not yet ported
to Go stderr. No full stdout/stderr compatibility claim follows from this slice.

Focused verification: 169 passed, zero failed, 700 assertions, including the
strict multi-header pending negative (not an equivalence case). Native package
unit/race each passed 116 tests; vet passed. Statement coverage was 96.4%; changed
executable lines were 601/645 (93.18%), with the existing 90% minimum unchanged.
The unified corpus/native reports are separate executions and remain the source
of aggregate evidence. Full CLI/Server/Harness and live Benchmark gates remain
pending.
