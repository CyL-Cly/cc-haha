# Runtime handoff regression audit · 2026-10-08

The previous failure was reproduced on the current checkout with the original assertions. It did not reach a runtime switch: `runtime_test.go` was waiting for the **initial** old-provider POST. The recorded new-provider POST already contained only `first turn`, and the old provider never opened. The saved index had `activeId: second` before native startup; the completed startup migration now reads that active provider exactly as original Source does.

Evidence: `/tmp/go-runtime-switch-native-current-first.log` (failed20s) and `/tmp/go-runtime-switch-native-physical-requests-second.log` (failed20s; actual URL/method/body observed). Fake credentials and private fixtures only; no public request or real user state.

## Corrected regression

`backend/internal/application/runtime_test.go` now installs two real saved providers with `activeId:first` before the initial startup. It selects second only through the actual desktop `set_runtime_config` command. No existing history, HTTP count or credential assertion was removed.

Additional assertions now prove that:

- The first actual HTTP remains held on the old service while the override is accepted.
- Deferred replacement waits for that completed turn, and the second real HTTP body contains `OLD PROVIDER ANSWER` with the new credentials.
- The old real Owner has closed after confirmation; the replacement Owner closes on desktop Close.
- Both real old/new assistant answers and confirmed provider metadata survive in the canonical transcript.

Native Keychain subprocess primitives in the owned test factories are explicitly unavailable. Product telemetry is disabled only in test environments. The production runtime, SDK, transcript actor, completion consumer and guards are unchanged, because no defect in their history handoff was demonstrated after fixing the conflicting initial state.

## Source and actual native route

`native-runtime-provider-selection.fixture.ts` calls original `readActiveProviderManagedEnv` and `ProviderService.getProviderRuntimeEnv` from a disposable profile. The Go comparison uses ordinary `NewSession` with **Native:nil**, actual factory and real loopback inference POSTs. Three profiles retain exact original field presence and API-key/token distinction:

1. Saved active second, no override: actual initial route second.
2. Saved active second, explicit first: actual initial route first, with the real host-owned-provider marker.
3. Saved active second, explicit second: actual initial route second.

This validates the Source cause and the explicit-selection boundary rather than accepting missing history.

Relevant original consumers: `src/utils/managedEnv.ts:getCcHahaSettingsEnv`, `src/server/services/providerRuntimeEnv.ts:readActiveProviderManagedEnv/mergeActiveProviderManagedEnv`, `src/server/services/conversationService.ts:buildChildEnv`, `src/server/ws/handler.ts:handleSetRuntimeConfig/restartSessionWithRuntimeConfig`. Original durable restarts remain deferred to the admitted turn and resume their persisted conversation; temporary side-chat model changes retain their separate in-process branch.

## Verification

- Authentic initial-route handoff regression: passed, `/tmp/go-runtime-switch-native-authentic-initial-third.log`, race3.034s.
- All focused Runtime consumers, metadata, rollback and Source/three actual-route profiles: passed, `/tmp/go-runtime-switch-default-explicit-source-seventh.log`, race10.896s.
- Full application race: passed, `/tmp/go-runtime-switch-full-application-eighth.log`, 244.984s / exit0.
- Application vet: passed, `/tmp/go-runtime-switch-vet-ninth.log`, exit0.
- `git diff --check`: passed.

Commands use `/tmp/go-openaiauth-final-environment.json` private paths and the frozen offline dependency cache:

```text
go test -mod=readonly -race ./internal/application -run '^TestDesktopRuntimeSwitchWaitsForActiveTurnThenRestoresHistory$' -count=1 -timeout=60s
go test -mod=readonly -race ./internal/application -run Runtime -count=1 -timeout=120s
go test -mod=readonly -race ./internal/application -count=1 -timeout=600s
go vet -mod=readonly ./internal/application
```

No new official-model quota run was needed for this fixture correction; existing signed or live receipts are not relabeled as new evidence. Root remains responsible for final repository impact/verify and whole signed desktop evidence.
