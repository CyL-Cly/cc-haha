# Bun response headers and original SDK notice metadata

This slice independently constructs the Bun 1.3.14 Headers representation and
captures original HTTP/1 response rows before Go's response parser consumes them.
It proves the declared response/SDK-error/notice profiles below. It does not prove
the full CLI, Server, SDK transport, retry integration, or Harness benchmark.

The production Go inputs are original constructor pairs or original wire bytes.
Source-generated Headers, descriptions, response maps, notices, or ordering are
never construction inputs. The checked-in native golden is expected output from
the actual Source self replay, used only by assertions.

## Pinned representation

- Bun 1.3.14, revision `0d9b296af33f2b851fcbf4df3e9ec89751734ba4`.
- Actual `@anthropic-ai/sdk` 0.80.0, `src/utils/messages.ts`, and
  `src/services/api/withRetry.ts:getRetryDelay` after the disposable-home and
  capability guards. No mock replaces these business functions.
- The 94-name classification comes from Bun's generated
  [HTTPHeaderNames.cpp](https://github.com/oven-sh/bun/blob/0d9b296af33f2b851fcbf4df3e9ec89751734ba4/src/jsc/bindings/webcore/HTTPHeaderNames.cpp).
  It is neither an IANA registry nor a fixed enumeration order. The editable
  `.in` omits the generated Strict-Transport-Security member.
- Constructor append combines common and uncommon fields, retaining empties.
  Fetch/Pico append combines common fields, drops empty values, and replaces
  uncommon values without moving their original vector position. Cookie joins
  with `; `; Set-Cookie keeps a separate ordered list.
- Iterator sorts non-cookie fields lexically, then yields Set-Cookie rows.
  `toJSON` emits common fields in insertion order, then Set-Cookie, then uncommon
  fields. JavaScript array-index property names enumerate numerically first.
  Clone owns independent vectors; set keeps position; delete/reinsert moves it.
- Original HTTP field bytes decode as Latin1. Six actual socket profiles send
  identical visible input as UTF8 or Latin1 and compare the distinct outcomes.

`BunHeaders` mutations and snapshots use a mutex. Header names are ASCII tokens;
valid values are Latin1 strings. General JavaScript coercion, arbitrary object
iterables, malformed JS input types, and exact invalid-value TypeError diagnostic
metadata are outside this typed Go API contract. Native tests reject invalid names,
NUL, interior CR/LF, non-Latin1 characters, and invalid UTF8 inputs.

## Shared error ownership

`SDKStreamError.OriginalHeaders` and `RawMessageStream.OriginalHeaders` retain the
original Headers object reference. `NewSDKResponseError` caches RequestID at error
creation, just as the SDK does. A later Headers mutation changes enumeration,
notice JSON, and header lookup without changing that cached RequestID. Four actual
Source profiles verify this, including HTTP and SSE errors and a successful stream.
The previous clone implementation failed the actual Source notice comparison
before it was corrected.

`SDKStreamError.HeaderValue` reads this live object first. The retry scheduler uses
it for Retry-After and X-Should-Retry. `Headers http.Header` remains a legacy policy
map snapshot. Consumers requiring subsequent mutation must use HeaderValue rather
than that map. Nil-original legacy fixtures retain their prior map rules. Notice
serialization prefers original vectors, including multiple fields, numeric names,
cookies, and Connection; it does not infer ordering from a map. A multi-field map
without originals still fails the existing capability check.

## Exchange seam and present transport boundary

```go
type HTTPExchange interface {
    Do(*http.Request) (*HTTPExchangeResponse, error)
}
type HTTPExchangeResponse struct {
    Response        *http.Response
    OriginalHeaders *BunHeaders
}

client := provider.NewAnthropic(provider.AnthropicConfig{
    APIKey: "offline-fixture-key", BaseURL: loopbackURL, Timeout: time.Second,
    HTTPExchange: &provider.RawHTTP1Exchange{},
})
// An actual RunAttempts operation can call this client and return the real error.
stream, err := client.OpenMessagesStream(ctx, independentlyBuiltPreparedRequest)
```

The explicit exchange result requires a body and original ordered metadata.
`RawHTTP1Exchange` makes one direct fresh HTTP connection and writes one physical
request. It captures CRLF-terminated original rows with Peek, then uses that same
buffered reader for `http.ReadResponse` and body framing. It preserves literal
`Connection: close` and `keep-alive, CLOSE`, and does not fabricate a Connection
field for HTTP/1.0 or EOF-framed bodies. Source and Go each make 36 loopback requests.
Content-Length, chunked, HTTP/1.0, EOF framing, duplicate and empty fields, and
Latin1 bytes have actual Source comparisons. Cancellation closes the owned connection;
closing it avoids blocking on a drain of an unbounded response body.

This adapter is an opt-in staging seam. Existing default HTTPClient behavior is
unchanged and still cannot recover original enumeration or a removed Connection
header. TLS/ALPN, proxies/CONNECT and proxy authentication, connection pooling,
keepalive lifecycle, redirects/interim responses/upgrades, HTTP/2, and gzip/br
decompression require subsequent implementations and actual Source proofs.
Unsupported URL/status/encoding/size/folding profiles fail explicitly with
`ErrHTTPExchangeProfile`; they are not successful compatibility cases. Original
headers have a declared 64 KiB default capture bound, configurable up to 1 MiB.
Exact Bun parsing and failure metadata for all malformed HTTP, duplicate framing
fields, trailers, and header-limit edges remain unproven. Identity encoding is
supported. There is no implicit http.Transport replay/retry in this adapter.

Original Go network causes remain attached and unwrap from SDKStreamError. Their
enumerable Bun native-code/message/cause projection is not implemented here;
notice serialization continues to reject an unsupported native cause. Fault notices
proven here are actual HTTP API/SSE API errors, not fabricated network failures.
Default outgoing Bun fetch headers, request write buffering/abort effects, physical
request creation retries and full withRetry-to-transport integration remain separate
work. Nothing in this corpus claims all outgoing request wire bytes are identical.

## Deterministic evidence

Run the pinned Bun with an explicit PATH containing Go 1.25.7:

```sh
bun --no-env-file test scripts/quality-gate/go-parity/http-metadata.test.ts
```

There are 249 original profiles: 213 constructor and 12 each HTTP 500, SSE error,
and successful completion. Each Source child uses a new HOME/config/cwd, no env
file, fake credentials, the actual SDK against its own raw loopback server, and
capability guards before production imports. Blocked operations and secure-store
reads must be empty. System-policy absences, if observed, are restricted to the
shared explicit absent-path adapter and recorded separately; final replay observed
none. Source-only capability/version data is not inserted into the Go comparison.

The suite runs Source twice, compares independent Go traces, checks HOME/config/cwd
rejections before production import/native execution with protected sentinels, and
rejects seven intentional mutations of ordering, duplicate values, Connection,
cookies, notice JSON, and physical request count. The native suite replays the same
249 actual Source expected traces plus focused malformed/cancellation/concurrency/
metadata/cause/ownership regressions. Native race, build, vet, formatting and the
existing 90% changed executable-line policy apply; package statement coverage is
not substituted for that line gate. Other supported native platforms are not run
by this focused local proof.

Evidence is under ignored `artifacts/go-parity/http-metadata/`. Parent integration
owns adding this test to the unified corpus and taking fresh whole-module/source
manifests; a focused pass is not a final migration or Benchmark result.
