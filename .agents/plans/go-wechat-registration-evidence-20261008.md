# WeChat QR registration: original protocol → actual Go desktop factory (2026-10-08)

Scope completed: WeChat QR start/poll/unbind through NewDesktopServer → native registration service → real HTTP/TLS transport → shared adapterconfig atomic persistence → original-shaped HTTP response. No TS production fallback, new dependency, account/base-URL override, real account, real message, model quota, UI, signed build or complete verify was used. WeChat messaging/getupdates/media/chat-runtime is a separate still-unimplemented scope; QR binding does not claim that runtime migrated.

## Producer and consumer contract

Original sources: adapters/wechat/protocol.ts startWechatLoginWithQr/pollWechatLoginWithQr/apiGetFetch/pollQrStatus/buildCommonHeaders/isLoginFresh; src/server/api/adapters.ts handleWechatAdaptersApi; src/server/services/adapterService.ts updateConfig/applyConfigPatch/writeConfig/getConfig.

The native producer always starts at https://ilinkai.weixin.qq.com with bot_type=3 by default. It sends iLink-App-Id:bot and iLink-App-ClientVersion:131335 (original 2.1.7 packing). It uses the original encodeURIComponent alphabet, JS coercion and lone-surrogate URI rejection, the existing WHATWG URL parser, UTF-8 response decoding/BOM behavior, and fetch-style follow redirects. Start has no explicit request timeout. Poll has the original 35-second deadline. Native service lifetime cancels/joins its actual HTTP operations and owned transport; it does not tie submitted business to renderer Request.Context.

Original active-login state is preserved: purge precedes UUID/key evaluation, TTL is strictly Date.now()-startedAt < 300000, cache reuse and force behave identically, startedAt is measured after successful QR response, redirect_host sets https://${redirect_host} and applies WHATWG resolution on the next poll, and invalid URL/URI/HTTP/JSON/network/abort failures inside pollQrStatus become wait. Unknown status still produces original undefined behavior (the real handler reports 500), rather than a fabricated pending result. Parsed null outside the poll catch retains its original error branch. Expired and confirmed delete the session, including confirmed with incomplete credentials.

The real desktop handler starts with force:true, validates sessionKey by raw UTF-16 length (1–256), preserves original trailing-tail dispatch and errorResponse shapes, and on connected success returns masked config. It saves accountId/botToken/baseUrl/userId, clears pairedUsers and retains allowedUsers and unknown fields. UpdateWechatBinding is a narrow typed producer: absent userId represents original undefined and deletes old userId inside the existing single atomic update; explicit null remains null. Masked tokens restore the original secret. A truthy non-string token fails Source isMasked before any write. Unbind deletes accountId/botToken/userId, sets default baseUrl, clears allowedUsers and pairedUsers, and preserves unrelated/future fields. Existing atomic temp/rename/0600 writer and global public Update signature remain intact.

## Changed paths

New Go:
- backend/internal/imregistration/wechat.go
- backend/internal/imregistration/wechat_source_test.go
- backend/internal/application/server_wechat_registration.go
- backend/internal/application/server_wechat_registration_test.go
- backend/internal/server/wechat_registration.go

Narrow integration:
- backend/internal/adapterconfig/service.go: UnbindWechat + typed undefined-userId producer on existing update core.
- backend/internal/application/server.go: WechatRegistrationHTTPClient test transport primitive, actual native constructor, failure cleanup and keepWechat ownership.
- backend/internal/server/server.go: typed service option and normal Close cancellation/join.
- backend/internal/server/adapters.go: completed WeChat route dispatch; other incomplete platform guards retained.

Actual original Source fixture:
- scripts/quality-gate/go-parity/native-wechat-registration.fixture.ts: original protocol and complete original handler. Bun.plugin adds only an active-session reset export; original functions are otherwise unchanged. Logical platform fetches map only to private physical HTTP, including real redirects and socket disconnects. Original before/after state, request headers/URL, physical wire counts, API responses, complete persisted bytes and 0600 mode are compared.

The shared batch was applied only after Root confirmed QQ consumer source/factory freeze. QQ/WeCom/Feishu constructors, options, routes and cleanup hunks are preserved. Previously completed debug/schema files were not edited.

## Evidence: red → green

