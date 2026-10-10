# Retry numeric and stream-fault boundary

`backend/internal/retrypolicy` ports the actual Source `getRetryDelay`,
`getDefaultMaxRetries`, `getMaxStreamTransientRetries`,
`isRetryableStreamError`, `isRetryableStreamTransportError` and
`shouldRetryStreamAfterTransportDisconnect` decisions.

The offline corpus executes those Source exports after a disposable HOME/config
guard and capability interception. Its SDK error classes remain the pinned real
SDK classes. Fault payloads/cause chains and RNG samples are explicit external
inputs. Go independently evaluates the same inputs; no Source decisions are
injected. Source self-replay and semantic comparator mutations are checked.
Import tripwires prove HOME/config/cwd refusal before the production Source graph
loads. Both children use private fixtures, no live network or credential store.

Numeric profiles retain `parseInt`, JavaScript whitespace, IEEE rounding,
overflow, NaN, Infinity and negative zero. JSON observation records the zero sign
separately because JavaScript stringify emits `0`. Numeric Retry-After bypasses
the cap and RNG; fallback jitter uses exactly one sample. Limits are values,
not a replacement implementation of the attempt loop.

Stream profiles cover genuine APIError instanceof versus SyntaxError/native
errors, exact serialized type matching, policy codes through SDK payload,
message and cause boundaries, the five-node first connection-code walk, cycles,
and all eight combinations of committed side effects/watchdog/user abort.
These three protected states short-circuit before fault inspection. The five
disconnect codes are accepted; timeout/certificate/unknown/empty/prose codes
are not inferred into a resend decision.

This numeric/stream-fault lane is distinct from the bounded request scheduler
described in [retry-attempt.md](retry-attempt.md). Neither is the full
`withRetry`/`withStreamRetry` production integration. OAuth refresh,
cloud-provider retry, persistent mode, watchdog timer or host recovery remains
pending. Go native
transport failures still need an independently verified adapter to the Source
fault-code semantics. Arbitrary object properties on native errors are outside
the current error interface; structured SDK payloads retain the relevant keys.
Go JSON grammar/depth profiles not accepted by its decoder return an explicit
capability error instead of permitting a resend. Native tests enforce this
boundary; a rejection is not a Source compatibility pass. No retry is attached
to the ordinary-success query loop yet. Full Harness and live Benchmark gates
remain pending.
