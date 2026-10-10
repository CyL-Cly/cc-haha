# Original bare interactive / tools shared-opener contract

`requestretry.Open` now admits the actual original bare interactive relay path,
original migrated native definitions, original extra tool schemas and textual
historical tool results. It uses the existing unique preparation/creation retry
implementation. `queryloop` integration through the one shared `modelyield`
is owned by the parent; this component is not a complete interactive/CLI/Server
host or full Benchmark invariance claim.

Source evidence uses the real `queryModelWithStreaming()` → real normalization,
`toolToAPISchema()` and `getAnthropicClient()` → SDK 0.80.0 → private raw HTTP
socket under pinned Bun 1.3.14 release `TRANSCRIPT_CLASSIFIER`. Neither business
normalization nor auth selection is stubbed. Original native Read/Edit/Write/
Glob/Grep modules are imported only after private-HOME/capability guards; a
registered test tool is instantiated from the same original string/object
Zod declaration that Go independently renders. Native tools are **prepared**,
not executed by this opener fixture. The fake peer supplies original wire
responses; Source actual transport, error creation, retry and stream behavior
run normally. No Source-selected credential, schema, request body or normalized
history is given to Go.

## API and finite auth/environment contract

```go
requestretry.Input {
    Context requestcontext.Context
    Runtime provider.SourceRuntime
    Auth    requestretry.OriginalAuthInputs
}
OriginalAuthInputs {
    FlagSettingsPath   string
    FlagSettingsInline json.RawMessage
}
```

`Auth` is the original CLI flag state, not a resolved helper absence. Its zero
value declares that the **original caller supplied no flag settings**. Real CLI
callers must pass actual supplied state. Any nonempty path or inline value other
than missing/null returns an explicit CapabilityError before preparation/client
construction/socket. Even `{}` currently fails this finite settings profile;
full original flag resolver/active helper lifecycle remain required migration
work. An environment key does not justify ignoring a flag helper: actual Source
`client.ts:469–475` configures helper headers before its later fallback key.

Interactive input is supported only with literal original
`CLAUDE_CODE_SIMPLE=1`. Other bare truthiness/argv forms and ordinary interactive
approval/FD/helper/OAuth/keychain/IDE/homespace profiles fail explicitly. Actual
Source `auth.ts:245–247` bare env-key return precedes approval and keychain;
this proves the finite interactive env-key case. The complete actual Source
client pipeline still observes one read of the exact empty external secure-store
snapshot (zero after initial abort). It does not prove nonempty auth snapshots.

SIMPLE is admitted in original host/auth state, then precisely excluded from
both requestcontext Host.Env and clientEnv. Retry-limit/disable-fast names retain
the existing retry-only view and TLS retains its existing process-owned memo.
Every other original field keeps its actual consuming view; unknown names fail,
rather than being silently dropped. `apipresentation.DomainFromOriginalEnv`
recognizes only this literal host-only SIMPLE input; the error mapper is unchanged.

Original Tools use shared `requestcontext.Prepare`, actual raw-definition parsing
and the retained `toolschema.Session`/ToolSchemaHost. ToolSearch/MCP and unresolved
prompt/session hosts remain capabilities. Extra schemas must be original objects
with nonempty string name; unknown fields/order/UTF-16 remain original. Current
ToolChoice scope is auto/any/tool with nonempty string name for explicit tool.
Schema delivery does not prove hosted server-tool execution.

Original user tool_result needs a string UTF-16 tool_use_id and missing/null/
string content or arrays of text/tool_reference. Nested text must be a string;
media and malformed blocks fail before preparation or socket. The already-ported
shared Prepare owns pairing, tool-input cleanup, alias handling, reference
stripping and generated IDs. We do not normalize twice or change old envelope
classification/API-error/virtual capability boundaries.

## Actual red → green and acceptance

