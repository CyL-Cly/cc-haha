# Native media error presentation and recovery — 2026-10-08

This is a separate delta after the signed P231 image success evidence (parent-owned SHA 7f3ca12b). Its deterministic model responses are isolated HTTP/SSE fixture responses, not official vision-provider success.

## Production

`apipresentation.NativeMediaFailure` uses the actual typed SDK error, status, original UTF-16 message, prepared `messagesForAPI`, selected model and real noninteractive mode. It ports ImageSize/ImageResize, unsupported-image wording/status conditions, PDF page/password/invalid errors, image maximum/many-image errors, HTTP 413 payload estimates and model-scoped 400/422 image rejection fallback. The same original Factory allocates the error envelope only after selection. Nonmedia, auth, capacity/compaction and administrative branches keep their guards; unmatched provider faults are never called successful.

`modelyield` captures the real normalized history through the existing `OnPrepared`, preserving the original duration/logging observer. Capture owns an immutable JSON snapshot, never a second history store. Creation and native stream error paths use the new mapper; transient retry exhaustion retains that attempt's history. Caller cancellation and Owner failures remain checked first. The normal query/controller/output/writer pipeline records the error and uses the existing original media normalizer on the next request.

The Source public SDK assistant projection intentionally emits its `error`/text rather than canonical `businessErrorCode`/details; assertions retain this behavior and verify business fields on canonical records. Failure results retain `is_error: true` and are not represented as successful model work.

## Source

`native-media-error.fixture.ts` calls the actual exported Source `getAssistantMessageFromError` and actual typed SDK/Image errors, real message Factory, and disposable configuration/capability guard. Only the external empty credential-store observations are fixtures. No Source production business code runs as a Go fallback.

114 profiles cover interactive/SDK strings, plain Error versus real API error, 400/422 versus 401/403/404/500 conditions, local image errors, PDF/image precedence, unrecognized image wording with top-level/nested/future image blocks, absence of images, HTTP 413 strings/documents/nested/unknown blocks, UTF-16 details and exact UUID -> Date -> message-ID allocation order. Strict canonical fields and messages are compared.

An actual Source discrepancy was found and ported narrowly: pinned Bun 1.3.14 `Buffer.byteLength('\ud800')` reports 2 while `Buffer.from` writes 3 bytes. Only the diagnostic request-size estimate uses the former behavior; physical UTF-8 encoding is unchanged. First red Source log remains saved.

## Actual consumers and lifecycle

- Same MainPrint/Owner/TLS HTTP input image -> true 400 or 422 -> failed SDK result + canonical original image/error -> next actual POST strips only rejected image -> normal response/canonical.
- PDF invalid/page-limit 400 -> true canonical PDF error -> next actual POST removes only rejected document -> normal response.
- Actual 413 for image+document -> canonical request_too_large -> next actual POST contains original Source [image]/[document] placeholders rather than original bytes.
- Actual SSE PDF error -> normal failed terminal, without extra model loop -> next actual POST succeeds with rejected document removed.
- Caller cancellation while real HTTP SSE response is open -> peer sees response close -> no fabricated media business error -> next actual request retains original document, as Source abort has no rejection anchor -> normal response.

All inputs/config/transcripts/credentials use disposable private directories, fake keys and loopback endpoints. No real home, account, Keychain or external telemetry/model endpoint is touched by these checks.

## Observed commands

Offline Go environment `/tmp/go-openaiauth-final-environment.json`; Go 1.25.7; `-mod=readonly`; no dependencies changed.

- FAILED initial leaf compile on unused type-switch variable; fixed immediately: `/tmp/go-sdk-media-error-leaf-compile-first.log`; stable compile `/tmp/go-sdk-media-error-leaf-compile-second.log`.
- FAILED Source sandbox on legacy empty-keychain / hardcoded remote absent-path observations (all operations denied; no external read/process took place): `/tmp/go-sdk-media-error-source-third.log`. Fixture now uses the previously established exact external-empty seam and explicit absent paths, real private fake-key approval.
- FAILED Source 413 lone-surrogate size estimate: `/tmp/go-sdk-media-error-source-fourth.log`.
- PASSED actual Source 114 profiles: `/tmp/go-sdk-media-error-source-fifth.log` (2.103s).
- FAILED five actual MainPrint cases before binding on typed no-status SDK creation guard: `/tmp/go-sdk-media-error-main-red-sixth.log`.
- FAILED first bound test expecting canonical business fields on public SDK assistant; Source QueryEngine projection inspected, assertion corrected while production unchanged: `/tmp/go-sdk-media-error-main-bound-seventh.log`.
- PASSED five actual MainPrint HTTP/canonical/next-turn cases: `/tmp/go-sdk-media-error-main-eighth.log` (4.833s).
- PASSED actual SSE PDF / caller-abort / next-turn / response-close: `/tmp/go-sdk-media-error-sse-cancel-ninth.log` (3.213s).
- PASSED full seven-package Source/race: `go test -mod=readonly -race -p 2 ./internal/apipresentation ./internal/modelyield ./internal/requestretry ./internal/requestcontext ./internal/sdkhost ./internal/clibootstrap ./internal/application -count=1 -timeout 12m`, `/tmp/go-sdk-media-error-seven-full-source-race-tenth.log` (exit 0; API 2.380s / modelyield 9.562s / requestretry 3.504s / requestcontext 31.015s / SDK 18.075s / CLI 489.127s / application 238.507s).
- PASSED seven-package `go vet -mod=readonly`, `/tmp/go-sdk-media-error-seven-vet-eleventh.log` (exit 0); `git diff --check` passed.

Still separate: other API auth/capacity/context-overflow/invalid-model presentation branches and array slash/preceding-block dispatch remain explicit guards outside this leaf. Official root image success/cold-resume evidence must be associated only with its actual signed SHA.
