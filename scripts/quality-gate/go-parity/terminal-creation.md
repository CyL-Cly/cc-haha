# Terminal creation presentation

This component ports the declared terminal connection-error branch of the actual
`src/services/api/errors.ts:getAssistantMessageFromError` export and the ordered
`src/utils/messages.ts:createAssistantAPIErrorMessage` factory. It accepts actual
typed Go SDK/retry errors and the caller's original environment. No Source-rendered
message, prepared request, subscriber boolean or Source-derived allocation enters
the Go production function.

## Production interface

`apipresentation.DomainFromOriginalEnv` independently admits the direct original
environment-key, fast-disabled profile and rejects undeclared environment keys or
unported TLS options. `CreationFailure(error, domain, sharedFactory)` unwraps the
actual `CannotRetryError` once, then returns an ordered `streamhistory.Envelope`.
The caller supplies the existing shared session `messages.Factory`; its allocation
order remains envelope UUID, Date, API message ID. Capability failures are typed
and happen before allocation. Consumer errors, abort suppression, normal stop,
fallback and stream failures belong to the caller rather than this presentation
function.

`messages.Factory.CreateOrderedAssistantAPIError` is additive. It uses the existing
factory defaults and allocator, retains undefined property slots, and admits the
string/null metadata domain exercised here. Message JSON is observed at the shared
JavaScript stringify boundary, including synthetic model/default usage, omitted
fields, explicit null, empty content and lone UTF-16 units.

## Original profiles and observations

The neutral constructor corpus covers the eight previously captured physical TLS
creation-fault classifications, all eighteen Source SSL codes, unknown/empty codes,
UTF-16 identities, first-code precedence, the five-Error limit, plain-object and
self causes, ASCII timeout precedence, and original shared-factory options. Six
additional actual Source observations explicitly require Go capability/no
allocation: custom Unicode and non-timeout connection messages, abort suppression,
HTTP API presentation and generic Error presentation. Those are not compatibility
passes.

The Source child installs the disposable-home and external-capability guards before
production imports. It calls the real pinned SDK constructors, the real retry
wrapper and exported presentation/factory functions. UUID and Date are deterministic
host adapters; they observe evaluation order rather than replace message business.
For this isolated direct-key mapper the actual capability observation is zero
secure-store and zero managed-policy reads. It makes no sockets. Native helper,
network, outside filesystem, environment/config/cwd and prepared-result escape
negative tests must fail before the business result is emitted.

Source self replay precedes strict independent Go comparison. The comparator rejects
message bytes, API-error flag, allocator count/order and timeout punctuation mutants.
`backend/internal/apipresentation/testdata/source_contract.json` contains captured
Source expected output for native regression; only its original constructor/env/
factory input fields enter Go. Raw Source input/stdout/stderr and the initial test
assertion failures are kept under ignored `artifacts/go-parity/terminal-creation/`.

## Remaining boundaries

This export-level oracle does not prove the outer query's telemetry/quota/activity,
partial-buffer flushing, hooks, original-history append, normal return/yield pause,
abort/fallback or response ownership. Those require actual physical caller evidence.
In particular, the original eight TLS profiles here reproduce their independently
captured typed classifications; this component itself does not open TLS sockets.
The separately integrated caller must compare actual request-creation faults and
terminal yields. HTTP status-specific, image/context/model errors, custom message
classifiers and subscriber/managed/cloud/helper authentication remain explicit
unimplemented presentation boundaries. This is not a complete CLI, Server, Harness,
default transport, platform or Benchmark equivalence claim.

Run the focused deterministic oracle with pinned Bun 1.3.14 and Go 1.25.7:

```sh
bun --no-env-file test scripts/quality-gate/go-parity/terminal-creation.test.ts
```

The native checkpoint must execute unit/race, vet, build, nonmutating gofmt and the
existing 90% changed executable-line gate, using verified offline dependencies and
a private HOME. Current-native results do not imply execution on the other targets.