- /tmp/go-wechat-desktop-factory-intended-red-first-1791432110.log: incomplete test imports caused a compilation failure; preserved and not treated as business red. Imports were immediately made coherent.
- /tmp/go-wechat-desktop-factory-intended-red-second-1791432255.log: actual NewDesktopServer/private HTTP poll for unknown session returned 501 GO_MIGRATION_NOT_READY instead of original 200/not_started. This is the intended pre-binding failure.
- /tmp/go-wechat-source-producer-physical-third-1791432498.log: test helper declaration snapshot failure; preserved, not a protocol assertion.
- /tmp/go-wechat-source-producer-physical-fourth-1791432611.log: 59 actual protocol profiles; only row42 differed because Go private peer json.Encoder appended a newline to HTTP error text while original peer used JSON.stringify. Fixed peer bytes, without changing production error handling.
- /tmp/go-wechat-source-producer-physical-fifth-1791432715.log: protocol Source/physical PASS2.698s.
- /tmp/go-wechat-desktop-factory-green-sixth-1791432788.log: actual factory 501→200/not_started PASS application3.798s. Server had no matching tests in this focused command.
- /tmp/go-wechat-full-source-desktop-handler-seventh-1791432880.log: original complete handler/actual factory exact persistence 24 profiles + original protocol59 green (application8.890s, imregistration4.576s).
- /tmp/go-wechat-original-cancel-and-factory-close-eighth-1791433029.log: draft composite literal syntax failure; preserved, corrected before continuation.
- /tmp/go-wechat-original-cancel-and-factory-close-ninth-1791433050.log: original Request.signal cancellation still completes binding; actual TCP renderer disconnect still persists the same bytes; Server.Close cancels/joins held requests and leaves old file bytes unchanged. Each Go case covers pending headers and partial response body, four physical conditions. PASS3.937s; barriers use actual pending request and completed handler, no sleeps.
- /tmp/go-wechat-redirect-writefail-source-factory-tenth-1791433201.log: Source/Go physical 302 follow for both start and poll, exact app headers on both hops/wire counts, real directory-write permission failures with original 500, unchanged bytes and later not_started. PASS imregistration2.918s/application9.185s.

## Final verification, exit0

Pinned Bun: /var/folders/sx/hl8zlmxd7gx9b58_1t2zhp5r0000gn/T/cc-haha-pr1441-bun-6exhle0_/bun-darwin-aarch64/bun.

BUN /tmp/go-current-test.ts ./internal/imregistration ./internal/adapterconfig ./internal/server ./internal/application -run 'Wechat|Wecom|Feishu|Registration|SlackOriginal|Adapter'
- /tmp/go-wechat-final-full-leaves-and-factory-eleventh-1791433313.log
- Source/race neighborhood: imregistration7.117s, adapterconfig4.340s, server4.553s, application26.622s.

BUN /tmp/go-current-test.ts ./internal/imregistration ./internal/adapterconfig
- /tmp/go-wechat-final-two-leaves-whole-source-race-thirteenth-1791433316.log
- Whole Source/race packages: imregistration32.652s, adapterconfig3.732s.

A final Source-order alignment put purge before UUID/key evaluation, preserving original clock cutpoint. Latest changed producer and all its actual consumers were rerun:
BUN /tmp/go-current-test.ts ./internal/imregistration ./internal/application ./internal/server ./internal/adapterconfig -run 'Wechat|DesktopQQRegistration|DesktopWecom|DesktopFeishu'
- /tmp/go-wechat-final-clock-cutpoint-source-physical-fourteenth-1791433463.log
- imregistration3.080s/application20.598s PASS. Server1.770s and adapterconfig2.299s compiled with no matching tests in this regex; their broader checks above remain valid for unchanged code.
- Final scope includes 61 original protocol/real HTTP profiles; 26 complete handler/factory/error/persisted-byte profiles; original renderer-signal abort case plus four actual TCP/ServerClose conditions; and unknown-session real factory proof.

BUN /tmp/go-ultracode-vet-private-1791420065619366000.ts ./internal/imregistration ./internal/adapterconfig ./internal/server ./internal/application ./cmd/claude-haha
- /tmp/go-wechat-final-five-packages-vet-twelfth-1791433315.log exit0.
- Latest cutpoint snapshot: /tmp/go-wechat-final-cutpoint-five-packages-vet-fifteenth-1791433465.log exit0.

Source and tests use disposable HOME/config/tmp, real private files and sockets, fake credentials and cleanup. diffcheck and six new-file no-index whitespace checks passed. Whole application/server packages, root verify, signed package, real QR scan/account and live models were not run here; Root owns final integration/GUI verification. No branch, commit or PR.

Production, tests, fixture and this evidence document are now frozen. No further feature work is scheduled by this owner during Root's package/source snapshot window.
