# Physical model creation and model-visible yields

`modelyield.Run` receives original history, request options, environment and
runtime. It calls the shared `requestretry.Open`, which owns the single
`requestcontext.Prepare` and all physical creation attempts. A successful raw
stream is consumed once by the shared stream-history Controller. This component
has no model loop, session writer or second request-preparation implementation.

The synchronous consumer receives retry notices and live stream/assistant
updates. Its explicit stop action suspends the next wait/request or completion.
An arbitrary consumer error keeps its identity, including an error wrapping the
internal stop sentinel. Caller abort yields no synthetic API-error assistant; supported visible text is committed through the same buffer while pending local tools are dropped. All acquired
response streams close on normal return, consumer stop, callback failure and
unsupported partial-stream failure; successful stream consumption never causes
a creation retry.

For supported exhausted typed connection/timeout errors, the adapter uses
`apipresentation.CreationFailure` and the same shared message Factory clock and
UUID sources. Presentation is evaluated after the final retry notice resumes.
The complete terminal envelope retains Source property order, text, defaults,
envelope UUID → Date → message-ID allocation, and unchanged original history.
Normal generator return after that envelope is recorded as `returned`; it does
not mean a successful model response or outer-query completion.

The physical corpus runs the actual original `queryModelWithStreaming`,
`getAnthropicClient`, SDK 0.80.0, CA configuration and retry code in private
children. It grants only a separately allocated HTTPS loopback origin before
production imports. The Go child independently prepares and sends its original
input. No Source-rendered error, classified fault, prepared body or normalized
history is supplied to Go.

Sixteen original profiles each run Source → Source → Go: ordinary success,
pause, notice stop, sleep/before/handshake abort, stream stop, delayed SSE, and
eight physical creation faults. The creation layer restores the complete actual
Source terminal projection for comparison while retaining the old creation-only
raw output separately. Complete prepared request bodies and pre-send headers, CA reads,
native SDK causes, notices, waits, yielded and final envelope bytes, ID counts,
original history and physical socket closure are compared. Only separately
allocated HOME and origin anchors are replaced. Raw input/output and wire/TLS
metadata are saved under ignored artifacts.

The socket check proves each observed pre-send header arrived with its value;
it does not assert equality of automatic outgoing headers or their raw order.
Those complete wire records remain saved for the final transport integration.
Compression, pooling and the default transport's automatic headers remain
pending and are not normalized into a positive parity result here.

The eight comparator mutants test terminal omission, final-value omission, ID
and request-allocation drift, history mutation, completion-layer substitution,
socket leakage and changed error classification. Native tests additionally
replay the existing twelve actual Source physical-request contracts and two
actual Source creation-terminal contracts, and exercise callback identity,
terminal stop, body abort, failed framing and no resubmission. Capability and
mutation tests are counted separately from positive parity profiles.

The Source fixture's body-abort timer is installed after actual SDK response
creation. That host event seam is not represented by the sixteen-profile corpus;
the native body-abort check is narrower evidence. Full activity/cost/quota and
diagnostic/telemetry state, error logging, outer `query()` history/stop-failure
hooks, watchdogs, non-caller partial-stream error/retry policy, 404 fallback, other
authentication/managed/cloud profiles, native tools and interactive/SDK host
integration remain pending. Neither this corpus nor the terminal presentation
mapper establishes full Harness or CLI migration or a live Benchmark result.

A separate stream-abort.test.ts corpus supplies four actual held HTTPS response schedules: completed text, unfinished text, pending local tool, and a server-tool boundary followed by unfinished text. Each runs the original single-model generator twice and independent Go once. Full yield-time/final bytes, unchanged original history, preparation and UUID/Date counts, requests and actual socket closure match; the cancellation comes from the same selected original stream event, not injected classified faults. Twelve Source/Go boundary checks prove private HOME/origin/CA/control/prepared-input rejection before model work; nine mutations probe comparison sensitivity. The Source generator still owns its original commit buffer, while Go Controller.Abort uses the existing Go buffer, preserves unfinished visible text and drains without local tools. Native tests reproduce all four old missing-text failures and verify stop/error ownership, closed-controller lifecycle and no physical resubmission. This proves the finite model cancellation phase; full SDK transcript writer/accounting, active tools and outer cancellation cleanup remain separate requirements.
