# SDK replay: choose an explicit physical-input schedule

Formal verify277 failed `TestActualOriginalTwoASKReplay/usage-fields` with only
the first assistant's physical JSONL position differing from the second
enqueue/dequeue records. The original log remains at
`artifacts/go-checks/2026-10-07T22-43-36-192Z/checks.log`.

Decoding the complete log shows Go's second enqueue/dequeue before the first
assistant. The unchanged captured Source golden has the first assistant before
the second queue operations. The SDK bytes, usage, UUIDs and parent chain agree.

The original caller waits for the first SDK result and then supplies the second
SDK message. A result does not join the outer `recordTranscript` promise. The
original Source awaits `getSessionMessages` and `discardDeletedTranscriptHistory`
(including native stat) before registering the insertion as a tracked write.
Project flush waits for admitted tracked writes; it does not retroactively join
an outer Record still awaiting that stat.

Actual Source evidence uses the original archived fixture and original current
`QueryEngine`, `sessionStorage` and `print` implementations. Private HOME, files,
loopback TLS and fake credentials isolate all IO. No public model or user state
is involved. The private experiment changes neither Source business nor the
original SDK caller:

- Twenty ordinary repetitions have assistant row5, second enqueue row6.
- Three repetitions execute the real first-assistant discard stat, retain its
  result, then delay only that native Promise's completion until the second
  enqueue. All complete with two model HTTP requests and no blocked capability.
  They have enqueue/dequeue rows5/6 followed by assistant row7, matching the
  failing Go interleaving. First SDK result is emitted before that stat settles.
- This controlled native completion proves one legal alternate schedule; it is
  not an exhaustive scheduler enumeration. No sleep, sorting, normalization of
  record order, generic drain or production flush was introduced.

Only `backend/internal/sdkhost/host_replay_test.go` changes. Its actual physical
Write observer parses a successfully written original assistant record with
message ID `probe-first` and signals a dedicated append channel. The full-file
golden caller explicitly waits for that event outside the Owner before admitting
the second SDK input. Empty-first has no assistant and does not enable this
barrier. Other callers remain unchanged.

The matching actual Source caller now waits for the same physical assistant
append after the first result. Twenty ordinary repetitions and three stat-held
repetitions all retain the old golden exactly across stdout, full transcript
bytes, history, requests, cache and AppState. All existing assertions and
`testdata/source_contract.json` remain unchanged. This fixes an unspecified
test input schedule, without imposing a stronger Record/queue order on the
product.

| Check | Result |
| --- | --- |
| Existing formal failure | Retained in verify277 checks.log |
| Actual Source old caller, ordinary20 + controlled stat3 | Both legal orders proven; all exit0/two HTTP requests/no blocked capability |
| Actual Source physical-append caller, ordinary20 + stat-held3 | All six full golden fields match exactly |
| All three strict Go golden cases, twenty repetitions with race | PASS20.070s, `/tmp/go-sdk-replay-physical-input-barrier-repeat-first.log` |
| Entire SDK package with actual Source Bun and race | PASS19.464s, `/tmp/go-sdk-replay-physical-caller-full-source-race-second.log` |
| SDK vet | exit0, `/tmp/go-sdk-replay-physical-caller-vet-third.log` |
| Whitespace review | `git diff --check`, exit0 |

Commands actually run: `go test -mod=readonly -race ./internal/sdkhost -run
'^TestActualOriginalTwoASKReplay$' -count=20`, `go test -mod=readonly -race
./internal/sdkhost -count=1`, and `go vet -mod=readonly ./internal/sdkhost`.
They use the established hermetic Go/Bun environment and offline pinned modules.

The private full Source runs, scripts, input wires and hashes remain under
`/private/tmp/go-sdk-replay-source-stress-18thh6ht`. The ignored, sanitized receipt
is `artifacts/go-migration-integration/sdk-replay-physical-input-schedule-receipt.json`.
It records Source code hashes, unchanged golden hash, all four groups and exact
full-field comparison results. These are deterministic private tests, not signed
desktop or official-provider evidence. Parent owns the subsequent full verify.
