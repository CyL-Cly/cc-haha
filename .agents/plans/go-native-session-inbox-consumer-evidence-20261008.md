# SDK session inbox consumer parity — 2026-10-08

This delta fixes the actual SDK attachment consumer and idle accepted-input boundary. It is not a claim that every migration feature or coverage gate is complete. Existing logger/abort handoff evidence remains separate.

## Source contract and implementation

`src/QueryEngine.ts:474–517` captures whether an accepted idle input is a pending session message, records it, awaits the actual flush, then consumes. Such input is `isMeta` channel communication and is excluded from initial SDK user acknowledgements. `src/QueryEngine.ts:921–997` rewrites a pending queued attachment's mutable canonical UUID to `source_uuid`, records and flushes before consuming, and emits the original queued prompt as a SDK user replay only when `replayUserMessages` is true. Queued replay does not increment tool-user turn counts.

Go changes:

- `sessioncontroller/session_inbox.go`: both actual attachment-extraction methods remove the command from the queue without prematurely consuming its inbox registration. Same-owner pending/consume/replay accessors keep the original Controller authority.
- `sessioncontroller/admission.go`: only removes the early idle consume call; existing priority selection, lifecycle-start, batching and guards remain.
- `sdkhost/host.go` and `queued_command.go`: actual initial Record and Flush await for pending idle input; actual queued consumer sets its stable mutable UUID, accepts into the same SerialASK, waits the same Record/Flush promises, consumes once, and emits the raw SDK replay. Persistence-disabled input consumes without writing. Idle meta input does not gain a replay.

The four StructuredOutput hooks in `host.go` were integrated for the separate owner. Their implementation and Source/runtime evidence belong to that owner; they are not counted as inbox evidence.

## Important original limitation retained

The actual original `sessionStorage.isLoggableMessage` at lines 4604–4631 filters external queued-command attachments, including session-collaboration ones. `cleanMessagesForLogging` filters before record insertion at line 4720. Consequently the stable queued UUID is in mutable history, but is absent from the external canonical transcript. A cold external inbox accepts that busy delivery as queued again. An idle inbox message is a real user message and is persisted/deduplicated on cold resume.

The original QueryEngine session-message unit test mocked `recordTranscript`, so its raw-array assertion did not prove physical queued persistence. This investigation explicitly executes the original Project writer, flush, transcript read and UUID lookup. Parent instruction was to retain Source filtering, not widen TypeScript or Go logging. The cold busy red expectation from the first test was corrected to the demonstrated original behavior, and is reported as a Source limitation, not a fixed delivery guarantee.

## Evidence

All homes/configs/workspaces and credentials are private fixtures; HTTP is loopback only. No real provider quota, Keychain, user home, native UI or public network was used.

- Initial actual MainPrint red: `/tmp/go-inbox-durable-consumer-red-first.log`. Demonstrated receipt before canonical consumer, mutable UUID mismatch, missing replay and the original busy cold behavior.
- Actual Source physical writer: `/tmp/go-inbox-original-physical-fifth.log`. No blocked external primitives; queued delivery absent from physical UUID lookup.
- Source regression `sdkhost/session_inbox_source_test.go` + `native-sdk-inbox-consumer.fixture.ts`: original QueryEngine and original Project writer; six profiles cover replay off/on, disabled persistence, Record rejection, Flush rejection and idle input. The exogenous query waits the actual initial Record promise, and a held Flush completion observes zero receipts and queued duplicate status. Failure profiles keep the delivery pending and do not replay.
- Actual Go MainPrint tests `native_session_inbox_durability_test.go`: real Read, two physical provider POSTs, queued prompt in continuation, stable mutable UUID, warm duplicate, busy cold Source filtering, disabled persistence, and idle physical Close barrier. During the real Close await there are zero receipts and zero provider calls; foreign-owner reads/consumption/config are rejected. Idle cold resume deduplicates without another provider request.
- Controller extraction/accessor tests `session_inbox_test.go`: both extraction producers, root/Agent separation, no premature receipt, same registration, once-only consumption and scope checks.

Passing checks:

- `/tmp/go-inbox-source-native-cold-and-disabled-eighth.log`: focused Source/native checks ×3, SDK 4.390s / CLI 12.933s.
- `/tmp/go-inbox-owner-extraction-eleventh.log`: controller extraction/accessors ×3, 1.999s.
- `/tmp/go-inbox-full-controller-sdk-source-race-thirteenth.log`: complete controller 46.912s / SDK 21.379s / inbox 3.246s, Source enabled and race enabled.
- `/tmp/go-inbox-current-final-delta-sixteenth.log`: final current snapshot focused ×3, SDK 4.153s / CLI 12.157s, new offline dependency seed.
- `/tmp/go-inbox-final-affected-vet-current-seed-fifteenth.log`: five affected packages, exit 0.

Preserved failures/limitations:

- Ninth/old tenth controller run: new test fixture omitted its actual PendingWriter and panicked; fixture repaired by constructing the canonical pending writer, without a production guard change.
- Old tenth full run: application setup blocked by the newly added qrcode dependency absent in the previously hydrated offline cache. Go lookup was disabled; no download occurred. The remaining CLI portion was interrupted after its obsolete snapshot exceeded nine minutes. It is not counted as a completed CLI regression.
- Fourteenth vet: same stale private dependency cache failure; superseded by a brand-new private environment hydrated from seed `ea6bd47bef489f2d340ab3a702af5a68284e1b0b4ea99dc4b002506f90685fde`, not a populated-cache overwrite.
- Current complete CLI/application run: `/tmp/go-inbox-current-full-cli-application-seventeenth.log`: CLI Source/race complete PASS 541.044s; application FAIL 253.491s only at the independent Feishu registration Source comparator (profiles 0–3: normalized Go addons placeholder versus raw Source gzip value). Forwarded to Root; no inbox failure or cleanup hang was observed. Application is not claimed green.
- A final test-only cleanup-order repair now unblocks its private physical-Close gate before MainPrint.Close on any failure path. Original deadlines and business assertions are unchanged. `/tmp/go-inbox-test-cleanup-delta-eighteenth.log`: final fixture ×3 PASS 12.119s. This supersedes the previous test-file freeze; the complete CLI passing code snapshot differed only in this cleanup order.

## Source identity

- QueryEngine.ts: `483322d035ef2370ad24f11008bd7cb311ebbea968cbdd81fd9cff41363dda15`
- sessionMessageInbox.ts: `9704a909a44f20b859e5797b389164b8bfbcd0efcfc4440015c997e07a235c0c`
- sessionStorage.ts: `b268b5fe0cd1c1f817b8cbb442297bd401868be3a3af34d367789d22e22f7cd5`

## Final freeze

Production, tests, Source fixture and this document are frozen after the eighteenth check. Formal repository verify and signed/live UI are owned by Root and were not run by this subtask. No broader migration completion is asserted.
