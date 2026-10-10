# Physical request-creation retry contract

`requestretry.Open` is an independently constructed Go opener for the declared
original `ANTHROPIC_API_KEY`, direct-relay, fast-disabled profile. It now also
admits original bare interactive sessions, migrated native tool definitions,
original extra schemas and textual tool results. Its input is the original
`requestcontext.Context`, pinned SDK wire runtime identity and raw
`OriginalAuthInputs` flag state; see [shared-opener.md](shared-opener.md) for the
current finite expansion and fresh evidence. It accepts no Source-prepared request, resolved credential,
rendered schema, response-derived request body, retry decision, or fault factory.

The production chain is:

```
original Context/env → requestcontext.Prepare (once)
  → retrypolicy.RunAttempts
    → independently resolve original environment API key
    → ResolveDirectClientOptions → NewAnthropic(explicit RawHTTP1Exchange)
    → PreparedContext.BuildAttempt(RetryContext) → OpenMessagesStream
  → return owned RawMessageStream
```

The caller consumes the successful stream after `Open` returns. A 200 SSE error,
body disconnect, or normal consumer stop cannot become a creation retry. Caller
cancellation remains SDK `APIUserAbortError`; a notice consumer returning
`requestretry.Stop` produces package-owned `ErrConsumerStopped`, and does not
become a notice or caller abort. `Notice` is a synchronous resume barrier: `Open`
performs no sleep or next physical request until the callback resumes. Arbitrary
notice/sleep failures retain their identity. The caller owns `Close` on success;
failed responses and an aborted creation remain the provider's responsibility.
`ObserveRequest` and `OnPreparation` receive copies and cannot inject bytes.

Source uses the actual `queryModelWithStreaming` caller at
`src/services/api/claude.ts`, actual `withRetry`, `getAnthropicClient` and
`getAnthropicApiKey`, and Anthropic SDK 0.80.0 under Bun 1.3.14 revision
`0d9b296af33f2b851fcbf4df3e9ec89751734ba4`. Observational wrappers call the original
business functions. `fetchOverride` records the final request and invokes actual
Bun fetch against a fresh raw loopback socket; it does not return fabricated
responses. A private host emits scripted raw HTTP responses with duplicate
`Retry-After`, duplicate uncommon headers, separate Set-Cookie fields and literal
`Connection: close`. Source's actual sleep helper is observed at its timer boundary
and advanced deterministically; Go supplies the required clock/RNG/sleep host.

The original environment union is explicit: `ANTHROPIC_BASE_URL`,
`ANTHROPIC_API_KEY`, `API_TIMEOUT_MS`, `CLAUDE_CODE_EXTRA_METADATA`,
`CLAUDE_CODE_MAX_RETRIES`, `CLAUDE_CODE_DISABLE_FAST_MODE`, plus host/auth-only
`CLAUDE_CODE_SIMPLE=1`. SIMPLE remains original auth/host state and is precisely
removed from request/client environment views. Only the retry-limit and fast-mode names are
policy-only inputs. They are projected to `RunAttempts` without reaching the
request/client components; the other names retain their original values. Missing
original credentials and unknown environment names fail with a capability error,
including helper, FD, OAuth, managed, cloud-provider, proxy and approval inputs.
Fast mode must be explicitly disabled. Input/history capabilities are checked
before network activity; unsupported media, non-bare interactive auth, active
flag settings/helper lifecycle, unported tool definitions/thinking and alternate
envelope classifications are rejected. Admitted textual tool results retain
the real shared preparation and schema cache. This
opener does not turn an empty external credential/policy adapter into an allow
fallback.

