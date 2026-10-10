# Private HTTPS request-creation integration

This corpus constructs both sides from the same original environment, raw history,
system blocks, model `deepseek-flash`, request options, runtime identity and private
CA file. Go receives no Source-built body, header projection, certificate pool,
credential resolution, retry decision or fault factory.

Source executes `queryModelWithStreaming`, `getAnthropicClient`,
`getAnthropicApiKey`, `getCustomCAcerts`, `withRetry` and the pinned Anthropic SDK
0.80.0. Observational wrappers invoke the original functions and rethrow the
original SDK error. `fetchOverride` records final inputs and calls real Bun fetch
against a freshly allocated private TLS socket. Source uses Bun 1.3.14 revision
`0d9b296af33f2b851fcbf4df3e9ec89751734ba4`, `NODE_ENV=production` and
`--no-env-file`.

Go independently invokes `requestretry.Open`: once-only `requestcontext.Prepare`,
original-environment key resolution, `ResolveDirectClientOptions`,
`NewAnthropic`, `PreparedContext.BuildAttempt`, `RunAttempts`,
`OpenMessagesStream` and the explicit `RawHTTP1Exchange` TLS path. A Go-owned
`tlsconfig.State` reads the original CA path once at client construction. The
captured Go array is resolved to a pool at each physical fetch; clearing or
changing an unrelated state cannot substitute a new Source-prepared array.
Successful stream consumption uses the production stream-history controller
after request creation has completed. Creation retry does not own successful
SSE consumption.

The host uses the committed public test certificate/key, a private `127.0.0.1`
port, TLS 1.2 minimum and HTTP/1 ALPN. It emits actual raw HTTP 500, then 429 with
two differently cased `Retry-After` fields, then 200 SSE. Raw response pairs also
contain duplicate uncommon headers, separate Set-Cookie fields and literal
`Connection: close`. No OS trust store, keychain, real credential or provider
socket is used.

Seventeen original-input profiles have two declared comparison layers:

- Nine full-query profiles cover successful retries; notice pause and normal
  stop; abort during retry sleep or before starting; normal streaming stop;
  caller abort after a real TCP accept during a stalled TLS handshake; SSE body
  delivery after the SDK header timeout has ended; and caller abort after headers
  but before the delayed body. Source yield-time and final yields, history,
  preparation, UUIDs, client/key counts, final bodies, paths, resolved headers,
  notices, sleeps and resource closure are compared to independently executed Go.
- Eight creation-layer profiles cover a stalled-handshake SDK timeout;
  malformed, wrong, missing and absent extra CA inputs; malformed CA with timeout
  `0` or `1`; and a one-MiB line-delimited malformed CA with timeout `1`.
  Actual SDK class/name/message, exact JSON and recursive native cause, notices,
  requests, CA reads and physical connections are compared. Source's subsequent
  terminal assistant-error yield and its allocation remain separately recorded
  in `sourceOnly.queryTerminal`; they are not counted as migrated Go query
  behavior.

Tiny timeout profiles preserve strict observations without timing normalization.
The targeted initial samples for all three malformed tiny-timeout inputs selected
`APIConnectionTimeoutError`, `Request timed out.`, three attempts and zero TCP
connections. This is the observed machine/run result, not a proof that arbitrary
PEM size, CPU scheduling or millisecond timeout races always select that error.
The large raw CA is exactly 1,048,576 bytes with SHA-256
`91b6ff2eb97abc19525bb8d4692654a037e00ab246f0b3c290ad8b085ac86f1b`.
If Source self replay differs, the test fails rather than weakening the error
comparison or treating the timing domain as accepted.

All resolved pre-fetch headers must arrive on the decrypted request socket;
body/path and physical send counts are checked there as well. Original ordered
response metadata and causes are inside strict notice/error comparison.
Automatic Bun request headers, outgoing socket enumeration, TLS cipher and ALPN
are retained separately. These side-specific observations are not silently
removed to claim complete wire or TLS implementation equivalence. Only allocated
private HOME and loopback-origin anchors are replaced during cross-child replay;
the complete literal inputs and outputs are also retained.

Before production imports the Source child validates its disposable HOME/config,
cwd, explicit profile, exact HTTPS origin, original environment whitelist and
private non-symlink CA file. The shared capability guard allows only that HTTPS
origin, denies native process/network/outside-state access, uses an observable
null secure-storage adapter and records the exact pinned absent managed paths.
Go validates the same allocation/path/profile boundaries before business code,
rejects unknown JSON fields, and uses the independently verified offline module
seed. Twenty-one negative guards protect the outside sentinel and require zero
TCP connections. Eight comparator mutants must be rejected, including header,
delay, client reuse, CA read, connection closure, cause JSON, UUID and body changes.

The owned Go file is a test-only physical integration driver. Race-instrumented
children produce real coverage for `requestretry`, `provider` and `tlsconfig`;
this slice does not invent a production changed-line denominator for test code.
The parent owns the unchanged 90% executable-line gate for the final production
diff and the unified corpus/native freshness report. Local evidence is
darwin/arm64 only. Other native platforms, default transport cutover, TLS pooling,
proxies, mTLS, system-CA flags, unproven PEM/parser or TLS-alert classes,
compression, complete outgoing automatic headers, terminal query error mapping,
full CLI/Server startup and live Benchmark results remain outside this contract.

Run the pinned focused corpus with:

```
bun --no-env-file test scripts/quality-gate/go-parity/https-request.test.ts
```

Ignored artifacts are under `artifacts/go-parity/https-request/`. Each run archives
both Source children and the independent Go child under a distinct timestamp;
literal input/CA bytes, stdout/stderr, errors, wire pairs, TLS metadata and closure
counts are saved before assertions. Negative guard raw logs are also preserved.
The final focused report records exact commands, test counts and raw selected
input hashes. Earlier setup/guard failures stay available as historical evidence.
