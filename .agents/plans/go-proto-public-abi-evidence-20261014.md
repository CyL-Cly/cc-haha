# Public protobuf ABI — focused evidence, 2026-10-14

This extends actual codec/public API consumer tests for the controlled WhatsApp schema. No generated production source, controlled fork, dependency, coverage collector, exclusion or 90% threshold changed. The overall migration and full quality gates remain incomplete.

New files: `backend/internal/whatsappcompat/proto_public_abi_test.go`, `proto_independent_abi_test.go`, and `testdata/node_native_proto_abi.fixture.ts`.

The first test reuses the original Baileys/protobufjs property corpus and actual production Go registry mapping. Original symbol/number tables verify public Enum pointer, String, Type, Number, enum Descriptor and physically decoded legacy gzip descriptor. Symbolic JSON restores the value, future numeric JSON preserves its unknown number, and malformed JSON rejects without altering the previous value. Actual Source-produced populated messages verify debug text parsing of the recursive known-field projection, binary unknown-field forwarding, generated Reset clearing all real populated fields/oneofs/opaque data, and cold reuse. Re-encoded messages return to the actual original Node decoder.

Four exact textual names differ in pinned native schemas, with unchanged numeric wires: BotUserSelectionMode `UNKNOWN_MODE` → `DEFAULT_MODE`, `REASONING_MODE` → `THINK_HARD_MODE`; KeepType `UNKNOWN` → `UNKNOWN_KEEP_TYPE`; PollContentType `UNKNOWN` → `UNKNOWN_POLL_CONTENT_TYPE`. Tests record these specific native textual ABI differences; they do not claim original symbolic JSON/name equivalence. Nested opaque fields stay in physical binary forwarding and are removed recursively only from the explicitly identified debug-text projection.

For schemas absent from the original inventory, a new actual protobufjs fixture parses the pinned native `.proto` tree independently of Go descriptors. It records schema SHA-256 and authority `independent-pinned-native-proto-not-original-equivalence`. Actual populated and alternate-oneof bytes verify generated nil/default/populated getters, tags, presence, discriminator selection, legacy message schema descriptors, debug text, Reset and unknown retention. Go's cold binary bytes return to this independent Node decoder. Google/libsignal registries have separate authorities and are not presented as part of the WhatsApp `.proto` oracle. No loop merely invokes getters to increment counters.

Passed commands used the existing hermetic `run-focused.ts` runner, pinned Bun 1.3.14, Go 1.25.7, prepared dependencies and disposable HOME/config/cache. No live model, OAuth or network account was used.

```text
bun artifacts/go-migration-20261013/cli-flags/run-focused.ts test -p 2 ./internal/whatsappcompat -run '^(TestActualOriginalProtocolPropertyCorpusNilDefaultsValuesPresenceAndColdForwarding|TestActualOriginalProtocolPublicABIEnumJSONDebugTextResetAndColdWire|TestIndependentPinnedNativeProtocolPublicABIAbsentOriginalSchemas)$' -count=1 -timeout=180s -covermode=atomic -coverpkg=go.mau.fi/whatsmeow/proto/... -coverprofile=../artifacts/go-migration-20261013/cli-flags/proto-abi-focus.out
bun artifacts/go-migration-20261013/cli-flags/run-focused.ts test -race ./internal/whatsappcompat -run '^(TestActualOriginalProtocolPublicABIEnumJSONDebugTextResetAndColdWire|TestIndependentPinnedNativeProtocolPublicABIAbsentOriginalSchemas)$' -count=1 -timeout=120s
bun artifacts/go-migration-20261013/cli-flags/run-focused.ts vet ./internal/whatsappcompat
```

Final results: focused unit **passed 29.536s**, compiler statement coverage **98.3%** for `go.mau.fi/whatsmeow/proto/...`; new ABI race **passed 26.453s**; vet **passed**. Existing `collectGoChangedLines` / `parseGoCoverageProfileFile` / `evaluateGoChangedCoverage`, unchanged at 90%, found **43,732 / 44,554 changed executable lines = 98.1550%**, no focused-scope failures. This is a focused controlled-proto receipt, not the whole aggregate quality gate.

The immutable checkpoint02 failed-unit partial profile previously missed 21,794 executable lines now hit by this focused profile. The checkpoint was failed and is diagnostic only; adding those hits does not turn it into a passed final gate. Remaining focused missing categories: getters 550, other public helpers 253, Reset 12, String 6, ProtoReflect 1. The ordinary full suite has other helper consumers outside this focused set and must be rerun for the final candidate.

Ignored evidence: `cli-flags/proto-abi-focus.log`, `proto-abi-race.log`, `proto-abi-focus.out`, `proto-abi-focus-analysis.json`, plus initial red logs. The red logs retain incorrect broad textual-equivalence/debug unknown projection assumptions and a fixture stdout teardown truncation; corrections changed test boundaries/receipt joining, not production ABI or coverage policy. The entire disposable compiler cache will be removed after these preserved receipts to free disk space for root integration.
