# Original CA configuration and pinned trust profile

`backend/internal/tlsconfig` independently implements the no-argument process
memo in `src/utils/caCerts.ts`. The host owns one `State` across clients,
controllers and sessions. `NewState` requires an explicit real file reader;
`Certificates(originalEnv)` reads the original extra-file path, never a Source
CA array or pool. `Clear` resets both arrays and cached undefined. Returning the
same mutable `CertificateArray` reference is intentional; external mutation
requires host serialization and is not advertised as concurrently race safe.

Thirty original operation profiles compare actual Source self-replay with Go:
undefined caching, env/file mutation, explicit clear, caught missing/directory
reads, empty/raw/multi-line file as exactly one item, BOM/replacement UTF8/NUL,
reference identity, base/tail mutations, lone UTF16 and exact `/\s+/` Node-option
splitting. Fresh system-store calls are excluded; cached results after flags
change remain observed. Array SHA-256 covers the complete ordered JSON strings,
not only the observed tail or certificate subjects. Read paths substitute only
the allocated HOME anchor; original literal paths/errors remain in raw logs.
Enabled debug logging and native startup extra-CA warnings are separate pending
stdio contracts. The direct function oracle sets extra env after guarded import
rather than launching Bun with it and conflating native startup behavior.

The embed resource contains the exact 145 public root strings from Bun 1.3.14,
revision `0d9b296af33f2b851fcbf4df3e9ec89751734ba4`. Each string has no final LF.
Joining with LF and appending one LF gives 217268 bytes and SHA-256
`e3569ed5bde6e59d7718cd962b1f07a95ab0a4226a9fc35a18d8f586f98ea6a4`.
The provenance is the pinned runtime builtin capture under ignored
`artifacts/go-parity/direct-https-probe/bundled-roots/`. No OS store is queried.
An upgrade to Bun/root data requires explicit regeneration and new Source
contracts; Go does not use `SystemCertPool` or silently a private-only pool.

## Native parser boundary and creation timing

`ValidateOriginalTLSOptions(env)` names unported native configuration before
client construction. `State.Certificates(env)` captures the Go-produced array
once into the client's TLS options; malformed PEM still succeeds here, matching
Source `getAnthropicClient/getTLSFetchOptions`. On each physical fetch,
`VerifiedConfigFromCertificates(capturedArray)` builds a new explicit Go pool.
It does not read env/files again. Nil selects the pinned runtime resource.
Process clear leaves an older client's array in place; direct mutation of that
same array remains visible on a later fetch. `State.VerifiedConfig(env)` is only
an independent convenience verifier, not the client-construction lifecycle.

`InvalidCertificateError{Index}` wraps `ErrInvalidPEM` as a configuration fact.
The physical transport/retry owner projects the actual URL-specific native
`FailedToOpenSocket` cause, ordered code/path/errno and message. Returning this
error from GetClient would move the real failure to the wrong retry boundary.

Thirteen generated-private-CA/IP-SAN original inputs each ran twice through the
unchanged Source `getAnthropicClient`, pinned SDK 0.80.0 and exact HTTPS loopback.
These clean Source-only parser observations are retained under ignored
`artifacts/go-parity/tls-config/pem-native/` and frozen into the native test-only
PEM contract. They are not Go HTTP/cause or full transport equivalence proof.
All native Source capability reports have `blocked=[]`; no OS trust/keychain,
public service, user state or model is used. Outcomes:

- Valid CA, two valid PEMs, CRLF, no final LF, junk on a prior line, trailing junk,
  and valid first followed by malformed/partial blocks: one successful POST.
- Empty/whitespace/junk extra item, BOM attached to BEGIN on the same line, and
  invalid first CERTIFICATE block followed by valid: `FailedToOpenSocket`, zero
  POST. Bundled roots do not compensate for an invalid extra array item.

Go's `pem.Decode` skips an invalid first block; the control test demonstrates
that known mismatch. The independent parser requires the first certificate and
retains successfully parsed certificates before malformed trailing blocks. A
real CRLF end-line failure was reproduced and corrected; initial logs remain.
PEM header extensions and other block types are explicit capabilities. This is
not proof that x509/OpenSSL/BoringSSL parsing/verification is equivalent for all
ASN.1, PEM, trust-chain, algorithm, hostname, expiry or policy domains. Native
fixtures verify generated certificates at their captured validity time so the
contract does not expire in CI.

## Remaining host and transport boundaries

System/OpenSSL CA stores, mTLS/client keys/passphrases, insecure verification,
SSL_CERT_FILE/DIR, OPENSSL overrides, arbitrary Node TLS options, externally
emptied/non-string arrays and unported PEM extensions remain explicit errors.
OS trust, CA config-from-settings initialization, mTLS's separate memo/cache,
Node-undici/proxy agents, DNS/SNI/IDN/revocation/constraint breadth, TLS session
reuse, HTTP2, native cause and exact physical retry/socket behavior belong to
separate declared integration profiles. The returned config has pinned RootCAs,
TLS1.2 minimum and HTTP/1.1 ALPN; it does not change production transport defaults.
Current native checks are darwin/arm64 evidence; other five native targets have
not run here. No full CLI/Server/Harness or live Benchmark completion is claimed.
