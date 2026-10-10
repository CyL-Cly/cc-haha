# Persisted Agent resume reducer and first-request boundary

This component migrates actual external release reducer business. It is not a desktop UI projection or a completed CLI/Server startup.

## Source callers and ordering

`src/utils/conversationRecovery.ts`:

- `loadMessagesFromJsonlPath` independently loads original JSONL, chooses the newest main leaf and strips only the original parent/sidechain envelope fields. Go uses the already migrated `sessionhistory.Loader`; it does not receive Source-selected history or Source SID as input.
- `deserializeMessagesWithInterruptDetection` drops malformed attachments, migrates legacy attachments, clears invalid permissions in place, removes fully unresolved tool-use assistant messages, filters orphan thinking, filters whitespace and conditionally merges adjacent users, detects interruption, appends genuine continuation, then independently inserts a genuine sentinel. Errors are reported and rethrown.
- `loadConversationForResume` additionally restores other session state and runs actual hooks. Those responsibilities are outside this reducer and remain required for complete resume.

`src/utils/messages.ts` provides the actual Source filters, merge/factory semantics, and `src/services/api/claude.ts::queryModelWithStreaming` consumes the resumed Agent envelopes. The fixture invokes these Source functions directly after disposable HOME and capability guards.

Go production APIs:

- `sessionresume.New(Options)` requires the real `messages.Factory`, relative-path capability, diagnostics callback and error callback. `TranscriptClassifier` is explicit; release request profiles enable it, and reducer profiles separately prove both build states. Internal KAIROS/BRIEF/HISTORY_SNIP/SendUserFile features remain off exactly as the external Source build.
- `ParseMessages` preserves original field order, unknown JSON and per-object identity; scalar/missing/null/object tool IDs remain distinct. Numeric ID comparison follows JS double and signed-zero equality. No incoming global normalization or new persisted shape is introduced.
- `Reducer.LoadPath(realLoader, originalPath)` feeds independently selected raw history into this reducer. It does not write or migrate original session files. A missing file retains the existing Source empty-history result; unsupported loader date/snip/restore boundaries propagate explicitly.
- `Reducer.AppendUser` constructs the caller's actual original next prompt through the real factory. It does not simulate the CLI print path's interruption requeue.
- `sessionresume.BuildRequest` rejects alternate prepared history and passes independently recovered ordered envelopes to the real `requestcontext.Build`. Unsupported media, attachments, tools, host policy and provider profiles fail before transport.

## What the strict fixtures observe

The reducer oracle compares actual Source self replay before independent Go: exact rendered JSON, unknown/order/UTF16 handling, input mutation and own-undefined permission state, original message pointer references, interruption-message reference, diagnostic event fields and order, error ownership, UUID/time allocations and persisted bytes. Branches include old attachment formats, malformed payloads, unresolved/resolved mixed tools, object/scalar/missing/numeric IDs, orphan thinking, whitespace merge, meta/compact flags, null terminal names, native exceptions, append/rewrite and compact/fork history.

Eight release first-request profiles use original private JSONL files, genuine Go loader/reducer/factory/requestcontext/provider and actual loopback HTTP. Source uses real `queryModelWithStreaming`. The socket observes method, path, body and every declared SDK header; generation before building and immediately before fetching is frozen. The model response is a deterministic local SSE fixture with fake credentials. Controller/yield/QueryEngine equivalence is not inferred from this request seam.

Both Source fixtures guard HOME/config before production imports. They deny outside filesystem/process/native/network access, expose an empty secure-store adapter and fixed ENOENT managed-policy adapters; only the request fixture grants one exact loopback origin. For each request Source actually calls empty secure-store read once and four fixed managed-policy ENOENT operations. These calls are recorded. Reducer-only profiles use neither adapter. Go fixtures validate canonical HOME/cwd/trusted allocation root/config/USERPROFILE/BUN_OPTIONS and known input/output paths, then supply explicit private FS and loopback transport. Go does not install a process-wide native/network capability deny hook; the exercised production seam has explicit FS/host/transport inputs. Leaf and chained symlink negative cases preserve outside private sentinels.

Ordinary tests write evidence only under ignored `artifacts/go-parity/session-resume`. Frozen native goldens are generated only by the explicit `session-resume.capture.ts --capture-frozen-native-golden` command with Bun 1.3.14. Native replay does not rewrite those goldens.

## Confirmed host boundaries and remaining work

Actual Bun Source path handling resolves relative strings against canonical process cwd independently of bootstrap cwd. Its native display path replaces lone UTF16 units, while the original persisted filename retains them. The Go adapter reproduces the observed Darwin profile without extra FS reads. Windows drive/UNC behavior and all native host matrices still require actual Source evidence; cross-platform compilation alone is not native equivalence.

Actual Bun exceptions render noncallable values differently from JSON.stringify, including literal quote/newline and unpaired UTF16 string units. `Failure.MessageValue` and custom JSON marshaling retain those raw diagnostic units. Native readable `Error()` and native Go stack remain separately visible. Raw Source and Go stacks are saved without normalization; stack equivalence is pending.

Complete CLI resume still requires the full `loadConversationForResume` orchestration: skill/plan/interruption policy, actual SessionStart hooks, file-history/attribution snapshots, content/context replacements, session/global config initialization, continuation locator integration and print/QueryEngine dispatch. The existing history loader's unsupported legacy/local Date.parse, collation, snip/context-collapse capabilities remain explicit. No identity/no-op restore or TS production fallback satisfies those gaps. Server routes, desktop presentation and long-running host shutdown/restart are not wired here.

This finite corpus is evidence for the declared business and request profiles, not a claim of universal JSON, all native error renderer inputs, complete Harness Benchmark equivalence or production readiness.
