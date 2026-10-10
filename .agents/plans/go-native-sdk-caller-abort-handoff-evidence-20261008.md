# Caller abort between native header receipt and SDK Response adoption

The actual full affected run in `/tmp/go-auth-error-reporting-full-source-race-first.log` failed `TestActualMainSSEMediaFailureAndCallerCancellationPreserveOriginalLifetimes/caller-abort` with `API presentation capability: typed no-status SDK creation error`. Its 539.975s CLI failure is preserved. Five isolated successful repeats were investigation evidence, not dismissal of the full failure.

A private real Boundary observer retained the original `Scope.AwaitNative` and actual loopback HTTP reservation. It canceled the real caller after a complete header receipt and awaited the reservation's physical closed checkpoint before returning those original bytes. The unmodified producer then failed Response adoption with:

- checkpoint concrete cause: `poll.errNetClosing`, `use of closed network connection`, matching `net.ErrClosed`;
- SDK creation concrete error: `*fmt.wrapError` carrying `ErrHTTPExchangeProfile` and `original HTTP framing: use of closed network connection`;
- caller concrete cause: `*errors.errorString`, `context canceled`.

Intended red: `/tmp/go-native-caller-abort-adoption-red-second.log`.

The minimal production change is in `provider/owner_native_timer.go`: when Response adoption fails with actual `net.ErrClosed`, an aborted original caller produces the original SDK fetch/create abort. The existing SDK mapper produces `SDKStreamError` / `APIUserAbortError`, `Request was aborted.`, undefined status/payload/cause. The same physical close caused by the actual SDK fetch timer preserves its timeout category. Non-caller framing/profile failures and administrative/foreign Owner errors retain their existing guards. `modelyield` and presentation's typed-error guard were not weakened.

Source evidence uses actual pinned Bun / `@anthropic-ai/sdk` 0.80.0 with private TCP peers, not replacement Responses or fabricated fetch exceptions:

- pre-header caller abort creates `APIUserAbortError`;
- after real headers, an already pending original response-body read is canceled before fetch handoff; its real `DOMException` / `AbortError` reaches the SDK and becomes `APIUserAbortError`;
- once the SDK stream has been created, caller abort quietly completes the original iterator with zero events.

The Source fixture records whether its peer was closed before cleanup. It does not assert general Bun pooling/transport lifecycle equivalence or replace unobserved close timing with success. Allocated fixture sockets are explicitly joined during test cleanup.

Verification:

- Original Source plus actual native handoff, disposal, and negative control: three repeats passed, `providernative` 1.666s, `/tmp/go-native-caller-abort-source-and-physical-green-third.log`.
- Actual handoff / close-without-caller / foreign Owner: ten repeats passed, 1.662s, `/tmp/go-native-caller-abort-adoption-green-second.log`.
- Actual MainPrint shared Harness: the existing media/cancel regression adds a separate pre-header cancellation case. Its original PDF error, response-header cancellation, canonical error assertions, real response disposal, and next normal human turn are retained. Ten repeats passed, 16.534s, `/tmp/go-native-caller-abort-mainprint-physical-green-first.log`.
- Complete Source-enabled race for provider/providernative/requestretry/modelyield/sdkhost passed: 12.776 / 3.101 / 4.873 / 10.499 / 16.284s; `/tmp/go-native-caller-abort-affected-full-source-race-first.log`.
- Vet for those five plus clibootstrap/application/errorlog passed, `/tmp/go-native-caller-abort-current-full-vet-first.log`.
- `git diff --check` passed. No live models, public endpoints, real Keychain, user HOME, or native UI/helper probes were used.

The earlier eight-package full run remains failed in CLI/application for the captured cancellation and other owners' then-active pane/Ultracode/TUI-history changes. Latest cancellation/Logger leaf evidence does not turn that old full snapshot green. Root owns the next frozen complete verify and any package/native UI acceptance after the new producer is built.
