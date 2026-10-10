# QQ native registration producer evidence — 2026-10-08

Implemented production files: `backend/internal/imregistration/qq.go`, `qq_http.go`, `qq_protocol.go`. Existing unrelated producer/consumer files are preserved. The server/application/adapterconfig integration belongs to the separate consumer owner.

The actual Source façade is `adapters/qq/qr-auth.ts`; the installed vendor is `@tencent-connect/qqbot-connector` 1.2.0, `dist/esm/qqbot-session.js` and `qr-connect.js`. Production Go does not execute these files. Tests load the original business code and real installed vendor; they redirect only the HTTPS primitive to a generated-certificate loopback server. The callback seam observes and delegates to the actual vendor, rather than returning fabricated QR/credentials. Crypto and clocks are isolated deterministic primitives.

## Stable factory contract

- `NewQQ(QQOptions{Client: *http.Client, Now, UUID, Random, Schedule}) (*QQService, error)`. Client must have nil/default or actual `*http.Transport`; the service clones and owns native sockets, retaining TLS/proxy policy. Remaining fields are optional and use real production primitives when omitted.
- `Start(context.Context, protocol.JSONObject) (protocol.JSONObject, error)` takes optional source/sessionKey and preserves raw UTF16 string fields.
- `PollJSON(context.Context, json.RawMessage) (protocol.JSONObject, error)` consumes a success/failure once and surfaces a changed URL once.
- `CancelJSON(json.RawMessage) error` removes visible login and disposes polling.
- `Close(context.Context) error` cancels administrative HTTP and joins workers/asynchronous non200 drains within the supplied caller lifetime; canceled caller context remains a reported error.

The official POST URLs are `https://q.qq.com/lite/create_bind_task` and `https://q.qq.com/lite/poll_bind_result`. The production connect URL stays under `https://q.qq.com/qqbot/openclaw/connect.html`. The driver actually generates a 32-byte key, parses strict numeric retcodes/status, decrypts AES-256-GCM credentials, repeats transient poll errors and rotates expired QR tasks. Source 20-second readiness, two-second polling, strict greater-than 10-minute TTL, first-credential validation, terminal latching and normal supersession are preserved. Source inserts a login only after QRready; concurrent pre-ready starts are not falsely claimed globally serialized.

## Observed repairs and original behavior

Red evidence is retained: `/tmp/go-qq-protocol-source-red-sixth.log` (UTF16/short crypto errors), `/tmp/go-qq-http-default-headers-thirteenth.log` (pinned Bun default UA/no compression/keepalive), `/tmp/go-qq-http-reject-before-body-red-fourteenth.log` (non200 waited for body), and `/tmp/go-qq-split-utf8-physical-red-nineteenth.log` (joined bytes incorrectly repaired a split Chinese character).

`/tmp/go-qq-source-physical-lifecycle-fifteenth.log` failed because the initial Source test awaited a terminal partial-response promise indefinitely. Actual vendor/socket instrumentation proves the pinned Bun request timeout stops after response headers: a partial 200 promise is still pending at 13 seconds with headers/data/close events, while no-headers requests terminate at 10 seconds. The 45-second failure and the following nil-interface test observation remain preserved. The corrected fixture makes a bounded phase observation; it does not lengthen that deadline or claim the pending promise completed. Go now preserves the same phase and cancels it only through real administrative IO cleanup. Reused native sockets explicitly rearm the next request timer.

Cancel matches the vendor: it aborts sleeps/next polls but does not cancel an already executing HTTPS request. A late successful callback cannot resurrect the deleted visible login. Service Close owns real cancellation and worker/drain joining.

## Passed checks

- `go test -race -count=1 -v ./internal/imregistration`: PASS 51.349s, `/tmp/go-qq-final-whole-source-race-twentysecond.log`. Covers all old Source neighbors, 149 vendor URL/decryption cases, 53 original façade + real vendor TLS profiles, actual non200/UTF8/socket reuse/timeouts, physical cancellation and Close.
- Subsequent test-only delta `go test -race -count=1 -run TestQQActualVendorRawBufferSecrets ./internal/imregistration`: PASS 1.630s, `/tmp/go-qq-raw-buffer-source-delta-twentythird.log`. Three valid original Buffer/array/arraylike secret cases; production unchanged after whole run.
- `go vet ./internal/imregistration`: PASS, `/tmp/go-qq-final-vet-twentyfourth.log`.

All checks use disposable home/config, pinned Bun, offline locked Go dependencies, generated fake credentials, and private TLS servers. No public platform requests, real home/keychain, official QQ account, live models, desktop build/UI, full verify or coverage check were run in this leaf. These deterministic results are not official account扫码 evidence or overall Go migration completion.

Ignored scoped receipt with Source/producer hashes: `artifacts/go-migration-integration/native-qq-registration-producer-receipt.json`.