Fourteen new original profiles each run Source→Source strict self replay then an
independent Go fixture, with literal ordered history/system strings and original
raw definitions. They cover bare interactive key; native Read; five native tools;
registered paired textual result; nested reference plus lone UTF-16 text;
null/missing result content; native-plus-extra order; server schema/auto choice, any/explicit tool choice;
initial abort with preparation; consumer stopping at notice; and context overflow.
Most peers send real HTTP 500→429→200; original schemas/history prepare once
before the creation retry and unchanged prompt/UUID counts are compared. Source
observations include before/after history, paired projection, exact body/CCH,
path, all resolved pre-fetch headers, notices, client/key/prompt counts, delays,
allocation-on-request counts, literal yields and live-after-completion values,
physical send/connection close counts. Automatic raw outgoing headers remain
side-specific evidence, unchanged from the existing request-retry contract.

The **old production Go** first failed all twelve admitted Source profiles with
zero sockets; both Source replays had passed for every profile. Raw original
stdin/Source stdout/old-Go stderr are retained in ignored
`artifacts/go-parity/shared-opener-input-next/old-go-red/`, with the failing log
`old-go-red.log`. The red run was 1 comparator test pass / 12 parity fail.
This was genuine missing capability, not a manufactured expected-value oracle.

The first fresh focused run (twelve profiles, before the two tool-choice additions) was 58/0/3679 assertions. The final exact-profile run includes fourteen original cases. Fresh focused command:

```sh
bun test scripts/quality-gate/go-parity/shared-opener.test.ts scripts/quality-gate/go-parity/request-retry.test.ts
```

Pinned Bun result: **60 passed / 0 failed / 4031 assertions**, including fourteen
new Source-self/Go profiles, twelve retained request-retry profiles, two comparator
tests (six new and ten retained mutants), fourteen new Source/Go admission guards
and eighteen retained guards. The new guards preserve an external temporary
sentinel and assert zero physical requests for unknown env, invalid SIMPLE,
non-bare interactive, original flag path/inline, media tool result and prepared
shortcut. Source guards are explicitly finite fixture admission before production
imports; these negative profiles do not count as migrated active-helper/media
business. Positive Source capabilities retain no blocked operations, exact managed
ENOENT observations and actual empty-secure-store read counts.

Fresh native unit/race each passed **158 tests/subtests**; requestretry statement
coverage was **97.6%**, apipresentation **96.9%**. Vet and real production package
build passed. The unchanged 90% executable-line policy evaluated both production
files as **355/376 = 94.4149%**, without exemptions. Native unit/race uses actual
captured Source goldens only **after** Go runs from
original inputs, plus original-auth/media/schema/tool-choice negatives and two
successful independent calls retaining the same schema cache. The new contract
is `backend/internal/requestretry/testdata/shared_opener_contract.json`. Ordinary
suite runs write only ignored artifacts. Frozen native goldens are generated only
by the explicitly opted-in offline command:

```sh
CC_HAHA_CAPTURE_SHARED_OPENER=1 bun test scripts/quality-gate/go-parity/shared-opener.test.ts
```

This flag is not set in ordinary checks. Current observed native evidence and
owned raw hashes are in `artifacts/go-parity/shared-opener-input-next/final-evidence.json`.

## Remaining boundaries

No complete host/Benchmark cutover is claimed. General auth settings/helper/
FD/OAuth/keychain/approval, ordinary interactive trust/hooks, active MCP/ToolSearch/
media, first-party/cloud/managed-proxy selection, fast-mode persistence, outer
streaming/nonstream fallback, partial-abort commit buffers, dynamic SDK queue/
cancel, actual native tool execution and all-host/native-platform matrix remain
outside this finite opener expansion. The caller must use unprepared original
input: invoking requestcontext.Build before this opener would duplicate Prepare.
Existing rawHTTP1/TLS capability and single-successful-stream no-resubmit ownership
are retained, with broader shared-Harness validation owned by the parent.