Twelve original-input profiles cover HTTP 500 → HTTP 429 (Retry-After 1 then 2) →
200; pause and normal stop at a notice; abort before start, after a physical POST
before headers, and during sleep; stop after the first streaming event with an
unbounded chunked body; original UTF-16/rounded metadata; explicit model suffix
and SDK beta; orphan historical tool_use followed by a user, or missing the final
user; and HTTP 400 context overflow → 200 with max_tokens 32000 → 29000.
Each runs Source self-replay and an independent Go construction. An orphan with
no final user allocates a synthetic repair ID once; all three request attempts
observe UUID counts `[1,2,3]`. The context-overflow profile observes `[1,1]`, one
preparation, and unchanged repaired history. Source-only pairing evidence retains
the original input/output envelopes. Original history is unchanged after the
query. Full model-visible request body bytes (including CCH signature), path,
all resolved pre-fetch headers, notices with original response headers, yields at
yield-time and after completion, client/key counts, delays, preparation, IDs,
physical send counts and connection closure are compared strictly.

Raw outgoing socket header pairs are retained separately for both sides. Each
resolved header must arrive at the socket, and body/path/counts are asserted there.
Automatic Bun transport headers and their wire order remain side-specific evidence;
this profile does not claim complete outgoing HTTP wire equivalence. Original
response headers, including Connection: close and JS JSON enumeration, are inside
the strict notice comparison and are not filtered.

Fixture guards run before production imports. All HOME/config/cwd are private,
`--no-env-file` is required, external processes/network/FS are denied, and native
secure storage is an observable null adapter. Managed-policy paths use only the
pinned absent-file adapter, recorded separately. Source assertions require no
blocked operations, exactly the expected managed-path observations, and one null
secure-store read for client construction (zero for initial abort). Guard tests
exercise HOME/config/cwd, profile, public origin, unknown credential environment,
missing key, native process, foreign-origin fetch, outside-home sentinel read, and
Go unknown prepared-body input. Comparator mutants must detect changed body/key,
client reuse, delays, notice order, preparation/ID counts, repaired history,
connection closure and terminal reason.

The native `testdata/source_contract.json` contains original neutral inputs and
separately captured actual Source expected observations. Expected observations are
used only after the candidate runs; request construction receives no Source output.
Native tests independently run real raw HTTP sockets, notice/cancel/error ownership,
successful-stream error separation and observer mutation protection. Use the
verified offline dependency seed with Go 1.25.7, disposable build HOME and caches.

This is an explicit raw HTTP1 integration, not a default transport cutover. TLS,
proxy, connection pooling, redirects, compression, native enumerable network
causes, OAuth/helper/keychain/cloud auth, general native-tool execution, fast
mode/persistence, model fallback outer-query behavior, transient stream replay,
nonstream fallback, complete queryloop/CLI/Server entry wiring and live Benchmark
scores remain outside this slice. The parent owns unified corpus/native/platform
verification and final integration; other native targets are not inferred from
this machine's race/unit evidence.

Historical first-slice commands/evidence (unchanged baseline, not this window's
fresh proof; current expansion evidence is in [shared-opener.md](shared-opener.md)):

- Pinned Bun: `bun --no-env-file test scripts/quality-gate/go-parity/request-retry.test.ts`
  → 31 passed, 0 failed, 1751 assertions (12 actual Source self/Go profiles,
  18 negative guard cases, one comparator test covering ten mutants).
- Hermetic Go: `go test -json ./internal/requestretry` and
  `go test -json -race -covermode=atomic -coverprofile=<private>/coverage.out ./internal/requestretry`
  → 48 tests/subtests passed in each run; race coverage 96.0% statements.
- `go vet ./internal/requestretry`, `go build ./internal/requestretry` and
  nonmutating `gofmt -l` over the three Go files → passed/no formatting output.
- Existing changed executable-line policy against HEAD: 129/141 = 91.4894%,
  above the unchanged 90% requirement; no missing mapping, skips or exemptions.

Machine evidence is current darwin/arm64 only. Whole-module, unified corpus,
other native targets, CLI maintainer-label approval and live quota were not run
by this slice. Final logs and the owned file/hash report are under ignored
`artifacts/go-parity/request-retry/`; the parent records unified freshness.
