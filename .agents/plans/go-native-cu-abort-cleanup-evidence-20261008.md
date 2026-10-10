# Computer Use enabled: Stop during permission wait

Scope: the P250 native failure `query hooks capability is not migrated: abort
computer-use overlay/badge/hidden-app/lock lifecycle`. This evidence concerns
abort cleanup, not completion of all Go features or the complete MCP serve path.

The query loop already supplies a real cleanup callback backed by
`NativeMCP.EndTurnOwner` on the same active Owner. `queryhooks.AbortCleanup`
rejected every active Computer Use turn and never consumed that callback.
The SDK Host also invoked it separately before the hook, causing duplicate
cleanup after the missing hook consumer was repaired.

Production changes:

- `backend/internal/queryhooks/cleanup.go` consumes the bound callback for the
  main thread and awaits its best-effort cleanup without the canceled model
  signal. Context values remain. Child Agents stay excluded; active Computer
  Use with a missing callback retains its capability error.
- `backend/internal/sdkhost/computer_use_cleanup.go` is the private abort handler
  bound directly into `queryloop.Host`. `host.go` removes the separate preceding
  call. The owning `runAccepted` deferred cleanup remains for exceptional exits.
- No `ComputerUseInactive` override, native helper/Swift change, new permission,
  new native registry or model loop is introduced.

Original Source evidence:

- `src/query.ts` has two main-only abort branches (streaming and tools). Both
  await `cleanupComputerUseAfterTurn` inside try/catch without passing the model
  abort signal. `src/utils/computerUse/cleanup.ts` owns badge, overlay, hidden
  app and lock cleanup. Source's current capture helper reports `hidden: []`;
  this change delegates to the existing native turn producer rather than
  fabricating hidden-app state.
- The new fixture transpiles and parses the actual `query.ts` with Acorn,
  executes both original abort branches, and imports the original cleanup
  function. Only its dynamic import dependency and external resource seams are
  injected. Two branches × sixteen profiles cover main/child, canceled signal,
  overlay rejection and badge rejection. Callback/error comparisons prove the
  Go consumer. Source-only phase assertions validate the imported producer;
  they are not represented as an executed Go overlay implementation.

The regression was first red in
`/tmp/go-cu-abort-bound-cleanup-red-first.log`: a canceled main turn with its
actual cleanup callback present still hit the capability error. No old test or
Source assertion was weakened to accept that error.

Actual native lifecycle evidence uses private HOME/config/work, loopback model
HTTP, the real MainPrint/SDK/Owner/permission Future/tool executor, the actual
isolated JavaScript worker, and real filesystem CU lock:

- With CU enabled but unused, wait for Bash approval, cancel, verify Bash did
  not run and no native CU action started, then continue the same Harness.
- After the native CU tool acquired its actual session lock, wait for Bash
  approval, prove a foreign Owner cannot release or change the lock, cancel,
  verify the lock is absent, reconnect the actual native MCP binding, then
  continue the same Harness with the original interruption/history in the
  next HTTP request. Explicit shutdown joins the original lifecycle.
- The original native daemon/AF_UNIX/overlay/turn-end/lease tests cover the
  downstream producer. Only external OS/Swift IO is replaced in these fixtures;
  no real app or real user profile is controlled.

| Check | Result and log |
| --- | --- |
| New callback regression before repair | FAILED as intended, `go-cu-abort-bound-cleanup-red-first.log` |
| Original Source two abort branches, 32 profiles | PASS3.130s, `/tmp/go-cu-abort-actual-source-third.log` |
| Initial native permission-stop test | FAILED on an incorrect capitalized interruption-text assertion; preserved fourth log; corrected to the actual existing Source text, not relaxed to ignore history |
| Actual MainPrint two lease states + owner rejection | PASS6.950s, `/tmp/go-cu-abort-cleanup-actual-main-race-seventh.log` |
| Five full packages, before Host duplicate removal | queryhooks12.483s, computerusemcp3.083s, mcptools2.806s and sdkhost18.734s PASS; computeruse FAILED one existing daemon reverse-attestation startup, `/tmp/go-cu-abort-cleanup-five-source-race-sixth.log` |
| Same daemon test, isolated three repetitions | PASS8.701s, `/tmp/go-cu-daemon-attestation-isolated-ninth.log` |
| Entire computeruse package, isolated | PASS25.976s, `/tmp/go-cu-abort-computeruse-full-source-race-tenth.log` |
| Final Host single-counter + Source + actual MainPrint focused race | queryhooks1.864s/sdkhost1.792s/clibootstrap6.059s PASS, `/tmp/go-cu-abort-single-host-counter-main-eleventh.log` |
| Six-package vet before private Host extraction | exit0, `/tmp/go-cu-abort-cleanup-six-vet-eighth.log` |
| Entire sdkhost package after final handler change | PASS17.049s, `/tmp/go-cu-abort-single-host-full-source-race-twelfth.log` |
| Final six-package vet | exit0, `/tmp/go-cu-abort-single-host-six-vet-thirteenth.log` |
| Whitespace review | `git diff --check`, exit0 |

Commands use the existing hermetic Go environment, pinned Go dependencies,
`-mod=readonly`, `-race`, and original pinned Bun. No public model call, external
telemetry, real Keychain, real HOME or live account is used by these checks.
The reverse-attestation failure remains recorded; the fixture, verifier and
production timeout/signature checks were not weakened.

The commands actually run were:

```sh
go test -mod=readonly -race ./internal/queryhooks -run '^TestComputerUseAbortUsesBoundCleanupWithoutCanceledModelSignal$' -count=1
go test -mod=readonly -race ./internal/queryhooks -run TestComputerUseAbort -count=1
go test -mod=readonly -race ./internal/queryhooks ./internal/computeruse ./internal/computerusemcp ./internal/mcptools ./internal/sdkhost -count=1
go test -mod=readonly -race ./internal/clibootstrap -run 'TestNativeComputerUse|TestComputerUseActualGoWorkerChild' -count=1
go test -mod=readonly -race ./internal/computeruse -run '^TestNativeDaemonHandshakeSessionTurnOverlayAndCleanup$' -count=3
go test -mod=readonly -race ./internal/computeruse -count=1
go test -mod=readonly -race ./internal/queryhooks ./internal/sdkhost ./internal/clibootstrap -run 'TestComputerUseAbort|TestDormantAbortCleanup|TestComputerUseStop|TestActualHostAbortHandler|TestNativeComputerUse' -count=1
go test -mod=readonly -race ./internal/sdkhost -count=1
go vet -mod=readonly ./internal/queryhooks ./internal/computeruse ./internal/computerusemcp ./internal/mcptools ./internal/sdkhost ./internal/clibootstrap
```

Root owns whole-checkout impact/verify and the next signed desktop validation.
This leaf did not run `bun run verify`, build or control the desktop, or consume
live-provider quota. The separate ordinary MCP model receipt remains unrelated
to this cancellation repair.

P250 predates this repair. A fresh signed desktop Stop/Read continuation and
its official-model receipt must be recorded by the integrating parent with the
new package SHA. The tests above do not claim that desktop/live proof.
