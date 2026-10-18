# Changed Go coverage: consumer analysis, 2026-10-13

This is analysis and focused deterministic evidence, not final quality-gate evidence. No threshold, source exclusion, generated source, controlled dependency, or production coverage policy was changed. The old `partial-coverage-debt.json` came from a failed baseline unit run; its 58.63% must not be reported as the final candidate's percentage.

## Actual measurement boundary

`scripts/pr/run-go-tests.ts:346` resolves the merge base of `baseRef` (default `origin/main`) and `HEAD`, then compares the **final working tree** directly with that commit. Untracked production Go sources contribute every line. `evaluateGoChangedCoverage` intersects those changed final-coordinate lines with actual compiler coverage counters and applies the existing 90% minimum to their **aggregate**. It is neither whole-source coverage nor a 90% minimum on each file.

Production scope includes `.go` under `backend/` except `_test.go` and `fixtures`, `testdata`, or `vendor` directories. A source excluded on the current native target remains required on another supported native target. Unknown/unwired sources or missing executable mappings fail. Declaration-only evidence requires compiler counters and AST inspection; it is not a blanket exemption.

Both controlled forks are explicit native test roots and coverage packages: `go.mau.fi/libsignal v0.2.1` → `backend/third_party/libsignal-node`, and `go.mau.fi/whatsmeow v0.0.0-20260218135554-9cbe80fb25a4` → `backend/third_party/whatsmeow-node`. Generated `*.pb.go` in those production forks participates in the existing changed-line scope. See `go-dependencies.ts:22`, `run-go-tests.ts:439`, and `go-platforms.ts` for authority validation.

The read-only diagnostic intersected the old failed profile with the current changed lines only where source SHA-256 still equaled the old baseline snapshot: 2,475 mapped unchanged files, 337,605 changed executable lines, 196,910 hit (58.33%). The other 3,114 diff entries were incompatible/unmapped or nonproduction. These numbers locate debt; they are deliberately not a current candidate receipt.

## Existing real codec corpus recovered

The old baseline's `TestActualOriginalProtocolPropertyCorpusNilDefaultsValuesPresenceAndColdForwarding` failed before its codec cases because adapter Node dependencies were absent. With the frozen dependencies restored, the **unchanged existing test passed in 18.057 seconds**:

```text
bun artifacts/go-migration-20261013/cli-flags/run-focused.ts test ./internal/whatsappcompat -run '^TestActualOriginalProtocolPropertyCorpusNilDefaultsValuesPresenceAndColdForwarding$' -count=1 -timeout=100s -covermode=atomic -coverpkg=go.mau.fi/whatsmeow/proto/... -coverprofile=../artifacts/go-migration-20261013/cli-flags/proto-property-focus.out
```

This uses isolated HOME/cache, pinned Bun, and the prepared Go dependency seed. `whatsappcompat/proto_property_contract_test.go:436` already obtains the real original Baileys/protobufjs inventory and encoded field cases from `testdata/node_property_contract.fixture.ts`, checks Go nil/default getters, tags/presence/oneofs/descriptors, carries opaque field 65000 through cold Go forwarding, then sends actual re-encoded bytes back to the original Node decoder. It is not a synthetic counter exercise.

That focused run covered 50.1% of statements in the proto coverpkg. Using the real changed-line evaluator on **only this focused profile's mapped proto sources** gives 21,295 / 44,554 changed executable lines (47.80%). It is not the full test suite or full 90% gate. Missing line categories in this restricted profile are:

| Public generated consumer | Missing changed executable lines |
| --- | ---: |
| Getters, including schemas outside original Baileys public inventory | 6,880 |
| Message/enum String | 3,267 |
| ProtoReflect | 3,265 |
| Reset | 2,220 |
| Legacy Descriptor | 1,842 |
| Legacy enum UnmarshalJSON | 1,799 |
| Enum pointer | 1,255 |
| Enum Type / Number / EnumDescriptor | 771 each |
| Other production helpers | 418 |

Largest focused gaps: `waE2E` 2,891 / 11,903 lines, `waBotMetadata` 2,388 / 2,455, `waArmadilloMiTransport` 1,625 / 1,683, `waAICommon` 1,508 / 4,347, `waArmadilloApplication` 1,455 / 1,526, and both `waConsumerApplication` variants 1,323 / 1,379. Original Node inventory covers the common E2E/AI/SyncAction schemas substantially; newer native public schemas absent from that inventory dominate its remaining getters. Detailed line/function mappings remain in ignored `cli-flags/proto-property-focus-analysis.json`.

## Concrete remaining verification tasks

1. Run the final unchanged full deterministic Go lane after the dependency repair and all concurrent implementation edits settle. Existing signed Noise/Signal/media transport tests had the same missing Node prerequisites; old zero counters in those consumers are not proof that no tests exist. Prioritize `whatsappcompat/message_client_test.go:43` (old credentials → physical Noise/Signal message/reply/reconnect/cold restore), `noise_client_test.go:59` and `:62` (signed Noise + QR/old credential restoration + physical close), and `whatsapp_media_client_test.go:66` (actual original media metadata + upload TLS + Noise/Signal). Only then classify remaining fresh-profile lines.
2. Extend the **independent wire contract**, not Source equivalence claims, for native public schemas outside original Baileys. `backend/third_party/whatsmeow-node/proto/public_wire_contract_test.go` has independently pre-encoded public golden wires, critical tags, oneof selection, unknown carry, reset, and malformed required-field cases. The adjacent pinned `.proto` files can be parsed by real protobufjs to construct richer independent Node encode → Go getters/presence → cold Go encode → independent Node decode receipts for BotMetadata/Armadillo/ConsumerApplication. Require specific semantic values, nil/defaults, oneof and unknown bytes. Do not call every getter merely to increment coverage, and do not invent an original API where the original inventory has none.
3. Reuse original corpus enum name/number/default schemas to verify real public enum pointer/String/Type/Number/legacy descriptor and JSON ABI: valid symbolic names, numeric unknown values, and rejection of malformed JSON. Verify message debug-text String by parse/semantic round trip and Reset by clearing actual populated fields/oneofs/unknown data before cold reload. These target public compatibility contracts with observable assertions; dummy calls are not acceptable.
4. Use the fresh full profile to target real transport branches beyond the already exercised DM path. Old failed-profile missing changed executable counts were `send.go` 998, `message.go` 877, `group.go` 872, `user.go` 774, `internals.go` 534, `appstate.go` 531, `sendfb.go` 508, `store.go` 482, and `retry.go` 410. Candidate actual boundaries are `Client.SendMessage` request validation/peer/group/newsletter/receipt/cancel, `parseMessageSource` participant/LID/bot/verified metadata, `decryptDM` and `decryptGroupMsg`, group/user IQ requests/replies, and AppState/history protocol processing. Extend the real signed loopback WebSocket peer and real runtime entry points with independent Node wire fixtures and actual store state/reconnect receipts. Direct internal method loops or invented success responses cannot establish those consumers.

Analysis artifacts and scripts are ignored under `artifacts/go-migration-20261013/cli-flags/`: `current-changed-debt-debug.json`, `missing-consumer-categories-debug.json`, `proto-property-focus.out`, and `proto-property-focus-analysis.json`. They are diagnostic/one-test evidence. Final candidate gates, native target receipts, persistence policy, and the requested 120 paired Benchmark remain owned by root integration.
